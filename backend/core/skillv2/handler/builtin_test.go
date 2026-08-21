package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gorilla/mux"

	skillbuiltin "lazymind/core/skillv2/builtin"
	skillservice "lazymind/core/skillv2/service"
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

func TestEnableBuiltinSkillRestoresTrashedInstall(t *testing.T) {
	builtinRoot, err := filepath.Abs("../../../../skills")
	if err != nil {
		t.Fatalf("resolve builtin skills root: %v", err)
	}
	t.Setenv("LAZYMIND_BUILTIN_SKILLS_DIR", builtinRoot)
	db := testutil.NewTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	manifest := skillbuiltin.Manifests[0]

	enable := func() string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/core/builtin-skills/"+manifest.UID+":enable", nil)
		req = mux.SetURLVars(req, map[string]string{"builtin_skill_uid": manifest.UID})
		req.Header.Set("X-User-Id", "user_001")
		req.Header.Set("X-User-Name", "User One")
		rec := httptest.NewRecorder()
		EnableBuiltinSkill(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("enable builtin status=%d body=%s", rec.Code, rec.Body.String())
		}
		var response struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatalf("decode enable response: %v", err)
		}
		if response.Data.ID == "" {
			t.Fatalf("enable response missing skill id: %s", rec.Body.String())
		}
		return response.Data.ID
	}

	installedSkillID := enable()
	skillService := skillservice.NewSkillService(skillservice.SkillServiceDeps{DB: db.DB})
	if err := skillService.DeleteSkill(context.Background(), skillservice.DeleteSkillRequest{
		SkillID: installedSkillID,
		UserID:  "user_001",
	}); err != nil {
		t.Fatalf("DeleteSkill returned error: %v", err)
	}

	reinstalledSkillID := enable()
	if reinstalledSkillID != installedSkillID {
		t.Fatalf("reinstall returned skill %q, want restored skill %q", reinstalledSkillID, installedSkillID)
	}
	if got := testutil.CountRows(t, db, "skills", "id = ? AND deleted_at IS NOT NULL", installedSkillID); got != 0 {
		t.Fatalf("trashed builtin skill count after reinstall = %d, want 0", got)
	}
	if got := testutil.CountRows(t, db, "skills", "id = ? AND deleted_at IS NULL", installedSkillID); got != 1 {
		t.Fatalf("restored builtin skill count after reinstall = %d, want 1", got)
	}
	trash, err := skillService.ListTrashedSkills(context.Background(), skillservice.ListSkillsRequest{UserID: "user_001"})
	if err != nil {
		t.Fatalf("ListTrashedSkills returned error: %v", err)
	}
	if len(trash.Items) != 0 {
		t.Fatalf("trash after builtin reinstall = %#v, want empty", trash.Items)
	}
}
