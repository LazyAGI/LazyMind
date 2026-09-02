package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/agentinvocation"
	"lazymind/core/common/orm"
	"lazymind/core/externalcontext"
	"lazymind/core/log"
	"lazymind/core/workflow/artifactfile"
)

var errExternalChatLeaseLost = errors.New("external chat lease is no longer owned by this host")

const externalRunClaimFallback = 2 * time.Second

type externalRunWakeup struct {
	mu     sync.Mutex
	signal chan struct{}
}

func newExternalRunWakeup() *externalRunWakeup {
	return &externalRunWakeup{signal: make(chan struct{})}
}

func (w *externalRunWakeup) subscribe() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.signal
}

func (w *externalRunWakeup) notify() {
	w.mu.Lock()
	close(w.signal)
	w.signal = make(chan struct{})
	w.mu.Unlock()
}

var externalRunsAvailable = newExternalRunWakeup()

type externalChatApplication struct {
	db       *gorm.DB
	now      func() time.Time
	hostTTL  time.Duration
	leaseTTL time.Duration
}

func newExternalChatApplication(db *gorm.DB) *externalChatApplication {
	return &externalChatApplication{
		db: db, now: func() time.Time { return time.Now().UTC() },
		hostTTL: externalHostTTL, leaseTTL: externalRunLeaseTTL,
	}
}

func externalLeaseToken() (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (a *externalChatApplication) hostStatus(ctx context.Context, owner, provider string) (chatExecutorStatus, error) {
	status := chatExecutorStatus{ID: provider}
	if a == nil || a.db == nil {
		status.UnavailableReason = "LazyMind Core database is unavailable"
		return status, nil
	}
	var hosts []orm.ExternalChatHost
	if err := a.db.WithContext(ctx).
		Where("actor_user_id = ? AND provider = ? AND last_seen >= ?", owner, provider, a.now().Add(-a.hostTTL)).
		Order("last_seen DESC").Find(&hosts).Error; err != nil {
		return status, err
	}
	for _, host := range hosts {
		status.HostOnline = true
		status.Installed = status.Installed || host.Installed
		if host.Ready {
			status.Available = true
			status.UnavailableReason = ""
			continue
		}
		if !status.Available && status.UnavailableReason == "" {
			status.UnavailableReason = host.UnavailableReason
		}
	}
	if !status.HostOnline {
		status.UnavailableReason = "LazyMind Agent Host is offline"
	} else if !status.Available && status.UnavailableReason == "" {
		status.UnavailableReason = "External Agent is not ready"
	}
	return status, nil
}

func (a *externalChatApplication) createRun(ctx context.Context, run *orm.ExternalChatRun) error {
	if a == nil || a.db == nil || run == nil {
		return errors.New("external chat store is unavailable")
	}
	now := a.now()
	run.Status = "pending"
	run.CreatedAt, run.UpdatedAt = now, now
	if err := a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(run).Error; err != nil {
			return err
		}
		if run.Action == "regenerate" {
			return claimChatHistoryRun(ctx, tx, run.HistoryID, run.ID)
		}
		return nil
	}); err != nil {
		return err
	}
	externalRunsAvailable.notify()
	return nil
}

func (a *externalChatApplication) findRunByRequest(
	ctx context.Context,
	owner, provider, requestKey string,
) (*orm.ExternalChatRun, error) {
	var run orm.ExternalChatRun
	err := a.db.WithContext(ctx).
		Where("actor_user_id = ? AND provider = ? AND request_id = ?", owner, provider, requestKey).
		Take(&run).Error
	return &run, err
}

func (a *externalChatApplication) reportHost(
	ctx context.Context,
	owner, provider, hostID string,
	installed, ready bool,
	reason string,
) error {
	now := a.now()
	host := orm.ExternalChatHost{
		ActorUserID: owner, Provider: provider, HostID: hostID,
		Installed: installed, Ready: ready, UnavailableReason: reason,
		LastSeen: now, UpdatedAt: now,
	}
	return a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Host IDs identify process instances. Prune projections long after
		// their availability TTL so ordinary restarts cannot grow this table.
		if err := tx.Where("last_seen < ?", now.Add(-4*a.hostTTL)).Delete(&orm.ExternalChatHost{}).Error; err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "actor_user_id"}, {Name: "provider"}, {Name: "host_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"installed": installed, "ready": ready, "unavailable_reason": reason,
				"last_seen": now, "updated_at": now,
			}),
		}).Create(&host).Error
	})
}

func (a *externalChatApplication) claim(
	ctx context.Context,
	owner, provider, hostID string,
) (*externalChatJob, error) {
	var job *externalChatJob
	err := a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := a.now()
		var run orm.ExternalChatRun
		err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("actor_user_id = ? AND provider = ? AND (host_id = '' OR host_id = ?) AND stop_requested = ? AND (status = ? OR (status = ? AND (lease_expires_at IS NULL OR lease_expires_at < ?)))",
				owner, provider, hostID, false, "pending", "running", now).
			Order("created_at ASC").Take(&run).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		token, err := externalLeaseToken()
		if err != nil {
			return err
		}
		expires := now.Add(a.leaseTTL)
		claimed := tx.Model(&orm.ExternalChatRun{}).
			Where("id = ? AND (host_id = '' OR host_id = ?) AND stop_requested = ? AND (status = ? OR (status = ? AND (lease_expires_at IS NULL OR lease_expires_at < ?)))",
				run.ID, hostID, false, "pending", "running", now).
			Updates(map[string]any{
				"status": "running", "host_id": hostID, "lease_token": token,
				"lease_expires_at": expires, "claimed_at": now, "last_heartbeat_at": now,
				"claim_count": gorm.Expr("claim_count + 1"), "updated_at": now,
			})
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected == 0 {
			return nil
		}
		recovering := run.Status == "running"
		if recovering {
			if _, err := agentinvocation.New(tx).InterruptRunningForExternalRef(
				ctx, owner, run.ID, now,
			); err != nil {
				return err
			}
		}
		action := run.Action
		if recovering {
			var completedCheckpoint int64
			if err := tx.Model(&orm.ExternalChatRunEvent{}).
				Where("run_id = ? AND type = ?", run.ID, "turn_completed").
				Count(&completedCheckpoint).Error; err != nil {
				return err
			}
			switch {
			case completedCheckpoint > 0:
				action = "finalize"
			case strings.TrimSpace(run.ProviderThreadID) != "":
				action = "recover"
			}
		} else if strings.TrimSpace(run.ProviderThreadID) != "" {
			action = "resume"
		}
		job = &externalChatJob{
			RunID: run.ID, ConversationID: run.ConversationID, HistoryID: run.HistoryID,
			Provider: run.Provider, ProviderThreadID: run.ProviderThreadID,
			Action: action, Prompt: run.Prompt, Query: run.Query, LeaseToken: token, HostID: hostID,
		}
		return nil
	})
	return job, err
}

func (a *externalChatApplication) heartbeat(
	ctx context.Context,
	owner, runID, hostID, leaseToken string,
) (bool, error) {
	var stop bool
	err := a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := a.now()
		var run orm.ExternalChatRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND actor_user_id = ?", runID, owner).Take(&run).Error; err != nil {
			return errExternalChatLeaseLost
		}
		if run.HostID != hostID || run.LeaseToken == "" || run.LeaseToken != leaseToken {
			return errExternalChatLeaseLost
		}
		stop = run.StopRequested || run.Status == "stopped"
		if stop {
			return nil
		}
		if run.Status != "running" || run.LeaseExpiresAt == nil || run.LeaseExpiresAt.Before(now) {
			return errExternalChatLeaseLost
		}
		expires := now.Add(a.leaseTTL)
		if err := tx.Model(&orm.ExternalChatRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"last_heartbeat_at": now, "lease_expires_at": expires, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&orm.ExternalChatHost{}).
			Where("actor_user_id = ? AND provider = ? AND host_id = ?", owner, run.Provider, hostID).
			Updates(map[string]any{"last_seen": now, "updated_at": now}).Error
	})
	return stop, err
}

func (a *externalChatApplication) appendEvent(
	ctx context.Context,
	owner, runID, hostID, leaseToken string,
	event externalChatEvent,
) (int64, error) {
	var sequence int64
	err := a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := a.now()
		var run orm.ExternalChatRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND actor_user_id = ?", runID, owner).Take(&run).Error; err != nil {
			return errExternalChatLeaseLost
		}
		if run.HostID != hostID || run.LeaseToken == "" || run.LeaseToken != leaseToken {
			return errExternalChatLeaseLost
		}
		if event.Type == "message" {
			normalized, err := rewriteExternalArtifactReferences(tx, run, event.Text)
			if err != nil {
				return err
			}
			event.Text = normalized
		}
		var existing orm.ExternalChatRunEvent
		if err := tx.Where("id = ?", event.EventID).Take(&existing).Error; err == nil {
			if !sameExternalChatEvent(existing, runID, event) {
				return errors.New("external chat event_id conflicts with an existing event")
			}
			sequence = existing.Sequence
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if run.Status != "running" || run.LeaseExpiresAt == nil || run.LeaseExpiresAt.Before(now) {
			return errExternalChatLeaseLost
		}
		sequence = run.NextEventSequence + 1
		record := orm.ExternalChatRunEvent{
			ID: event.EventID, RunID: run.ID, Sequence: sequence, Type: event.Type,
			Text: event.Text, ProviderThreadID: event.ProviderThreadID,
			ErrorMessage: event.Error, CreatedAt: now,
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		updates := map[string]any{
			"next_event_sequence": sequence, "updated_at": now,
			"last_heartbeat_at": now, "lease_expires_at": now.Add(a.leaseTTL),
		}
		switch event.Type {
		case "thread_started":
			if strings.TrimSpace(event.ProviderThreadID) != "" {
				if err := externalcontext.New(tx).BindManagedThread(
					ctx, owner, run.Provider, run.HostID, event.ProviderThreadID, run.ConversationID,
				); err != nil {
					return err
				}
				updates["provider_thread_id"] = event.ProviderThreadID
				run.ProviderThreadID = event.ProviderThreadID
			}
		case "completed":
			updates["status"], updates["completed_at"] = "completed", now
		case "failed":
			message := strings.TrimSpace(event.Error)
			if message == "" {
				message = "external Agent failed"
			}
			updates["status"], updates["error_message"], updates["completed_at"] = "failed", message, now
		}
		if err := tx.Model(&orm.ExternalChatRun{}).Where("id = ?", run.ID).Updates(updates).Error; err != nil {
			return err
		}
		if event.Type == "completed" || event.Type == "failed" {
			run.Status = event.Type
			if event.Type == "completed" {
				run.Status = "completed"
			} else {
				run.Status, run.ErrorMessage = "failed", updates["error_message"].(string)
			}
			_, err := finalizeExternalChatHistory(tx, &run, now)
			return err
		}
		return nil
	})
	return sequence, err
}

func sameExternalChatEvent(existing orm.ExternalChatRunEvent, runID string, event externalChatEvent) bool {
	return existing.RunID == runID && existing.Type == event.Type && existing.Text == event.Text &&
		existing.ProviderThreadID == event.ProviderThreadID && existing.ErrorMessage == event.Error
}

func (a *externalChatApplication) requestStop(ctx context.Context, owner, conversationID, historyID string) error {
	return a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("actor_user_id = ? AND conversation_id = ? AND status IN ?", owner, conversationID, []string{"pending", "running"})
		if historyID != "" {
			query = query.Where("history_id = ?", historyID)
		}
		var runs []orm.ExternalChatRun
		if err := query.Find(&runs).Error; err != nil {
			return err
		}
		now := a.now()
		for index := range runs {
			run := &runs[index]
			sequence := run.NextEventSequence + 1
			eventID := fmt.Sprintf("stop-%s-%d", run.ID, sequence)
			if err := tx.Create(&orm.ExternalChatRunEvent{
				ID: eventID, RunID: run.ID, Sequence: sequence, Type: "stopped", CreatedAt: now,
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&orm.ExternalChatRun{}).Where("id = ?", run.ID).Updates(map[string]any{
				"status": "stopped", "stop_requested": true, "next_event_sequence": sequence,
				"completed_at": now, "updated_at": now,
			}).Error; err != nil {
				return err
			}
			run.Status, run.StopRequested = "stopped", true
			if _, err := finalizeExternalChatHistory(tx, run, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func activeExternalChatHistoryIDs(
	ctx context.Context,
	db *gorm.DB,
	owner, conversationID string,
	historyIDs []string,
) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	if db == nil || len(historyIDs) == 0 {
		return out, nil
	}
	var runs []orm.ExternalChatRun
	if err := db.WithContext(ctx).Select("history_id").
		Where("actor_user_id = ? AND conversation_id = ? AND history_id IN ? AND status IN ?", owner, conversationID, historyIDs, []string{"pending", "running"}).
		Find(&runs).Error; err != nil {
		return nil, err
	}
	for _, run := range runs {
		out[run.HistoryID] = struct{}{}
	}
	return out, nil
}

func finalizeExternalChatHistory(tx *gorm.DB, run *orm.ExternalChatRun, now time.Time) (bool, error) {
	var events []orm.ExternalChatRunEvent
	if err := tx.Where("run_id = ? AND type = ?", run.ID, "message").Order("sequence ASC").Find(&events).Error; err != nil {
		return false, err
	}
	var result strings.Builder
	for index := range events {
		normalized, err := rewriteExternalArtifactReferences(tx, *run, events[index].Text)
		if err != nil {
			return false, err
		}
		if normalized != events[index].Text {
			if err := tx.Model(&orm.ExternalChatRunEvent{}).Where("id = ?", events[index].ID).
				Update("text", normalized).Error; err != nil {
				return false, err
			}
		}
		result.WriteString(normalized)
	}
	terminalEvent := externalRunTerminalEvent(run.ID, run.Status, result.Len() > 0)
	terminal, _ := terminalEvent.Terminal()
	history := orm.ChatHistory{
		ID: run.HistoryID, Seq: run.Sequence, ConversationID: run.ConversationID,
		AlgorithmID: "external:" + run.Provider, RawContent: run.Query, Content: run.Query,
		Result: result.String(), RunID: run.ID, RunStatus: terminal.Status,
		RunTerminal: terminalJSON(terminal), Ext: run.HistoryExt,
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}
	var existing orm.ChatHistory
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", run.HistoryID).Take(&existing).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		if err := tx.Create(&history).Error; err != nil {
			return false, err
		}
	case err != nil:
		return false, err
	case existing.RunID != run.ID:
		log.Logger.Info().
			Str("conversation_id", run.ConversationID).
			Str("history_id", run.HistoryID).
			Str("run_id", run.ID).
			Str("current_run_id", existing.RunID).
			Msg("ignored stale external chat history finalization")
		return false, nil
	default:
		persisted, err := updateOwnedChatHistory(tx.Statement.Context, tx, run.HistoryID, run.ID, map[string]any{
			"seq": history.Seq, "conversation_id": history.ConversationID,
			"algorithm_id": history.AlgorithmID, "raw_content": history.RawContent,
			"content": history.Content, "result": history.Result,
			"run_status": history.RunStatus, "run_terminal": history.RunTerminal, "ext": history.Ext,
			"update_time": now,
		})
		if err != nil || !persisted {
			return persisted, err
		}
	}
	if err := tx.Model(&orm.Conversation{}).Where("id = ?", run.ConversationID).
		Update("updated_at", now).Error; err != nil {
		return false, err
	}
	if run.Action != "regenerate" {
		if err := tx.Model(&orm.Conversation{}).Where("id = ?", run.ConversationID).
			UpdateColumn("chat_times", gorm.Expr("chat_times + ?", 1)).Error; err != nil {
			return false, err
		}
	}
	taskStatus := "succeeded"
	if run.Status == "failed" {
		taskStatus = "failed"
	} else if run.Status == "stopped" {
		taskStatus = "canceled"
	}
	err = tx.Model(&orm.TaskCenterTask{}).
		Where("conversation_id = ? AND task_type = ? AND archived_at IS NULL AND status NOT IN ('succeeded','failed','canceled')", run.ConversationID, "background_chat").
		Updates(map[string]any{"status": taskStatus, "finished_at": now, "updated_at": now}).Error
	return err == nil, err
}

var markdownLinkDestination = regexp.MustCompile(`\]\(([^)\n]+)\)`)

func rewriteExternalArtifactReferences(tx *gorm.DB, run orm.ExternalChatRun, text string) (string, error) {
	links := markdownLinkDestination.FindAllString(text, -1)
	hasHostLocalReference := false
	for _, link := range links {
		target, _ := markdownTargetAndSuffix(strings.TrimSpace(link[2 : len(link)-1]))
		if hostLocalTarget(target) {
			hasHostLocalReference = true
			break
		}
	}
	if !hasHostLocalReference {
		return text, nil
	}
	var sessionIDs []string
	if err := tx.Model(&orm.WorkflowSession{}).
		Where("create_user_id = ? AND conversation_id = ? AND origin_ref = ?", run.ActorUserID, run.ConversationID, run.ID).
		Pluck("id", &sessionIDs).Error; err != nil {
		return "", err
	}
	var invokedSessionIDs []string
	if err := tx.Model(&orm.AgentInvocation{}).
		Where("owner_user_id = ? AND external_ref = ? AND session_id <> ''", run.ActorUserID, run.ID).
		Pluck("session_id", &invokedSessionIDs).Error; err != nil {
		return "", err
	}
	seenSessions := make(map[string]struct{}, len(sessionIDs)+len(invokedSessionIDs))
	for _, sessionID := range append(sessionIDs, invokedSessionIDs...) {
		if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
			seenSessions[sessionID] = struct{}{}
		}
	}
	if len(seenSessions) == 0 {
		return text, nil
	}
	sessionIDs = sessionIDs[:0]
	for sessionID := range seenSessions {
		sessionIDs = append(sessionIDs, sessionID)
	}

	var revisions []orm.WorkflowSlotRevision
	if err := tx.Where("session_id IN ? AND selected = ? AND human_artifact_id IS NOT NULL", sessionIDs, true).
		Order("created_at DESC").Find(&revisions).Error; err != nil {
		return "", err
	}
	humanIDs := make([]string, 0, len(revisions))
	for _, revision := range revisions {
		if revision.HumanArtifactID != nil && *revision.HumanArtifactID != "" {
			humanIDs = append(humanIDs, *revision.HumanArtifactID)
		}
	}
	if len(humanIDs) == 0 {
		return text, nil
	}
	var artifacts []orm.WorkflowHumanArtifact
	if err := tx.Where("id IN ?", humanIDs).Order("created_at DESC").Find(&artifacts).Error; err != nil {
		return "", err
	}
	references := make(map[string]string, len(artifacts))
	for _, artifact := range artifacts {
		name, reference, ok := artifactfile.Reference(artifact.Value)
		if ok {
			if _, exists := references[name]; !exists {
				references[name] = reference
			}
		}
	}
	if len(references) == 0 {
		return text, nil
	}
	return markdownLinkDestination.ReplaceAllStringFunc(text, func(link string) string {
		inside := strings.TrimSpace(link[2 : len(link)-1])
		target, suffix := markdownTargetAndSuffix(inside)
		if !hostLocalTarget(target) {
			return link
		}
		filename := path.Base(strings.ReplaceAll(strings.TrimPrefix(target, "file://"), "\\", "/"))
		if decoded, err := url.PathUnescape(filename); err == nil {
			filename = decoded
		}
		if reference := references[filename]; reference != "" {
			return "](" + reference + suffix + ")"
		}
		return link
	}), nil
}

func markdownTargetAndSuffix(value string) (string, string) {
	if strings.HasPrefix(value, "<") {
		if end := strings.Index(value, ">"); end > 0 {
			return value[1:end], value[end+1:]
		}
	}
	if index := strings.IndexAny(value, " \t"); index > 0 {
		return value[:index], value[index:]
	}
	return value, ""
}

func hostLocalTarget(target string) bool {
	value := strings.TrimSpace(target)
	return strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "/static-files/") ||
		strings.HasPrefix(strings.ToLower(value), "file://") ||
		(len(value) >= 3 && value[1] == ':' && (value[2] == '\\' || value[2] == '/'))
}

func (a *externalChatApplication) eventsAfter(ctx context.Context, owner, runID string, after int64) ([]orm.ExternalChatRunEvent, orm.ExternalChatRun, error) {
	var run orm.ExternalChatRun
	if err := a.db.WithContext(ctx).Where("id = ? AND actor_user_id = ?", runID, owner).Take(&run).Error; err != nil {
		return nil, run, err
	}
	var events []orm.ExternalChatRunEvent
	if err := a.db.WithContext(ctx).Where("run_id = ? AND sequence > ?", run.ID, after).
		Order("sequence ASC").Find(&events).Error; err != nil {
		return nil, run, err
	}
	return events, run, nil
}

func (a *externalChatApplication) hasSemanticOutput(ctx context.Context, runID string) (bool, error) {
	var events []orm.ExternalChatRunEvent
	if err := a.db.WithContext(ctx).Select("text").
		Where("run_id = ? AND type = ?", runID, "message").Find(&events).Error; err != nil {
		return false, err
	}
	for _, event := range events {
		if event.Text != "" {
			return true, nil
		}
	}
	return false, nil
}

// executionProjections joins existing authorities into the user-facing read
// model. It deliberately does not write back to any source table.
func (a *externalChatApplication) executionProjections(
	ctx context.Context,
	owner string,
	historyIDs []string,
) (map[string]externalExecutionProjection, error) {
	result := make(map[string]externalExecutionProjection)
	if a == nil || a.db == nil || strings.TrimSpace(owner) == "" || len(historyIDs) == 0 {
		return result, nil
	}

	var runs []orm.ExternalChatRun
	if err := a.db.WithContext(ctx).
		Where("actor_user_id = ? AND history_id IN ?", owner, historyIDs).
		Order("created_at DESC").Find(&runs).Error; err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return result, nil
	}
	runByID := make(map[string]orm.ExternalChatRun, len(runs))
	runIDs := make([]string, 0, len(runs))
	for _, run := range runs {
		if _, exists := result[run.HistoryID]; exists {
			continue
		}
		result[run.HistoryID] = basicExternalExecutionProjection(run, a.now())
		runByID[run.ID] = run
		runIDs = append(runIDs, run.ID)
	}

	var invocations []orm.AgentInvocation
	if err := a.db.WithContext(ctx).
		Where("owner_user_id = ? AND external_ref IN ?", owner, runIDs).
		Order("started_at ASC, id ASC").Find(&invocations).Error; err != nil {
		return nil, err
	}
	toolSets := make(map[string]map[string]struct{}, len(runIDs))
	sessionSets := make(map[string]map[string]struct{}, len(runIDs))
	for _, invocation := range invocations {
		run, exists := runByID[invocation.ExternalRef]
		if !exists {
			continue
		}
		projection := result[run.HistoryID]
		projection.Invocation.Total++
		switch invocation.Status {
		case "running":
			projection.Invocation.Running++
		case "succeeded":
			projection.Invocation.Succeeded++
		case "failed":
			projection.Invocation.Failed++
		case "interrupted":
			projection.Invocation.Interrupted++
		}
		if toolSets[run.ID] == nil {
			toolSets[run.ID] = make(map[string]struct{})
		}
		if invocation.ToolName != "" {
			toolSets[run.ID][invocation.ToolName] = struct{}{}
		}
		if invocation.SessionID != "" {
			if sessionSets[run.ID] == nil {
				sessionSets[run.ID] = make(map[string]struct{})
			}
			sessionSets[run.ID][invocation.SessionID] = struct{}{}
		}
		result[run.HistoryID] = projection
	}

	sessionIDs := make([]string, 0)
	for runID, tools := range toolSets {
		run := runByID[runID]
		projection := result[run.HistoryID]
		for tool := range tools {
			projection.Invocation.Tools = append(projection.Invocation.Tools, tool)
		}
		sort.Strings(projection.Invocation.Tools)
		result[run.HistoryID] = projection
	}
	for _, sessions := range sessionSets {
		for sessionID := range sessions {
			sessionIDs = append(sessionIDs, sessionID)
		}
	}
	if len(sessionIDs) > 0 {
		var sessions []orm.WorkflowSession
		if err := a.db.WithContext(ctx).
			Where("id IN ? AND create_user_id = ?", sessionIDs, owner).
			Order("created_at ASC, id ASC").Find(&sessions).Error; err != nil {
			return nil, err
		}
		type revisionCount struct {
			SessionID string
			Revisions int64
			Selected  int64
		}
		var counts []revisionCount
		filteredSessionIDs := make([]string, 0, len(sessions))
		for _, session := range sessions {
			filteredSessionIDs = append(filteredSessionIDs, session.ID)
		}
		if len(filteredSessionIDs) > 0 {
			if err := a.db.WithContext(ctx).Model(&orm.WorkflowSlotRevision{}).
				Select("session_id, COUNT(*) AS revisions, SUM(CASE WHEN selected THEN 1 ELSE 0 END) AS selected").
				Where("session_id IN ?", filteredSessionIDs).Group("session_id").Scan(&counts).Error; err != nil {
				return nil, err
			}
		}
		countBySession := make(map[string]revisionCount, len(counts))
		for _, count := range counts {
			countBySession[count.SessionID] = count
		}
		for _, run := range runByID {
			projection := result[run.HistoryID]
			for _, session := range sessions {
				if _, linked := sessionSets[run.ID][session.ID]; !linked {
					continue
				}
				count := countBySession[session.ID]
				if len(projection.Workflows) < 20 {
					projection.Workflows = append(projection.Workflows, externalWorkflowProjection{
						SessionID: session.ID, WorkflowID: session.WorkflowID, Status: session.Status,
						CurrentStepID: session.CurrentStepID, StateVersion: session.StateVersion,
						ArtifactCount: count.Selected, ArtifactRevisionCount: count.Revisions,
					})
				}
				projection.ArtifactCount += count.Selected
				projection.ArtifactRevisionCount += count.Revisions
			}
			result[run.HistoryID] = projection
		}
	}

	type artifactCount struct {
		HistoryID string
		Total     int64
	}
	var directCounts []artifactCount
	if err := a.db.WithContext(ctx).Model(&orm.ConversationArtifact{}).
		Select("history_id, COUNT(*) AS total").
		Where("create_user_id = ? AND history_id IN ?", owner, historyIDs).
		Group("history_id").Scan(&directCounts).Error; err != nil {
		return nil, err
	}
	for _, count := range directCounts {
		projection, exists := result[count.HistoryID]
		if !exists {
			continue
		}
		projection.ArtifactCount += count.Total
		projection.ArtifactRevisionCount += count.Total
		result[count.HistoryID] = projection
	}
	providerStatuses := make(map[string]chatExecutorStatus)
	for _, run := range runByID {
		if _, loaded := providerStatuses[run.Provider]; loaded {
			continue
		}
		status, err := a.hostStatus(ctx, owner, run.Provider)
		if err != nil {
			return nil, err
		}
		providerStatuses[run.Provider] = status
	}
	for historyID, projection := range result {
		projection.HostOnline = providerStatuses[projection.Provider].HostOnline
		result[historyID] = projection
	}
	return result, nil
}

func (a *externalChatApplication) findRunForResume(ctx context.Context, owner, conversationID, historyID string) (*orm.ExternalChatRun, error) {
	query := a.db.WithContext(ctx).Where("actor_user_id = ? AND conversation_id = ?", owner, conversationID)
	if historyID != "" {
		query = query.Where("history_id = ?", historyID)
	}
	var run orm.ExternalChatRun
	if err := query.Order("created_at DESC").Take(&run).Error; err != nil {
		return nil, err
	}
	return &run, nil
}

func externalRunTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "stopped"
}
