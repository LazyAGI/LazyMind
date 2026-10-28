package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
)

type followUpFailingStatusStore struct{ state.Store }

func (store followUpFailingStatusStore) HGet(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("status backend unavailable")
}

func followUpTestDB(t *testing.T) *orm.DB {
	t.Helper()
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ChatHistory{}, &orm.ConversationFollowUp{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	return db
}

func TestClaimConversationFollowUpWaitsForTerminalHistory(t *testing.T) {
	db := followUpTestDB(t)
	seedFollowUp(t, db, orm.ConversationFollowUp{
		ID: "queued", ConversationID: "conversation", Mode: followUpModeQueued, Status: followUpPending,
	})
	now := time.Now().UTC()
	if err := db.Create(&orm.ChatHistory{
		ID: "history", ConversationID: "conversation", Seq: 1, RunID: "run", RunStatus: "generating",
		RawContent: "question", Content: "question", TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error; err != nil {
		t.Fatal(err)
	}

	recorder, request := followUpRequest(http.MethodPost, "conversation", "", nil)
	ClaimNextConversationFollowUp(recorder, request)
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte(`"item":null`)) {
		t.Fatalf("generating history was not held: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var pending orm.ConversationFollowUp
	if err := db.First(&pending, "id = ?", "queued").Error; err != nil {
		t.Fatal(err)
	}
	if pending.Status != followUpPending {
		t.Fatalf("follow-up was claimed before history finalized: %+v", pending)
	}

	if err := db.Model(&orm.ChatHistory{}).Where("id = ?", "history").Updates(map[string]any{
		"result": "answer", "run_status": "completed", "update_time": time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	recorder, request = followUpRequest(http.MethodPost, "conversation", "", nil)
	ClaimNextConversationFollowUp(recorder, request)
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte(`"status":"dispatching"`)) {
		t.Fatalf("terminal history did not release queue: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAcceptedSteeringMessagesRemainInLaterModelContext(t *testing.T) {
	db := followUpTestDB(t)
	now := time.Now().UTC()
	histories := []orm.ChatHistory{
		{ID: "history-1", ConversationID: "conversation", Seq: 1, RawContent: "original", Result: "partial answer"},
		{ID: "history-2", ConversationID: "conversation", Seq: 2, RawContent: "next", Result: "next answer"},
	}
	for index := range histories {
		histories[index].TimeMixin = orm.TimeMixin{CreateTime: now, UpdateTime: now}
		if err := db.Create(&histories[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	for index, guidance := range []struct {
		id   string
		text string
	}{{id: "steer-1", text: "first guidance"}, {id: "steer-2", text: "second guidance"}} {
		acceptedAt := now.Add(time.Duration(index) * time.Second)
		seedFollowUp(t, db, orm.ConversationFollowUp{
			ID: guidance.id, ConversationID: "conversation",
			Mode: followUpModeSteer, Status: followUpAccepted,
			TargetHistoryID: "history-1", AcceptedAt: &acceptedAt,
		})
		if err := db.Model(&orm.ConversationFollowUp{}).
			Where("id = ?", guidance.id).
			Update("text", guidance.text).Error; err != nil {
			t.Fatal(err)
		}
	}

	messages := injectConversationSteeringMessages(
		context.Background(), db.DB, "conversation", "u1", histories,
		buildHistoryMessages(histories, nil),
	)
	got := make([]string, 0, len(messages))
	for _, message := range messages {
		got = append(got, message["role"].(string)+":"+message["content"].(string))
	}
	want := []string{
		"user:original", "assistant:partial answer",
		"user:first guidance", "user:second guidance",
		"user:next", "assistant:next answer",
	}
	if len(got) != len(want) {
		t.Fatalf("unexpected model history length: got=%#v want=%#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("model history[%d]=%q want %q; all=%#v", index, got[index], want[index], got)
		}
	}
}

func TestLegacyFollowUpAssistantSnapshotRemainsReadable(t *testing.T) {
	snapshot := "first partial answer"
	content := json.RawMessage(`{"text":"guide","_steering_assistant_snapshot":"first partial answer"}`)
	if got := followUpAssistantSnapshot(content); got != snapshot {
		t.Fatalf("persisted snapshot=%q want %q content=%s", got, snapshot, content)
	}
	if public := string(publicFollowUpContent(content)); public != `{"text":"guide"}` {
		t.Fatalf("internal snapshot leaked through queue content: %s", public)
	}
}

func followUpRequest(method, conversationID, followUpID string, body any) (*httptest.ResponseRecorder, *http.Request) {
	var payload bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&payload).Encode(body)
	}
	request := httptest.NewRequest(method, "/follow-ups", &payload)
	request.Header.Set("X-User-Id", "u1")
	request = mux.SetURLVars(request, map[string]string{
		"conversation_id": conversationID,
		"follow_up_id":    followUpID,
	})
	return httptest.NewRecorder(), request
}

func seedFollowUp(t *testing.T, db *orm.DB, row orm.ConversationFollowUp) {
	t.Helper()
	now := time.Now().UTC()
	if row.Revision == 0 {
		row.Revision = 1
	}
	if row.IdempotencyKey == "" {
		row.IdempotencyKey = "key-" + row.ID
	}
	row.Content = json.RawMessage(`{"text":"guide"}`)
	row.Text = "guide"
	row.SortOrder = 1024
	row.BaseModel = orm.BaseModel{
		CreateUserID: "u1", CreateUserName: "User 1", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
}

func TestRetryConversationFollowUpReturnsSteerFailureToOrdinaryQueue(t *testing.T) {
	db := followUpTestDB(t)
	seedFollowUp(t, db, orm.ConversationFollowUp{
		ID: "retry", ConversationID: "conversation", Mode: followUpModeSteer, Status: followUpFailed,
		TargetRunID: "old-run", TargetHistoryID: "old-history", ErrorCode: "offline", ErrorMessage: "offline",
	})

	recorder, request := followUpRequest(http.MethodPost, "conversation", "retry", nil)
	RetryConversationFollowUp(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var saved orm.ConversationFollowUp
	if err := db.First(&saved, "id = ?", "retry").Error; err != nil {
		t.Fatal(err)
	}
	if saved.Mode != followUpModeQueued || saved.Status != followUpPending || saved.TargetRunID != "" || saved.TargetHistoryID != "" {
		t.Fatalf("retry did not restore ordinary queue state: %+v", saved)
	}
}

func TestCompleteConversationFollowUpCanReleaseClaimAfterNavigation(t *testing.T) {
	db := followUpTestDB(t)
	seedFollowUp(t, db, orm.ConversationFollowUp{
		ID: "release", ConversationID: "conversation", Mode: followUpModeQueued, Status: followUpDispatching,
	})

	recorder, request := followUpRequest(http.MethodPost, "conversation", "release", map[string]any{"requeue": true})
	CompleteConversationFollowUp(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("release status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var saved orm.ConversationFollowUp
	if err := db.First(&saved, "id = ?", "release").Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != followUpPending || saved.Mode != followUpModeQueued {
		t.Fatalf("claim was not released: %+v", saved)
	}
}

func TestListConversationFollowUpsDoesNotReplayOldAcceptedRowsAndRetainsTranscript(t *testing.T) {
	db := followUpTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{
		ID: "conversation",
		BaseModel: orm.BaseModel{
			CreateUserID: "u1", CreateUserName: "User 1", CreatedAt: now, UpdatedAt: now,
		},
	}).Error; err != nil {
		t.Fatal(err)
	}
	oldAcceptedAt := now.Add(-followUpAcceptedVisibility - time.Second)
	recentAcceptedAt := now
	expiredAcceptedAt := now.Add(-followUpSentRetention - time.Second)
	seedFollowUp(t, db, orm.ConversationFollowUp{
		ID: "old-accepted", ConversationID: "conversation", Mode: followUpModeSteer,
		Status: followUpAccepted, AcceptedAt: &oldAcceptedAt,
	})
	seedFollowUp(t, db, orm.ConversationFollowUp{
		ID: "recent-accepted", ConversationID: "conversation", Mode: followUpModeSteer,
		Status: followUpAccepted, AcceptedAt: &recentAcceptedAt,
	})
	seedFollowUp(t, db, orm.ConversationFollowUp{
		ID: "expired-accepted", ConversationID: "conversation", Mode: followUpModeSteer,
		Status: followUpAccepted, AcceptedAt: &expiredAcceptedAt,
	})

	recorder, request := followUpRequest(http.MethodGet, "conversation", "", nil)
	ListConversationFollowUps(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Items []conversationFollowUpDTO `json:"items"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Items) != 1 || response.Data.Items[0].ID != "recent-accepted" {
		t.Fatalf("expired accepted rows leaked into the actionable list: %+v", response.Data.Items)
	}
	var expiredCount int64
	if err := db.Unscoped().Model(&orm.ConversationFollowUp{}).Where("id = ?", "expired-accepted").Count(&expiredCount).Error; err != nil {
		t.Fatal(err)
	}
	if expiredCount != 1 {
		t.Fatal("accepted steering row must remain available to conversation history")
	}
}

func TestListConversationFollowUpsUsesIDAsStableTieBreaker(t *testing.T) {
	db := followUpTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{
		ID: "conversation",
		BaseModel: orm.BaseModel{
			CreateUserID: "u1", CreateUserName: "User 1", CreatedAt: now, UpdatedAt: now,
		},
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"b", "a"} {
		seedFollowUp(t, db, orm.ConversationFollowUp{
			ID: id, ConversationID: "conversation", Mode: followUpModeQueued, Status: followUpPending,
		})
		if err := db.Model(&orm.ConversationFollowUp{}).Where("id = ?", id).Updates(map[string]any{
			"sort_order": 1024, "created_at": now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	recorder, request := followUpRequest(http.MethodGet, "conversation", "", nil)
	ListConversationFollowUps(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Items []conversationFollowUpDTO `json:"items"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Items) != 2 || response.Data.Items[0].ID != "a" || response.Data.Items[1].ID != "b" {
		t.Fatalf("unstable tied order: %+v", response.Data.Items)
	}
}

func TestCurrentFollowUpRunDoesNotTreatStatusFailureAsIdle(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ChatHistory{})
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "follow-up-state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	now := time.Now().UTC()
	if err := db.Create(&orm.ChatHistory{
		ID: "history", ConversationID: "conversation", Seq: 1, AlgorithmID: "algorithm",
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := setChatRuntimeStatus(context.Background(), stateStore, "conversation", "history", "generating", "", "run", nil); err != nil {
		t.Fatal(err)
	}
	store.Init(db.DB, nil, followUpFailingStatusStore{Store: stateStore})
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	active, err := currentFollowUpRun(context.Background(), db.DB, "conversation", "u1")
	if err == nil || active != nil {
		t.Fatalf("status failure was treated as idle: active=%+v err=%v", active, err)
	}
}

func TestClaimConversationFollowUpReportsStatusFailure(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ChatHistory{}, &orm.ConversationFollowUp{})
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "follow-up-claim-state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	now := time.Now().UTC()
	if err := db.Create(&orm.ChatHistory{
		ID: "history", ConversationID: "conversation", Seq: 1, AlgorithmID: "algorithm",
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := setChatRuntimeStatus(context.Background(), stateStore, "conversation", "history", "generating", "", "run", nil); err != nil {
		t.Fatal(err)
	}
	store.Init(db.DB, nil, followUpFailingStatusStore{Store: stateStore})
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	recorder, request := followUpRequest(http.MethodPost, "conversation", "", nil)
	ClaimNextConversationFollowUp(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("claim status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMaintainConversationFollowUpsRecoversInterruptedSteering(t *testing.T) {
	db := followUpTestDB(t)
	seedFollowUp(t, db, orm.ConversationFollowUp{
		ID: "stale-steer", ConversationID: "conversation", Mode: followUpModeSteer, Status: followUpSteering,
		TargetRunID: "run", TargetHistoryID: "history", SteerID: "steer-1",
	})
	staleAt := time.Now().UTC().Add(-followUpSteeringLease - time.Second)
	if err := db.Model(&orm.ConversationFollowUp{}).Where("id = ?", "stale-steer").
		UpdateColumn("updated_at", staleAt).Error; err != nil {
		t.Fatal(err)
	}

	if err := maintainConversationFollowUps(context.Background(), db.DB, "conversation", "u1"); err != nil {
		t.Fatal(err)
	}
	var saved orm.ConversationFollowUp
	if err := db.First(&saved, "id = ?", "stale-steer").Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != followUpFailed || saved.ErrorCode != "steering_interrupted" || saved.Revision != 2 {
		t.Fatalf("stale steering row was not made retryable: %+v", saved)
	}
	if saved.Mode != followUpModeSteer || saved.SteerID != "steer-1" || saved.TargetRunID != "run" {
		t.Fatalf("steering retry identity was not preserved: %+v", saved)
	}
}

func TestBeginFollowUpSteeringRejectsStaleRevision(t *testing.T) {
	db := followUpTestDB(t)
	seedFollowUp(t, db, orm.ConversationFollowUp{
		ID: "edited", ConversationID: "conversation", Mode: followUpModeQueued, Status: followUpPending,
	})
	var stale orm.ConversationFollowUp
	if err := db.First(&stale, "id = ?", "edited").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.ConversationFollowUp{}).Where("id = ?", "edited").Updates(map[string]any{
		"text": "new direction", "revision": stale.Revision + 1,
	}).Error; err != nil {
		t.Fatal(err)
	}

	err := beginFollowUpSteering(context.Background(), db.DB, stale, &activeFollowUpRun{
		RunID: "run", HistoryID: "history", AlgorithmID: "algorithm",
	}, "steer-1")
	if !errors.Is(err, errFollowUpConflict) {
		t.Fatalf("stale revision was allowed to steer: %v", err)
	}
	var saved orm.ConversationFollowUp
	if err := db.First(&saved, "id = ?", "edited").Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != followUpPending || saved.Text != "new direction" || saved.Revision != stale.Revision+1 {
		t.Fatalf("stale steering changed the edited row: %+v", saved)
	}
}

func TestDeleteConversationFollowUpHardDeletesQueueRecord(t *testing.T) {
	db := followUpTestDB(t)
	seedFollowUp(t, db, orm.ConversationFollowUp{
		ID: "delete", ConversationID: "conversation", Mode: followUpModeQueued, Status: followUpPending,
	})

	recorder, request := followUpRequest(http.MethodDelete, "conversation", "delete", nil)
	DeleteConversationFollowUp(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var count int64
	if err := db.Unscoped().Model(&orm.ConversationFollowUp{}).Where("id = ?", "delete").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("deleted operational queue record is still present")
	}
}

func TestChatServiceSteerChat(t *testing.T) {
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != steerChatPath || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-LazyMind-Internal-Token") != "internal-token" {
			t.Fatal("missing internal token")
		}
		var request LazyChatSteerRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.AlgorithmID != "algorithm-1" || request.RunID != "run-1" || request.Input != "change direction" {
			t.Fatalf("unexpected payload: %#v", request)
		}
		_ = json.NewEncoder(w).Encode(LazyChatSteerResponse{Accepted: true, SteerID: request.SteerID})
	}))
	defer server.Close()

	response, status, err := NewChatServiceWithEndpoint(server.URL).SteerChat(context.Background(), LazyChatSteerRequest{
		AlgorithmID: "algorithm-1", ConversationID: "conversation-1", RunID: "run-1",
		SteerID: "steer-1", Input: "change direction",
	})
	if err != nil || status != http.StatusOK || response == nil || !response.Accepted {
		t.Fatalf("response=%#v status=%d err=%v", response, status, err)
	}
}
