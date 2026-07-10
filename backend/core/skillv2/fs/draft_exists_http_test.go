package fs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/skillv2/testutil"
)

func TestDraftExistsHTTP_MapsServiceResult(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Updates(map[string]any{
		"task_id":         "task1",
		"conversation_id": "conv1",
		"version":         3,
	}).Error; err != nil {
		t.Fatalf("update draft: %v", err)
	}
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_draft")
	handler := NewDraftHandler(DraftHandlerDeps{DB: db.DB})

	req := httptest.NewRequest(http.MethodGet, "/skills/skill1/draft/exists", nil)
	req = mux.SetURLVars(req, map[string]string{"skillID": "skill1"})
	rec := httptest.NewRecorder()
	handler.DraftExists(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			HasUncommittedDraft bool   `json:"has_uncommitted_draft"`
			DraftVersion        int64  `json:"draft_version"`
			BaseRevisionID      string `json:"base_revision_id"`
			TaskID              string `json:"task_id"`
			ConversationID      string `json:"conversation_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Data.HasUncommittedDraft || resp.Data.DraftVersion != 3 || resp.Data.BaseRevisionID != "rev1" || resp.Data.TaskID != "task1" || resp.Data.ConversationID != "conv1" {
		t.Fatalf("unexpected draft exists response: %#v", resp.Data)
	}
}
