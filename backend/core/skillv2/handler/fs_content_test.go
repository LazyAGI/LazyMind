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

func TestFSContentMissingFileReturnsOKEnvelope(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(http.MethodGet, "/skills/skill1/fs/content?path=12322", nil)
	req.Header.Set("X-User-Id", "user_001")
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill1"})
	rec := httptest.NewRecorder()

	FSContent(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 2000954 || resp.Message != "file not found" || resp.Data["code"] != "not_found" || resp.Data["detail"] != "12322" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}
