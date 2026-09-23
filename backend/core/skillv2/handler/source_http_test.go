package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common"
	"lazymind/core/skillv2/testutil"
)

func TestListHTTPSourceUsesBuiltinIdentityBeforeCategory(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, row := range []struct{ id, category, origin string }{
		{"builtin-internal", "internal", "uid-internal"},
		{"builtin-legacy", "research", "uid-legacy"},
		{"builtin-external", "external", "uid-external"},
		{"ordinary-internal", "internal", ""},
		{"ordinary-external", "external", ""},
		{"ordinary-legacy", "research", ""},
	} {
		testutil.SeedSkillWithRevision(t, db, row.id, "rev-"+row.id)
		name := row.id
		if row.id == "ordinary-internal" {
			name = "builtin-legacy" // A matching name never establishes builtin identity.
		}
		setHandlerSkillMetadata(t, db, row.id, name, row.category, "", `[]`)
		if err := db.Model(&testutil.SkillRow{}).Where("id = ?", row.id).Update("origin_builtin_skill_uid", row.origin).Error; err != nil {
			t.Fatal(err)
		}
	}
	withHandlerDB(t, db)
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"source=builtin", []string{"builtin-external", "builtin-internal", "builtin-legacy"}},
		{"source=internal", []string{"ordinary-internal"}},
		{"source=external", []string{"ordinary-external"}},
		{"category=internal", []string{"builtin-internal", "ordinary-internal"}},
		{"category=research", []string{"builtin-legacy", "ordinary-legacy"}},
		{"source=builtin&category=research", []string{"builtin-legacy"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			data := listSkillsHTTP(t, "/api/core/skills?"+tc.query)
			var ids []string
			for _, value := range data["items"].([]any) {
				ids = append(ids, value.(map[string]any)["id"].(string))
			}
			sort.Strings(ids)
			if !reflect.DeepEqual(ids, tc.want) || data["total"] != float64(len(tc.want)) {
				t.Fatalf("items=%v total=%v, want %v", ids, data["total"], tc.want)
			}
		})
	}
}

func TestSkillHTTPListAndDetailExposeBuiltinOrigin(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").Update("origin_builtin_skill_uid", "builtin-uid-1").Error; err != nil {
		t.Fatal(err)
	}
	withHandlerDB(t, db)
	data := listSkillsHTTP(t, "/api/core/skills")
	item := data["items"].([]any)[0].(map[string]any)
	if item["origin_builtin_skill_uid"] != "builtin-uid-1" {
		t.Errorf("list origin=%v", item["origin_builtin_skill_uid"])
	}
	req := httptest.NewRequest(http.MethodGet, "/api/core/skills/skill1", nil)
	req.Header.Set("X-User-Id", "user_001")
	req = mux.SetURLVars(req, map[string]string{"skill_id": "skill1"})
	rec := httptest.NewRecorder()
	Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response common.APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	detail := response.Data.(map[string]any)
	if detail["origin_builtin_skill_uid"] != "builtin-uid-1" {
		t.Errorf("detail origin=%v", detail["origin_builtin_skill_uid"])
	}
}
