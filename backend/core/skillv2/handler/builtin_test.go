package handler

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	skillbuiltin "lazymind/core/skillv2/builtin"
	skillservice "lazymind/core/skillv2/service"
	skillpackage "lazymind/core/skillv2/skillpackage"
	"lazymind/core/skillv2/testutil"
	"lazymind/core/store"
)

func TestListBuiltinSkillsIncludesTemplatesAndUserInstallState(t *testing.T) {
	uid := "bsk_demo"
	useBuiltinCatalog(t, skillbuiltin.Catalog{SchemaVersion: skillbuiltin.CatalogSchemaVersion, Skills: []skillbuiltin.CatalogSkill{{
		Key: "demo", UID: uid, SourceURL: "https://example.test/demo.zip", ResolvedURL: "https://example.test/demo.zip",
		Version: "1.0.0", Name: "demo", Description: "demo skill", Category: "research", Provider: "WorkBuddy", Content: "# Demo",
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
				Provider         string `json:"provider"`
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
	if first.UID != uid || first.Content == "" || first.Provider != "WorkBuddy" || !first.Installed || first.InstalledSkillID != "installed_builtin_skill" {
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

func TestEnableBuiltinSkillMigratesOldUIDInstallToCurrentPackage(t *testing.T) {
	uid := "bsk_current"
	files := map[string]string{
		"SKILL.md":            "---\nname: chat-eq-reply\ndescription: current builtin\n---\n# Current builtin\n",
		"references/guide.md": "current guide\n",
	}
	useBuiltinCatalogWithZip(t, skillbuiltin.CatalogSkill{
		Key:         "chat-eq-reply",
		UID:         uid,
		SourceURL:   "https://skillhub.cn/skills/chat-eq-reply",
		ResolvedURL: "https://api.skillhub.cn/api/v1/download?slug=chat-eq-reply&version=3.5.0",
		Version:     "3.5.0",
		Name:        "chat-eq-reply",
		Description: "current builtin",
		Category:    "communication",
		Provider:    "SkillHub",
		Content:     files["SKILL.md"],
	}, files)

	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").Updates(map[string]any{
		"category":                 "communication",
		"skill_name":               "chat-eq-reply",
		"relative_root":            "communication/chat-eq-reply",
		"description":              "old builtin",
		"origin_builtin_skill_uid": "bsk_old",
		"is_enabled":               false,
	}).Error; err != nil {
		t.Fatal(err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(http.MethodPost, "/api/core/builtin-skills/"+uid+":enable", nil)
	req = mux.SetURLVars(req, map[string]string{"builtin_skill_uid": uid})
	req.Header.Set("X-User-Id", "user_001")
	req.Header.Set("X-User-Name", "User One")
	rec := httptest.NewRecorder()
	EnableBuiltinSkill(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable builtin status=%d body=%s", rec.Code, rec.Body.String())
	}

	if got := testutil.CountRows(t, db, "skills", "owner_user_id = ? AND category = ? AND skill_name = ? AND deleted_at IS NULL", "user_001", "communication", "chat-eq-reply"); got != 1 {
		t.Fatalf("live chat-eq-reply rows = %d, want 1", got)
	}
	var row testutil.SkillRow
	if err := db.Where("id = ?", "skill1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.OriginBuiltinSkillUID != uid || !row.IsEnabled || row.Description != "current builtin" {
		t.Fatalf("migrated row = %#v", row)
	}
	if row.HeadRevisionID == nil || *row.HeadRevisionID == "rev1" {
		t.Fatalf("head revision was not replaced: %#v", row.HeadRevisionID)
	}
	service := skillservice.NewSkillService(skillservice.SkillServiceDeps{DB: db.DB})
	file, err := service.ReadFile(context.Background(), skillservice.FileRef{SkillID: "skill1", RefType: "head", Path: "SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(file.Content, "# Current builtin") {
		t.Fatalf("SKILL.md content = %q", file.Content)
	}
	if got := testutil.CountRows(t, db, "skill_distribution_bindings", "skill_id = ? AND builtin_skill_uid = ?", "skill1", uid); got != 1 {
		t.Fatalf("distribution binding count = %d, want 1", got)
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

func useBuiltinCatalogWithZip(t *testing.T, entry skillbuiltin.CatalogSkill, files map[string]string) {
	t.Helper()
	root := t.TempDir()
	workingDirectory := filepath.Join(root, "backend", "core")
	if err := os.MkdirAll(workingDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	catalogDirectory := filepath.Join(root, "skills", ".runtime", "builtin-skills")
	archivePath := filepath.Join(catalogDirectory, "packages", entry.Key+".zip")
	writeBuiltinTestZip(t, archivePath, files)
	body, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(body)
	entry.ArchiveSHA256 = hex.EncodeToString(hash[:])
	entry.ArchiveSize = int64(len(body))
	entry.TreeSHA256 = skillpackage.TreeHash(stringMapBytes(files))
	entry.PackageFile = filepath.ToSlash(filepath.Join("packages", entry.Key+".zip"))
	catalog := skillbuiltin.Catalog{SchemaVersion: skillbuiltin.CatalogSchemaVersion, Skills: []skillbuiltin.CatalogSkill{entry}}
	catalogBody, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(catalogDirectory, "catalog.json"), catalogBody, 0o644); err != nil {
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

func writeBuiltinTestZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	paths := make([]string, 0, len(files))
	for name := range files {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	for _, name := range paths {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func stringMapBytes(files map[string]string) map[string][]byte {
	out := make(map[string][]byte, len(files))
	for name, content := range files {
		out[name] = []byte(content)
	}
	return out
}

func TestEnableBuiltinSkillRestoresTrashedInstall(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	uid := "bsk_trashed"
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").
		Update("origin_builtin_skill_uid", uid).Error; err != nil {
		t.Fatal(err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	enable := func() string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/core/builtin-skills/"+uid+":enable", nil)
		req = mux.SetURLVars(req, map[string]string{"builtin_skill_uid": uid})
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

	installedSkillID := "skill1"
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
