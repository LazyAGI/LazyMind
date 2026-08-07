package externalagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

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
	runStatusSteered      = "steered"
	defaultRequestWait    = 10 * time.Minute
	defaultUnmanagedQuiet = 2 * time.Minute
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
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan Event, len(r.events)+64)
	for _, event := range r.events {
		ch <- event
	}
	if r.terminal {
		close(ch)
		return ch
	}
	r.subscribers[ch] = struct{}{}
	return ch
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

func (r *managedRun) eventsSnapshot() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
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
	quietTime time.Duration
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
	defaultService = NewService(db, NewCodexClient())
	return defaultService, nil
}

func NewService(db *gorm.DB, client *CodexClient) *Service {
	service := &Service{
		db:        db,
		client:    client,
		byThread:  make(map[string]*managedRun),
		byRequest: make(map[string]*managedRun),
		requests:  make(map[string]*pendingRequest),
		loaded:    make(map[string]int64),
		quietTime: defaultUnmanagedQuiet,
	}
	go service.consumeClientEvents()
	go service.recoverActiveRuns()
	return service
}

func validateProvider(provider string) error {
	if strings.TrimSpace(strings.ToLower(provider)) != ProviderCodex {
		return ErrUnsupportedProvider
	}
	return nil
}

func (s *Service) ListThreads(ctx context.Context, cursor string, limit int) (ThreadPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	params := map[string]any{
		"limit":         limit,
		"sortKey":       "updated_at",
		"sortDirection": "desc",
		// Codex Desktop currently persists its local tasks as "vscode";
		// appServer covers tasks created through LazyMind itself.
		"sourceKinds": []string{"cli", "vscode", "appServer"},
	}
	if cursor != "" {
		params["cursor"] = cursor
	}
	var page ThreadPage
	if err := s.client.Call(ctx, "thread/list", params, &page); err != nil {
		return ThreadPage{}, err
	}
	if err := s.decorateThreads(ctx, page.Data); err != nil {
		return ThreadPage{}, err
	}
	s.markThreadAvailability(page.Data)
	page.HasMore = page.NextCursor != nil && strings.TrimSpace(*page.NextCursor) != ""
	if page.Total <= 0 && !page.HasMore {
		page.Total = len(page.Data)
	}
	return page, nil
}

func (s *Service) ReadThread(ctx context.Context, threadID string) (Thread, error) {
	return s.readThread(ctx, threadID, true)
}

func (s *Service) ReadThreadPage(ctx context.Context, threadID string, offset, limit int) (TurnPage, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	thread, err := s.readThread(ctx, threadID, true)
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
	end := offset + limit
	if offset > total {
		offset = total
	}
	if end > total {
		end = total
	}
	pageTurns := turns[offset:end]
	raw, err := json.Marshal(pageTurns)
	if err != nil {
		return TurnPage{}, err
	}
	thread.Turns = nil
	return TurnPage{
		Thread:     thread,
		Turns:      raw,
		Offset:     offset,
		Limit:      limit,
		TotalTurns: total,
		HasMore:    end < total,
	}, nil
}

func (s *Service) readThread(ctx context.Context, threadID string, includeTurns bool) (Thread, error) {
	var response struct {
		Thread Thread `json:"thread"`
	}
	if err := s.client.Call(ctx, "thread/read", map[string]any{
		"threadId":     threadID,
		"includeTurns": includeTurns,
	}, &response); err != nil {
		return Thread{}, err
	}
	threads := []Thread{response.Thread}
	if err := s.decorateThreads(ctx, threads); err != nil {
		return Thread{}, err
	}
	s.markThreadAvailability(threads)
	return threads[0], nil
}

func (s *Service) markThreadAvailability(threads []Thread) {
	now := time.Now()
	for index := range threads {
		thread := &threads[index]
		thread.Available = thread.Status.Type == "idle"
		if thread.Status.Type == "notLoaded" && thread.UpdatedAt > 0 &&
			now.Sub(time.Unix(thread.UpdatedAt, 0)) >= s.quietTime {
			thread.Available = true
		}
	}
}

func (s *Service) decorateThreads(ctx context.Context, threads []Thread) error {
	if len(threads) == 0 {
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
			threads[index].Managed = binding.ManagedByLazyMind
			threads[index].ConversationID = binding.ConversationID
		}
	}
	return nil
}

func (s *Service) StartThread(ctx context.Context, input StartThreadInput) (Thread, error) {
	cwd := strings.TrimSpace(input.Cwd)
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return Thread{}, err
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
	response.Thread.Managed = true
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
		ManagedByLazyMind: input.Managed,
		CreatedByUserID:   input.CreatedByUserID,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := s.db.WithContext(ctx).Create(&binding).Error; err != nil {
		if lookupErr := s.db.WithContext(ctx).
			Where("provider = ? AND provider_thread_id = ?", ProviderCodex, input.ProviderThreadID).
			First(&existing).Error; lookupErr == nil {
			return existing, nil
		}
		return orm.ExternalAgentBinding{}, err
	}
	return binding, nil
}

func (s *Service) BindingByConversation(ctx context.Context, conversationID string) (orm.ExternalAgentBinding, error) {
	var binding orm.ExternalAgentBinding
	if err := s.db.WithContext(ctx).
		Where("conversation_id = ?", conversationID).
		First(&binding).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return orm.ExternalAgentBinding{}, ErrBindingNotFound
		}
		return orm.ExternalAgentBinding{}, err
	}
	return binding, nil
}

func (s *Service) BindingByThread(ctx context.Context, provider, threadID string) (orm.ExternalAgentBinding, error) {
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
	return binding, nil
}

func (s *Service) StartOrSteer(ctx context.Context, input ChatInput) (Execution, error) {
	if err := validateProvider(input.Provider); err != nil {
		return Execution{}, err
	}
	requestKey := input.Provider + "\x00" + input.RequestID
	s.mu.Lock()
	if running := s.byRequest[requestKey]; running != nil {
		record, _, _, seq := running.snapshot()
		s.mu.Unlock()
		if record.ActorUserID != input.ActorUserID {
			return Execution{}, ErrThreadBusy
		}
		return Execution{RunID: record.ID, HistoryID: record.HistoryID, Seq: seq, Events: running.subscribe()}, nil
	}
	s.mu.Unlock()

	if completed, ok, err := s.completedExecution(ctx, input); err != nil || ok {
		return completed, err
	}
	binding, err := s.BindingByConversation(ctx, input.ConversationID)
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
		return s.steer(ctx, active, input)
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
		s.finishActive(run)
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
	return Execution{RunID: record.ID, HistoryID: record.HistoryID, Seq: input.Seq, Events: run.subscribe()}, nil
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
	if record.Status == runStatusStarting || record.Status == runStatusRunning || record.Status == runStatusWaiting {
		s.mu.Lock()
		active := s.byRequest[input.Provider+"\x00"+input.RequestID]
		if active == nil {
			active = s.byThread[record.ProviderThreadID]
		}
		s.mu.Unlock()
		if active != nil {
			live, _, _, seq := active.snapshot()
			return Execution{RunID: live.ID, HistoryID: live.HistoryID, Seq: seq, Events: active.subscribe()}, true, nil
		}
		recovered, recoverErr := s.reattachOrFinalizeActiveRun(ctx, record)
		if recoverErr != nil {
			return Execution{}, true, recoverErr
		}
		return recovered, true, nil
	}
	var history orm.ChatHistory
	_ = s.db.WithContext(ctx).Where("id = ?", record.HistoryID).First(&history).Error
	eventType := "turn_completed"
	if record.Status == runStatusFailed {
		eventType = "turn_failed"
	}
	if record.Status == runStatusInterrupted {
		eventType = "turn_interrupted"
	}
	events := make(chan Event, 1)
	events <- Event{
		Type: eventType, Provider: record.Provider, ThreadID: record.ProviderThreadID,
		TurnID: record.ProviderTurnID, RunID: record.ID, Message: history.Result,
		Status: record.Status, Terminal: true,
	}
	close(events)
	return Execution{RunID: record.ID, HistoryID: record.HistoryID, Seq: history.Seq, Events: events}, true, nil
}

func (s *Service) recoverActiveRuns() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var records []orm.ExternalAgentRun
	if err := s.db.WithContext(ctx).
		Where("provider = ? AND status IN ?", ProviderCodex, []string{runStatusStarting, runStatusRunning, runStatusWaiting}).
		Find(&records).Error; err != nil {
		corelog.Logger.Warn().Err(err).Msg("external agent active run recovery query failed")
		return
	}
	for _, record := range records {
		go s.recoverActiveRun(record)
	}
}

func (s *Service) recoverActiveRun(record orm.ExternalAgentRun) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := s.reattachOrFinalizeActiveRun(ctx, record)
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

func (s *Service) reattachOrFinalizeActiveRun(ctx context.Context, record orm.ExternalAgentRun) (Execution, error) {
	s.mu.Lock()
	if existing := s.byThread[record.ProviderThreadID]; existing != nil {
		live, _, _, seq := existing.snapshot()
		s.mu.Unlock()
		return Execution{RunID: live.ID, HistoryID: live.HistoryID, Seq: seq, Events: existing.subscribe()}, nil
	}
	s.mu.Unlock()

	thread, err := s.readThread(ctx, record.ProviderThreadID, true)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(err, context.Canceled) {
			return Execution{}, err
		}
		s.markRunInterrupted(record, "Codex thread unavailable after restart; please retry")
		return s.terminalExecution(record, "turn_interrupted", "Codex thread unavailable after restart; please retry")
	}
	if record.Status == runStatusWaiting {
		// Pending Codex RPC request IDs are process-local and cannot be resumed.
		s.markRunInterrupted(record, "Interactive request lost after restart; please retry")
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
		_ = s.db.WithContext(ctx).Model(&orm.ExternalAgentRun{}).Where("id = ?", record.ID).Updates(map[string]any{
			"status":     status,
			"updated_at": time.Now(),
		}).Error
		if answer != "" {
			var history orm.ChatHistory
			if s.db.WithContext(ctx).Where("id = ?", record.HistoryID).First(&history).Error == nil {
				_ = s.updateHistory(record, history.Content, answer, history.Seq)
			}
		}
		record.Status = status
		return s.terminalExecution(record, eventType, answer)
	}
	if thread.Status.Type == "idle" {
		s.markRunInterrupted(record, "Codex turn ended while LazyMind was offline")
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
		live, _, _, seq := existing.snapshot()
		s.mu.Unlock()
		return Execution{RunID: live.ID, HistoryID: live.HistoryID, Seq: seq, Events: existing.subscribe()}, nil
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
	return Execution{RunID: record.ID, HistoryID: record.HistoryID, Seq: history.Seq, Events: run.subscribe()}, nil
}

func (s *Service) markRunInterrupted(record orm.ExternalAgentRun, message string) {
	now := time.Now()
	_ = s.db.Model(&orm.ExternalAgentRun{}).Where("id = ?", record.ID).Updates(map[string]any{
		"status":        runStatusInterrupted,
		"error_message": message,
		"updated_at":    now,
	}).Error
	var history orm.ChatHistory
	if s.db.Where("id = ?", record.HistoryID).First(&history).Error == nil && message != "" {
		_ = s.updateHistory(record, history.Content, message, history.Seq)
	}
}

func (s *Service) terminalExecution(record orm.ExternalAgentRun, eventType, message string) (Execution, error) {
	var history orm.ChatHistory
	_ = s.db.Where("id = ?", record.HistoryID).First(&history).Error
	if message == "" {
		message = history.Result
	}
	events := make(chan Event, 1)
	events <- Event{
		Type: eventType, Provider: record.Provider, ThreadID: record.ProviderThreadID,
		TurnID: record.ProviderTurnID, RunID: record.ID, Message: message,
		Status: record.Status, Terminal: true,
	}
	close(events)
	return Execution{RunID: record.ID, HistoryID: record.HistoryID, Seq: history.Seq, Events: events}, nil
}

func (s *Service) SnapshotConversation(ctx context.Context, conversationID string) (RunSnapshot, error) {
	binding, err := s.BindingByConversation(ctx, conversationID)
	if err != nil {
		return RunSnapshot{}, err
	}
	snapshot := RunSnapshot{
		ConversationID:   conversationID,
		Provider:         binding.Provider,
		ProviderThreadID: binding.ProviderThreadID,
		Status:           "idle",
	}
	s.mu.Lock()
	active := s.byThread[binding.ProviderThreadID]
	var pendingID string
	for id, request := range s.requests {
		if request.Run != nil {
			record, _, _, _ := request.Run.snapshot()
			if record.ProviderThreadID == binding.ProviderThreadID {
				pendingID = id
				break
			}
		}
	}
	s.mu.Unlock()
	if active != nil {
		record, _, answer, _ := active.snapshot()
		snapshot.RunID = record.ID
		snapshot.RequestID = record.RequestID
		snapshot.Status = record.Status
		snapshot.Answer = answer
		snapshot.Events = active.eventsSnapshot()
		snapshot.PendingRequestID = pendingID
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
	snapshot.RequestID = record.RequestID
	snapshot.Status = record.Status
	var history orm.ChatHistory
	if s.db.WithContext(ctx).Where("id = ?", record.HistoryID).First(&history).Error == nil {
		snapshot.Answer = history.Result
	}
	return snapshot, nil
}

func (s *Service) requireThreadAvailable(ctx context.Context, threadID string) error {
	thread, err := s.readThread(ctx, threadID, false)
	if err != nil {
		return err
	}
	if thread.Status.Type == "idle" {
		return nil
	}
	if thread.UpdatedAt == 0 || time.Since(time.Unix(thread.UpdatedAt, 0)) < s.quietTime {
		return ErrUnmanagedActive
	}
	thread, err = s.ReadThread(ctx, threadID)
	if err != nil {
		return err
	}
	var turns []struct {
		Status string `json:"status"`
	}
	if len(thread.Turns) == 0 || json.Unmarshal(thread.Turns, &turns) != nil || len(turns) == 0 {
		return ErrUnmanagedActive
	}
	last := turns[len(turns)-1].Status
	if last != runStatusCompleted && last != runStatusFailed && last != runStatusInterrupted {
		return ErrUnmanagedActive
	}
	return nil
}

func (s *Service) steer(ctx context.Context, active *managedRun, input ChatInput) (Execution, error) {
	record, _, _, _ := active.snapshot()
	var response struct {
		TurnID string `json:"turnId"`
	}
	if err := s.client.Call(ctx, "turn/steer", map[string]any{
		"threadId":       record.ProviderThreadID,
		"expectedTurnId": record.ProviderTurnID,
		"input":          []map[string]string{{"type": "text", "text": input.Query}},
	}, &response); err != nil {
		return Execution{}, err
	}
	now := time.Now()
	steerRun := orm.ExternalAgentRun{
		ID: uuid.NewString(), RequestID: input.RequestID, ConversationID: input.ConversationID,
		HistoryID: input.HistoryID, Provider: ProviderCodex, ProviderThreadID: record.ProviderThreadID,
		ProviderTurnID: record.ProviderTurnID, ActorUserID: input.ActorUserID,
		Action: "steer", Status: runStatusSteered, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.db.WithContext(ctx).Create(&steerRun).Error; err != nil {
		return Execution{}, err
	}
	message := "已追加到正在执行的 Codex 任务"
	if err := s.createHistory(steerRun, input.Query, message, input.Seq); err != nil {
		return Execution{}, err
	}
	events := make(chan Event, 1)
	events <- Event{
		Type: "turn_steered", Provider: ProviderCodex, ThreadID: record.ProviderThreadID,
		TurnID: record.ProviderTurnID, RunID: steerRun.ID, Message: message,
		Status: runStatusSteered, Terminal: true,
	}
	close(events)
	return Execution{RunID: steerRun.ID, HistoryID: steerRun.HistoryID, Seq: input.Seq, Events: events}, nil
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
	run.setTurn(started.Turn.ID)
	now := time.Now()
	_ = s.db.Model(&orm.ExternalAgentRun{}).Where("id = ?", record.ID).Updates(map[string]any{
		"provider_turn_id": started.Turn.ID,
		"status":           runStatusRunning,
		"updated_at":       now,
	}).Error
	run.broadcast(Event{
		Type: "turn_started", Provider: ProviderCodex, ThreadID: record.ProviderThreadID,
		TurnID: started.Turn.ID, RunID: record.ID, Status: runStatusRunning,
	})
	go s.watchRun(run)
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
		s.unsubscribeThread(newThreadID)
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
		thread, err := s.ReadThread(context.Background(), record.ProviderThreadID)
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
	record.UpdatedAt = time.Now()
	_ = s.db.Model(&orm.ExternalAgentRun{}).Where("id = ?", record.ID).Updates(map[string]any{
		"status": record.Status, "error_message": record.ErrorMessage, "updated_at": record.UpdatedAt,
	}).Error
	_ = s.updateHistory(record, query, "Codex 执行失败："+err.Error(), seq)
	s.finishActive(run)
	run.broadcast(Event{
		Type: "turn_failed", Provider: record.Provider, ThreadID: record.ProviderThreadID,
		TurnID: record.ProviderTurnID, RunID: record.ID, Message: record.ErrorMessage,
		Status: record.Status, Terminal: true,
	})
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
	s.markRunInterrupted(record, message)
	s.finishActive(run)
	run.broadcast(Event{
		Type: "turn_interrupted", Provider: record.Provider,
		ThreadID: record.ProviderThreadID, TurnID: record.ProviderTurnID,
		RunID: record.ID, Message: message,
		Status: runStatusInterrupted, Terminal: true,
	})
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
		RunID: record.ID, ProviderEventType: message.Method,
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
		base.Message = run.appendAnswer(params.Delta)
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
			Type    string `json:"type"`
			Text    string `json:"text"`
			Phase   string `json:"phase"`
			Command string `json:"command"`
			Status  string `json:"status"`
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
	messageText := answer
	if messageText == "" && record.ErrorMessage != "" {
		messageText = record.ErrorMessage
	}
	_ = s.db.Model(&orm.ExternalAgentRun{}).Where("id = ?", record.ID).Updates(map[string]any{
		"provider_turn_id": record.ProviderTurnID,
		"status":           record.Status,
		"error_message":    record.ErrorMessage,
		"updated_at":       record.UpdatedAt,
	}).Error
	_ = s.updateHistory(record, query, messageText, seq)
	s.finishActive(run)
	run.broadcast(Event{
		Type: eventType, Provider: record.Provider, ThreadID: record.ProviderThreadID,
		TurnID: record.ProviderTurnID, RunID: record.ID, ProviderEventType: message.Method,
		Message: messageText, Status: record.Status, Terminal: true,
	})
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
		updates := map[string]any{"updated_at": history.UpdateTime}
		if record.Action != "steer" {
			updates["chat_times"] = gorm.Expr("chat_times + ?", 1)
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

func (s *Service) finishActive(run *managedRun) {
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
	if s.client != nil {
		go s.unsubscribeThread(record.ProviderThreadID)
	}
}

func (s *Service) unsubscribeThread(threadID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var response struct {
		Status string `json:"status"`
	}
	if err := s.client.Call(ctx, "thread/unsubscribe", map[string]any{
		"threadId": threadID,
	}, &response); err != nil {
		corelog.Logger.Warn().
			Err(err).
			Str("thread_id", threadID).
			Msg("external agent thread unsubscribe failed")
	}
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
	request := &pendingRequest{
		ID: uuid.NewString(), RPCID: append(json.RawMessage(nil), message.ID...), Kind: kind,
		Run: run, ExpiresAt: time.Now().Add(defaultRequestWait),
	}
	s.mu.Lock()
	s.requests[request.ID] = request
	s.mu.Unlock()
	_ = s.db.Model(&orm.ExternalAgentRun{}).Where("id = ?", record.ID).
		Updates(map[string]any{"status": runStatusWaiting, "updated_at": time.Now()}).Error
	run.broadcast(Event{
		Type: "request_required", Provider: record.Provider, ThreadID: record.ProviderThreadID,
		TurnID: record.ProviderTurnID, RunID: record.ID, ProviderEventType: message.Method,
		RequestID: request.ID, RequestKind: kind, RequestPayload: append(json.RawMessage(nil), message.Params...),
		Summary: requestSummary(kind, message.Params), Status: runStatusWaiting,
	})
	go s.expireRequest(request)
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
		return "Codex 请求执行命令：" + details.Command
	}
	if details.Reason != "" {
		return details.Reason
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
			return details.Questions[0].Question
		}
		return "Codex 请求用户输入"
	}
	return "Codex 请求交互"
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
	delete(s.requests, request.ID)
	s.mu.Unlock()
	_ = s.client.Respond(request.RPCID, requestTimeoutResponse(request.Kind))
	s.markRequestResolved(request, "交互请求超时，已自动拒绝")
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
	if err := validateRequestResponse(request.Kind, input.Payload); err != nil {
		return err
	}
	s.mu.Lock()
	if s.requests[input.RequestID] != request {
		s.mu.Unlock()
		return ErrRequestNotFound
	}
	if err := s.client.Respond(request.RPCID, input.Payload); err != nil {
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
	_ = s.db.Model(&orm.ExternalAgentRun{}).Where("id = ?", record.ID).
		Updates(map[string]any{"status": runStatusRunning, "updated_at": time.Now()}).Error
	request.Run.broadcast(Event{
		Type: "progress", Provider: record.Provider, ThreadID: record.ProviderThreadID,
		TurnID: record.ProviderTurnID, RunID: record.ID,
		Summary: summary, Status: runStatusRunning,
	})
}

func validateRequestResponse(kind string, payload json.RawMessage) error {
	var body map[string]json.RawMessage
	if len(payload) == 0 || json.Unmarshal(payload, &body) != nil || body == nil {
		return fmt.Errorf("invalid request: response must be an object")
	}
	switch kind {
	case "file_change_approval":
		var decision string
		if json.Unmarshal(body["decision"], &decision) != nil {
			return fmt.Errorf("invalid request: approval decision")
		}
		switch decision {
		case "accept", "acceptForSession", "decline", "cancel":
			return nil
		default:
			return fmt.Errorf("invalid request: approval decision")
		}
	case "command_approval":
		var decision string
		if json.Unmarshal(body["decision"], &decision) == nil {
			switch decision {
			case "accept", "acceptForSession", "decline", "cancel":
				return nil
			default:
				return fmt.Errorf("invalid request: approval decision")
			}
		}
		var amendment map[string]json.RawMessage
		if json.Unmarshal(body["decision"], &amendment) != nil || amendment == nil {
			return fmt.Errorf("invalid request: approval decision")
		}
		if _, ok := amendment["acceptWithExecpolicyAmendment"]; ok {
			return nil
		}
		if _, ok := amendment["applyNetworkPolicyAmendment"]; ok {
			return nil
		}
		return fmt.Errorf("invalid request: approval decision")
	case "permissions_approval":
		var permissions map[string]json.RawMessage
		if raw, ok := body["permissions"]; !ok || json.Unmarshal(raw, &permissions) != nil || permissions == nil {
			return fmt.Errorf("invalid request: permissions response")
		}
		return nil
	case "user_input":
		var answers map[string]json.RawMessage
		if raw, ok := body["answers"]; !ok || json.Unmarshal(raw, &answers) != nil || answers == nil {
			return fmt.Errorf("invalid request: user input answers")
		}
		return nil
	default:
		return fmt.Errorf("invalid request: unsupported response kind")
	}
}

func requestTimeoutResponse(kind string) any {
	switch kind {
	case "command_approval", "file_change_approval":
		return map[string]any{"decision": "decline"}
	case "permissions_approval":
		return map[string]any{"permissions": map[string]any{}}
	case "user_input":
		return map[string]any{"answers": map[string]any{}}
	default:
		return map[string]any{}
	}
}

func (s *Service) Interrupt(ctx context.Context, conversationID, actorUserID string) error {
	binding, err := s.BindingByConversation(ctx, conversationID)
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
	return s.client.Call(ctx, "turn/interrupt", map[string]any{
		"threadId": record.ProviderThreadID,
		"turnId":   record.ProviderTurnID,
	}, &map[string]any{})
}

func (s *Service) Release(ctx context.Context, conversationID, actorUserID string) error {
	binding, err := s.BindingByConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if binding.CreatedByUserID != actorUserID {
		return ErrThreadBusy
	}
	s.mu.Lock()
	active := s.byThread[binding.ProviderThreadID]
	s.mu.Unlock()
	if active != nil && !active.finished() {
		return fmt.Errorf("invalid request: external agent turn is still running")
	}
	var response struct {
		Status string `json:"status"`
	}
	if err := s.client.Call(ctx, "thread/unsubscribe", map[string]any{
		"threadId": binding.ProviderThreadID,
	}, &response); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.loaded, binding.ProviderThreadID)
	s.mu.Unlock()
	return nil
}
