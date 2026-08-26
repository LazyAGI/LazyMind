package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestEnableBuiltinSkillClaimsLegacySameNameInstall(t *testing.T) {
	uid := "bsk_wechat_cover"
	files := map[string][]byte{
		"SKILL.md": []byte("---\nname: wechat-cover\ndescription: WeChat cover designer\ncategory: design\nversion: 1.3.1\n---\n# WeChat Cover\n"),
	}
	useBuiltinCatalogWithPackage(t, skillbuiltin.CatalogSkill{
		Key: "wechat-cover", UID: uid, SourceURL: "https://skillhub.cn/skills/user_8d36cde0/wechat-cover", ResolvedURL: "https://example.test/wechat-cover.zip",
		Version: "1.3.1", Name: "wechat-cover", Description: "WeChat cover designer", Category: "design", Content: string(files["SKILL.md"]),
		PackageFile: "packages/wechat-cover.zip",
	}, files)
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").Updates(map[string]any{
		"category":                 "design",
		"skill_name":               "wechat-cover",
		"relative_root":            "design/wechat-cover",
		"origin_builtin_skill_uid": "",
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
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var row testutil.SkillRow
	if err := db.Where("id = ?", "skill1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.OriginBuiltinSkillUID != uid || !row.IsEnabled {
		t.Fatalf("legacy install was not claimed and enabled: %#v", row)
	}
	if got := testutil.CountRows(t, db, "skills", "owner_user_id = ? AND category = ? AND skill_name = ? AND deleted_at IS NULL", "user_001", "design", "wechat-cover"); got != 1 {
		t.Fatalf("active wechat-cover skill count = %d, want 1", got)
	}
}

func TestEnableBuiltinSkillReusesSamePathInstallWithDifferentOrigin(t *testing.T) {
	uid := "bsk_wechat_cover"
	files := map[string][]byte{
		"SKILL.md": []byte("---\nname: wechat-cover\ndescription: WeChat cover designer\ncategory: design\nversion: 1.3.1\n---\n# WeChat Cover\n"),
	}
	useBuiltinCatalogWithPackage(t, skillbuiltin.CatalogSkill{
		Key: "wechat-cover", UID: uid, SourceURL: "https://skillhub.cn/skills/user_8d36cde0/wechat-cover", ResolvedURL: "https://example.test/wechat-cover.zip",
		Version: "1.3.1", Name: "wechat-cover", Description: "WeChat cover designer", Category: "design", Content: string(files["SKILL.md"]),
		PackageFile: "packages/wechat-cover.zip",
	}, files)
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").Updates(map[string]any{
		"category":                 "design",
		"skill_name":               "wechat-cover",
		"relative_root":            "design/wechat-cover",
		"origin_builtin_skill_uid": "bsk_previous_wechat_cover",
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
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var row testutil.SkillRow
	if err := db.Where("id = ?", "skill1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.OriginBuiltinSkillUID != "bsk_previous_wechat_cover" || !row.IsEnabled {
		t.Fatalf("same-path install was not reused and enabled: %#v", row)
	}
	if got := testutil.CountRows(t, db, "skills", "owner_user_id = ? AND relative_root = ? AND deleted_at IS NULL", "user_001", "design/wechat-cover"); got != 1 {
		t.Fatalf("active design/wechat-cover skill count = %d, want 1", got)
	}
}

func TestListBuiltinSkillsTreatsCompatibleInstallAsInstalled(t *testing.T) {
	uid := "bsk_wechat_cover"
	useBuiltinCatalog(t, skillbuiltin.Catalog{SchemaVersion: skillbuiltin.CatalogSchemaVersion, Skills: []skillbuiltin.CatalogSkill{{
		Key: "wechat-cover", UID: uid, SourceURL: "https://example.test/wechat-cover.zip", ResolvedURL: "https://example.test/wechat-cover.zip",
		Version: "1.3.1", Name: "wechat-cover", Description: "WeChat cover designer", Category: "design", Content: "# WeChat Cover",
		ArchiveSHA256: strings.Repeat("a", 64), TreeSHA256: strings.Repeat("b", 64), ArchiveSize: 1, PackageFile: "packages/wechat-cover.zip",
	}}})

	for _, tc := range []struct {
		name   string
		origin string
	}{
		{name: "legacy empty origin", origin: ""},
		{name: "same path different origin", origin: "bsk_previous_wechat_cover"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testutil.NewTestDB(t)
			testutil.MustCreate(t, db, &testutil.SkillRow{
				ID:                    "skill1",
				OwnerUserID:           "user_001",
				CreateUserID:          "user_001",
				Category:              "design",
				SkillName:             "wechat-cover",
				OriginBuiltinSkillUID: tc.origin,
				RelativeRoot:          "design/wechat-cover",
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
						Installed        bool   `json:"installed"`
						InstalledSkillID string `json:"installed_skill_id"`
					} `json:"items"`
				} `json:"data"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(response.Data.Items) != 1 {
				t.Fatalf("items = %#v, want 1", response.Data.Items)
			}
			item := response.Data.Items[0]
			if item.UID != uid || !item.Installed || item.InstalledSkillID != "skill1" {
				t.Fatalf("compatible install was not listed as installed: %#v", item)
			}
		})
	}
}

func useBuiltinCatalog(t *testing.T, catalog skillbuiltin.Catalog) {
	t.Helper()
	writeBuiltinCatalog(t, catalog, nil)
}

func useBuiltinCatalogWithPackage(t *testing.T, entry skillbuiltin.CatalogSkill, files map[string][]byte) {
	t.Helper()
	writeBuiltinCatalog(t, skillbuiltin.Catalog{
		SchemaVersion: skillbuiltin.CatalogSchemaVersion,
		Skills:        []skillbuiltin.CatalogSkill{entry},
	}, files)
}

func writeBuiltinCatalog(t *testing.T, catalog skillbuiltin.Catalog, files map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	workingDirectory := filepath.Join(root, "backend", "core")
	if err := os.MkdirAll(workingDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	catalogDirectory := filepath.Join(root, "skills", ".runtime", "builtin-skills")
	if files != nil {
		if len(catalog.Skills) != 1 {
			t.Fatal("package files require exactly one catalog skill")
		}
		entry := catalog.Skills[0]
		zipPath := filepath.Join(catalogDirectory, filepath.FromSlash(entry.PackageFile))
		testutil.WriteSkillZip(t, zipPath, files)
		body, err := os.ReadFile(zipPath)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		entry.ArchiveSHA256 = fmt.Sprintf("%x", sum[:])
		entry.ArchiveSize = int64(len(body))
		entry.TreeSHA256 = skillpackage.TreeHash(files)
		catalog.Skills[0] = entry
	} else if err := os.MkdirAll(catalogDirectory, 0o755); err != nil {
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
