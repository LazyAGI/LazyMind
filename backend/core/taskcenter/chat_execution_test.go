package taskcenter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
)

func setupChatExecutionState(t *testing.T, db *orm.DB) {
	t.Helper()
	var shared state.Store
	var err error
	if redisURL := os.Getenv("TEST_WORKSPACE_REDIS_URL"); redisURL != "" {
		shared, err = state.NewRedisStoreFromURL(redisURL)
	} else {
		shared, err = state.NewSQLiteStore(t.TempDir() + "/executions.db")
	}
	if err != nil {
		t.Fatal(err)
	}
	store.Init(db.DB, nil, shared)
	t.Cleanup(func() {
		_ = shared.Del(context.Background(), chatExecutionsKey("c"))
		store.Init(nil, nil, nil)
		_ = shared.Close()
	})
}

func TestChatExecutionTrackingAndRemovalSnapshot(t *testing.T) {
	db := removalDB(t)
	setupChatExecutionState(t, db)
	if err := db.Create(&orm.Conversation{ID: "c", Ext: json.RawMessage(`{"existing":"preserved"}`), BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"c_100", "c_101", "c_100"} {
		if err := RegisterChatExecution(t.Context(), db.DB, "u", "c", sid); err != nil {
			t.Fatal(err)
		}
	}
	if err := RegisterChatExecution(t.Context(), db.DB, "other", "c", "c_102"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign registration: %v", err)
	}
	seedRemoval(t, db, "running", "c", "running")
	if _, err := removeTaskWithImpact(t.Context(), db.DB, "u", "running", ""); err != nil {
		t.Fatal(err)
	}
	if err := RegisterChatExecution(t.Context(), db.DB, "u", "c", "c_103"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("registration after deletion: %v", err)
	}
	var job orm.AsyncJob
	if err := db.First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if err := FinishChatExecution(t.Context(), db.DB, "u", "c", "c_100"); err != nil {
		t.Fatal(err)
	}
	// Simulate restoration/new execution before an old cancellation is retried.
	if err := db.Model(&orm.Conversation{}).Where("id=?", "c").Update("deleted_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	if err := RegisterChatExecution(t.Context(), db.DB, "u", "c", "c_200"); err != nil {
		t.Fatal(err)
	}
	previous := OnRemovalCancelHook
	t.Cleanup(func() { OnRemovalCancelHook = previous })
	OnRemovalCancelHook = func(_ context.Context, owner, conv string, _ time.Time, ids []string) error {
		if owner != "u" || conv != "c" || !reflect.DeepEqual(ids, []string{"c_100", "c_101"}) {
			t.Fatalf("cancellation snapshot changed: %s %s %v", owner, conv, ids)
		}
		return nil
	}
	if _, err := runRemovalCancellation(t.Context(), asyncjob.Job{CreateUserID: "u", PayloadJSON: job.PayloadJSON}, nil); err != nil {
		t.Fatal(err)
	}
	var conv orm.Conversation
	if err := db.First(&conv, "id=?", "c").Error; err != nil {
		t.Fatal(err)
	}
	var ext map[string]any
	if err := json.Unmarshal(conv.Ext, &ext); err != nil || ext["existing"] != "preserved" {
		t.Fatalf("lost unrelated conversation metadata: %s %v", conv.Ext, err)
	}
}

func TestChatExecutionFinishOnlyRemovesItsOwnExecution(t *testing.T) {
	db := removalDB(t)
	setupChatExecutionState(t, db)
	if err := db.Create(&orm.Conversation{ID: "c", BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"c_100", "c_200"} {
		if err := RegisterChatExecution(t.Context(), db.DB, "u", "c", id); err != nil {
			t.Fatal(err)
		}
	}
	if err := FinishChatExecution(t.Context(), db.DB, "other", "c", "c_200"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign finish: %v", err)
	}
	if err := FinishChatExecution(t.Context(), db.DB, "u", "c", "c_100"); err != nil {
		t.Fatal(err)
	}
	seedRemoval(t, db, "running", "c", "running")
	if _, err := removeTaskWithImpact(t.Context(), db.DB, "u", "running", ""); err != nil {
		t.Fatal(err)
	}
	var job orm.AsyncJob
	if err := db.First(&job).Error; err != nil {
		t.Fatal(err)
	}
	var payload struct {
		IDs []string `json:"chat_execution_ids"`
	}
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil || !reflect.DeepEqual(payload.IDs, []string{"c_200"}) {
		t.Fatalf("finish removed a newer execution: %s %v", job.PayloadJSON, err)
	}
}

type unavailableChatExecutionSnapshot struct{ state.Store }

func (s unavailableChatExecutionSnapshot) HGetAll(context.Context, string) (map[string]string, error) {
	return nil, errors.New("synthetic shared state failure")
}

func TestChatExecutionSnapshotFailureRollsBackRemoval(t *testing.T) {
	db := removalDB(t)
	setupChatExecutionState(t, db)
	if err := db.Create(&orm.Conversation{ID: "c", BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := RegisterChatExecution(t.Context(), db.DB, "u", "c", "c_100"); err != nil {
		t.Fatal(err)
	}
	seedRemoval(t, db, "running", "c", "running")
	store.Init(db.DB, nil, unavailableChatExecutionSnapshot{store.State()})
	if _, err := removeTaskWithImpact(t.Context(), db.DB, "u", "running", ""); err == nil {
		t.Fatal("removal ignored unavailable execution state")
	}
	var conv orm.Conversation
	var task orm.TaskCenterTask
	if err := db.First(&conv, "id=?", "c").Error; err != nil || conv.DeletedAt != nil {
		t.Fatalf("conversation was removed: %+v %v", conv, err)
	}
	if err := db.First(&task, "id=?", "running").Error; err != nil || task.Status != "running" || task.ArchivedAt != nil {
		t.Fatalf("task was removed: %+v %v", task, err)
	}
	var jobs int64
	if err := db.Model(&orm.AsyncJob{}).Count(&jobs).Error; err != nil || jobs != 0 {
		t.Fatalf("queued cancellation on rollback: %d %v", jobs, err)
	}
}

func TestChatExecutionRegistrationRacingRemovalIsIncludedOrRejected(t *testing.T) {
	db := removalDB(t)
	setupChatExecutionState(t, db)
	if err := db.Create(&orm.Conversation{ID: "c", BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
		t.Fatal(err)
	}
	seedRemoval(t, db, "running", "c", "running")
	start := make(chan struct{})
	registered, removed := make(chan error, 1), make(chan error, 1)
	go func() { <-start; registered <- RegisterChatExecution(t.Context(), db.DB, "u", "c", "c_100") }()
	go func() {
		<-start
		_, err := removeTaskWithImpact(t.Context(), db.DB, "u", "running", "")
		removed <- err
	}()
	close(start)
	registrationErr, removalErr := <-registered, <-removed
	if removalErr != nil || (registrationErr != nil && !errors.Is(registrationErr, gorm.ErrRecordNotFound)) {
		t.Fatalf("register=%v remove=%v", registrationErr, removalErr)
	}
	var job orm.AsyncJob
	if err := db.First(&job).Error; err != nil {
		t.Fatal(err)
	}
	var payload struct {
		IDs []string `json:"chat_execution_ids"`
	}
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if (registrationErr == nil) != reflect.DeepEqual(payload.IDs, []string{"c_100"}) {
		t.Fatalf("accepted execution missing from cancellation snapshot: %s register=%v", job.PayloadJSON, registrationErr)
	}
}

func TestRemovalWithoutExecutionSnapshotDoesNotUseBroadCancellation(t *testing.T) {
	previous := OnRemovalCancelHook
	t.Cleanup(func() { OnRemovalCancelHook = previous })
	OnRemovalCancelHook = func(context.Context, string, string, time.Time, []string) error {
		t.Fatal("legacy job must not cancel the current conversation execution")
		return nil
	}
	payload, _ := json.Marshal(map[string]any{"conversation_id": "c", "before": time.Now().UTC()})
	result, err := runRemovalCancellation(t.Context(), asyncjob.Job{CreateUserID: "u", PayloadJSON: payload}, nil)
	if err == nil || !result.Permanent {
		t.Fatalf("missing snapshot: %+v %v", result, err)
	}
}
