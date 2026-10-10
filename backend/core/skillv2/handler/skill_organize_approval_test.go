package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/skillv2/testutil"
)

func TestSkillOrganizeApprovalWaitsUntilTaskFinishesAndAppliesMergeAtomically(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedSkillWithRevision(t, db, "skill2", "rev2")
	testutil.SeedSkillWithRevision(t, db, "skill3", "rev3")
	summary := organizeApprovalSummary(t)
	task := seedOrganizeReservation(t, db, "org_merge", []string{"skill1", "skill2", "skill3"}, summary, orm.ResourceUpdateTaskStatusRunning)
	withHandlerDB(t, db)

	if tasks := listOrganizeApprovalTasks(t); len(tasks) != 0 {
		t.Fatalf("running task exposed approvals: %#v", tasks)
	}
	rec := postOrganizeApprovals(t, "org_merge", []string{"0"}, "accept")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want approval to stay closed while the task is running", rec.Code, rec.Body.String())
	}
	assertSkillUntouched(t, db, "skill1", "论文精读", "rev1")
	assertSkillUntouched(t, db, "skill2", "论文精读-skill2", "rev2")

	if err := db.Model(&orm.ResourceUpdateTask{}).Where("id = ?", task.ID).Updates(map[string]any{
		"status": orm.ResourceUpdateTaskStatusDone, "locked_by": "", "locked_until": nil, "finished_at": time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	tasks := listOrganizeApprovalTasks(t)
	if len(tasks) != 1 {
		t.Fatalf("tasks=%#v, want the completed organize task", tasks)
	}
	items, _ := tasks[0]["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items=%#v, want merge and refactor grouped under one task", items)
	}
	assertSkillUntouched(t, db, "skill1", "论文精读", "rev1")
	assertSkillUntouched(t, db, "skill2", "论文精读-skill2", "rev2")
	assertSkillUntouched(t, db, "skill3", "论文精读-skill3", "rev3")

	rec = postOrganizeApprovals(t, "org_merge", []string{"0"}, "accept")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"accepted"`) {
		t.Fatalf("accept status=%d body=%s", rec.Code, rec.Body.String())
	}
	var merged testutil.SkillRow
	if err := db.Where("id = ?", "skill1").Take(&merged).Error; err != nil {
		t.Fatal(err)
	}
	if merged.SkillName != "论文精读合订" || merged.Description != "合并后的论文技能" || merged.Field != "writing" || merged.DeletedAt != nil {
		t.Fatalf("merged skill=%#v", merged)
	}
	var removed testutil.SkillRow
	if err := db.Where("id = ?", "skill2").Take(&removed).Error; err != nil {
		t.Fatal(err)
	}
	if removed.DeletedAt == nil {
		t.Fatal("merge accept left the source skill in place")
	}
	assertSkillUntouched(t, db, "skill3", "论文精读-skill3", "rev3")

	rec = postOrganizeApprovals(t, "org_merge", []string{"1"}, "reject")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"rejected"`) {
		t.Fatalf("reject status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertSkillUntouched(t, db, "skill3", "论文精读-skill3", "rev3")
	tasks = listOrganizeApprovalTasks(t)
	if len(tasks) != 1 {
		t.Fatalf("accepted merge should stay available to revoke: %#v", tasks)
	}
	items, _ = tasks[0]["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items=%#v, want only the accepted merge", items)
	}
}

func TestSkillOrganizeRejectLeavesOfficialSkillsUntouched(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedSkillWithRevision(t, db, "skill2", "rev2")
	testutil.SeedSkillWithRevision(t, db, "skill3", "rev3")
	seedOrganizeReservation(t, db, "org_reject", []string{"skill1", "skill2", "skill3"}, organizeApprovalSummary(t), orm.ResourceUpdateTaskStatusDone)
	withHandlerDB(t, db)

	rec := postOrganizeApprovals(t, "org_reject", []string{"0", "1"}, "reject")
	if rec.Code != http.StatusOK {
		t.Fatalf("reject status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertSkillUntouched(t, db, "skill1", "论文精读", "rev1")
	assertSkillUntouched(t, db, "skill2", "论文精读-skill2", "rev2")
	assertSkillUntouched(t, db, "skill3", "论文精读-skill3", "rev3")
	if tasks := listOrganizeApprovalTasks(t); len(tasks) != 0 {
		t.Fatalf("rejected items remained visible: %#v", tasks)
	}
}

func TestSkillOrganizeMergeConflictDoesNotDeleteSources(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedSkillWithRevision(t, db, "skill2", "rev2")
	testutil.SeedSkillWithRevision(t, db, "skill3", "rev3")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill3").Update("skill_name", "论文精读合订").Error; err != nil {
		t.Fatal(err)
	}
	seedOrganizeReservation(t, db, "org_conflict", []string{"skill1", "skill2", "skill3"}, organizeApprovalSummary(t), orm.ResourceUpdateTaskStatusDone)
	withHandlerDB(t, db)

	rec := postOrganizeApprovals(t, "org_conflict", []string{"0"}, "accept")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"failed"`) {
		t.Fatalf("conflict status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertSkillUntouched(t, db, "skill1", "论文精读", "rev1")
	assertSkillUntouched(t, db, "skill2", "论文精读-skill2", "rev2")
}

func TestFailedOrganizeResultIsNotDelivered(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedSkillWithRevision(t, db, "skill2", "rev2")
	testutil.SeedSkillWithRevision(t, db, "skill3", "rev3")
	seedOrganizeReservation(t, db, "org_failed", []string{"skill1", "skill2", "skill3"}, organizeApprovalSummary(t), orm.ResourceUpdateTaskStatusFailed)
	if err := db.Model(&orm.SkillReviewStats{}).Where("requestid = ?", "org_failed").Update("status", "failed").Error; err != nil {
		t.Fatal(err)
	}
	withHandlerDB(t, db)

	if tasks := listOrganizeApprovalTasks(t); len(tasks) != 0 {
		t.Fatalf("failed run delivered approvals: %#v", tasks)
	}
	assertSkillUntouched(t, db, "skill1", "论文精读", "rev1")
	assertSkillUntouched(t, db, "skill2", "论文精读-skill2", "rev2")
}

func TestRevokeAcceptedOrganizeItemRestoresSkillsUntilLaterEdit(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedSkillWithRevision(t, db, "skill2", "rev2")
	testutil.SeedSkillWithRevision(t, db, "skill3", "rev3")
	seedOrganizeReservation(t, db, "org_revoke", []string{"skill1", "skill2", "skill3"}, organizeApprovalSummary(t), orm.ResourceUpdateTaskStatusDone)
	withHandlerDB(t, db)

	if rec := postOrganizeApprovals(t, "org_revoke", []string{"0"}, "accept"); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"status":"failed"`) {
		t.Fatalf("accept status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := postOrganizeApprovals(t, "org_revoke", []string{"0"}, "revoke"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"pending"`) {
		t.Fatalf("revoke status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertSkillUntouched(t, db, "skill1", "论文精读", "rev1")
	assertSkillUntouched(t, db, "skill2", "论文精读-skill2", "rev2")

	if rec := postOrganizeApprovals(t, "org_revoke", []string{"0"}, "accept"); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"status":"failed"`) {
		t.Fatalf("re-accept status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").Update("head_revision_id", "later-edit").Error; err != nil {
		t.Fatal(err)
	}
	rec := postOrganizeApprovals(t, "org_revoke", []string{"0"}, "revoke")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "后续已有修改") {
		t.Fatalf("later edit revoke status=%d body=%s", rec.Code, rec.Body.String())
	}
	var removed testutil.SkillRow
	if err := db.Where("id = ?", "skill2").Take(&removed).Error; err != nil {
		t.Fatal(err)
	}
	if removed.DeletedAt == nil {
		t.Fatal("failed revoke restored a source after a later edit")
	}
}

func organizeApprovalSummary(t *testing.T) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"status": "completed",
		"approval_items": []map[string]any{
			{
				"id": "0", "type": "merge",
				"source_keys":       []string{"research/论文精读", "research/论文精读-skill2"},
				"target_source_key": "research/论文精读",
				"target_name":       "论文精读合订",
				"content":           testutil.SkillMD("论文精读合订", "合并后的论文技能"),
				"search_metadata":   map[string]any{"field": "writing", "tags": []string{"paper"}, "aliases": []string{"精读"}, "keywords": []string{"论文"}},
				"delete_keys":       []string{"research/论文精读-skill2"},
				"depends_on":        []string{},
			},
			{
				"id": "1", "type": "refactor",
				"source_keys":       []string{"research/论文精读-skill3"},
				"target_source_key": "",
				"target_name":       "论文精读-skill3",
				"content":           testutil.SkillMD("论文精读-skill3", "整理后的会议纪要"),
				"search_metadata":   map[string]any{"field": "meeting"},
				"delete_keys":       []string{},
				"depends_on":        []string{},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func seedOrganizeReservation(t *testing.T, db *testutil.TestDB, requestID string, skillIDs []string, summary, status string) orm.ResourceUpdateTask {
	t.Helper()
	task, err := createSkillOrganizeReservation(t.Context(), db.DB, "user_001", requestID)
	if err != nil {
		t.Fatal(err)
	}
	if err := noteSkillOrganizeAccepted(t.Context(), db.DB, task.ID, requestID+"-run", skillOrganizeSubmitRequest{
		RequestID: requestID, Mode: "deep", Skills: []string{"skills/research/论文精读", "skills/research/论文精读-skill2", "skills/research/论文精读-skill3"},
	}, skillIDs); err != nil {
		t.Fatal(err)
	}
	testutil.MustCreate(t, db, &orm.SkillReviewStats{
		ID: requestID + "-stats", RequestID: requestID, UserID: "user_001", Status: orm.SkillReviewStatsStatusCompleted, StartedAt: "2026-10-10T00:00:00Z", Summary: summary,
	})
	if status != orm.ResourceUpdateTaskStatusRunning {
		if err := db.Model(&orm.ResourceUpdateTask{}).Where("id = ?", task.ID).Updates(map[string]any{
			"status": status, "locked_by": "", "locked_until": nil, "finished_at": time.Now().UTC(),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return task
}

func listOrganizeApprovalTasks(t *testing.T) []map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/core/skill_organize/approvals", nil)
	req.Header.Set("X-User-Id", "user_001")
	rec := httptest.NewRecorder()
	ListSkillOrganizeApprovals(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response common.APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	data, _ := response.Data.(map[string]any)
	raw, _ := data["tasks"].([]any)
	tasks := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		task, _ := item.(map[string]any)
		tasks = append(tasks, task)
	}
	return tasks
}

func postOrganizeApprovals(t *testing.T, requestID string, itemIDs []string, action string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"request_id": requestID, "item_ids": itemIDs, "action": action})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/core/skill_organize/approvals:resolve", strings.NewReader(string(body)))
	req.Header.Set("X-User-Id", "user_001")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ResolveSkillOrganizeApprovals(rec, req)
	return rec
}

func assertSkillUntouched(t *testing.T, db *testutil.TestDB, skillID, name, revisionID string) {
	t.Helper()
	var skill testutil.SkillRow
	if err := db.Where("id = ?", skillID).Take(&skill).Error; err != nil {
		t.Fatal(err)
	}
	if skill.SkillName != name || skill.DeletedAt != nil || skill.HeadRevisionID == nil || *skill.HeadRevisionID != revisionID || skill.Field != "" {
		t.Fatalf("skill %s changed before approval: %#v", skillID, skill)
	}
}
