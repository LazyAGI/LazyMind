package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/skillv2/testutil"
)

func insertRunningOrganizeScope(t *testing.T, db *testutil.TestDB, requestID, userID string, skillIDs []string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"requestid": requestID, "skill_ids": skillIDs})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	testutil.MustCreate(t, db, &orm.ResourceUpdateTask{
		ID:           requestID + "-reservation",
		TaskType:     orm.ResourceUpdateTaskTypeOrganizeSkill,
		ResourceType: orm.ResourceUpdateResourceTypeSkill,
		UserID:       userID,
		TriggerType:  orm.ResourceUpdateTriggerTypeManual,
		TriggerID:    "skill_organize:" + userID + ":" + requestID,
		Status:       orm.ResourceUpdateTaskStatusRunning,
		RequestJSON:  raw,
		ResultJSON:   json.RawMessage(`{"organize_rollback_version":1}`),
		NextRunAt:    now,
		LaneOrderAt:  now,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
}

func TestCommitRejectsSkillInsideRunningOrganizeScope(t *testing.T) {
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
	insertRunningOrganizeScope(t, db, "org_running", "user_001", []string{"skill1", "skill2"})
	withHandlerDB(t, db)

	req := httptest.NewRequest(http.MethodPost, "/api/core/skills/skill1/commit", strings.NewReader(`{"draft_version":1}`))
	req.Header.Set("X-User-Id", "user_001")
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill1"})
	rec := httptest.NewRecorder()

	Commit(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want running organize scope to block approval", rec.Code, rec.Body.String())
	}
	var skill testutil.SkillRow
	if err := db.Where("id = ?", "skill1").Take(&skill).Error; err != nil {
		t.Fatal(err)
	}
	if skill.HeadRevisionID == nil || *skill.HeadRevisionID != "rev1" {
		t.Fatalf("running approval changed head to %v", skill.HeadRevisionID)
	}
}

func TestCommitAllowsOldDraftOutsideRunningOrganizeScope(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedSkillWithRevision(t, db, "skill2", "rev2")
	testutil.SeedTextBlob(t, db, "draft_hash", testutil.SkillMD("论文精读", "旧任务留下的草稿"))
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "draft_hash")
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Updates(map[string]any{
		"task_id": "org_previous", "draft_status": "pending_confirm",
	}).Error; err != nil {
		t.Fatalf("mark old draft: %v", err)
	}
	insertHandlerMaintenanceTask(t, db, "org_running", "user_001")
	insertRunningOrganizeScope(t, db, "org_running", "user_001", []string{"skill2", "skill3"})
	withHandlerDB(t, db)

	req := httptest.NewRequest(http.MethodPost, "/api/core/skills/skill1/commit", strings.NewReader(`{"draft_version":1}`))
	req.Header.Set("X-User-Id", "user_001")
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill1"})
	rec := httptest.NewRecorder()
	Commit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want an old draft outside the running scope to be approvable", rec.Code, rec.Body.String())
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
	insertRunningOrganizeScope(t, db, "org_running", "user_001", []string{"skill1"})
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
	if rec := commit(`{"draft_version":1}`); rec.Code != http.StatusConflict {
		t.Fatalf("retry status=%d body=%s, want the running scope to keep blocking approval", rec.Code, rec.Body.String())
	}
	if err := db.Where("skill_id = ?", "skill1").Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	if draft.TaskID != "org_running" {
		t.Fatalf("blocked approval changed task owner to %q", draft.TaskID)
	}
}
