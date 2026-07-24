package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/skillv2/testutil"
	"lazymind/core/store"
)

func TestDraftDeletePathRejectsSkillMD(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(
		http.MethodDelete,
		"/api/core/skills/skill1/draft/fs/path?path=SKILL.md&expected_draft_version=1",
		nil,
	)
	req.Header.Set("X-User-Id", "user_001")
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill1"})
	rec := httptest.NewRecorder()

	DraftDeletePath(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 2001990 || resp.Message != "cannot delete SKILL.md" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	var draft testutil.SkillDraftRow
	if err := db.Where("skill_id = ?", "skill1").Take(&draft).Error; err != nil {
		t.Fatalf("query draft: %v", err)
	}
	if draft.Version != 1 {
		t.Fatalf("draft version = %d, want 1", draft.Version)
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ? AND path = ?", "skill1", "SKILL.md"); got != 0 {
		t.Fatalf("SKILL.md draft overlay count = %d, want 0", got)
	}
}
