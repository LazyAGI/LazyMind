package externalagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	corelog "lazymind/core/log"
	"lazymind/core/store"
)

const (
	runStatusStarting     = "starting"
	runStatusRunning      = "running"
	runStatusWaiting      = "waiting"
	runStatusCompleted    = "completed"
	runStatusFailed       = "failed"
	runStatusInterrupted  = "interrupted"
	runStatusReleasing    = "releasing"
	controlReleasePending = "pending"
	controlReleaseFailed  = "failed"
	defaultRequestWait    = 10 * time.Minute
	managedRunPollPeriod  = 2 * time.Second
)

type managedRun struct {
	mu          sync.Mutex
	record      orm.ExternalAgentRun
	query       string
	seq         int
	answer      string
	events      []Event
	subscribers map[chan Event]struct{}
	finishing   bool
	terminal    bool
	fileChanges map[string]fileChangeReview
}

type fileChangeReview struct {
	Changes   json.RawMessage
	Truncated bool
}

func newManagedRun(record orm.ExternalAgentRun, query string, seq int) *managedRun {
	return &managedRun{
		record:      record,
		query:       query,
		seq:         seq,
		subscribers: make(map[chan Event]struct{}),
	}
}

func (r *managedRun) subscribe() <-chan Event {
	events, _ := r.subscribeCancelable()
	return events
}

func (r *managedRun) subscribeCancelable() (<-chan Event, func()) {
	r.mu.Lock()
	ch := make(chan Event, len(r.events)+64)
	for _, event := range r.events {
		ch <- event
	}
	if r.terminal {
		close(ch)
		r.mu.Unlock()
		return ch, func() {}
	}
	r.subscribers[ch] = struct{}{}
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		if _, subscribed := r.subscribers[ch]; subscribed {
			delete(r.subscribers, ch)
			close(ch)
		}
		r.mu.Unlock()
	}
}

func (r *managedRun) execution() Execution {
	record, _, _, seq := r.snapshot()
	events, cancel := r.subscribeCancelable()
	return Execution{
		RunID: record.ID, HistoryID: record.HistoryID,
		Seq: seq, Events: events, Cancel: cancel,
	}
}

func (r *managedRun) broadcast(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminal {
		return
	}
	if len(r.events) >= 512 {
		r.events = append([]Event(nil), r.events[len(r.events)-255:]...)
	}
	r.events = append(r.events, event)
	for subscriber := range r.subscribers {
		select {
		case subscriber <- event:
		default:
			if event.Terminal || event.Type == "request_required" {
				// Preserve control and terminal events even when an HTTP consumer
				// is slow; dropping one stale progress frame is safe.
				select {
				case <-subscriber:
				default:
				}
				subscriber <- event
			}
		}
	}
	if event.Terminal {
		r.terminal = true
		for subscriber := range r.subscribers {
			close(subscriber)
			delete(r.subscribers, subscriber)
		}
	}
}

func (r *managedRun) setAnswer(answer string) {
	if strings.TrimSpace(answer) == "" {
		return
	}
	r.mu.Lock()
	r.answer = answer
	r.mu.Unlock()
}

func (r *managedRun) appendAnswer(delta string) string {
	if delta == "" {
		return ""
	}
	r.mu.Lock()
	r.answer += delta
	answer := r.answer
	r.mu.Unlock()
	return answer
}

func (r *managedRun) snapshot() (orm.ExternalAgentRun, string, string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.record, r.query, r.answer, r.seq
}

func (r *managedRun) setTurn(turnID string) {
	r.mu.Lock()
	r.record.ProviderTurnID = turnID
	r.record.Status = runStatusRunning
	r.mu.Unlock()
}

func (r *managedRun) setThread(threadID string) {
	r.mu.Lock()
	r.record.ProviderThreadID = threadID
	r.mu.Unlock()
}

func (r *managedRun) setFileChanges(itemID string, changes json.RawMessage) {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" || len(itemID) > 512 || len(changes) == 0 {
		return
	}
	review := fileChangeReview{}
	if len(changes) > 64*1024 {
		review.Truncated = true
	} else {
		review.Changes = append(json.RawMessage(nil), changes...)
	}
	r.mu.Lock()
	if r.fileChanges == nil {
		r.fileChanges = make(map[string]fileChangeReview)
	}
	if _, exists := r.fileChanges[itemID]; !exists && len(r.fileChanges) >= 64 {
		r.mu.Unlock()
		return
	}
	r.fileChanges[itemID] = review
	r.mu.Unlock()
}

func (r *managedRun) takeFileChange(itemID string) fileChangeReview {
	r.mu.Lock()
	defer r.mu.Unlock()
	itemID = strings.TrimSpace(itemID)
	review := r.fileChanges[itemID]
	delete(r.fileChanges, itemID)
	review.Changes = append(json.RawMessage(nil), review.Changes...)
	return review
}

func (r *managedRun) setTerminalState(status, controlRelease, controlError string) {
	r.mu.Lock()
	r.record.Status = status
	r.record.ControlRelease = controlRelease
	r.record.ControlError = controlError
	r.mu.Unlock()
}

func (r *managedRun) beginFinish() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finishing || r.terminal {
		return false
	}
	r.finishing = true
	return true
}

func (r *managedRun) finished() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.finishing || r.terminal
}

type pendingRequest struct {
	ID        string
	RPCID     json.RawMessage
	Kind      string
	Payload   json.RawMessage
	View      ExternalRequest
	Responses map[string]json.RawMessage
	Run       *managedRun
	ExpiresAt time.Time
}

type Service struct {
	db        *gorm.DB
	client    *CodexClient
	mu        sync.Mutex
	byThread  map[string]*managedRun
	byRequest map[string]*managedRun
	requests  map[string]*pendingRequest
	loaded    map[string]int64
}

var (
	defaultServiceMu sync.Mutex
	defaultService   *Service
)

func Default() (*Service, error) {
	defaultServiceMu.Lock()
	defer defaultServiceMu.Unlock()
	if defaultService != nil {
		return defaultService, nil
	}
	db := store.DB()
	if db == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	service, err := NewService(db, NewCodexClient())
	if err != nil {
		return nil, err
	}
	defaultService = service
	return defaultService, nil
}

func NewService(db *gorm.DB, client *CodexClient) (*Service, error) {
	service := &Service{
		db:        db,
		client:    client,
		byThread:  make(map[string]*managedRun),
		byRequest: make(map[string]*managedRun),
		requests:  make(map[string]*pendingRequest),
		loaded:    make(map[string]int64),
	}
	// Rebuild the ownership index before callers can observe provider threads.
	// Provider reconciliation and unsubscribe remain asynchronous below.
	if err := service.recoverActiveRuns(); err != nil {
		return nil, err
	}
	go service.consumeClientEvents()
	return service, nil
}

func validateProvider(provider string) error {
	if strings.TrimSpace(strings.ToLower(provider)) != ProviderCodex {
		return ErrUnsupportedProvider
	}
	return nil
}

func (s *Service) ListThreads(ctx context.Context, cursor, cwd string, limit int, actorUserID string) (ThreadPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset := 0
	var err error
	if strings.TrimSpace(cursor) != "" {
		offset, err = strconv.Atoi(strings.TrimSpace(cursor))
		if err != nil || offset < 0 {
			return ThreadPage{}, ErrInvalidCursor
		}
	}
	threads, err := s.listProjectedThreads(ctx, strings.TrimSpace(cwd), actorUserID)
	if err != nil {
		return ThreadPage{}, err
	}
	if offset > len(threads) {
		offset = len(threads)
	}
	end := offset + limit
	if end > len(threads) {
		end = len(threads)
	}
	data := append([]Thread(nil), threads[offset:end]...)
	s.markThreadAvailability(data)
	page := ThreadPage{Data: data, Total: len(threads), HasMore: end < len(threads)}
	if page.HasMore {
		next := strconv.Itoa(end)
		page.NextCursor = &next
	}
	return page, nil
}

func (s *Service) ListProjects(ctx context.Context, cursor string, limit int, actorUserID string) (ProjectList, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset := 0
	var err error
	if strings.TrimSpace(cursor) != "" {
		offset, err = strconv.Atoi(strings.TrimSpace(cursor))
		if err != nil || offset < 0 {
			return ProjectList{}, ErrInvalidCursor
		}
	}
	threads, err := s.listProjectedThreads(ctx, "", actorUserID)
	if err != nil {
		return ProjectList{}, err
	}
	projects := make(map[string]Project)
	for _, thread := range threads {
		cwd := strings.TrimSpace(thread.Cwd)
		if cwd == "" {
			continue
		}
		project := projects[cwd]
		project.Cwd = cwd
		project.Name = filepath.Base(filepath.Clean(cwd))
		if project.Name == "." || project.Name == string(filepath.Separator) {
			project.Name = cwd
		}
		project.ThreadCount++
		if thread.UpdatedAt > project.UpdatedAt {
			project.UpdatedAt = thread.UpdatedAt
		}
		projects[cwd] = project
	}
	data := make([]Project, 0, len(projects))
	for _, project := range projects {
		data = append(data, project)
	}
	sort.Slice(data, func(i, j int) bool {
		if data[i].UpdatedAt == data[j].UpdatedAt {
			return data[i].Cwd < data[j].Cwd
		}
		return data[i].UpdatedAt > data[j].UpdatedAt
	})
	if offset > len(data) {
		offset = len(data)
	}
	end := offset + limit
	if end > len(data) {
		end = len(data)
	}
	page := ProjectList{
		Data:    append([]Project(nil), data[offset:end]...),
		Total:   len(data),
		HasMore: end < len(data),
	}
	if page.HasMore {
		next := strconv.Itoa(end)
		page.NextCursor = &next
	}
	return page, nil
}

func (s *Service) listProjectedThreads(
	ctx context.Context,
	cwd, actorUserID string,
) ([]Thread, error) {
	threads := make([]Thread, 0, 100)
	cursor := ""
	seenCursors := make(map[string]struct{})
	seenThreads := make(map[string]struct{})
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		params := map[string]any{
			"limit":         100,
			"sortKey":       "updated_at",
			"sortDirection": "desc",
			"sourceKinds":   []string{"cli", "vscode", "appServer"},
		}
		if cwd != "" {
			params["cwd"] = cwd
		}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page ThreadPage
		if err := s.client.Call(ctx, "thread/list", params, &page); err != nil {
			return nil, err
		}
		pageThreads := make([]Thread, 0, len(page.Data))
		for _, thread := range page.Data {
			thread, valid := canonicalThread(thread)
			if !valid {
				continue
			}
			threadID := thread.ID
			if cwd != "" && strings.TrimSpace(thread.Cwd) != cwd {
				continue
			}
			if _, exists := seenThreads[threadID]; exists {
				continue
			}
			seenThreads[threadID] = struct{}{}
			thread.Turns = nil
			pageThreads = append(pageThreads, thread)
		}
		if err := s.decorateThreads(ctx, pageThreads, actorUserID); err != nil {
			return nil, err
		}
		threads = append(threads, actorVisibleThreads(pageThreads)...)
		if page.NextCursor == nil || strings.TrimSpace(*page.NextCursor) == "" {
			break
		}
		next := strings.TrimSpace(*page.NextCursor)
		if _, exists := seenCursors[next]; exists {
			return nil, fmt.Errorf("codex thread/list returned a repeated cursor")
		}
		seenCursors[next] = struct{}{}
		cursor = next
		if pageNumber == 99 {
			return nil, fmt.Errorf("codex thread projection exceeded 100 provider pages")
		}
	}
	sort.SliceStable(threads, func(i, j int) bool {
		if threads[i].UpdatedAt == threads[j].UpdatedAt {
			return threads[i].ID < threads[j].ID
		}
		return threads[i].UpdatedAt > threads[j].UpdatedAt
	})
	return threads, nil
}

func (s *Service) ReadThread(ctx context.Context, threadID, actorUserID string) (Thread, error) {
	return s.readThread(ctx, threadID, true, actorUserID)
}

func (s *Service) ReadThreadPage(ctx context.Context, threadID string, offset, limit int, tail bool, actorUserID string) (TurnPage, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	thread, err := s.readThread(ctx, threadID, true, actorUserID)
	if err != nil {
		return TurnPage{}, err
	}
	var turns []json.RawMessage
	if len(thread.Turns) > 0 {
		if err := json.Unmarshal(thread.Turns, &turns); err != nil {
			return TurnPage{}, err
		}
	}
	total := len(turns)
	if tail {
		offset = max(0, total-limit)
	}
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	pageTurns := turns[offset:end]
	thread.Turns = nil
	var snapshot *RunSnapshot
	if thread.ConversationID != "" {
		value, err := s.SnapshotConversation(ctx, thread.ConversationID, actorUserID)
		if err != nil {
			return TurnPage{}, err
		}
		snapshot = &value
	}
	return TurnPage{
		Thread:     thread,
		Turns:      summarizeTurns(pageTurns),
		Offset:     offset,
		Limit:      limit,
		TotalTurns: total,
		HasMore:    end < total,
		Snapshot:   snapshot,
	}, nil
}

func summarizeTurns(turns []json.RawMessage) []TurnSummary {
	summaries := make([]TurnSummary, 0, len(turns)*2)
	for _, raw := range turns {
		var turn map[string]any
		if json.Unmarshal(raw, &turn) != nil {
			continue
		}
		items, _ := turn["items"].([]any)
		userText := ""
		assistantFallback := ""
		finalAnswers := make([]string, 0, 1)
		for _, rawItem := range items {
			item, ok := rawItem.(map[string]any)
			if !ok {
				continue
			}
			text := externalAgentItemText(item)
			if text == "" {
				continue
			}
			itemType := strings.ToLower(strings.TrimSpace(stringValue(item["type"])))
			role := strings.ToLower(strings.TrimSpace(stringValue(item["role"])))
			switch {
			case strings.Contains(itemType, "user") || role == "user":
				userText = text
			case strings.Contains(itemType, "agent") || role == "assistant" || role == "agent":
				assistantFallback = text
				if stringValue(item["phase"]) == "final_answer" {
					finalAnswers = append(finalAnswers, text)
				}
			}
		}
		assistantText := assistantFallback
		if len(finalAnswers) > 0 {
			assistantText = strings.Join(finalAnswers, "\n\n")
		}
		if userText == "" && assistantText == "" {
			role := strings.TrimSpace(stringValue(turn["role"]))
			text := externalAgentItemText(turn)
			if role != "" && text != "" {
				summaries = append(summaries, TurnSummary{Role: role, Text: truncateRunes(text, 64000)})
			}
			continue
		}
		if userText != "" {
			summaries = append(summaries, TurnSummary{Role: "user", Text: truncateRunes(userText, 64000)})
		}
		if assistantText != "" {
			summaries = append(summaries, TurnSummary{Role: "assistant", Text: truncateRunes(assistantText, 64000)})
		}
	}
	return summaries
}

func externalAgentItemText(value any) string {
	switch item := value.(type) {
	case string:
		return strings.TrimSpace(item)
	case []any:
		parts := make([]string, 0, len(item))
		for _, child := range item {
			if text := externalAgentItemText(child); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	case map[string]any:
		itemType := strings.ToLower(strings.TrimSpace(stringValue(item["type"])))
		switch itemType {
		case "reasoning", "contextcompaction", "commandexecution", "filechange":
			return ""
		}
		for _, key := range []string{"text", "message", "content"} {
			if text := externalAgentItemText(item[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func canonicalThread(thread Thread) (Thread, bool) {
	thread.ID = strings.TrimSpace(thread.ID)
	thread.Cwd = strings.TrimSpace(thread.Cwd)
	if thread.ID == "" || len([]rune(thread.ID)) > 128 || len([]rune(thread.Cwd)) > 500 {
		return Thread{}, false
	}
	if thread.Name != nil {
		name := truncateRunes(strings.TrimSpace(*thread.Name), 200)
		thread.Name = &name
	}
	thread.Preview = truncateRunes(strings.TrimSpace(thread.Preview), 300)
	thread.Source = truncateRunes(strings.TrimSpace(thread.Source), 100)
	thread.Status.Type = truncateRunes(strings.TrimSpace(thread.Status.Type), 100)
	if len(thread.Status.ActiveFlags) > 20 {
		thread.Status.ActiveFlags = thread.Status.ActiveFlags[:20]
	}
	for index := range thread.Status.ActiveFlags {
		thread.Status.ActiveFlags[index] = truncateRunes(
			strings.TrimSpace(thread.Status.ActiveFlags[index]), 100,
		)
	}
	return thread, true
}

func (s *Service) readThread(ctx context.Context, threadID string, includeTurns bool, actorUserID string) (Thread, error) {
	var response struct {
		Thread Thread `json:"thread"`
	}
	params := map[string]any{"threadId": threadID}
	if includeTurns {
		params["includeTurns"] = true
	}
	if err := s.client.Call(ctx, "thread/read", params, &response); err != nil {
		if !includeTurns || !isUnmaterializedThreadRead(err) {
			return Thread{}, err
		}
		response = struct {
			Thread Thread `json:"thread"`
		}{}
		if fallbackErr := s.client.Call(ctx, "thread/read", map[string]any{
			"threadId": threadID,
		}, &response); fallbackErr != nil {
			return Thread{}, fmt.Errorf(
				"thread/read with turns failed: %v; lightweight thread/read failed: %w",
				err,
				fallbackErr,
			)
		}
	}
	canonical, valid := canonicalThread(response.Thread)
	if !valid {
		return Thread{}, ErrThreadNotFound
	}
	response.Thread = canonical
	threads := []Thread{response.Thread}
	if err := s.decorateThreads(ctx, threads, actorUserID); err != nil {
		return Thread{}, err
	}
	if threads[0].BoundByOther {
		return Thread{}, ErrThreadNotFound
	}
	s.markThreadAvailability(threads)
	return threads[0], nil
}

func isUnmaterializedThreadRead(err error) bool {
	var callErr *rpcCallError
	return errors.As(err, &callErr) &&
		callErr.method == "thread/read" &&
		callErr.response.Code == -32600 &&
		strings.Contains(callErr.response.Message, "is not materialized yet") &&
		strings.Contains(callErr.response.Message, "includeTurns is unavailable")
}

func actorVisibleThreads(threads []Thread) []Thread {
	visible := threads[:0]
	for _, thread := range threads {
		if !thread.BoundByOther {
			visible = append(visible, thread)
		}
	}
	return visible
}

func (s *Service) markThreadAvailability(threads []Thread) {
	s.mu.Lock()
	controlled := make(map[string]string, len(s.byThread))
	for threadID, run := range s.byThread {
		record, _, _, _ := run.snapshot()
		controlled[threadID] = record.ActorUserID
	}
	s.mu.Unlock()
	for index := range threads {
		thread := &threads[index]
		if thread.BoundByOther {
			thread.Available = false
			continue
		}
		thread.Available = thread.Status.Type == "idle" || thread.Status.Type == "notLoaded"
		if thread.CanAcceptInput != nil {
			thread.Available = *thread.CanAcceptInput
		}
		thread.ControlledByLazyMind = controlled[thread.ID] != ""
		if thread.ControlledByLazyMind {
			thread.Available = false
		}
	}
}

func (s *Service) decorateThreads(ctx context.Context, threads []Thread, actorUserID string) error {
	if len(threads) == 0 || strings.TrimSpace(actorUserID) == "" {
		return nil
	}
	ids := make([]string, 0, len(threads))
	for _, thread := range threads {
		ids = append(ids, thread.ID)
	}
	var bindings []orm.ExternalAgentBinding
	if err := s.db.WithContext(ctx).
		Where("provider = ? AND provider_thread_id IN ?", ProviderCodex, ids).
		Find(&bindings).Error; err != nil {
		return err
	}
	byID := make(map[string]orm.ExternalAgentBinding, len(bindings))
	for _, binding := range bindings {
		byID[binding.ProviderThreadID] = binding
	}
	for index := range threads {
		if binding, ok := byID[threads[index].ID]; ok {
			if binding.CreatedByUserID == actorUserID {
				threads[index].CreatedByLazyMind = binding.ManagedByLazyMind
				threads[index].ConversationID = binding.ConversationID
			} else {
				threads[index].BoundByOther = true
			}
		}
	}
	return nil
}

func (s *Service) StartThread(ctx context.Context, input StartThreadInput) (Thread, error) {
	cwd := strings.TrimSpace(input.Cwd)
	if cwd == "" {
		cwd = strings.TrimSpace(os.Getenv("LAZYMIND_CODEX_DEFAULT_CWD"))
		if cwd == "" {
			var err error
			cwd, err = os.Getwd()
			if err != nil {
				return Thread{}, err
			}
		}
	}
	var response struct {
		Thread Thread `json:"thread"`
	}
	if err := s.client.Call(ctx, "thread/start", map[string]any{
		"cwd":               cwd,
		"serviceName":       "lazymind",
		"approvalPolicy":    "on-request",
		"approvalsReviewer": "user",
		"sandbox":           "workspace-write",
	}, &response); err != nil {
		return Thread{}, err
	}
	canonical, valid := canonicalThread(response.Thread)
	if !valid {
		return Thread{}, errors.New("codex thread/start returned invalid thread")
	}
	response.Thread = canonical
	response.Thread.CreatedByLazyMind = true
	response.Thread.Available = true
	s.mu.Lock()
	s.loaded[response.Thread.ID] = s.client.Generation()
	s.mu.Unlock()
	return response.Thread, nil
}

func (s *Service) Bind(ctx context.Context, input BindInput) (orm.ExternalAgentBinding, error) {
	if err := validateProvider(input.Provider); err != nil {
		return orm.ExternalAgentBinding{}, err
	}
	var existing orm.ExternalAgentBinding
	err := s.db.WithContext(ctx).
		Where("provider = ? AND provider_thread_id = ?", ProviderCodex, input.ProviderThreadID).
		First(&existing).Error
	if err == nil {
		if existing.CreatedByUserID != input.CreatedByUserID {
			return orm.ExternalAgentBinding{}, ErrThreadBusy
		}
		return existing, nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return orm.ExternalAgentBinding{}, err
	}
	now := time.Now()
	binding := orm.ExternalAgentBinding{
		ID:                uuid.NewString(),
		ConversationID:    input.ConversationID,
		Provider:          ProviderCodex,
		ProviderThreadID:  input.ProviderThreadID,
		ManagedByLazyMind: input.CreatedByLazyMind,
		CreatedByUserID:   input.CreatedByUserID,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := s.db.WithContext(ctx).Create(&binding).Error; err != nil {
		if lookupErr := s.db.WithContext(ctx).
			Where("provider = ? AND provider_thread_id = ?", ProviderCodex, input.ProviderThreadID).
			First(&existing).Error; lookupErr == nil {
			if existing.CreatedByUserID != input.CreatedByUserID {
				return orm.ExternalAgentBinding{}, ErrThreadBusy
			}
			return existing, nil
		}
		return orm.ExternalAgentBinding{}, err
	}
	return binding, nil
}

func (s *Service) BindingByConversation(ctx context.Context, conversationID, actorUserID string) (orm.ExternalAgentBinding, error) {
	var binding orm.ExternalAgentBinding
	if err := s.db.WithContext(ctx).
		Where("conversation_id = ?", conversationID).
		First(&binding).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return orm.ExternalAgentBinding{}, ErrBindingNotFound
		}
		return orm.ExternalAgentBinding{}, err
	}
	if binding.CreatedByUserID != actorUserID {
		return orm.ExternalAgentBinding{}, ErrThreadBusy
	}
	return binding, nil
}

func (s *Service) BindingByThread(ctx context.Context, provider, threadID, actorUserID string) (orm.ExternalAgentBinding, error) {
	if err := validateProvider(provider); err != nil {
		return orm.ExternalAgentBinding{}, err
	}
	var binding orm.ExternalAgentBinding
	if err := s.db.WithContext(ctx).
		Where("provider = ? AND provider_thread_id = ?", ProviderCodex, threadID).
		First(&binding).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return orm.ExternalAgentBinding{}, ErrBindingNotFound
		}
		return orm.ExternalAgentBinding{}, err
	}
	if binding.CreatedByUserID != actorUserID {
		return orm.ExternalAgentBinding{}, ErrThreadBusy
	}
	return binding, nil
}

func (s *Service) StartOrSteer(ctx context.Context, input ChatInput) (Execution, error) {
	if err := validateProvider(input.Provider); err != nil {
		return Execution{}, err
	}
	requestKey := input.Provider + "\x00" + input.RequestID
	s.mu.Lock()
	if running := s.byRequest[requestKey]; running != nil {
		record, query, _, _ := running.snapshot()
		s.mu.Unlock()
		if !runInputMatches(record, query, input) {
			return Execution{}, ErrOperationMismatch
		}
		return running.execution(), nil
	}
	s.mu.Unlock()

	if completed, ok, err := s.completedExecution(ctx, input); err != nil || ok {
		return completed, err
	}
	binding, err := s.BindingByConversation(ctx, input.ConversationID, input.ActorUserID)
	if err != nil {
		return Execution{}, err
	}
	if binding.Provider != ProviderCodex || binding.ProviderThreadID != input.ProviderThreadID {
		return Execution{}, ErrBindingNotFound
	}

	s.mu.Lock()
	active := s.byThread[input.ProviderThreadID]
	s.mu.Unlock()
	if active != nil {
		record, _, _, _ := active.snapshot()
		if record.ActorUserID != input.ActorUserID {
			return Execution{}, ErrThreadBusy
		}
		return Execution{}, ErrThreadBusy
	}
	forkBeforeStart := false
	if err := s.requireThreadAvailable(ctx, input.ProviderThreadID); err != nil {
		if !errors.Is(err, ErrUnmanagedActive) {
			return Execution{}, err
		}
		forkBeforeStart = true
	}

	now := time.Now()
	record := orm.ExternalAgentRun{
		ID:               uuid.NewString(),
		RequestID:        input.RequestID,
		ConversationID:   input.ConversationID,
		HistoryID:        input.HistoryID,
		Provider:         ProviderCodex,
		ProviderThreadID: input.ProviderThreadID,
		ActorUserID:      input.ActorUserID,
		Action:           "start",
		Status:           runStatusStarting,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
		return Execution{}, err
	}
	run := newManagedRun(record, input.Query, input.Seq)
	s.mu.Lock()
	if active = s.byThread[input.ProviderThreadID]; active != nil {
		s.mu.Unlock()
		_ = s.db.WithContext(ctx).Delete(&record).Error
		return Execution{}, ErrThreadBusy
	}
	s.byThread[input.ProviderThreadID] = run
	s.byRequest[requestKey] = run
	s.mu.Unlock()
	if err := s.createHistory(record, input.Query, "", input.Seq); err != nil {
		s.detachActive(run)
		_ = s.db.WithContext(ctx).Delete(&record).Error
		return Execution{}, err
	}
	go func() {
		if forkBeforeStart {
			if err := s.forkBusyThread(context.Background(), run); err != nil {
				s.failRun(run, fmt.Errorf("continue in fork failed: %w", err))
				return
			}
		}
		s.startRun(run)
	}()
	return run.execution(), nil
}

func runInputMatches(record orm.ExternalAgentRun, query string, input ChatInput) bool {
	return record.Provider == input.Provider &&
		record.ProviderThreadID == input.ProviderThreadID &&
		record.ConversationID == input.ConversationID &&
		record.ActorUserID == input.ActorUserID &&
		query == input.Query
}

func (s *Service) completedExecution(ctx context.Context, input ChatInput) (Execution, bool, error) {
	var record orm.ExternalAgentRun
	err := s.db.WithContext(ctx).
		Where("provider = ? AND request_id = ?", input.Provider, input.RequestID).
		First(&record).Error
	if err == gorm.ErrRecordNotFound {
		return Execution{}, false, nil
	}
	if err != nil {
		return Execution{}, false, err
	}
	if record.ActorUserID != input.ActorUserID {
		return Execution{}, true, ErrThreadBusy
	}
	var history orm.ChatHistory
	if err := s.db.WithContext(ctx).Where("id = ?", record.HistoryID).First(&history).Error; err != nil {
		return Execution{}, true, err
	}
	if !runInputMatches(record, history.Content, input) {
		return Execution{}, true, ErrOperationMismatch
	}
	if record.Status == runStatusStarting || record.Status == runStatusRunning || record.Status == runStatusWaiting {
		s.mu.Lock()
		active := s.byRequest[input.Provider+"\x00"+input.RequestID]
		if active == nil {
			active = s.byThread[record.ProviderThreadID]
		}
		s.mu.Unlock()
		if active != nil {
			return active.execution(), true, nil
		}
		recovered, recoverErr := s.reattachOrFinalizeActiveRun(ctx, record)
		if recoverErr != nil {
			return Execution{}, true, recoverErr
		}
		return recovered, true, nil
	}
	eventType := terminalEventType(record.Status)
	if record.ControlRelease == "" || record.ControlRelease == controlReleasePending {
		execution, releaseErr := s.terminalExecution(record, eventType, "")
		return execution, true, releaseErr
	}
	events := make(chan Event, 1)
	events <- Event{
		Type: eventType, Provider: record.Provider, ThreadID: record.ProviderThreadID,
		TurnID: record.ProviderTurnID, RunID: record.ID, Message: history.Result,
		Status: record.Status, ControlRelease: record.ControlRelease,
		ControlError: record.ControlError, Terminal: true,
	}
	close(events)
	return Execution{RunID: record.ID, HistoryID: record.HistoryID, Seq: history.Seq, Events: events}, true, nil
}

func terminalEventType(status string) string {
	switch status {
	case runStatusFailed:
		return "turn_failed"
	case runStatusInterrupted:
		return "turn_interrupted"
	default:
		return "turn_completed"
	}
}

func (s *Service) recoverActiveRuns() error {
	var records []orm.ExternalAgentRun
	var queryErr error
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		queryErr = s.db.WithContext(ctx).
			Where(
				"provider = ? AND (status IN ? OR control_release IN ?)",
				ProviderCodex,
				[]string{runStatusStarting, runStatusRunning, runStatusWaiting},
				[]string{controlReleasePending, controlReleaseFailed},
			).
			Find(&records).Error
		cancel()
		if queryErr == nil {
			break
		}
		if attempt < 2 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if queryErr != nil {
		corelog.Logger.Warn().Err(queryErr).Msg("external agent active run recovery query failed")
		return fmt.Errorf("recover external agent ownership: %w", queryErr)
	}
	for _, record := range records {
		if record.ControlRelease == controlReleasePending {
			run, created := s.attachTerminalRun(record)
			if created {
				go s.finishTerminal(run, Event{
					Type: terminalEventType(record.Status), Provider: record.Provider,
					ThreadID: record.ProviderThreadID, TurnID: record.ProviderTurnID,
					RunID: record.ID, Status: record.Status, Terminal: true,
				})
			}
			continue
		}
		if record.ControlRelease == controlReleaseFailed {
			run, created := s.attachTerminalRun(record)
			if created {
				_, _, answer, _ := run.snapshot()
				run.broadcast(Event{
					Type: terminalEventType(record.Status), Provider: record.Provider,
					ThreadID: record.ProviderThreadID, TurnID: record.ProviderTurnID,
					RunID: record.ID, Message: answer, Status: record.Status,
					ControlRelease: controlReleaseFailed, ControlError: record.ControlError,
					Terminal: true,
				})
			}
			continue
		}
		run, created := s.attachRecoveredRun(record, false)
		if created {
			go s.recoverPersistedRun(record, run)
		}
	}
	return nil
}

func (s *Service) recoverPersistedRun(record orm.ExternalAgentRun, run *managedRun) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := s.reconcileRecoveredRun(ctx, record, run)
		cancel()
		if err == nil {
			return
		}
		if !errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, context.Canceled) {
			corelog.Logger.Warn().
				Err(err).
				Str("run_id", record.ID).
				Str("thread_id", record.ProviderThreadID).
				Msg("external agent active run recovery failed")
			return
		}
		corelog.Logger.Info().
			Str("run_id", record.ID).
			Str("thread_id", record.ProviderThreadID).
			Msg("waiting for Codex app-server before recovering active run")
	}
}

func (s *Service) reconcileRecoveredRun(
	ctx context.Context,
	record orm.ExternalAgentRun,
	run *managedRun,
) error {
	thread, err := s.readThread(ctx, record.ProviderThreadID, true, "")
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return err
		}
		s.interruptManagedRun(run, "Codex thread unavailable after restart; please retry")
		return nil
	}
	if record.Status == runStatusWaiting {
		s.interruptManagedRun(run, "Interactive request lost after restart; please retry")
		return nil
	}
	if turn, ok := completedProviderTurn(thread.Turns, record.ProviderTurnID); ok {
		if turn.Answer != "" {
			run.setAnswer(turn.Answer)
		}
		params, _ := json.Marshal(map[string]any{
			"threadId": record.ProviderThreadID,
			"turn": map[string]any{
				"id": record.ProviderTurnID, "status": turn.Status, "error": turn.Error,
			},
		})
		s.completeRun(run, rpcMessage{Method: "lazymind/recovered", Params: params})
		return nil
	}
	if thread.Status.Type == "idle" {
		s.interruptManagedRun(run, "Codex turn ended while LazyMind was offline")
		return nil
	}
	if run.finished() {
		return nil
	}
	run.broadcast(Event{
		Type: "run_attached", Provider: ProviderCodex, ThreadID: record.ProviderThreadID,
		TurnID: record.ProviderTurnID, RunID: record.ID, Status: record.Status,
		Message: "Recovered active Codex run after restart",
	})
	go s.watchRun(run)
	return nil
}

func (s *Service) reattachOrFinalizeActiveRun(ctx context.Context, record orm.ExternalAgentRun) (Execution, error) {
	s.mu.Lock()
	if s.byThread == nil {
		s.byThread = make(map[string]*managedRun)
	}
	if s.byRequest == nil {
		s.byRequest = make(map[string]*managedRun)
	}
	if existing := s.byThread[record.ProviderThreadID]; existing != nil {
		s.mu.Unlock()
		return existing.execution(), nil
	}
	s.mu.Unlock()

	thread, err := s.readThread(ctx, record.ProviderThreadID, true, "")
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(err, context.Canceled) {
			return Execution{}, err
		}
		if persistErr := s.markRunInterrupted(record, "Codex thread unavailable after restart; please retry"); persistErr != nil {
			return Execution{}, persistErr
		}
		record.Status = runStatusInterrupted
		record.ControlRelease = controlReleasePending
		return s.terminalExecution(record, "turn_interrupted", "Codex thread unavailable after restart; please retry")
	}
	if record.Status == runStatusWaiting {
		// Pending Codex RPC request IDs are process-local and cannot be resumed.
		if err := s.markRunInterrupted(record, "Interactive request lost after restart; please retry"); err != nil {
			return Execution{}, err
		}
		record.Status = runStatusInterrupted
		record.ControlRelease = controlReleasePending
		return s.terminalExecution(record, "turn_interrupted", "Interactive request lost after restart; please retry")
	}
	if turn, ok := completedProviderTurn(thread.Turns, record.ProviderTurnID); ok {
		answer := turn.Answer
		status := runStatusCompleted
		eventType := "turn_completed"
		if turn.Status == runStatusFailed {
			status = runStatusFailed
			eventType = "turn_failed"
		} else if turn.Status == runStatusInterrupted {
			status = runStatusInterrupted
			eventType = "turn_interrupted"
		}
		if err := s.persistControlRelease(
			record.ID, status, controlReleasePending, "", nil,
		); err != nil {
			return Execution{}, err
		}
		if answer != "" {
			var history orm.ChatHistory
			if s.db.WithContext(ctx).Where("id = ?", record.HistoryID).First(&history).Error == nil {
				_ = s.updateHistory(record, history.Content, answer, history.Seq)
			}
		}
		record.Status = status
		record.ControlRelease = controlReleasePending
		return s.terminalExecution(record, eventType, answer)
	}
	if thread.Status.Type == "idle" {
		if err := s.markRunInterrupted(record, "Codex turn ended while LazyMind was offline"); err != nil {
			return Execution{}, err
		}
		record.Status = runStatusInterrupted
		record.ControlRelease = controlReleasePending
		return s.terminalExecution(record, "turn_interrupted", "Codex turn ended while LazyMind was offline")
	}
	var history orm.ChatHistory
	_ = s.db.WithContext(ctx).Where("id = ?", record.HistoryID).First(&history).Error
	run := newManagedRun(record, history.Content, history.Seq)
	if history.Result != "" {
		run.setAnswer(history.Result)
	}
	s.mu.Lock()
	if existing := s.byThread[record.ProviderThreadID]; existing != nil {
		s.mu.Unlock()
		return existing.execution(), nil
	}
	s.byThread[record.ProviderThreadID] = run
	s.byRequest[record.Provider+"\x00"+record.RequestID] = run
	s.mu.Unlock()
	run.broadcast(Event{
		Type: "run_attached", Provider: ProviderCodex, ThreadID: record.ProviderThreadID,
		TurnID: record.ProviderTurnID, RunID: record.ID, Status: record.Status,
		Message: "Recovered active Codex run after restart",
	})
	go s.watchRun(run)
	return run.execution(), nil
}

func (s *Service) markRunInterrupted(record orm.ExternalAgentRun, message string) error {
	if err := s.persistControlRelease(
		record.ID, runStatusInterrupted, controlReleasePending, "",
		map[string]any{"error_message": message},
	); err != nil {
		return err
	}
	var history orm.ChatHistory
	if s.db.Where("id = ?", record.HistoryID).First(&history).Error == nil && message != "" {
		_ = s.updateHistory(record, history.Content, message, history.Seq)
	}
	return nil
}

func (s *Service) terminalExecution(record orm.ExternalAgentRun, eventType, message string) (Execution, error) {
	if record.ControlRelease == "" {
		if err := s.persistControlRelease(
			record.ID, record.Status, controlReleasePending, "", nil,
		); err != nil {
			return Execution{}, err
		}
		record.ControlRelease = controlReleasePending
		record.ControlError = ""
	}
	if record.ControlRelease == controlReleasePending {
		return s.resumeTerminalRelease(record, eventType, message)
	}
	var history orm.ChatHistory
	_ = s.db.Where("id = ?", record.HistoryID).First(&history).Error
	if message == "" {
		message = history.Result
	}
	events := make(chan Event, 1)
	events <- Event{
		Type: eventType, Provider: record.Provider, ThreadID: record.ProviderThreadID,
		TurnID: record.ProviderTurnID, RunID: record.ID, Message: message,
		Status: record.Status, ControlRelease: record.ControlRelease,
		ControlError: record.ControlError, Terminal: true,
	}
	close(events)
	return Execution{RunID: record.ID, HistoryID: record.HistoryID, Seq: history.Seq, Events: events}, nil
}

func (s *Service) resumeTerminalRelease(
	record orm.ExternalAgentRun,
	eventType, message string,
) (Execution, error) {
	run, created := s.attachTerminalRun(record)
	live, _, answer, seq := run.snapshot()
	if message == "" {
		message = answer
	}
	events, cancel := run.subscribeCancelable()
	if created {
		go s.finishTerminal(run, Event{
			Type: eventType, Provider: record.Provider,
			ThreadID: record.ProviderThreadID, TurnID: record.ProviderTurnID,
			RunID: record.ID, Message: message, Status: record.Status, Terminal: true,
		})
	}
	return Execution{
		RunID: live.ID, HistoryID: live.HistoryID,
		Seq: seq, Events: events, Cancel: cancel,
	}, nil
}

func (s *Service) attachTerminalRun(record orm.ExternalAgentRun) (*managedRun, bool) {
	return s.attachRecoveredRun(record, true)
}

func (s *Service) attachRecoveredRun(
	record orm.ExternalAgentRun,
	finishing bool,
) (*managedRun, bool) {
	var history orm.ChatHistory
	_ = s.db.Where("id = ?", record.HistoryID).First(&history).Error
	s.mu.Lock()
	if s.byThread == nil {
		s.byThread = make(map[string]*managedRun)
	}
	if s.byRequest == nil {
		s.byRequest = make(map[string]*managedRun)
	}
	if existing := s.byThread[record.ProviderThreadID]; existing != nil {
		s.mu.Unlock()
		return existing, false
	}
	run := newManagedRun(record, history.Content, history.Seq)
	run.finishing = finishing
	if history.Result != "" {
		run.setAnswer(history.Result)
	}
	s.byThread[record.ProviderThreadID] = run
	s.byRequest[record.Provider+"\x00"+record.RequestID] = run
	s.mu.Unlock()
	return run, true
}

func (s *Service) SnapshotConversation(ctx context.Context, conversationID, actorUserID string) (RunSnapshot, error) {
	binding, err := s.BindingByConversation(ctx, conversationID, actorUserID)
	if err != nil {
		return RunSnapshot{}, err
	}
	snapshot := RunSnapshot{
		ConversationID: conversationID,
		Status:         "idle",
	}
	s.mu.Lock()
	active := s.byThread[binding.ProviderThreadID]
	var pending *ExternalRequest
	for _, request := range s.requests {
		if request.Run != nil {
			record, _, _, _ := request.Run.snapshot()
			if record.ProviderThreadID == binding.ProviderThreadID {
				value := request.View
				pending = &value
				break
			}
		}
	}
	s.mu.Unlock()
	if active != nil {
		record, _, answer, _ := active.snapshot()
		snapshot.RunID = record.ID
		snapshot.Status = projectedRunStatus(record)
		snapshot.Answer = truncateRunes(answer, 16000)
		snapshot.PendingRequest = pending
		snapshot.ControlRelease = record.ControlRelease
		return snapshot, nil
	}
	var record orm.ExternalAgentRun
	err = s.db.WithContext(ctx).
		Where("conversation_id = ?", conversationID).
		Order("created_at DESC").
		First(&record).Error
	if err == gorm.ErrRecordNotFound {
		return snapshot, nil
	}
	if err != nil {
		return RunSnapshot{}, err
	}
	snapshot.RunID = record.ID
	snapshot.Status = projectedRunStatus(record)
	snapshot.ControlRelease = record.ControlRelease
	var history orm.ChatHistory
	if s.db.WithContext(ctx).Where("id = ?", record.HistoryID).First(&history).Error == nil {
		snapshot.Answer = truncateRunes(history.Result, 16000)
	}
	return snapshot, nil
}

func projectedRunStatus(record orm.ExternalAgentRun) string {
	if record.ControlRelease == controlReleasePending {
		return runStatusReleasing
	}
	return record.Status
}

func (s *Service) requireThreadAvailable(ctx context.Context, threadID string) error {
	thread, err := s.readThread(ctx, threadID, false, "")
	if err != nil {
		return err
	}
	if thread.Available {
		return nil
	}
	return ErrUnmanagedActive
}

func (s *Service) startRun(run *managedRun) {
	record, query, _, _ := run.snapshot()
	ctx := context.Background()
	s.mu.Lock()
	loaded := s.loaded[record.ProviderThreadID] == s.client.Generation()
	s.mu.Unlock()
	if !loaded {
		var resumed struct {
			Thread Thread `json:"thread"`
		}
		if err := s.client.Call(ctx, "thread/resume", map[string]any{"threadId": record.ProviderThreadID}, &resumed); err != nil {
			if !isActiveWriterError(err) {
				s.failRun(run, err)
				return
			}
			if forkErr := s.forkBusyThread(ctx, run); forkErr != nil {
				s.failRun(run, fmt.Errorf("continue in fork failed: original=%v; fork=%w", err, forkErr))
				return
			}
			record, query, _, _ = run.snapshot()
		} else {
			s.mu.Lock()
			s.loaded[record.ProviderThreadID] = s.client.Generation()
			s.mu.Unlock()
		}
	}
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	startTurn := func() error {
		return s.client.Call(ctx, "turn/start", map[string]any{
			"threadId":            record.ProviderThreadID,
			"clientUserMessageId": record.RequestID,
			"input":               []map[string]string{{"type": "text", "text": query}},
		}, &started)
	}
	if err := startTurn(); err != nil {
		if !isActiveWriterError(err) {
			s.failRun(run, err)
			return
		}
		if forkErr := s.forkBusyThread(ctx, run); forkErr != nil {
			s.failRun(run, fmt.Errorf("continue in fork failed: original=%v; fork=%w", err, forkErr))
			return
		}
		record, query, _, _ = run.snapshot()
		started = struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}{}
		if err := startTurn(); err != nil {
			s.failRun(run, err)
			return
		}
	}
	turnID := strings.TrimSpace(started.Turn.ID)
	if turnID == "" {
		s.blockStartedRun(run, errors.New("codex turn/start returned no turn id"))
		return
	}
	now := time.Now()
	updated := s.db.Model(&orm.ExternalAgentRun{}).
		Where("id = ? AND status = ?", record.ID, runStatusStarting).
		Updates(map[string]any{
			"provider_turn_id": turnID,
			"status":           runStatusRunning,
			"updated_at":       now,
		})
	if updated.Error != nil || updated.RowsAffected != 1 {
		err := updated.Error
		if err == nil {
			err = errors.New("external agent run changed before turn/start commit")
		}
		s.blockStartedRun(run, err)
		return
	}
	run.setTurn(turnID)
	run.broadcast(Event{
		Type: "turn_started", Provider: ProviderCodex, ThreadID: record.ProviderThreadID,
		TurnID: turnID, RunID: record.ID, Status: runStatusRunning,
	})
	go s.watchRun(run)
}

func (s *Service) blockStartedRun(run *managedRun, err error) {
	record, _, _, _ := run.snapshot()
	corelog.Logger.Error().Err(err).
		Str("run_id", record.ID).
		Str("thread_id", record.ProviderThreadID).
		Msg("external agent turn started but run state was not persisted")
	run.broadcast(Event{
		Type: "control_error", Provider: record.Provider,
		ThreadID: record.ProviderThreadID, RunID: record.ID,
		Status:  record.Status,
		Summary: "Codex 已启动，但运行状态尚未确认；正在等待恢复",
	})
}

func isActiveWriterError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already has an active writer")
}

func (s *Service) forkBusyThread(ctx context.Context, run *managedRun) error {
	record, _, _, _ := run.snapshot()
	var forked struct {
		Thread Thread `json:"thread"`
	}
	if err := s.client.Call(ctx, "thread/fork", map[string]any{
		"threadId": record.ProviderThreadID,
	}, &forked); err != nil {
		return err
	}
	newThreadID := strings.TrimSpace(forked.Thread.ID)
	if newThreadID == "" {
		return fmt.Errorf("request failed: codex thread/fork returned no thread id")
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		binding := tx.Model(&orm.ExternalAgentBinding{}).
			Where("conversation_id = ? AND provider_thread_id = ?", record.ConversationID, record.ProviderThreadID).
			Updates(map[string]any{
				"provider_thread_id":  newThreadID,
				"managed_by_lazymind": true,
				"updated_at":          time.Now(),
			})
		if binding.Error != nil {
			return binding.Error
		}
		if binding.RowsAffected != 1 {
			return ErrBindingNotFound
		}
		return tx.Model(&orm.ExternalAgentRun{}).Where("id = ?", record.ID).
			Update("provider_thread_id", newThreadID).Error
	}); err != nil {
		_, _ = s.releaseThreadWithRetry(newThreadID)
		return err
	}

	run.setThread(newThreadID)
	s.mu.Lock()
	if s.byThread[record.ProviderThreadID] == run {
		delete(s.byThread, record.ProviderThreadID)
	}
	s.byThread[newThreadID] = run
	delete(s.loaded, record.ProviderThreadID)
	s.loaded[newThreadID] = s.client.Generation()
	s.mu.Unlock()
	run.broadcast(Event{
		Type: "thread_forked", Provider: ProviderCodex, ThreadID: newThreadID,
		RunID: record.ID, Status: runStatusStarting,
		Message: "原会话正在 Codex Desktop 中使用，已自动创建原生续接会话",
	})
	corelog.Logger.Info().
		Str("source_thread_id", record.ProviderThreadID).
		Str("thread_id", newThreadID).
		Str("run_id", record.ID).
		Msg("external agent continued in fork because source thread has an active writer")
	return nil
}

func (s *Service) watchRun(run *managedRun) {
	ticker := time.NewTicker(managedRunPollPeriod)
	defer ticker.Stop()
	for range ticker.C {
		if run.finished() {
			return
		}
		record, _, _, _ := run.snapshot()
		thread, err := s.readThread(context.Background(), record.ProviderThreadID, true, "")
		if err != nil {
			continue
		}
		turn, ok := completedProviderTurn(thread.Turns, record.ProviderTurnID)
		if !ok {
			continue
		}
		if turn.Answer != "" {
			run.setAnswer(turn.Answer)
		}
		params, _ := json.Marshal(map[string]any{
			"threadId": record.ProviderThreadID,
			"turn": map[string]any{
				"id":     record.ProviderTurnID,
				"status": turn.Status,
				"error":  turn.Error,
			},
		})
		s.completeRun(run, rpcMessage{
			Method: "lazymind/thread-read-reconciled",
			Params: params,
		})
		return
	}
}

type providerTurnResult struct {
	Status string
	Answer string
	Error  any
}

func completedProviderTurn(raw json.RawMessage, turnID string) (providerTurnResult, bool) {
	var turns []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  any    `json:"error"`
		Items  []struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Phase string `json:"phase"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &turns) != nil {
		return providerTurnResult{}, false
	}
	for _, turn := range turns {
		if turn.ID != turnID || (turn.Status != runStatusCompleted && turn.Status != runStatusFailed && turn.Status != runStatusInterrupted) {
			continue
		}
		answer := ""
		for _, item := range turn.Items {
			if item.Type != "agentMessage" || strings.TrimSpace(item.Text) == "" {
				continue
			}
			answer = item.Text
			if item.Phase == "final_answer" {
				break
			}
		}
		return providerTurnResult{Status: turn.Status, Answer: answer, Error: turn.Error}, true
	}
	return providerTurnResult{}, false
}

func (s *Service) failRun(run *managedRun, err error) {
	if !run.beginFinish() {
		return
	}
	record, query, _, seq := run.snapshot()
	record.Status = runStatusFailed
	record.ErrorMessage = err.Error()
	record.ControlRelease = controlReleasePending
	record.ControlError = ""
	record.UpdatedAt = time.Now()
	if persistErr := s.persistControlRelease(
		record.ID, record.Status, record.ControlRelease, record.ControlError,
		map[string]any{"error_message": record.ErrorMessage},
	); persistErr != nil {
		s.blockTerminalOnPersistenceFailure(run, record, persistErr)
		return
	}
	run.setTerminalState(record.Status, record.ControlRelease, record.ControlError)
	_ = s.updateHistory(record, query, "Codex 执行失败："+err.Error(), seq)
	event := Event{
		Type: "turn_failed", Provider: record.Provider, ThreadID: record.ProviderThreadID,
		TurnID: record.ProviderTurnID, RunID: record.ID, Message: record.ErrorMessage,
		Status: record.Status, Terminal: true,
	}
	go s.finishTerminal(run, event)
}

func (s *Service) consumeClientEvents() {
	for message := range s.client.Events() {
		if message.Method == "" {
			continue
		}
		if message.Method == "lazymind/transport/disconnected" {
			s.notifyActiveRunsDisconnected()
			continue
		}
		threadID := threadIDFromParams(message.Params)
		s.mu.Lock()
		run := s.byThread[threadID]
		s.mu.Unlock()
		if run == nil {
			continue
		}
		if message.isServerRequest() {
			s.handleServerRequest(run, message)
			continue
		}
		s.handleNotification(run, message)
	}
}

func (s *Service) notifyActiveRunsDisconnected() {
	s.mu.Lock()
	runs := make([]*managedRun, 0, len(s.byThread))
	waiting := make(map[*managedRun]struct{}, len(s.requests))
	for _, run := range s.byThread {
		runs = append(runs, run)
	}
	for _, request := range s.requests {
		waiting[request.Run] = struct{}{}
	}
	s.mu.Unlock()
	for _, run := range runs {
		if _, awaitingResponse := waiting[run]; awaitingResponse {
			s.interruptManagedRun(
				run,
				"Codex 在等待交互时重启，请重新发送上一条消息",
			)
			continue
		}
		record, _, _, _ := run.snapshot()
		run.broadcast(Event{
			Type: "progress", Provider: record.Provider,
			ThreadID: record.ProviderThreadID, TurnID: record.ProviderTurnID,
			RunID: record.ID, Status: record.Status,
			Summary: "Codex 连接中断，正在自动重连",
		})
	}
}

func (s *Service) interruptManagedRun(run *managedRun, message string) {
	if !run.beginFinish() {
		return
	}
	record, _, _, _ := run.snapshot()
	record.Status = runStatusInterrupted
	record.ControlRelease = controlReleasePending
	if err := s.markRunInterrupted(record, message); err != nil {
		s.blockTerminalOnPersistenceFailure(run, record, err)
		return
	}
	run.setTerminalState(record.Status, record.ControlRelease, "")
	event := Event{
		Type: "turn_interrupted", Provider: record.Provider,
		ThreadID: record.ProviderThreadID, TurnID: record.ProviderTurnID,
		RunID: record.ID, Message: message,
		Status: runStatusInterrupted, Terminal: true,
	}
	go s.finishTerminal(run, event)
}

func threadIDFromParams(params json.RawMessage) string {
	var envelope struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(params, &envelope)
	return envelope.ThreadID
}

func (s *Service) handleNotification(run *managedRun, message rpcMessage) {
	record, _, _, _ := run.snapshot()
	base := Event{
		Provider: ProviderCodex, ThreadID: record.ProviderThreadID, TurnID: record.ProviderTurnID,
		RunID: record.ID,
	}
	switch message.Method {
	case "item/agentMessage/delta":
		var params struct {
			TurnID string `json:"turnId"`
			Delta  string `json:"delta"`
		}
		_ = json.Unmarshal(message.Params, &params)
		base.Type = "agent_message_delta"
		base.TurnID = params.TurnID
		base.Delta = params.Delta
		run.appendAnswer(params.Delta)
		run.broadcast(base)
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		var params struct {
			TurnID string `json:"turnId"`
			Delta  string `json:"delta"`
		}
		_ = json.Unmarshal(message.Params, &params)
		base.Type, base.TurnID, base.Summary = "progress", params.TurnID, params.Delta
		run.broadcast(base)
	case "item/started", "item/completed":
		s.handleItemEvent(run, message, base)
	case "item/fileChange/patchUpdated":
		var params struct {
			ItemID  string          `json:"itemId"`
			Changes json.RawMessage `json:"changes"`
		}
		_ = json.Unmarshal(message.Params, &params)
		run.setFileChanges(params.ItemID, params.Changes)
		base.Type, base.Summary = "artifact_available", "代码变更已更新"
		run.broadcast(base)
	case "turn/plan/updated":
		base.Type, base.Summary = "progress", "Codex 已更新执行计划"
		run.broadcast(base)
	case "turn/diff/updated":
		base.Type, base.Summary = "artifact_available", "代码变更已更新"
		run.broadcast(base)
	case "error":
		var params struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(message.Params, &params)
		base.Type, base.Summary = "progress", params.Error.Message
		run.broadcast(base)
	case "turn/completed":
		s.completeRun(run, message)
	}
}

func (s *Service) handleItemEvent(run *managedRun, message rpcMessage, event Event) {
	var params struct {
		TurnID string `json:"turnId"`
		Item   struct {
			Type    string          `json:"type"`
			Text    string          `json:"text"`
			Phase   string          `json:"phase"`
			Command string          `json:"command"`
			Status  string          `json:"status"`
			ID      string          `json:"id"`
			Changes json.RawMessage `json:"changes"`
		} `json:"item"`
	}
	_ = json.Unmarshal(message.Params, &params)
	event.TurnID = params.TurnID
	switch params.Item.Type {
	case "agentMessage":
		if message.Method == "item/completed" {
			run.setAnswer(params.Item.Text)
		}
	case "commandExecution":
		event.Type = "progress"
		if message.Method == "item/started" {
			event.Summary = "正在执行命令：" + params.Item.Command
		} else {
			event.Summary = "命令执行" + providerItemStatus(params.Item.Status)
		}
		run.broadcast(event)
	case "fileChange":
		run.setFileChanges(params.Item.ID, params.Item.Changes)
		event.Type = "artifact_available"
		if message.Method == "item/started" {
			event.Summary = "正在准备文件变更"
		} else {
			event.Summary = "文件变更" + providerItemStatus(params.Item.Status)
		}
		run.broadcast(event)
	case "reasoning":
		if message.Method == "item/started" {
			event.Type, event.Summary = "progress", "Codex 正在分析"
			run.broadcast(event)
		}
	}
}

func providerItemStatus(status string) string {
	switch status {
	case "completed":
		return "完成"
	case "failed":
		return "失败"
	case "declined":
		return "被拒绝"
	default:
		return "结束"
	}
}

func (s *Service) completeRun(run *managedRun, message rpcMessage) {
	if !run.beginFinish() {
		return
	}
	var params struct {
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(message.Params, &params)
	record, query, answer, seq := run.snapshot()
	record.ProviderTurnID = params.Turn.ID
	record.Status = runStatusCompleted
	eventType := "turn_completed"
	if params.Turn.Status == "interrupted" {
		record.Status, eventType = runStatusInterrupted, "turn_interrupted"
	}
	if params.Turn.Status == "failed" {
		record.Status, eventType = runStatusFailed, "turn_failed"
		if params.Turn.Error != nil {
			record.ErrorMessage = params.Turn.Error.Message
		}
	}
	record.UpdatedAt = time.Now()
	record.ControlRelease = controlReleasePending
	record.ControlError = ""
	messageText := answer
	if messageText == "" && record.ErrorMessage != "" {
		messageText = record.ErrorMessage
	}
	if err := s.persistControlRelease(
		record.ID, record.Status, record.ControlRelease, record.ControlError,
		map[string]any{
			"provider_turn_id": record.ProviderTurnID,
			"error_message":    record.ErrorMessage,
		},
	); err != nil {
		s.blockTerminalOnPersistenceFailure(run, record, err)
		return
	}
	run.setTerminalState(record.Status, record.ControlRelease, record.ControlError)
	_ = s.updateHistory(record, query, messageText, seq)
	event := Event{
		Type: eventType, Provider: record.Provider, ThreadID: record.ProviderThreadID,
		TurnID: record.ProviderTurnID, RunID: record.ID,
		Message: messageText, Status: record.Status, Terminal: true,
	}
	go s.finishTerminal(run, event)
}

func externalAgentHistory(record orm.ExternalAgentRun, query, answer string, seq int) orm.ChatHistory {
	now := time.Now()
	ext, _ := json.Marshal(map[string]any{"external_agent": map[string]any{
		"provider": record.Provider, "thread_id": record.ProviderThreadID,
		"turn_id": record.ProviderTurnID, "run_id": record.ID,
	}})
	return orm.ChatHistory{
		ID: record.HistoryID, Seq: seq, ConversationID: record.ConversationID,
		RawContent: query, Content: query, Result: answer, Ext: ext,
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}
}

func (s *Service) createHistory(record orm.ExternalAgentRun, query, answer string, seq int) error {
	history := externalAgentHistory(record, query, answer, seq)
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&history).Error; err != nil {
			return err
		}
		updates := map[string]any{
			"updated_at": history.UpdateTime,
			"chat_times": gorm.Expr("chat_times + ?", 1),
		}
		return tx.Model(&orm.Conversation{}).Where("id = ?", record.ConversationID).Updates(updates).Error
	})
}

func (s *Service) updateHistory(record orm.ExternalAgentRun, query, answer string, seq int) error {
	history := externalAgentHistory(record, query, answer, seq)
	result := s.db.Model(&orm.ChatHistory{}).Where("id = ?", record.HistoryID).Updates(map[string]any{
		"seq": history.Seq, "raw_content": history.RawContent, "content": history.Content,
		"result": history.Result, "ext": history.Ext, "update_time": history.UpdateTime,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("history not found: %s", record.HistoryID)
	}
	return s.db.Model(&orm.Conversation{}).Where("id = ?", record.ConversationID).
		Update("updated_at", history.UpdateTime).Error
}

func (s *Service) detachActive(run *managedRun) {
	record, _, _, _ := run.snapshot()
	s.mu.Lock()
	if s.byThread[record.ProviderThreadID] == run {
		delete(s.byThread, record.ProviderThreadID)
	}
	delete(s.loaded, record.ProviderThreadID)
	delete(s.byRequest, record.Provider+"\x00"+record.RequestID)
	for id, request := range s.requests {
		if request.Run == run {
			delete(s.requests, id)
		}
	}
	s.mu.Unlock()
}

func (s *Service) finishTerminal(run *managedRun, event Event) {
	record, _, _, _ := run.snapshot()
	if record.ControlRelease != controlReleasePending {
		if err := s.persistControlRelease(
			record.ID, event.Status, controlReleasePending, "", nil,
		); err != nil {
			s.blockTerminalOnPersistenceFailure(run, record, err)
			return
		}
		record.ControlRelease = controlReleasePending
		record.ControlError = ""
		run.setTerminalState(event.Status, record.ControlRelease, record.ControlError)
	}
	if s.client == nil {
		event.ControlRelease = "not_loaded"
		if err := s.persistControlRelease(
			record.ID, event.Status, event.ControlRelease, "", nil,
		); err != nil {
			s.blockTerminalOnPersistenceFailure(run, record, err)
			return
		}
		run.setTerminalState(event.Status, event.ControlRelease, "")
		run.broadcast(event)
		s.detachActive(run)
		return
	}
	status, err := s.releaseThreadWithRetry(record.ProviderThreadID)
	if err != nil {
		event.ControlRelease = controlReleaseFailed
		event.ControlError = err.Error()
		corelog.Logger.Error().
			Err(err).
			Str("thread_id", record.ProviderThreadID).
			Msg("external agent terminal control release failed")
	} else {
		event.ControlRelease = status
	}
	if persistErr := s.persistControlRelease(
		record.ID, event.Status, event.ControlRelease, event.ControlError, nil,
	); persistErr != nil {
		s.blockTerminalOnPersistenceFailure(run, record, persistErr)
		return
	}
	run.setTerminalState(event.Status, event.ControlRelease, event.ControlError)
	run.broadcast(event)
	if err == nil {
		s.detachActive(run)
	}
}

func (s *Service) blockTerminalOnPersistenceFailure(
	run *managedRun,
	record orm.ExternalAgentRun,
	err error,
) {
	run.setTerminalState(record.Status, controlReleaseFailed, err.Error())
	run.broadcast(Event{
		Type: "control_release_failed", Provider: record.Provider,
		ThreadID: record.ProviderThreadID, TurnID: record.ProviderTurnID,
		RunID: record.ID, Status: record.Status,
		ControlRelease: controlReleaseFailed, ControlError: err.Error(),
		Summary: "Core could not persist control-release state; retry release",
	})
	corelog.Logger.Error().Err(err).Str("run_id", record.ID).
		Msg("external agent terminal blocked by control release persistence failure")
}

func (s *Service) persistControlRelease(
	runID, status, controlRelease, controlError string,
	extra map[string]any,
) error {
	updates := map[string]any{
		"status":          status,
		"control_release": controlRelease,
		"control_error":   controlError,
		"updated_at":      time.Now(),
	}
	for key, value := range extra {
		updates[key] = value
	}
	result := s.db.Model(&orm.ExternalAgentRun{}).Where("id = ?", runID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Service) releaseThreadWithRetry(threadID string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		var response struct {
			Status string `json:"status"`
		}
		err := s.client.Call(ctx, "thread/unsubscribe", map[string]any{
			"threadId": threadID,
		}, &response)
		cancel()
		status := strings.TrimSpace(response.Status)
		if err == nil && (status == "unsubscribed" || status == "notSubscribed" || status == "notLoaded") {
			return status, nil
		}
		if err == nil {
			err = fmt.Errorf("unexpected thread/unsubscribe status %q", status)
		}
		lastErr = err
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
		}
	}
	return "", lastErr
}

func (s *Service) handleServerRequest(run *managedRun, message rpcMessage) {
	kind := ""
	switch message.Method {
	case "item/commandExecution/requestApproval":
		kind = "command_approval"
	case "item/fileChange/requestApproval":
		kind = "file_change_approval"
	case "item/permissions/requestApproval":
		kind = "permissions_approval"
	case "item/tool/requestUserInput":
		kind = "user_input"
	default:
		_ = s.client.RespondError(message.ID, -32601, "unsupported server request")
		return
	}
	record, _, _, _ := run.snapshot()
	payload := message.Params
	if kind == "file_change_approval" {
		payload = fileChangeApprovalPayload(run, payload)
	}
	summary := requestSummary(kind, payload)
	requestID := uuid.NewString()
	view, responses := projectExternalRequest(
		requestID, kind, summary, payload,
	)
	request := &pendingRequest{
		ID: requestID, RPCID: append(json.RawMessage(nil), message.ID...), Kind: kind,
		Payload: append(json.RawMessage(nil), payload...), View: view, Responses: responses,
		Run: run, ExpiresAt: time.Now().Add(defaultRequestWait),
	}
	s.mu.Lock()
	s.requests[request.ID] = request
	s.mu.Unlock()
	_ = s.db.Model(&orm.ExternalAgentRun{}).Where("id = ?", record.ID).
		Updates(map[string]any{"status": runStatusWaiting, "updated_at": time.Now()}).Error
	run.broadcast(Event{
		Type: "request_required", Provider: record.Provider, ThreadID: record.ProviderThreadID,
		TurnID: record.ProviderTurnID, RunID: record.ID,
		Request: &view, Summary: summary, Status: runStatusWaiting,
	})
	go s.expireRequest(request)
}

func fileChangeApprovalPayload(run *managedRun, raw json.RawMessage) json.RawMessage {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil || payload == nil {
		return raw
	}
	itemID, _ := payload["itemId"].(string)
	review := run.takeFileChange(itemID)
	if review.Truncated {
		payload["changesTruncated"] = true
	} else if len(review.Changes) > 0 {
		payload["changes"] = review.Changes
	}
	enriched, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return enriched
}

func requestSummary(kind string, params json.RawMessage) string {
	var details struct {
		Command   string `json:"command"`
		Reason    string `json:"reason"`
		Questions []struct {
			Question string `json:"question"`
		} `json:"questions"`
	}
	_ = json.Unmarshal(params, &details)
	if kind == "command_approval" && details.Command != "" {
		return boundedRequestSummary("Codex 请求执行命令：" + details.Command)
	}
	if details.Reason != "" {
		return boundedRequestSummary(details.Reason)
	}
	switch kind {
	case "command_approval":
		return "Codex 请求批准命令"
	case "file_change_approval":
		return "Codex 请求批准文件变更"
	case "permissions_approval":
		return "Codex 请求额外权限"
	case "user_input":
		if len(details.Questions) > 0 {
			return boundedRequestSummary(details.Questions[0].Question)
		}
		return "Codex 请求用户输入"
	}
	return "Codex 请求交互"
}

func boundedRequestSummary(value string) string {
	runes := []rune(value)
	if len(runes) <= 1000 {
		return value
	}
	return string(runes[:999]) + "…"
}

func (s *Service) expireRequest(request *pendingRequest) {
	timer := time.NewTimer(time.Until(request.ExpiresAt))
	defer timer.Stop()
	<-timer.C
	s.mu.Lock()
	if s.requests[request.ID] != request {
		s.mu.Unlock()
		return
	}
	response, ok := requestTimeoutResponse(request.Kind, request.Payload)
	var err error
	if ok {
		err = s.client.Respond(request.RPCID, response)
	} else {
		err = s.client.RespondError(
			request.RPCID,
			-32000,
			"interactive request timed out without a safe rejection decision",
		)
	}
	if err != nil {
		s.mu.Unlock()
		corelog.Logger.Warn().Err(err).
			Str("request_id", request.ID).
			Str("request_kind", request.Kind).
			Msg("external agent timed-out request response failed; retry scheduled")
		time.AfterFunc(5*time.Second, func() {
			s.expireRequest(request)
		})
		return
	}
	delete(s.requests, request.ID)
	s.mu.Unlock()
	summary := "交互请求超时，已自动拒绝"
	if !ok {
		summary = "交互请求超时，已向 Codex 返回错误"
	}
	s.markRequestResolved(request, summary)
}

func (s *Service) RespondRequest(input RequestResponse) error {
	s.mu.Lock()
	request := s.requests[input.RequestID]
	s.mu.Unlock()
	if request == nil {
		return ErrRequestNotFound
	}
	record, _, _, _ := request.Run.snapshot()
	if record.ActorUserID != input.ActorUserID {
		return ErrRequestNotFound
	}
	response, err := responseForExternalRequest(request, input)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.requests[input.RequestID] != request {
		s.mu.Unlock()
		return ErrRequestNotFound
	}
	if err := s.client.Respond(request.RPCID, response); err != nil {
		s.mu.Unlock()
		return err
	}
	delete(s.requests, input.RequestID)
	s.mu.Unlock()
	s.markRequestResolved(request, "Codex 交互请求已处理")
	return nil
}

func (s *Service) markRequestResolved(request *pendingRequest, summary string) {
	record, _, _, _ := request.Run.snapshot()
	result := s.db.Model(&orm.ExternalAgentRun{}).
		Where("id = ? AND status = ?", record.ID, runStatusWaiting).
		Updates(map[string]any{"status": runStatusRunning, "updated_at": time.Now()})
	if result.Error != nil {
		corelog.Logger.Warn().Err(result.Error).
			Str("request_id", request.ID).
			Str("run_id", record.ID).
			Msg("external agent request resolution persistence failed")
		return
	}
	if result.RowsAffected != 1 {
		return
	}
	request.Run.broadcast(Event{
		Type: "progress", Provider: record.Provider, ThreadID: record.ProviderThreadID,
		TurnID: record.ProviderTurnID, RunID: record.ID,
		Summary: summary, Status: runStatusRunning,
	})
}

func commandAvailableDecisions(payload json.RawMessage) ([]any, error) {
	var request map[string]json.RawMessage
	if json.Unmarshal(payload, &request) != nil || request == nil {
		return nil, fmt.Errorf("invalid request: command approval payload")
	}
	raw, provided := request["availableDecisions"]
	if !provided || string(raw) == "null" {
		return []any{"accept", "decline"}, nil
	}
	var decisions []any
	if json.Unmarshal(raw, &decisions) != nil || len(decisions) == 0 || len(decisions) > 8 {
		return nil, fmt.Errorf("invalid request: command approval choices")
	}
	for _, decision := range decisions {
		if err := validateCommandDecision(decision); err != nil {
			return nil, fmt.Errorf("invalid request: command approval choices")
		}
	}
	return decisions, nil
}

func validateCommandDecision(decision any) error {
	if value, ok := decision.(string); ok {
		switch value {
		case "accept", "acceptForSession", "decline", "cancel":
			return nil
		default:
			return fmt.Errorf("unknown command decision")
		}
	}
	value, ok := decision.(map[string]any)
	if !ok || len(value) != 1 {
		return fmt.Errorf("invalid command amendment")
	}
	if raw, ok := value["acceptWithExecpolicyAmendment"]; ok {
		outer, ok := raw.(map[string]any)
		if !ok || len(outer) != 1 {
			return fmt.Errorf("invalid execpolicy amendment")
		}
		parts, ok := outer["execpolicy_amendment"].([]any)
		if !ok || len(parts) == 0 || len(parts) > 20 {
			return fmt.Errorf("invalid execpolicy amendment")
		}
		for _, part := range parts {
			text, ok := part.(string)
			if !ok || text == "" || utf8.RuneCountInString(text) > 300 {
				return fmt.Errorf("invalid execpolicy amendment")
			}
		}
		return nil
	}
	if raw, ok := value["applyNetworkPolicyAmendment"]; ok {
		outer, ok := raw.(map[string]any)
		if !ok || len(outer) != 1 {
			return fmt.Errorf("invalid network policy amendment")
		}
		amendment, ok := outer["network_policy_amendment"].(map[string]any)
		if !ok || len(amendment) != 2 {
			return fmt.Errorf("invalid network policy amendment")
		}
		host, hostOK := amendment["host"].(string)
		action, actionOK := amendment["action"].(string)
		if !hostOK || host == "" || len(host) > 253 || !actionOK || (action != "allow" && action != "deny") {
			return fmt.Errorf("invalid network policy amendment")
		}
		return nil
	}
	return fmt.Errorf("unknown command amendment")
}

func requestTimeoutResponse(kind string, payload json.RawMessage) (any, bool) {
	switch kind {
	case "command_approval":
		decision, ok := commandTimeoutDecision(payload)
		return map[string]any{"decision": decision}, ok
	case "file_change_approval":
		return map[string]any{"decision": "decline"}, true
	case "permissions_approval":
		return map[string]any{
			"permissions": map[string]any{},
			"scope":       "turn",
		}, true
	case "user_input":
		return map[string]any{"answers": map[string]any{}}, true
	default:
		return map[string]any{}, false
	}
}

func commandTimeoutDecision(payload json.RawMessage) (string, bool) {
	var body map[string]json.RawMessage
	if json.Unmarshal(payload, &body) != nil || body == nil {
		return "", false
	}
	raw, provided := body["availableDecisions"]
	if !provided || string(raw) == "null" {
		return "decline", true
	}
	var decisions []json.RawMessage
	if json.Unmarshal(raw, &decisions) != nil {
		return "", false
	}
	fallback := ""
	for _, item := range decisions {
		var decision string
		if json.Unmarshal(item, &decision) != nil {
			continue
		}
		if decision == "decline" {
			return decision, true
		}
		if decision == "cancel" {
			fallback = decision
		}
	}
	return fallback, fallback != ""
}

func (s *Service) Interrupt(ctx context.Context, conversationID, actorUserID, expectedRunID string) error {
	binding, err := s.BindingByConversation(ctx, conversationID, actorUserID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	run := s.byThread[binding.ProviderThreadID]
	s.mu.Unlock()
	if run == nil {
		var record orm.ExternalAgentRun
		err := s.db.WithContext(ctx).
			Where(
				"conversation_id = ? AND provider_thread_id = ? AND status IN ?",
				conversationID,
				binding.ProviderThreadID,
				[]string{runStatusStarting, runStatusRunning, runStatusWaiting},
			).
			Order("created_at DESC").
			First(&record).Error
		if err != nil {
			return fmt.Errorf("task not found: external agent thread has no active managed turn")
		}
		if record.ActorUserID != actorUserID {
			return ErrThreadBusy
		}
		if _, recoverErr := s.reattachOrFinalizeActiveRun(ctx, record); recoverErr != nil {
			return recoverErr
		}
		s.mu.Lock()
		run = s.byThread[binding.ProviderThreadID]
		s.mu.Unlock()
		if run == nil {
			return fmt.Errorf("task not found: external agent turn is already terminal")
		}
	}
	record, _, _, _ := run.snapshot()
	if record.ActorUserID != actorUserID {
		return ErrThreadBusy
	}
	if expectedRunID == "" || record.ID != expectedRunID {
		return fmt.Errorf("task not found: external agent run changed")
	}
	return s.client.Call(ctx, "turn/interrupt", map[string]any{
		"threadId": record.ProviderThreadID,
		"turnId":   record.ProviderTurnID,
	}, &map[string]any{})
}

func (s *Service) Release(ctx context.Context, conversationID, actorUserID string) error {
	binding, err := s.BindingByConversation(ctx, conversationID, actorUserID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	active := s.byThread[binding.ProviderThreadID]
	s.mu.Unlock()
	if active != nil && !active.finished() {
		return fmt.Errorf("invalid request: external agent turn is still running")
	}
	var record orm.ExternalAgentRun
	if active != nil {
		record, _, _, _ = active.snapshot()
	} else {
		err = s.db.WithContext(ctx).
			Where("conversation_id = ? AND provider_thread_id = ?", conversationID, binding.ProviderThreadID).
			Order("created_at DESC").First(&record).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
	}
	if record.ID != "" {
		if record.ControlRelease == controlReleasePending {
			return ErrReleasePending
		}
		if controlReleased(record.ControlRelease) {
			if active != nil {
				s.detachActive(active)
			}
			return nil
		}
		claimed, err := s.claimControlRelease(ctx, record)
		if err != nil {
			return err
		}
		if !claimed {
			if active != nil {
				s.detachActive(active)
			}
			return nil
		}
		if active != nil {
			active.setTerminalState(record.Status, controlReleasePending, "")
		}
	}
	status := "not_loaded"
	if s.client != nil {
		status, err = s.releaseThreadWithRetry(binding.ProviderThreadID)
	}
	if err != nil {
		if record.ID != "" {
			persistErr := s.persistControlRelease(
				record.ID, record.Status, controlReleaseFailed, err.Error(), nil,
			)
			if active != nil {
				active.setTerminalState(record.Status, controlReleaseFailed, err.Error())
			}
			if persistErr != nil {
				return errors.Join(err, persistErr)
			}
		}
		return err
	}
	if record.ID != "" {
		if err := s.persistControlRelease(record.ID, record.Status, status, "", nil); err != nil {
			if active != nil {
				active.setTerminalState(record.Status, controlReleaseFailed, err.Error())
			}
			return err
		}
		if active != nil {
			active.setTerminalState(record.Status, status, "")
		}
	}
	if active != nil {
		_, _, answer, _ := active.snapshot()
		active.broadcast(Event{
			Type: terminalEventType(record.Status), Provider: record.Provider,
			ThreadID: record.ProviderThreadID, TurnID: record.ProviderTurnID,
			RunID: record.ID, Message: answer, Status: record.Status,
			ControlRelease: status, Terminal: true,
		})
		s.detachActive(active)
	}
	s.mu.Lock()
	delete(s.loaded, binding.ProviderThreadID)
	s.mu.Unlock()
	return nil
}

func (s *Service) claimControlRelease(
	ctx context.Context,
	record orm.ExternalAgentRun,
) (bool, error) {
	expected := record.ControlRelease
	for attempt := 0; attempt < 2; attempt++ {
		result := s.db.WithContext(ctx).
			Model(&orm.ExternalAgentRun{}).
			Where("id = ? AND control_release = ?", record.ID, expected).
			Updates(map[string]any{
				"status":          record.Status,
				"control_release": controlReleasePending,
				"control_error":   "",
				"updated_at":      time.Now(),
			})
		if result.Error != nil {
			return false, result.Error
		}
		if result.RowsAffected == 1 {
			return true, nil
		}
		var current orm.ExternalAgentRun
		if err := s.db.WithContext(ctx).Where("id = ?", record.ID).
			First(&current).Error; err != nil {
			return false, err
		}
		if controlReleased(current.ControlRelease) {
			return false, nil
		}
		if current.ControlRelease == controlReleasePending {
			return false, ErrReleasePending
		}
		if current.ControlRelease == expected {
			return false, ErrReleasePending
		}
		expected = current.ControlRelease
	}
	return false, ErrReleasePending
}

func controlReleased(status string) bool {
	switch status {
	case "unsubscribed", "notSubscribed", "notLoaded", "not_loaded":
		return true
	default:
		return false
	}
}
