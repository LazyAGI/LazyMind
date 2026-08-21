package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	skillbuiltin "lazymind/core/skillv2/builtin"
	"lazymind/core/skillv2/testutil"
	"lazymind/core/store"
)

func TestListBuiltinSkillsIncludesTemplatesAndUserInstallState(t *testing.T) {
	uid := "bsk_demo"
	useBuiltinCatalog(t, skillbuiltin.Catalog{SchemaVersion: skillbuiltin.CatalogSchemaVersion, Skills: []skillbuiltin.CatalogSkill{{
		Key: "demo", UID: uid, SourceURL: "https://example.test/demo.zip", ResolvedURL: "https://example.test/demo.zip",
		Version: "1.0.0", Name: "demo", Description: "demo skill", Category: "research", Content: "# Demo",
		ArchiveSHA256: strings.Repeat("a", 64), TreeSHA256: strings.Repeat("b", 64), ArchiveSize: 1, PackageFile: "packages/demo.zip",
	}}})
	db := testutil.NewTestDB(t)
	testutil.MustCreate(t, db, &testutil.SkillRow{
		ID:                    "installed_builtin_skill",
		OwnerUserID:           "user_001",
		CreateUserID:          "user_001",
		Category:              "research",
		SkillName:             "demo",
		OriginBuiltinSkillUID: uid,
		RelativeRoot:          "research/demo",
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
	if response.Data.Total != 1 || len(response.Data.Items) != 1 {
		t.Fatalf("unexpected builtin list size: %#v", response.Data)
	}
	first := response.Data.Items[0]
	if first.UID != uid || first.Content == "" || !first.Installed || first.InstalledSkillID != "installed_builtin_skill" {
		t.Fatalf("unexpected first builtin item: %#v", first)
	}
}

func TestVisibleBuiltinPackagesHidesFeaturedOnlyPackages(t *testing.T) {
	packages := visibleBuiltinPackages([]skillbuiltin.Package{
		{UID: "market", MarketVisible: true},
		{UID: "featured", MarketVisible: false},
	})
	if len(packages) != 1 || packages[0].UID != "market" {
		t.Fatalf("visible packages = %#v", packages)
	}
}

func TestEnableBuiltinSkillReusesAndEnablesExistingInstall(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	uid := "bsk_existing"
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").Updates(map[string]any{
		"origin_builtin_skill_uid": uid,
		"is_enabled":               false,
	}).Error; err != nil {
		t.Fatal(err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(http.MethodPost, "/api/core/builtin-skills/"+uid+":enable", nil)
	req = mux.SetURLVars(req, map[string]string{"builtin_skill_uid": uid})
	req.Header.Set("X-User-Id", "user_001")
	req.Header.Set("X-User-Name", "张三")
	rec := httptest.NewRecorder()
	EnableBuiltinSkill(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var row testutil.SkillRow
	if err := db.Where("id = ?", "skill1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if !row.IsEnabled {
		t.Fatal("existing builtin Skill was not enabled")
	}
}

func useBuiltinCatalog(t *testing.T, catalog skillbuiltin.Catalog) {
	t.Helper()
	root := t.TempDir()
	workingDirectory := filepath.Join(root, "backend", "core")
	if err := os.MkdirAll(workingDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	catalogDirectory := filepath.Join(root, "skills", ".runtime", "builtin-skills")
	if err := os.MkdirAll(catalogDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(catalogDirectory, "catalog.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workingDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
}
