package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common"
	"lazymind/core/skillv2/testutil"
)

func TestCreateDuplicateSkillReturnsBusinessConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	withHandlerDB(t, db)
	for i := 0; i < 3; i++ {
		payload, err := json.Marshal(map[string]any{
			"name": "duplicate-install-test", "category": "external",
			"description": "duplicate install regression",
			"content":     "# Test\nNo execution required.",
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/core/skills", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", "user_001")
		rec := httptest.NewRecorder()
		Create(rec, req)
		if i == 0 {
			if rec.Code != http.StatusOK {
				t.Fatalf("first create: %d %s", rec.Code, rec.Body.String())
			}
			continue
		}
		if rec.Code != http.StatusConflict {
			t.Fatalf("duplicate: %d %s", rec.Code, rec.Body.String())
		}
		var response common.APIResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		data, ok := response.Data.(map[string]any)
		if !ok || data["code"] != "skill_already_exists" || response.Code != 2001108 {
			t.Fatalf("unexpected business error: %s", rec.Body.String())
		}
		for _, fragment := range []string{"uk_skills", "UNIQUE constraint", "SQLSTATE", "INSERT INTO"} {
			if strings.Contains(rec.Body.String(), fragment) {
				t.Fatalf("database details leaked: %s", rec.Body.String())
			}
		}
	}
	for _, table := range []string{"skills", "skill_revisions", "skill_drafts"} {
		if got := testutil.CountRows(t, db, table, ""); got != 1 {
			t.Fatalf("%s count = %d, want 1", table, got)
		}
	}
}
