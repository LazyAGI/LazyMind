package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/skillv2/taskguard"
	"lazymind/core/skillv2/testutil"
)

func TestCommitAllowsDraftProducedByRunningOrganizeTask(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "draft_hash", testutil.SkillMD("论文精读", "优化后的论文阅读技能"))
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "draft_hash")
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Updates(map[string]any{
		"task_id": "org_running", "draft_status": "pending_confirm",
	}).Error; err != nil {
		t.Fatalf("mark organizer draft: %v", err)
	}
	insertHandlerMaintenanceTask(t, db, "org_running", "user_001")
	withHandlerDB(t, db)

	req := httptest.NewRequest(http.MethodPost, "/api/core/skills/skill1/commit", strings.NewReader(`{"draft_version":1}`))
	req.Header.Set("X-User-Id", "user_001")
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill1"})
	rec := httptest.NewRecorder()

	Commit(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want approval of the running organizer's own draft", rec.Code, rec.Body.String())
	}
	var resolved testutil.SkillDraftRow
	if err := db.Where("skill_id = ?", "skill1").Take(&resolved).Error; err != nil {
		t.Fatalf("reload resolved draft: %v", err)
	}
	if resolved.TaskID != "org_running" || resolved.DraftStatus != taskguard.DraftStatusResolved {
		t.Fatalf("resolved draft fence = task %q status %q", resolved.TaskID, resolved.DraftStatus)
	}
	decision, err := taskguard.EvaluateSkillOperation(context.Background(), db.DB, nil, taskguard.SkillOperationRequest{
		UserID: "user_001", SkillID: "skill1", TaskID: "org_running", Operation: taskguard.WriteSkillDraft,
	})
	if err != nil {
		t.Fatalf("evaluate late organizer write: %v", err)
	}
	if decision.Allowed {
		t.Fatalf("late organizer write allowed after approval: %#v", decision)
	}
	if err := db.Table("skill_review_stats").Where("requestid = ?", "org_running").Update("status", "completed").Error; err != nil {
		t.Fatalf("complete organizer: %v", err)
	}
	insertHandlerMaintenanceTask(t, db, "org_next", "user_001")
	decision, err = taskguard.EvaluateSkillOperation(context.Background(), db.DB, nil, taskguard.SkillOperationRequest{
		UserID: "user_001", SkillID: "skill1", TaskID: "org_next", Operation: taskguard.WriteSkillDraft,
	})
	if err != nil || !decision.Allowed {
		t.Fatalf("future organizer could not take over resolved empty draft: decision=%#v err=%v", decision, err)
	}
}

func TestCommitRejectsDraftOwnedByDifferentRunningOrganizeTask(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "draft_hash", testutil.SkillMD("论文精读", "其他任务生成的草稿"))
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "draft_hash")
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Updates(map[string]any{
		"task_id": "org_previous", "draft_status": "pending_confirm",
	}).Error; err != nil {
		t.Fatalf("mark organizer draft: %v", err)
	}
	insertHandlerMaintenanceTask(t, db, "org_running", "user_001")
	withHandlerDB(t, db)

	req := httptest.NewRequest(http.MethodPost, "/api/core/skills/skill1/commit", strings.NewReader(`{"draft_version":1}`))
	req.Header.Set("X-User-Id", "user_001")
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill1"})
	rec := httptest.NewRecorder()

	Commit(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want unrelated running task to keep conflict protection", rec.Code, rec.Body.String())
	}
}

func TestCommitDoesNotBroadenRunningReviewTaskAdmission(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "draft_hash", testutil.SkillMD("论文精读", "review 生成的草稿"))
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "draft_hash")
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Updates(map[string]any{
		"task_id": "review_running", "draft_status": "pending_confirm",
	}).Error; err != nil {
		t.Fatalf("mark review draft: %v", err)
	}
	insertHandlerMaintenanceTask(t, db, "review_running", "user_001")
	withHandlerDB(t, db)

	req := httptest.NewRequest(http.MethodPost, "/api/core/skills/skill1/commit", strings.NewReader(`{"draft_version":1}`))
	req.Header.Set("X-User-Id", "user_001")
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill1"})
	rec := httptest.NewRecorder()
	Commit(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want running review task to retain existing conflict policy", rec.Code, rec.Body.String())
	}
}

func TestFailedCommitKeepsRunningOrganizerOwnershipForRetry(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "draft_hash", testutil.SkillMD("论文精读", "可重试审批的草稿"))
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "draft_hash")
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Updates(map[string]any{
		"task_id": "org_running", "draft_status": "pending_confirm",
	}).Error; err != nil {
		t.Fatalf("mark organizer draft: %v", err)
	}
	insertHandlerMaintenanceTask(t, db, "org_running", "user_001")
	withHandlerDB(t, db)

	commit := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/core/skills/skill1/commit", strings.NewReader(body))
		req.Header.Set("X-User-Id", "user_001")
		req = mux.SetURLVars(req, map[string]string{"skill_id": "skill1"})
		rec := httptest.NewRecorder()
		Commit(rec, req)
		return rec
	}

	if rec := commit(`{"draft_version":0}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid request status=%d body=%s", rec.Code, rec.Body.String())
	}
	var draft testutil.SkillDraftRow
	if err := db.Where("skill_id = ?", "skill1").Take(&draft).Error; err != nil {
		t.Fatalf("reload draft: %v", err)
	}
	if draft.TaskID != "org_running" {
		t.Fatalf("failed approval changed task owner to %q", draft.TaskID)
	}
	if rec := commit(`{"draft_version":1}`); rec.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", rec.Code, rec.Body.String())
	}
}
