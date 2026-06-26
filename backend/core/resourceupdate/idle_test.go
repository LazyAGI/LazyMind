package resourceupdate

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

type fakeIdleStore struct {
	mu      sync.Mutex
	history map[string][]idleHistoryMessage
	values  map[string]string
	locks   map[string]bool
}

func newFakeIdleStore() *fakeIdleStore {
	return &fakeIdleStore{
		history: map[string][]idleHistoryMessage{},
		values:  map[string]string{},
		locks:   map[string]bool{},
	}
}

func (s *fakeIdleStore) AppendHistory(_ context.Context, key string, messages []idleHistoryMessage, maxMessages int, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history[key] = append(s.history[key], messages...)
	if maxMessages > 0 && len(s.history[key]) > maxMessages {
		s.history[key] = append([]idleHistoryMessage(nil), s.history[key][len(s.history[key])-maxMessages:]...)
	}
	return nil
}

func (s *fakeIdleStore) ReadHistory(_ context.Context, key string) ([]idleHistoryMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]idleHistoryMessage(nil), s.history[key]...), nil
}

func (s *fakeIdleStore) SetTTLKey(_ context.Context, key, value string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *fakeIdleStore) AcquireProcessingLock(_ context.Context, key string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks[key] {
		return false, nil
	}
	s.locks[key] = true
	return true, nil
}

func (s *fakeIdleStore) CleanupIdleKeys(_ context.Context, ttlKey, expectedTTLValue, historyKey string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[ttlKey]
	if ok && value != expectedTTLValue {
		return false, nil
	}
	delete(s.values, ttlKey)
	delete(s.history, historyKey)
	return true, nil
}

func TestIdleRecorderSupersedesWaitingEvent(t *testing.T) {
	db := newIdleTestDB(t)
	store := newFakeIdleStore()
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	recorder := newIdleRecorderWithStore(db, store, Config{ConversationIdleSeconds: time.Hour})
	recorder.clock = func() time.Time { return now }

	if err := recorder.RecordConversationMessage(ctx, ConversationIdleRecord{
		SessionID:      "session-1",
		UserID:         "user-1",
		LastMessageID:  "h1",
		LastActivityAt: now,
		UserContent:    "hello",
		AssistantText:  "hi",
	}); err != nil {
		t.Fatalf("record first message: %v", err)
	}
	if err := recorder.RecordConversationMessage(ctx, ConversationIdleRecord{
		SessionID:      "session-1",
		UserID:         "user-1",
		LastMessageID:  "h2",
		LastActivityAt: now.Add(time.Minute),
		UserContent:    "next",
		AssistantText:  "ok",
	}); err != nil {
		t.Fatalf("record second message: %v", err)
	}

	var events []orm.ConversationIdleEvent
	if err := db.Order("last_message_id ASC").Find(&events).Error; err != nil {
		t.Fatalf("list idle events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected two idle events, got %d", len(events))
	}
	if events[0].Status != orm.ConversationIdleEventStatusSkipped || events[0].SkipReason != conversationIdleSkipSuperseded {
		t.Fatalf("expected first event superseded, got %#v", events[0])
	}
	if events[1].Status != orm.ConversationIdleEventStatusWaiting {
		t.Fatalf("expected second event waiting, got %#v", events[1])
	}
	if got := store.values[conversationIdleTTLKey("session-1")]; got != "session-1:h2" {
		t.Fatalf("expected ttl value latest event id, got %q", got)
	}
}

func TestConversationIdleDefaultsMatchPlan2(t *testing.T) {
	cfg := normalizeConfig(Config{})
	if cfg.ConversationIdleSeconds != 5*time.Minute ||
		cfg.ConversationIdleHistoryTTL != 30*time.Minute ||
		cfg.ConversationIdleHistoryMaxMessages != 100 ||
		cfg.ConversationIdleFallbackScanInterval != 5*time.Minute ||
		cfg.ConversationIdleFallbackBatchSize != 100 ||
		!cfg.ConversationIdleEnableExpiredKeyNotify {
		t.Fatalf("unexpected idle defaults: %#v", cfg)
	}
	if cfg := normalizeConfig(Config{}.WithConversationIdleExpiredKeyNotify(false)); cfg.ConversationIdleEnableExpiredKeyNotify {
		t.Fatalf("explicit expired-key notify disable was not preserved: %#v", cfg)
	}
}

func TestIdleRecorderTrimsHistorySnapshot(t *testing.T) {
	db := newIdleTestDB(t)
	store := newFakeIdleStore()
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	recorder := newIdleRecorderWithStore(db, store, Config{
		ConversationIdleSeconds:            time.Hour,
		ConversationIdleHistoryMaxMessages: 3,
	})
	recorder.clock = func() time.Time { return now }

	for i, msg := range []string{"u1", "u2", "u3"} {
		if err := recorder.RecordConversationMessage(ctx, ConversationIdleRecord{
			SessionID:      "session-trim",
			UserID:         "user-1",
			LastMessageID:  "h" + msg,
			LastActivityAt: now.Add(time.Duration(i) * time.Minute),
			UserContent:    msg,
			AssistantText:  "a" + msg,
		}); err != nil {
			t.Fatalf("record message %d: %v", i, err)
		}
	}

	history := store.history[conversationIdleHistoryKey("session-trim")]
	if len(history) != 3 {
		t.Fatalf("expected trimmed history length 3, got %d: %#v", len(history), history)
	}
	got := history[0].Content + "," + history[1].Content + "," + history[2].Content
	if got != "au2,u3,au3" {
		t.Fatalf("unexpected trimmed history: %s", got)
	}
}

func TestIdleProcessorSkipsWhenNoUserMessage(t *testing.T) {
	db := newIdleTestDB(t)
	store := newFakeIdleStore()
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	insertIdleResources(t, db, "user-1", now)
	insertIdleEvent(t, db, "session-empty:h1", "session-empty", "user-1", "h1", now.Add(-time.Hour), now.Add(-time.Minute))
	store.history[conversationIdleHistoryKey("session-empty")] = []idleHistoryMessage{{Role: "assistant", Content: "only assistant"}}

	processor := newIdleProcessorWithStore(db, store, Config{WorkerLockTTL: time.Minute}, "idle-test")
	processor.clock = func() time.Time { return now }
	if err := processor.ProcessEvent(ctx, "session-empty:h1"); err != nil {
		t.Fatalf("process idle event: %v", err)
	}

	var event orm.ConversationIdleEvent
	if err := db.First(&event, "event_id = ?", "session-empty:h1").Error; err != nil {
		t.Fatalf("read event: %v", err)
	}
	if event.Status != orm.ConversationIdleEventStatusSkipped || event.SkipReason != conversationIdleSkipNoUserMessage {
		t.Fatalf("expected no user message skip, got %#v", event)
	}
	var taskCount int64
	if err := db.Model(&orm.ResourceUpdateTask{}).Count(&taskCount).Error; err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("expected no tasks, got %d", taskCount)
	}
	if _, ok := store.history[conversationIdleHistoryKey("session-empty")]; ok {
		t.Fatal("expected idle history to be cleaned after skipped event")
	}
}

func TestIdleProcessorIsIdempotentForSameEvent(t *testing.T) {
	db := newIdleTestDB(t)
	store := newFakeIdleStore()
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	insertIdleResources(t, db, "user-1", now)
	insertIdleEvent(t, db, "session-idem:h1", "session-idem", "user-1", "h1", now.Add(-time.Hour), now.Add(-time.Minute))
	store.history[conversationIdleHistoryKey("session-idem")] = []idleHistoryMessage{
		{Role: "user", Content: "remember this"},
		{Role: "assistant", Content: "done"},
	}

	processor := newIdleProcessorWithStore(db, store, Config{WorkerLockTTL: time.Minute}, "idle-test")
	processor.clock = func() time.Time { return now }
	if err := processor.ProcessEvent(ctx, "session-idem:h1"); err != nil {
		t.Fatalf("process idle event first: %v", err)
	}
	store.locks = map[string]bool{}
	if err := processor.ProcessEvent(ctx, "session-idem:h1"); err != nil {
		t.Fatalf("process idle event second: %v", err)
	}

	var taskCount int64
	if err := db.Model(&orm.ResourceUpdateTask{}).Count(&taskCount).Error; err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if taskCount != 1 {
		t.Fatalf("expected exactly one task, got %d", taskCount)
	}
	var event orm.ConversationIdleEvent
	if err := db.First(&event, "event_id = ?", "session-idem:h1").Error; err != nil {
		t.Fatalf("read event: %v", err)
	}
	if event.Status != orm.ConversationIdleEventStatusTriggered || event.MemoryTaskID == "" || event.UserPreferenceTaskID == "" {
		t.Fatalf("unexpected event after idempotent processing: %#v", event)
	}
}

func TestIdleFallbackCreatesCombinedMemoryReviewTaskWithoutSensitiveRequestFields(t *testing.T) {
	db := newIdleTestDB(t)
	store := newFakeIdleStore()
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	insertIdleResources(t, db, "user-1", now)
	insertIdleEvent(t, db, "session-fallback:h1", "session-fallback", "user-1", "h1", now.Add(-time.Hour), now.Add(-time.Minute))
	store.history[conversationIdleHistoryKey("session-fallback")] = []idleHistoryMessage{
		{Role: "user", Content: "please remember I like concise answers"},
		{Role: "assistant", Content: "noted"},
	}

	processor := newIdleProcessorWithStore(db, store, Config{
		WorkerLockTTL:                     time.Minute,
		ConversationIdleFallbackBatchSize: 10,
	}, "idle-test")
	processor.clock = func() time.Time { return now }
	result, err := processor.RunFallbackOnce(ctx)
	if err != nil {
		t.Fatalf("fallback run: %v", err)
	}
	if result.Found != 1 || result.Triggered != 1 {
		t.Fatalf("unexpected fallback result: %#v", result)
	}

	var tasks []orm.ResourceUpdateTask
	if err := db.Order("resource_type ASC").Find(&tasks).Error; err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one task, got %d", len(tasks))
	}
	for _, task := range tasks {
		if task.TaskType != orm.ResourceUpdateTaskTypeGenerateReview ||
			task.ResourceType != orm.ResourceUpdateResourceTypeMemory ||
			task.TriggerType != orm.ResourceUpdateTriggerTypeConversationIdle ||
			task.Status != orm.ResourceUpdateTaskStatusPending {
			t.Fatalf("unexpected task: %#v", task)
		}
		if !strings.HasPrefix(task.TriggerID, "session-fallback:h1:") {
			t.Fatalf("unexpected trigger id: %s", task.TriggerID)
		}
		assertRequestJSONHasNoSensitiveFields(t, task.RequestJSON)
		var request memoryGenerateRequestJSON
		if err := json.Unmarshal(task.RequestJSON, &request); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		if request.SessionID != "session-fallback" || len(request.History) == 0 {
			t.Fatalf("unexpected request json: %#v", request)
		}
		if request.Memory != "current memory" {
			t.Fatalf("expected memory content in request, got %q", request.Memory)
		}
		wantUser := "---\nagent_persona: |-\n 当前角色\npreferred_name: |-\n 当前称谓\nresponse_style: |-\n 当前风格\n---\n\ncurrent preference"
		if request.User != wantUser {
			t.Fatalf("expected formatted user_preference in request, got %q", request.User)
		}
		if strings.Contains(string(task.RequestJSON), "api_key") || strings.Contains(string(task.RequestJSON), "model_configs") || strings.Contains(string(task.RequestJSON), "llm_config") {
			t.Fatalf("request_json contains sensitive field: %s", string(task.RequestJSON))
		}
	}
	if _, ok := store.history[conversationIdleHistoryKey("session-fallback")]; ok {
		t.Fatal("expected idle history to be cleaned after triggered event")
	}
}

func TestIdleCleanupKeepsHistoryForNewerEvent(t *testing.T) {
	store := newFakeIdleStore()
	ctx := context.Background()
	store.values[conversationIdleTTLKey("session-active")] = "session-active:h2"
	store.history[conversationIdleHistoryKey("session-active")] = []idleHistoryMessage{
		{Role: "user", Content: "new message"},
	}

	cleaned, err := store.CleanupIdleKeys(ctx, conversationIdleTTLKey("session-active"), "session-active:h1", conversationIdleHistoryKey("session-active"))
	if err != nil {
		t.Fatalf("cleanup idle keys: %v", err)
	}
	if cleaned {
		t.Fatal("expected cleanup to skip keys owned by newer event")
	}
	if _, ok := store.history[conversationIdleHistoryKey("session-active")]; !ok {
		t.Fatal("expected newer event history to remain")
	}
}

func newIdleTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "idle.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.ResourceUpdateTask{},
		&orm.ConversationIdleEvent{},
		&orm.SystemMemory{},
		&orm.SystemUserPreference{},
	); err != nil {
		t.Fatalf("auto migrate idle models: %v", err)
	}
	return db.DB
}

func insertIdleResources(t *testing.T, db *gorm.DB, userID string, now time.Time) {
	t.Helper()
	if err := db.Create(&orm.SystemMemory{
		ID:          "memory-" + userID,
		UserID:      userID,
		Content:     "current memory",
		ContentHash: "memory-hash",
		Version:     1,
		AutoEvo:     true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}).Error; err != nil {
		t.Fatalf("insert memory: %v", err)
	}
	if err := db.Create(&orm.SystemUserPreference{
		ID:            "preference-" + userID,
		UserID:        userID,
		Content:       "current preference",
		AgentPersona:  "当前角色",
		PreferredName: "当前称谓",
		ResponseStyle: "当前风格",
		ContentHash:   "preference-hash",
		Version:       1,
		AutoEvo:       true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}).Error; err != nil {
		t.Fatalf("insert preference: %v", err)
	}
}

func insertIdleEvent(t *testing.T, db *gorm.DB, eventID, sessionID, userID, messageID string, lastActivityAt, dueAt time.Time) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Create(&orm.ConversationIdleEvent{
		ID:             "event-" + strings.ReplaceAll(eventID, ":", "-"),
		EventID:        eventID,
		SessionID:      sessionID,
		UserID:         userID,
		LastMessageID:  messageID,
		LastActivityAt: lastActivityAt,
		DueAt:          dueAt,
		Status:         orm.ConversationIdleEventStatusWaiting,
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("insert idle event %s: %v", eventID, err)
	}
}
