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
	skilldistribution "lazymind/core/skillv2/distribution"
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

func TestEnableBuiltinSkillInstallsDistinctBuiltinWhenSameNameExists(t *testing.T) {
	for _, tc := range []struct {
		name   string
		origin string
	}{
		{name: "legacy empty origin", origin: ""},
		{name: "same path different origin", origin: "bsk_previous_wechat_cover"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uid := wechatCoverCatalog(t)
			db := testutil.NewTestDB(t)
			testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
			if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").Updates(map[string]any{
				"category":                 "design",
				"skill_name":               "wechat-cover",
				"relative_root":            "design/wechat-cover",
				"origin_builtin_skill_uid": tc.origin,
				"is_enabled":               false,
			}).Error; err != nil {
				t.Fatal(err)
			}
			store.Init(db.DB, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })

			enableWechatCover(t, uid)
			var existing testutil.SkillRow
			if err := db.Where("id = ?", "skill1").Take(&existing).Error; err != nil {
				t.Fatal(err)
			}
			if existing.OriginBuiltinSkillUID != tc.origin || existing.IsEnabled {
				t.Fatalf("existing install was unexpectedly reused: %#v", existing)
			}
			assertRenamedWechatCoverInstall(t, db, uid, "skill1")
		})
	}
}

func TestEnableBuiltinSkillBindsDistributionWhenNameAvailable(t *testing.T) {
	uid := wechatCoverCatalog(t)
	db := testutil.NewTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	enableWechatCover(t, uid)
	var installed testutil.SkillRow
	if err := db.Where("owner_user_id = ? AND origin_builtin_skill_uid = ? AND deleted_at IS NULL", "user_001", uid).Take(&installed).Error; err != nil {
		t.Fatal(err)
	}
	if installed.SkillName != "wechat-cover" || installed.RelativeRoot != "design/wechat-cover" || !installed.IsEnabled {
		t.Fatalf("unconflicted builtin install identity = %#v", installed)
	}
	file, err := newSkillService(db.DB).ReadFile(context.Background(), skillservice.FileRef{SkillID: installed.ID, RefType: "head", Path: "SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(file.Content, "name: wechat-cover\n") {
		t.Fatalf("unconflicted SKILL.md name changed:\n%s", file.Content)
	}
	pkg, found, err := skillbuiltin.PackageByUID(uid)
	if err != nil || !found {
		t.Fatalf("PackageByUID(%q) found=%v err=%v", uid, found, err)
	}
	if got := testutil.CountRows(t, db, "skill_distribution_bindings", "skill_id = ?", installed.ID); got != 1 {
		t.Fatalf("distribution bindings = %d, want 1", got)
	}
	if got := testutil.CountRows(t, db, "skill_distribution_artifacts", "archive_sha256 = ?", pkg.SHA256); got != 1 {
		t.Fatalf("distribution artifacts = %d, want 1", got)
	}
}

func TestBuiltinInstallNameCandidateSequence(t *testing.T) {
	if got := builtinInstallNameCandidate("wechat-cover", "abc", 0); got != "wechat-cover-abc" {
		t.Fatalf("attempt 0 = %q, want wechat-cover-abc", got)
	}
	if got := builtinInstallNameCandidate("wechat-cover", "abc", 1); got != "wechat-cover-abc-1" {
		t.Fatalf("attempt 1 = %q, want wechat-cover-abc-1", got)
	}
}

func TestListBuiltinSkillsDoesNotTreatSameNameDifferentUIDAsInstalled(t *testing.T) {
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
			if item.UID != uid || item.Installed || item.InstalledSkillID != "" {
				t.Fatalf("same-name different-uid install was listed as installed: %#v", item)
			}
		})
	}
}

func wechatCoverCatalog(t *testing.T) string {
	t.Helper()
	uid := "bsk_wechat_cover"
	files := map[string][]byte{
		"SKILL.md": []byte("---\nname: wechat-cover\ndescription: WeChat cover designer\ncategory: design\nversion: 1.3.1\n---\n# WeChat Cover\n"),
	}
	useBuiltinCatalogWithPackage(t, skillbuiltin.CatalogSkill{
		Key: "wechat-cover", UID: uid, SourceURL: "https://skillhub.cn/skills/user_8d36cde0/wechat-cover", ResolvedURL: "https://example.test/wechat-cover.zip",
		Version: "1.3.1", Name: "wechat-cover", Description: "WeChat cover designer", Category: "design", Content: string(files["SKILL.md"]),
		PackageFile: "packages/wechat-cover.zip",
	}, files)
	return uid
}

const cangjieLikeSkillMD = "---\n" +
	"name: cangjie-skill\n" +
	"description: |\n" +
	"  Distill a book into a coherent set of executable skills.\n" +
	"  Use when the user asks to \"拆书\".\n" +
	"# keep this comment\n" +
	"license: MIT\n" +
	"compatibility: claude\n" +
	"---\n" +
	"# Cangjie\n" +
	"body line\n"

const cangjieLikeDescription = "Distill a book into a coherent set of executable skills.\nUse when the user asks to \"拆书\".\n"

const cangjieLikeUpdatedDescription = "Distill a book into a coherent set of executable skills.\nUse when the user asks to \"拆书\" / \"蒸馏一本书\".\n"

func enableWechatCover(t *testing.T, uid string) {
	enableBuiltin(t, uid)
}

func enableBuiltin(t *testing.T, uid string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/core/builtin-skills/"+uid+":enable", nil)
	req = mux.SetURLVars(req, map[string]string{"builtin_skill_uid": uid})
	req.Header.Set("X-User-Id", "user_001")
	req.Header.Set("X-User-Name", "张三")
	rec := httptest.NewRecorder()
	EnableBuiltinSkill(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
}

func assertRenamedWechatCoverInstall(t *testing.T, db *testutil.TestDB, uid, occupiedID string) testutil.SkillRow {
	t.Helper()
	wantName := builtinInstallNameCandidate("wechat-cover", builtinUIDSuffix(uid), 0)
	var installed testutil.SkillRow
	if err := db.Where("owner_user_id = ? AND origin_builtin_skill_uid = ? AND deleted_at IS NULL", "user_001", uid).Take(&installed).Error; err != nil {
		t.Fatal(err)
	}
	if installed.ID == occupiedID || !installed.IsEnabled {
		t.Fatalf("builtin install did not get an independent local identity: %#v", installed)
	}
	if installed.SkillName != wantName || installed.RelativeRoot != "design/"+wantName {
		t.Fatalf("install identity = name=%q root=%q, want %q", installed.SkillName, installed.RelativeRoot, wantName)
	}
	file, err := newSkillService(db.DB).ReadFile(context.Background(), skillservice.FileRef{SkillID: installed.ID, RefType: "head", Path: "SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(file.Content, "name: "+wantName+"\n") {
		t.Fatalf("SKILL.md was not rewritten to %q:\n%s", wantName, file.Content)
	}
	pkg, found, err := skillbuiltin.PackageByUID(uid)
	if err != nil || !found {
		t.Fatalf("PackageByUID(%q) found=%v err=%v", uid, found, err)
	}
	if got := testutil.CountRows(t, db, "skill_distribution_bindings", "skill_id = ?", installed.ID); got != 1 {
		t.Fatalf("distribution bindings = %d, want 1", got)
	}
	if got := testutil.CountRows(t, db, "skill_distribution_artifacts", "archive_sha256 = ?", pkg.SHA256); got != 1 {
		t.Fatalf("distribution artifacts = %d, want 1", got)
	}
	artifact := distributionArtifactSkillMD(t, db, pkg.SHA256)
	if !strings.Contains(artifact, "name: wechat-cover\n") || strings.Contains(artifact, wantName) {
		t.Fatalf("official artifact was polluted:\n%s", artifact)
	}
	status, err := newDistributionService(db.DB).GetStatus(context.Background(), skilldistribution.StatusRequest{SkillID: installed.ID, UserID: "user_001"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Managed || status.CurrentArchiveSHA256 != pkg.SHA256 {
		t.Fatalf("renamed install status = %#v", status)
	}
	if got := testutil.CountRows(t, db, "skills", "owner_user_id = ? AND category = ? AND deleted_at IS NULL", "user_001", "design"); got != 2 {
		t.Fatalf("active design skill count = %d, want 2", got)
	}
	return installed
}

func distributionArtifactSkillMD(t *testing.T, db *testutil.TestDB, archiveSHA string) string {
	t.Helper()
	var entry struct {
		BlobHash *string `gorm:"column:blob_hash"`
	}
	if err := db.Table("skill_distribution_entries").Select("blob_hash").Where("archive_sha256 = ? AND path = ?", archiveSHA, "SKILL.md").Take(&entry).Error; err != nil {
		t.Fatal(err)
	}
	if entry.BlobHash == nil {
		t.Fatal("distribution SKILL.md blob hash is empty")
	}
	var blob struct {
		Content []byte `gorm:"column:content"`
	}
	if err := db.Table("skill_blobs").Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
		t.Fatal(err)
	}
	return string(blob.Content)
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

func TestEnableBuiltinSkillReinstallsWhenTrashedBuiltinPathConflicts(t *testing.T) {
	uid := wechatCoverCatalog(t)
	db := testutil.NewTestDB(t)
	testutil.RelaxSQLiteFixtureSkillUniqueIndexes(t, db.DB)
	testutil.SeedSkillWithRevision(t, db, "trashed_builtin", "rev_trashed")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "trashed_builtin").Updates(map[string]any{
		"category":                 "design",
		"skill_name":               "wechat-cover",
		"relative_root":            "design/wechat-cover",
		"origin_builtin_skill_uid": uid,
	}).Error; err != nil {
		t.Fatal(err)
	}
	service := skillservice.NewSkillService(skillservice.SkillServiceDeps{DB: db.DB})
	if err := service.DeleteSkill(context.Background(), skillservice.DeleteSkillRequest{SkillID: "trashed_builtin", UserID: "user_001"}); err != nil {
		t.Fatal(err)
	}
	testutil.MustCreate(t, db, &testutil.SkillRow{
		ID:                    "active_same_name",
		OwnerUserID:           "user_001",
		CreateUserID:          "user_001",
		Category:              "design",
		SkillName:             "wechat-cover",
		OriginBuiltinSkillUID: "bsk_other_wechat_cover",
		RelativeRoot:          "design/wechat-cover",
	})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	enableWechatCover(t, uid)
	assertRenamedWechatCoverInstall(t, db, uid, "trashed_builtin")
}

func TestEnableBuiltinSkillRenamedInstallKeepsComplexFrontmatterAndAutoMergesDescription(t *testing.T) {
	uid := "bsk_cangjie_skill"
	original := cangjieLikeSkillMD
	files := map[string][]byte{"SKILL.md": []byte(original)}
	useBuiltinCatalogWithPackage(t, skillbuiltin.CatalogSkill{
		Key: "cangjie-skill", UID: uid, SourceURL: "https://skillhub.cn/skills/user_1a7e2e57/cangjie-distill",
		ResolvedURL: "https://example.test/cangjie-skill.zip", Version: "1.0.0", Name: "cangjie-skill",
		Description: cangjieLikeDescription, Category: "writing", Content: original, PackageFile: "packages/cangjie-skill.zip",
	}, files)

	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").Updates(map[string]any{
		"category":                 "writing",
		"skill_name":               "cangjie-skill",
		"relative_root":            "writing/cangjie-skill",
		"origin_builtin_skill_uid": "bsk_previous_cangjie",
	}).Error; err != nil {
		t.Fatal(err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	enableBuiltin(t, uid)
	wantName := builtinInstallNameCandidate("cangjie-skill", builtinUIDSuffix(uid), 0)
	var installed testutil.SkillRow
	if err := db.Where("owner_user_id = ? AND origin_builtin_skill_uid = ? AND deleted_at IS NULL", "user_001", uid).Take(&installed).Error; err != nil {
		t.Fatal(err)
	}
	file, err := newSkillService(db.DB).ReadFile(context.Background(), skillservice.FileRef{SkillID: installed.ID, RefType: "head", Path: "SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(original, "name: cangjie-skill\n", "name: "+wantName+"\n", 1)
	if file.Content != want {
		t.Fatalf("conflict copy changed more than name:\ngot:\n%s\nwant:\n%s", file.Content, want)
	}

	updated := strings.Replace(original, "  Use when the user asks to \"拆书\".\n", "  Use when the user asks to \"拆书\" / \"蒸馏一本书\".\n", 1)
	useBuiltinCatalogWithPackage(t, skillbuiltin.CatalogSkill{
		Key: "cangjie-skill", UID: uid, SourceURL: "https://skillhub.cn/skills/user_1a7e2e57/cangjie-distill",
		ResolvedURL: "https://example.test/cangjie-skill.zip", Version: "1.0.1", Name: "cangjie-skill",
		Description: cangjieLikeUpdatedDescription, Category: "writing", Content: updated, PackageFile: "packages/cangjie-skill.zip",
	}, map[string][]byte{"SKILL.md": []byte(updated)})

	req := httptest.NewRequest(http.MethodPost, "/api/core/skills/"+installed.ID+"/distribution-upgrade:prepare", nil)
	req = mux.SetURLVars(req, map[string]string{"skill_id": installed.ID})
	req.Header.Set("X-User-Id", "user_001")
	req.Header.Set("X-User-Name", "张三")
	rec := httptest.NewRecorder()
	PrepareDistributionUpgrade(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("prepare status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response struct {
		Data struct {
			AutoMerged bool  `json:"auto_merged"`
			Conflicts  []any `json:"conflicts"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode prepare response: %v", err)
	}
	if !response.Data.AutoMerged || len(response.Data.Conflicts) != 0 {
		t.Fatalf("prepare = %#v, want auto-merged without conflicts", response.Data)
	}
	draft := draftSkillMD(t, db, installed.ID)
	if !strings.Contains(draft, "name: "+wantName+"\n") || !strings.Contains(draft, `"拆书" / "蒸馏一本书"`) {
		t.Fatalf("upgrade draft did not keep local name and apply official description:\n%s", draft)
	}
	if strings.Contains(draft, "category:") || !strings.Contains(draft, "# keep this comment") {
		t.Fatalf("upgrade draft restyled frontmatter:\n%s", draft)
	}
}

func TestEnableBuiltinSkillRenamedInstallCanPrepareOfficialUpgrade(t *testing.T) {
	uid := wechatCoverCatalog(t)
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").Updates(map[string]any{
		"category":                 "design",
		"skill_name":               "wechat-cover",
		"relative_root":            "design/wechat-cover",
		"origin_builtin_skill_uid": "bsk_previous_wechat_cover",
	}).Error; err != nil {
		t.Fatal(err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	enableWechatCover(t, uid)
	installed := assertRenamedWechatCoverInstall(t, db, uid, "skill1")
	wantName := builtinInstallNameCandidate("wechat-cover", builtinUIDSuffix(uid), 0)

	updated := map[string][]byte{
		"SKILL.md": []byte("---\nname: wechat-cover\ndescription: WeChat cover designer v2\ncategory: design\nversion: 1.3.2\n---\n# WeChat Cover\n"),
	}
	useBuiltinCatalogWithPackage(t, skillbuiltin.CatalogSkill{
		Key: "wechat-cover", UID: uid, SourceURL: "https://skillhub.cn/skills/user_8d36cde0/wechat-cover", ResolvedURL: "https://example.test/wechat-cover.zip",
		Version: "1.3.2", Name: "wechat-cover", Description: "WeChat cover designer v2", Category: "design", Content: string(updated["SKILL.md"]),
		PackageFile: "packages/wechat-cover.zip",
	}, updated)

	req := httptest.NewRequest(http.MethodPost, "/api/core/skills/"+installed.ID+"/distribution-upgrade:prepare", nil)
	req = mux.SetURLVars(req, map[string]string{"skill_id": installed.ID})
	req.Header.Set("X-User-Id", "user_001")
	req.Header.Set("X-User-Name", "张三")
	rec := httptest.NewRecorder()
	PrepareDistributionUpgrade(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("prepare status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response struct {
		Data struct {
			AutoMerged bool  `json:"auto_merged"`
			Conflicts  []any `json:"conflicts"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode prepare response: %v", err)
	}
	if !response.Data.AutoMerged || len(response.Data.Conflicts) != 0 {
		t.Fatalf("prepare = %#v, want auto-merged without conflicts", response.Data)
	}
	draft := draftSkillMD(t, db, installed.ID)
	if !strings.Contains(draft, "name: "+wantName+"\n") || !strings.Contains(draft, "WeChat cover designer v2") {
		t.Fatalf("upgrade draft did not keep local name and apply official description:\n%s", draft)
	}
}

func draftSkillMD(t *testing.T, db *testutil.TestDB, skillID string) string {
	t.Helper()
	var entry struct {
		BlobHash *string `gorm:"column:blob_hash"`
	}
	if err := db.Table("skill_draft_entries").Where("skill_id = ? AND path = ?", skillID, "SKILL.md").Take(&entry).Error; err != nil {
		t.Fatal(err)
	}
	if entry.BlobHash == nil {
		t.Fatal("draft SKILL.md blob hash is empty")
	}
	var blob struct {
		Content []byte `gorm:"column:content"`
	}
	if err := db.Table("skill_blobs").Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
		t.Fatal(err)
	}
	return string(blob.Content)
}
