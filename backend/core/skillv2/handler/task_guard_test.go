package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/skillv2/testutil"
	"lazymind/core/store"
)

func TestMaintenanceTaskStatusIsScopedToCurrentUser(t *testing.T) {
	db := testutil.NewTestDB(t)
	oldDB := store.DB()
	t.Cleanup(func() { store.Init(oldDB, nil, nil) })
	store.Init(db.DB, nil, nil)
	insertHandlerMaintenanceTask(t, db, "org_other", "other-user")

	request := func() map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/api/core/skills/maintenance-task", nil)
		req.Header.Set("X-User-Id", "user-1")
		rec := httptest.NewRecorder()
		MaintenanceTaskStatus(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var response common.APIResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return response.Data.(map[string]any)
	}

	if data := request(); data["has_active_task"] != false {
		t.Fatalf("other user's task blocked current user: %#v", data)
	}
	insertHandlerMaintenanceTask(t, db, "review_own", "user-1")
	data := request()
	if data["has_active_task"] != true {
		t.Fatalf("own task was not reported: %#v", data)
	}
	task, _ := data["task"].(map[string]any)
	if task["request_id"] != "review_own" || task["type"] != "skill_review" {
		t.Fatalf("unexpected task payload: %#v", task)
	}
}

func TestSkillOrganizeDraftConflictDoesNotCallAlgorithm(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "draft_hash", "draft")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "draft_hash")
	oldDB := store.DB()
	oldCaller := skillOrganizeCaller
	t.Cleanup(func() {
		store.Init(oldDB, nil, nil)
		skillOrganizeCaller = oldCaller
	})
	store.Init(db.DB, nil, nil)
	called := false
	skillOrganizeCaller = func(_ context.Context, _ algo.SkillOrganizeRequest) (*algo.SkillOrganizeResponse, int, error) {
		called = true
		return nil, 0, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/skill_organize", strings.NewReader(`{
		"requestid":"org_test",
		"skills":["skills/research/论文精读"]
	}`))
	req.Header.Set("X-User-Id", "user_001")
	rec := httptest.NewRecorder()
	SubmitSkillOrganize(rec, req)
	if rec.Code != http.StatusConflict || called {
		t.Fatalf("status=%d called=%v body=%s", rec.Code, called, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "skill_organize_draft_conflict") || !strings.Contains(rec.Body.String(), "skills/research/论文精读") {
		t.Fatalf("missing conflict detail: %s", rec.Body.String())
	}
}

func insertHandlerMaintenanceTask(t *testing.T, db *testutil.TestDB, taskID, userID string) {
	t.Helper()
	if err := db.Table("skill_review_stats").Create(map[string]any{
		"id": taskID, "requestid": taskID, "userid": userID, "status": "review_apply",
		"started_at": "2026-07-13T10:00:00Z", "duration_ms": 0, "summary": "{}",
	}).Error; err != nil {
		t.Fatalf("insert maintenance task: %v", err)
	}
}
