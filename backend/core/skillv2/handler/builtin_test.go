package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	skillbuiltin "lazymind/core/skillv2/builtin"
	"lazymind/core/skillv2/testutil"
	"lazymind/core/store"
)

func TestListBuiltinSkillsIncludesTemplatesAndUserInstallState(t *testing.T) {
	builtinRoot, err := filepath.Abs("../../../../skills")
	if err != nil {
		t.Fatalf("resolve builtin skills root: %v", err)
	}
	t.Setenv("LAZYMIND_BUILTIN_SKILLS_DIR", builtinRoot)
	db := testutil.NewTestDB(t)
	manifest := skillbuiltin.Manifests[0]
	testutil.MustCreate(t, db, &testutil.SkillRow{
		ID:                    "installed_builtin_skill",
		OwnerUserID:           "user_001",
		CreateUserID:          "user_001",
		Category:              manifest.Category,
		SkillName:             manifest.DirName,
		OriginBuiltinSkillUID: manifest.UID,
		RelativeRoot:          manifest.Category + "/" + manifest.DirName,
	})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(http.MethodGet, "/api/core/builtin-skills", nil)
	req.Header.Set("X-User-Id", "user_001")
	rec := httptest.NewRecorder()
	ListBuiltinSkills(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response struct {
		Data struct {
			Items []struct {
				UID              string `json:"builtin_skill_uid"`
				Content          string `json:"content"`
				Installed        bool   `json:"installed"`
				InstalledSkillID string `json:"installed_skill_id"`
			} `json:"items"`
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Data.Total != len(skillbuiltin.Manifests) || len(response.Data.Items) != len(skillbuiltin.Manifests) {
		t.Fatalf("unexpected builtin list size: %#v", response.Data)
	}
	first := response.Data.Items[0]
	if first.UID != manifest.UID || first.Content == "" || !first.Installed || first.InstalledSkillID != "installed_builtin_skill" {
		t.Fatalf("unexpected first builtin item: %#v", first)
	}
}
