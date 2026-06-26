package resourceupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/state"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
)

const (
	conversationIdleTTLKeyPrefix        = "lazymind:conversation_idle:ttl:"
	conversationIdleHistoryKeyPrefix    = "lazymind:conversation_idle:history:"
	conversationIdleProcessingKeyPrefix = "lazymind:conversation_idle:processing:"

	conversationIdleSkipSuperseded    = "superseded_by_new_message"
	conversationIdleSkipNoUserMessage = "no_non_empty_user_message"
)

type ConversationIdleRecord struct {
	SessionID      string
	UserID         string
	LastMessageID  string
	LastActivityAt time.Time
	UserContent    string
	AssistantText  string
}

type ConversationIdleFallbackResult struct {
	Found     int
	Triggered int
	Skipped   int
	Failed    int
}

type idleHistoryMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
}

type idleStateStore interface {
	AppendHistory(ctx context.Context, key string, messages []idleHistoryMessage, maxMessages int, ttl time.Duration) error
	ReadHistory(ctx context.Context, key string) ([]idleHistoryMessage, error)
	SetTTLKey(ctx context.Context, key, value string, ttl time.Duration) error
	AcquireProcessingLock(ctx context.Context, key string, ttl time.Duration) (bool, error)
	CleanupIdleKeys(ctx context.Context, ttlKey, expectedTTLValue, historyKey string) (bool, error)
}

type stateIdleStore struct {
	store state.Store
}

func newStateIdleStore(store state.Store) idleStateStore {
	if store == nil {
		return nil
	}
	return &stateIdleStore{store: store}
}

func (s *stateIdleStore) AppendHistory(ctx context.Context, key string, messages []idleHistoryMessage, maxMessages int, ttl time.Duration) error {
	if s == nil || s.store == nil {
		return errors.New("state store is nil")
	}
	if maxMessages <= 0 {
		return nil
	}
	for _, msg := range messages {
		body, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		if err := s.store.RPush(ctx, key, body, ttl); err != nil {
			return err
		}
	}
	return s.store.LTrim(ctx, key, int64(-maxMessages), -1)
}

func (s *stateIdleStore) ReadHistory(ctx context.Context, key string) ([]idleHistoryMessage, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("state store is nil")
	}
	raw, err := s.store.LRange(ctx, key, 0, -1)
	if err != nil {
		return nil, err
	}
	messages := make([]idleHistoryMessage, 0, len(raw))
	for _, item := range raw {
		var msg idleHistoryMessage
		if err := json.Unmarshal([]byte(item), &msg); err != nil {
			continue
		}
		msg.Role = strings.TrimSpace(msg.Role)
		messages = append(messages, msg)
	}
	return messages, nil
}

func (s *stateIdleStore) SetTTLKey(ctx context.Context, key, value string, ttl time.Duration) error {
	if s == nil || s.store == nil {
		return errors.New("state store is nil")
	}
	return s.store.Set(ctx, key, []byte(value), ttl)
}

func (s *stateIdleStore) AcquireProcessingLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if s == nil || s.store == nil {
		return false, errors.New("state store is nil")
	}
	return s.store.SetNX(ctx, key, []byte("1"), ttl)
}

func (s *stateIdleStore) CleanupIdleKeys(ctx context.Context, ttlKey, expectedTTLValue, historyKey string) (bool, error) {
	if s == nil || s.store == nil {
		return false, errors.New("state store is nil")
	}
	value, err := s.store.Get(ctx, ttlKey)
	missing := state.IsMissing(err)
	if err != nil && !missing {
		return false, err
	}
	if missing || string(value) == expectedTTLValue {
		return true, s.store.Del(ctx, ttlKey, historyKey)
	}
	return false, nil
}

type IdleRecorder struct {
	db    *gorm.DB
	store idleStateStore
	cfg   Config
	clock clockFunc
}

func NewIdleRecorder(db *gorm.DB, store state.Store, cfg Config) *IdleRecorder {
	return newIdleRecorderWithStore(db, newStateIdleStore(store), cfg)
}

func RecordConversationIdleMessage(ctx context.Context, db *gorm.DB, store state.Store, record ConversationIdleRecord) error {
	return NewIdleRecorder(db, store, DefaultConfig()).RecordConversationMessage(ctx, record)
}

func newIdleRecorderWithStore(db *gorm.DB, store idleStateStore, cfg Config) *IdleRecorder {
	cfg = normalizeConfig(cfg)
	return &IdleRecorder{
		db:    db,
		store: store,
		cfg:   cfg,
		clock: time.Now,
	}
}

func (r *IdleRecorder) RecordConversationMessage(ctx context.Context, record ConversationIdleRecord) error {
	if r == nil || r.db == nil {
		return errors.New("idle recorder db is nil")
	}
	if r.store == nil {
		return errors.New("idle recorder state store is nil")
	}
	record.SessionID = strings.TrimSpace(record.SessionID)
	record.UserID = strings.TrimSpace(record.UserID)
	record.LastMessageID = strings.TrimSpace(record.LastMessageID)
	if record.SessionID == "" || record.UserID == "" || record.LastMessageID == "" {
		return errors.New("session_id, user_id, and last_message_id are required")
	}
	now := r.clock().UTC()
	lastActivityAt := record.LastActivityAt.UTC()
	if lastActivityAt.IsZero() {
		lastActivityAt = now
	}
	eventID := idleEventID(record.SessionID, record.LastMessageID)
	dueAt := lastActivityAt.Add(r.cfg.ConversationIdleSeconds)

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&orm.ConversationIdleEvent{}).
			Where("session_id = ? AND status = ? AND event_id <> ?", record.SessionID, orm.ConversationIdleEventStatusWaiting, eventID).
			Updates(map[string]any{
				"status":      orm.ConversationIdleEventStatusSkipped,
				"skip_reason": conversationIdleSkipSuperseded,
				"updated_at":  now,
			}).Error; err != nil {
			return err
		}
		event := orm.ConversationIdleEvent{
			ID:             common.GenerateID(),
			EventID:        eventID,
			SessionID:      record.SessionID,
			UserID:         record.UserID,
			LastMessageID:  record.LastMessageID,
			LastActivityAt: lastActivityAt,
			DueAt:          dueAt,
			Status:         orm.ConversationIdleEventStatusWaiting,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		return tx.Clauses(clauseOnConflictDoNothing()).Create(&event).Error
	})
	if err != nil {
		return err
	}

	messages := []idleHistoryMessage{
		{Role: "user", Content: record.UserContent},
		{Role: "assistant", Content: record.AssistantText},
	}
	if err := r.store.SetTTLKey(ctx, conversationIdleTTLKey(record.SessionID), eventID, r.cfg.ConversationIdleSeconds); err != nil {
		return err
	}
	if err := r.store.AppendHistory(ctx, conversationIdleHistoryKey(record.SessionID), messages, r.cfg.ConversationIdleHistoryMaxMessages, r.cfg.ConversationIdleHistoryTTL); err != nil {
		return err
	}
	resourceUpdateInfo(logEventIdleEventRecorded).
		Str("event_id", eventID).
		Str("session_id", record.SessionID).
		Str("user_id", record.UserID).
		Str("last_message_id", record.LastMessageID).
		Time("due_at", dueAt).
		Msg(logEventIdleEventRecorded)
	return nil
}

type IdleProcessor struct {
	db       *gorm.DB
	store    idleStateStore
	cfg      Config
	workerID string
	clock    clockFunc
}

func NewIdleProcessor(db *gorm.DB, store state.Store, cfg Config, workerID string) *IdleProcessor {
	return newIdleProcessorWithStore(db, newStateIdleStore(store), cfg, workerID)
}

func newIdleProcessorWithStore(db *gorm.DB, store idleStateStore, cfg Config, workerID string) *IdleProcessor {
	cfg = normalizeConfig(cfg)
	if strings.TrimSpace(workerID) == "" {
		workerID = defaultWorkerID("resourceupdate-idle")
	}
	return &IdleProcessor{
		db:       db,
		store:    store,
		cfg:      cfg,
		workerID: workerID,
		clock:    time.Now,
	}
}

func (p *IdleProcessor) ProcessLatestWaitingSession(ctx context.Context, sessionID string) error {
	if p == nil || p.db == nil {
		return errors.New("idle processor db is nil")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	var event orm.ConversationIdleEvent
	err := p.db.WithContext(ctx).
		Where("session_id = ? AND status = ?", sessionID, orm.ConversationIdleEventStatusWaiting).
		Order("due_at DESC, created_at DESC").
		Take(&event).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return p.ProcessEvent(ctx, event.EventID)
}

func (p *IdleProcessor) RunFallbackOnce(ctx context.Context) (ConversationIdleFallbackResult, error) {
	var result ConversationIdleFallbackResult
	if p == nil || p.db == nil {
		return result, errors.New("idle processor db is nil")
	}
	now := p.clock().UTC()
	var eventIDs []string
	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return withUpdateSkipLocked(tx.Model(&orm.ConversationIdleEvent{})).
			Where("status = ? AND due_at <= ?", orm.ConversationIdleEventStatusWaiting, now).
			Order("due_at ASC").
			Limit(p.cfg.ConversationIdleFallbackBatchSize).
			Pluck("event_id", &eventIDs).Error
	})
	if err != nil {
		return result, err
	}
	result.Found = len(eventIDs)
	for _, eventID := range eventIDs {
		before, err := p.loadEventStatus(ctx, eventID)
		if err != nil {
			return result, err
		}
		if err := p.ProcessEvent(ctx, eventID); err != nil {
			return result, err
		}
		after, err := p.loadEventStatus(ctx, eventID)
		if err != nil {
			return result, err
		}
		if before == after {
			continue
		}
		switch after {
		case orm.ConversationIdleEventStatusTriggered:
			result.Triggered++
		case orm.ConversationIdleEventStatusSkipped:
			result.Skipped++
		case orm.ConversationIdleEventStatusFailed:
			result.Failed++
		}
	}
	return result, nil
}

func (p *IdleProcessor) ProcessEvent(ctx context.Context, eventID string) error {
	if p == nil || p.db == nil {
		return errors.New("idle processor db is nil")
	}
	if p.store == nil {
		return errors.New("idle processor state store is nil")
	}
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return nil
	}
	locked, err := p.store.AcquireProcessingLock(ctx, conversationIdleProcessingKey(eventID), p.cfg.WorkerLockTTL)
	if err != nil {
		return err
	}
	if !locked {
		resourceUpdateInfo(logEventIdleEventSkipped).
			Str("event_id", eventID).
			Str("reason", "processing_lock_busy").
			Msg(logEventIdleEventSkipped)
		return nil
	}
	now := p.clock().UTC()
	cleanupSessionID := ""
	err = p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var event orm.ConversationIdleEvent
		if err := withUpdateLock(tx).Where("event_id = ?", eventID).Take(&event).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				resourceUpdateInfo(logEventIdleEventSkipped).
					Str("event_id", eventID).
					Str("reason", "event_not_found").
					Msg(logEventIdleEventSkipped)
				return nil
			}
			return err
		}
		if event.Status != orm.ConversationIdleEventStatusWaiting {
			resourceUpdateInfo(logEventIdleEventSkipped).
				Str("event_id", event.EventID).
				Str("session_id", event.SessionID).
				Str("user_id", event.UserID).
				Str("status", event.Status).
				Str("reason", "event_not_waiting").
				Msg(logEventIdleEventSkipped)
			return nil
		}
		if now.Before(event.DueAt) {
			resourceUpdateInfo(logEventIdleEventSkipped).
				Str("event_id", event.EventID).
				Str("session_id", event.SessionID).
				Str("user_id", event.UserID).
				Time("due_at", event.DueAt).
				Str("reason", "event_not_due").
				Msg(logEventIdleEventSkipped)
			return nil
		}
		if err := tx.Model(&orm.ConversationIdleEvent{}).Where("id = ?", event.ID).Updates(map[string]any{
			"status":     orm.ConversationIdleEventStatusProcessing,
			"updated_at": now,
		}).Error; err != nil {
			return err
		}

		history, err := p.store.ReadHistory(ctx, conversationIdleHistoryKey(event.SessionID))
		if err != nil {
			cleanupSessionID = event.SessionID
			return p.markEventFailed(tx, event.ID, now, "read_history_failed", err.Error())
		}
		if !historyHasNonEmptyUserMessage(history) {
			cleanupSessionID = event.SessionID
			return p.markEventSkipped(tx, event.ID, now, conversationIdleSkipNoUserMessage)
		}
		historyJSON, err := json.Marshal(history)
		if err != nil {
			cleanupSessionID = event.SessionID
			return p.markEventFailed(tx, event.ID, now, "marshal_history_failed", err.Error())
		}

		memory, err := loadSystemMemoryForIdle(ctx, tx, event.UserID)
		if err != nil {
			cleanupSessionID = event.SessionID
			return p.markEventFailed(tx, event.ID, now, "load_system_memory_failed", err.Error())
		}
		preference, err := loadSystemUserPreferenceForIdle(ctx, tx, event.UserID)
		if err != nil {
			cleanupSessionID = event.SessionID
			return p.markEventFailed(tx, event.ID, now, "load_system_user_preference_failed", err.Error())
		}

		userContent := evolution.FormatSystemUserPreferenceForChat(preference)
		memoryTaskID, err := createIdleGenerateTask(ctx, tx, event, memory.ID, memory.Content, userContent, historyJSON, now)
		if err != nil {
			cleanupSessionID = event.SessionID
			return p.markEventFailed(tx, event.ID, now, "create_memory_task_failed", err.Error())
		}
		resourceUpdateInfo(logEventIdleEventTriggered).
			Str("event_id", event.EventID).
			Str("session_id", event.SessionID).
			Str("user_id", event.UserID).
			Str("memory_task_id", memoryTaskID).
			Str("user_preference_task_id", memoryTaskID).
			Int("history_message_count", len(history)).
			Msg(logEventIdleEventTriggered)
		triggeredAt := now
		cleanupSessionID = event.SessionID
		return tx.Model(&orm.ConversationIdleEvent{}).Where("id = ?", event.ID).Updates(map[string]any{
			"status":                  orm.ConversationIdleEventStatusTriggered,
			"error_code":              "",
			"error_message":           "",
			"memory_task_id":          memoryTaskID,
			"user_preference_task_id": memoryTaskID,
			"triggered_at":            &triggeredAt,
			"updated_at":              now,
		}).Error
	})
	if err == nil && cleanupSessionID != "" {
		if cleanupErr := p.cleanupIdleStateKeys(ctx, cleanupSessionID, eventID); cleanupErr != nil {
			resourceUpdateWarn(logEventIdleStateCleanupFailed, cleanupErr).
				Str("event_id", eventID).
				Str("session_id", cleanupSessionID).
				Msg(logEventIdleStateCleanupFailed)
		}
	}
	return err
}

func (p *IdleProcessor) cleanupIdleStateKeys(ctx context.Context, sessionID, eventID string) error {
	if p == nil || p.store == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	eventID = strings.TrimSpace(eventID)
	if sessionID == "" || eventID == "" {
		return nil
	}
	_, err := p.store.CleanupIdleKeys(ctx, conversationIdleTTLKey(sessionID), eventID, conversationIdleHistoryKey(sessionID))
	return err
}

func (p *IdleProcessor) loadEventStatus(ctx context.Context, eventID string) (string, error) {
	var event orm.ConversationIdleEvent
	if err := p.db.WithContext(ctx).Select("status").Where("event_id = ?", eventID).Take(&event).Error; err != nil {
		return "", err
	}
	return event.Status, nil
}

func (p *IdleProcessor) markEventSkipped(tx *gorm.DB, id string, now time.Time, reason string) error {
	resourceUpdateInfo(logEventIdleEventSkipped).
		Str("event_row_id", id).
		Str("reason", reason).
		Msg(logEventIdleEventSkipped)
	return tx.Model(&orm.ConversationIdleEvent{}).Where("id = ?", id).Updates(map[string]any{
		"status":      orm.ConversationIdleEventStatusSkipped,
		"skip_reason": reason,
		"updated_at":  now,
	}).Error
}

func (p *IdleProcessor) markEventFailed(tx *gorm.DB, id string, now time.Time, code, message string) error {
	resourceUpdateWarn(logEventIdleEventFailed, nil).
		Str("event_row_id", id).
		Str("error_code", code).
		Str("error_message", message).
		Msg(logEventIdleEventFailed)
	return tx.Model(&orm.ConversationIdleEvent{}).Where("id = ?", id).Updates(map[string]any{
		"status":        orm.ConversationIdleEventStatusFailed,
		"error_code":    code,
		"error_message": message,
		"updated_at":    now,
	}).Error
}

func loadSystemMemoryForIdle(ctx context.Context, db *gorm.DB, userID string) (orm.SystemMemory, error) {
	var row orm.SystemMemory
	err := db.WithContext(ctx).
		Where("user_id = ?", strings.TrimSpace(userID)).
		Order("created_at ASC").
		Take(&row).Error
	return row, err
}

func loadSystemUserPreferenceForIdle(ctx context.Context, db *gorm.DB, userID string) (orm.SystemUserPreference, error) {
	var row orm.SystemUserPreference
	err := db.WithContext(ctx).
		Where("user_id = ?", strings.TrimSpace(userID)).
		Order("created_at ASC").
		Take(&row).Error
	return row, err
}

func createIdleGenerateTask(ctx context.Context, db *gorm.DB, event orm.ConversationIdleEvent, resourceID, memoryContent, userContent string, historyJSON json.RawMessage, now time.Time) (string, error) {
	triggerID := fmt.Sprintf("%s:%s", strings.TrimSpace(event.EventID), "memory_review")
	request := memoryGenerateRequestJSON{
		SessionID: strings.TrimSpace(event.SessionID),
		History:   historyJSON,
		Memory:    memoryContent,
		User:      userContent,
	}
	body, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	task := orm.ResourceUpdateTask{
		ID:           common.GenerateID(),
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeMemory,
		UserID:       strings.TrimSpace(event.UserID),
		ResourceID:   strings.TrimSpace(resourceID),
		TriggerType:  orm.ResourceUpdateTriggerTypeConversationIdle,
		TriggerID:    triggerID,
		Status:       orm.ResourceUpdateTaskStatusPending,
		RequestJSON:  body,
		NextRunAt:    now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	tx := db.WithContext(ctx).Clauses(clauseOnConflictDoNothing()).Create(&task)
	if tx.Error != nil {
		return "", tx.Error
	}
	if tx.RowsAffected == 1 {
		return task.ID, nil
	}
	var existing orm.ResourceUpdateTask
	if err := db.WithContext(ctx).
		Select("id").
		Where("task_type = ? AND resource_type = ? AND trigger_type = ? AND trigger_id = ?",
			orm.ResourceUpdateTaskTypeGenerateReview, orm.ResourceUpdateResourceTypeMemory, orm.ResourceUpdateTriggerTypeConversationIdle, triggerID).
		Take(&existing).Error; err != nil {
		return "", err
	}
	return existing.ID, nil
}

func historyHasNonEmptyUserMessage(history []idleHistoryMessage) bool {
	for _, msg := range history {
		if strings.TrimSpace(msg.Role) == "user" && strings.TrimSpace(msg.Content) != "" {
			return true
		}
	}
	return false
}

func idleEventID(sessionID, messageID string) string {
	return strings.TrimSpace(sessionID) + ":" + strings.TrimSpace(messageID)
}

func conversationIdleTTLKey(sessionID string) string {
	return conversationIdleTTLKeyPrefix + strings.TrimSpace(sessionID)
}

func conversationIdleHistoryKey(sessionID string) string {
	return conversationIdleHistoryKeyPrefix + strings.TrimSpace(sessionID)
}

func conversationIdleProcessingKey(eventID string) string {
	return conversationIdleProcessingKeyPrefix + strings.TrimSpace(eventID)
}

func parseConversationIdleTTLKey(key string) (string, bool) {
	if !strings.HasPrefix(key, conversationIdleTTLKeyPrefix) {
		return "", false
	}
	sessionID := strings.TrimSpace(strings.TrimPrefix(key, conversationIdleTTLKeyPrefix))
	return sessionID, sessionID != ""
}

func runIdleFallbackLoop(ctx context.Context, processor *IdleProcessor, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := processor.RunFallbackOnce(ctx); err != nil {
			resourceUpdateWarn(logEventIdleFallbackFailed, err).
				Msg(logEventIdleFallbackFailed)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runIdleExpiredKeyNotifyLoop(ctx context.Context, store state.Store, processor *IdleProcessor) {
	notifier, ok := store.(state.ExpiredKeyNotifier)
	if !ok || processor == nil {
		return
	}
	notifier.SubscribeExpiredKeys(ctx, func(key string) error {
		sessionID, ok := parseConversationIdleTTLKey(key)
		if !ok {
			return nil
		}
		if err := processor.ProcessLatestWaitingSession(ctx, sessionID); err != nil {
			resourceUpdateWarn(logEventIdleExpiredKeyNotifyFailed, err).
				Str("session_id", sessionID).
				Msg(logEventIdleExpiredKeyNotifyFailed)
			return err
		}
		return nil
	})
}
