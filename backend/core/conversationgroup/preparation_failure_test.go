package conversationgroup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type resultPreparation struct {
	preparationFixture
	results []algo.ConversationTitleResult
}

func (p resultPreparation) ResolveBatch(_ context.Context, _ *gorm.DB, _ string, inputs []json.RawMessage, _ map[string]any) ([]algo.ConversationTitleResult, error) {
	return p.results[:len(inputs)], nil
}

func startPreparationRun(t *testing.T, results []algo.ConversationTitleResult) (*gorm.DB, orm.ConversationOrganizerRun, asyncjob.Job) {
	t.Helper()
	previous := titlePreparer
	titlePreparer = resultPreparation{results: results}
	t.Cleanup(func() { titlePreparer = previous })
	db := orm.MigrateTestDB(t,
		&orm.Conversation{}, &orm.ConversationOpening{}, &orm.AsyncJob{},
		&orm.ConversationGroup{}, &orm.ConversationGroupMember{}, &orm.ConversationGroupState{},
		&orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{}, &orm.ConversationOrganizerChange{},
		&orm.UserSelectedModel{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{},
	)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	for i := range results {
		conv := orm.Conversation{ID: fmt.Sprintf("c%d", i), ChatExecutor: "lazymind", BaseModel: orm.BaseModel{CreateUserID: "u", CreatedAt: now, UpdatedAt: now}}
		if err := db.Create(&conv).Error; err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-User-Id", "u")
	w := httptest.NewRecorder()
	StartOrganizer(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	var run orm.ConversationOrganizerRun
	if err := db.Where("user_id=?", "u").Take(&run).Error; err != nil {
		t.Fatal(err)
	}
	until := now.Add(time.Hour)
	if err := db.Model(&orm.AsyncJob{}).Where("id=?", run.JobID).Updates(map[string]any{"status": "running", "attempt_count": 1, "lock_until": until}).Error; err != nil {
		t.Fatal(err)
	}
	return db.DB, run, asyncjob.Job{ID: run.JobID, AttemptCount: 1, CreateUserID: "u", PayloadJSON: json.RawMessage(fmt.Sprintf(`{"run_id":%q}`, run.ID))}
}

func TestOrganizerFailsWhenNoSummariesAreUsable(t *testing.T) {
	failed := algo.ConversationTitleResult{Status: "failed", ErrorCode: "invalid_output"}
	db, run, job := startPreparationRun(t, []algo.ConversationTitleResult{failed, failed, failed})
	result, err := handleOrganizerJob(t.Context(), job, nil)
	if err == nil || !result.Permanent || result.ErrorCode != "summary_preparation_failed" {
		t.Fatalf("summary failures were treated as success: %+v %v", result, err)
	}
	if err := db.Where("id=?", run.ID).Take(&run).Error; err != nil {
		t.Fatal(err)
	}
	dto := runDTO(t.Context(), db, run, true)
	if run.Status != "failed" || dto["can_restart"] != true || dto["can_retry"] != false || dto["can_undo"] != false {
		t.Fatalf("unexpected failed result: %+v", dto)
	}
	var failures int64
	db.Model(&orm.ConversationOrganizerSnapshotItem{}).Where("run_id=? AND preparation_reason=? AND preparation_error=?", run.ID, "summary_failed", "invalid_output").Count(&failures)
	if failures != 3 {
		t.Fatalf("summary failure details lost: %d", failures)
	}
	var members int64
	db.Model(&orm.ConversationGroupMember{}).Count(&members)
	if members != 0 {
		t.Fatalf("failed preparation changed membership: %d", members)
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-User-Id", "u")
	w := httptest.NewRecorder()
	StartOrganizer(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("fresh preparation could not start: %d %s", w.Code, w.Body)
	}
	var pending int64
	db.Model(&orm.ConversationOrganizerRun{}).Where("user_id=? AND status=? AND id<>?", "u", "pending", run.ID).Count(&pending)
	if pending != 1 {
		t.Fatalf("restart reused failed summaries: %d", pending)
	}
}

func TestOrganizerKeepsNoTaskIntentAsANormalEmptyResult(t *testing.T) {
	empty := algo.ConversationTitleResult{Status: "succeeded", Output: algo.ConversationTitle{IntentStatus: "empty"}}
	db, run, job := startPreparationRun(t, []algo.ConversationTitleResult{empty})
	if result, err := handleOrganizerJob(t.Context(), job, nil); err != nil {
		t.Fatalf("no-task conversation failed: %+v %v", result, err)
	}
	if err := db.Where("id=?", run.ID).Take(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != "succeeded" {
		t.Fatalf("no-task result: %s", run.Status)
	}
}

func TestPreparationRetainsUsableSummariesAfterPartialFailure(t *testing.T) {
	ready := algo.ConversationTitleResult{Status: "succeeded", Output: algo.ConversationTitle{Summary: "处理客户邮件", IntentStatus: "ready"}}
	failed := algo.ConversationTitleResult{Status: "failed", ErrorCode: "invalid_output"}
	db, run, job := startPreparationRun(t, []algo.ConversationTitleResult{ready, failed, ready, ready})
	if err := db.Model(&run).Updates(map[string]any{"status": "running", "stage": "preparing"}).Error; err != nil {
		t.Fatal(err)
	}
	run.Status = "running"
	if err := prepareOrganizer(t.Context(), db, &run, job, nil); err != nil {
		t.Fatal(err)
	}
	var snapshot organizerSnapshot
	if err := json.Unmarshal(run.SnapshotJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Conversations) != 3 || run.Stage != "organizing" {
		t.Fatalf("usable inputs lost: %+v stage=%s", snapshot.Conversations, run.Stage)
	}
	var item orm.ConversationOrganizerSnapshotItem
	if err := db.Where("run_id=? AND conversation_id=?", run.ID, "c1").Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.PreparationReason != "summary_failed" || item.PreparationError != "invalid_output" {
		t.Fatalf("partial failure detail lost: %+v", item)
	}
}
