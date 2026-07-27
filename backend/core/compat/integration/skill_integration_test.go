package integration_test

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/compat/contract"
	adaptercore "lazymind/core/compat/internal/adapters/core"
	compatruntime "lazymind/core/compat/runtime"
	compatskill "lazymind/core/compat/skill"
	skillservice "lazymind/core/skillv2/service"

	"gorm.io/gorm"
)

func TestSkillRuntimeWithRealSkillService(t *testing.T) {
	if strings.TrimSpace(os.Getenv("COMPAT_INTEGRATION")) != "1" {
		t.Skip("set COMPAT_INTEGRATION=1 to run compat integration tests")
	}
	userID := strings.TrimSpace(os.Getenv("COMPAT_TEST_USER_ID"))
	if userID == "" {
		t.Fatal("COMPAT_TEST_USER_ID is required")
	}

	driver, dsn := dbConfigFromCoreEnv(t)
	db, err := orm.Connect(driver, dsn)
	if err != nil {
		t.Fatalf("connect core db: %v", err)
	}
	sqlDB, err := db.DB.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	objectRoot := t.TempDir()
	svc := skillservice.NewSkillService(skillservice.SkillServiceDeps{
		DB:        db.DB,
		BlobStore: skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(objectRoot)),
	})
	adapter, err := adaptercore.NewSkillAdapterForDB(svc, db.DB)
	if err != nil {
		t.Fatalf("NewSkillAdapterForDB: %v", err)
	}
	rt, err := compatruntime.New(compatruntime.Dependencies{SkillPort: adapter})
	if err != nil {
		t.Fatalf("Runtime.New: %v", err)
	}
	if rt.Skill == nil {
		t.Fatal("Runtime.Skill is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	prefix := uniqueIntegrationPrefix("basic")
	fixture := createIntegrationSkill(t, ctx, db.DB, svc, userID, prefix, "basic", "basic SKILL.md content")
	t.Cleanup(func() { cleanupIntegrationSkills(t, context.Background(), db.DB, []string{fixture.SkillID}) })

	callCtx := contract.CallContext{UserID: userID}
	list, err := rt.Skill.List(ctx, callCtx, compatskill.ListInput{
		Keyword: prefix,
		Page: contract.PageRequest{
			PageSize: contract.DefaultPageSize,
		},
	})
	if err != nil {
		t.Fatalf("Skill.List: %v", err)
	}

	if len(list.Items) != 1 {
		t.Fatalf("Skill.List returned %d skills for prefix %q, want 1", len(list.Items), prefix)
	}

	if list.Page.Total == nil {
		t.Fatal("list total is nil")
	}

	if *list.Page.Total < int64(len(list.Items)) {
		t.Fatalf(
			"total = %d, returned = %d",
			*list.Page.Total,
			len(list.Items),
		)
	}

	// 打印 Compat List 的实际返回结果。
	t.Logf(
		"Compat Skill.List: total=%d, returned=%d",
		*list.Page.Total,
		len(list.Items),
	)

	for i, item := range list.Items {
		t.Logf(
			"Compat List[%d]: id=%s name=%q category=%q enabled=%v head_revision_id=%s",
			i,
			item.ID,
			item.Name,
			item.Category,
			item.Enabled,
			item.HeadRevisionID,
		)
	}

	first := list.Items[0]
	if strings.TrimSpace(first.ID) == "" {
		t.Fatal("first skill ID is empty")
	}

	got, err := rt.Skill.Get(
		ctx,
		callCtx,
		compatskill.GetInput{
			SkillID: first.ID,
		},
	)
	if err != nil {
		t.Fatalf("Skill.Get(%q): %v", first.ID, err)
	}

	if got.Skill.ID != first.ID {
		t.Fatalf(
			"Get ID = %q, want %q",
			got.Skill.ID,
			first.ID,
		)
	}

	// 打印 Compat Get 的实际返回结果。
	t.Logf(
		"Compat Skill.Get: id=%s name=%q category=%q enabled=%v head_revision_id=%s description=%q",
		got.Skill.ID,
		got.Skill.Name,
		got.Skill.Category,
		got.Skill.Enabled,
		got.Skill.HeadRevisionID,
		got.Skill.Description,
	)
}

func TestSkillRuntimeExplicitRevisionContentWithRealPostgres(t *testing.T) {
	if strings.TrimSpace(os.Getenv("COMPAT_INTEGRATION")) != "1" {
		t.Skip("set COMPAT_INTEGRATION=1 to run compat integration tests")
	}
	userID := strings.TrimSpace(os.Getenv("COMPAT_TEST_USER_ID"))
	if userID == "" {
		t.Fatal("COMPAT_TEST_USER_ID is required")
	}

	driver, dsn := dbConfigFromCoreEnv(t)
	if driver != "postgres" {
		t.Fatalf("driver = %q, want postgres", driver)
	}
	t.Log("PostgreSQL driver confirmation: postgres")
	db, err := orm.Connect(driver, dsn)
	if err != nil {
		t.Fatalf("connect core db: %v", err)
	}
	sqlDB, err := db.DB.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	objectRoot := t.TempDir()
	svc := skillservice.NewSkillService(skillservice.SkillServiceDeps{
		DB:        db.DB,
		BlobStore: skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(objectRoot)),
	})
	adapter, err := adaptercore.NewSkillAdapterForDB(svc, db.DB)
	if err != nil {
		t.Fatalf("NewSkillAdapterForDB: %v", err)
	}
	rt, err := compatruntime.New(compatruntime.Dependencies{SkillPort: adapter})
	if err != nil {
		t.Fatalf("Runtime.New: %v", err)
	}
	if rt.Skill == nil {
		t.Fatal("Runtime.Skill is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	prefix := uniqueIntegrationPrefix("explicit")
	skillA := createIntegrationSkill(t, ctx, db.DB, svc, userID, prefix, "skill-a", "original revision content "+prefix)
	skillB := createIntegrationSkill(t, ctx, db.DB, svc, userID, prefix, "skill-b", "skill B content "+prefix)
	binarySkill := createIntegrationSkill(t, ctx, db.DB, svc, userID, prefix, "binary", "binary branch seed "+prefix)
	createdSkillIDs := []string{skillA.SkillID, skillB.SkillID, binarySkill.SkillID}
	t.Cleanup(func() { cleanupIntegrationSkills(t, context.Background(), db.DB, createdSkillIDs) })

	originalRevisionID := skillA.HeadRevisionID
	nextRevisionID := replaceIntegrationSkillContent(t, ctx, svc, userID, skillA, "new head content "+prefix)
	if nextRevisionID == originalRevisionID {
		t.Fatalf("PatchSkill kept head revision %q, want new revision", nextRevisionID)
	}
	makeSkillMDBinary(t, ctx, db.DB, binarySkill)

	callCtx := contract.CallContext{UserID: userID}
	withoutContent, err := rt.Skill.Get(ctx, callCtx, compatskill.GetInput{SkillID: skillA.SkillID})
	if err != nil {
		t.Fatalf("Skill.Get without content: %v", err)
	}
	if withoutContent.Content != nil {
		t.Fatalf("Content = %#v, want nil", withoutContent.Content)
	}
	if withoutContent.Skill.HeadRevisionID != nextRevisionID {
		t.Fatalf("metadata head = %q, want %q", withoutContent.Skill.HeadRevisionID, nextRevisionID)
	}
	t.Log("include_content=false PASS")

	withContent, err := rt.Skill.Get(ctx, callCtx, compatskill.GetInput{SkillID: skillA.SkillID, IncludeContent: true})
	if err != nil {
		t.Fatalf("Skill.Get with content: %v", err)
	}
	if withContent.Content == nil {
		t.Fatal("Content is nil, want SKILL.md content")
	}
	if withContent.Skill.HeadRevisionID == "" || withContent.Content.RevisionID == "" {
		t.Fatalf("revision ids are empty: skill=%q content=%q", withContent.Skill.HeadRevisionID, withContent.Content.RevisionID)
	}
	if withContent.Skill.HeadRevisionID != withContent.Content.RevisionID {
		t.Fatalf("revision mismatch: skill=%q content=%q", withContent.Skill.HeadRevisionID, withContent.Content.RevisionID)
	}
	if !strings.Contains(withContent.Content.Text, "new head content "+prefix) {
		t.Fatalf("content text = %q, want new SKILL.md content", withContent.Content.Text)
	}
	t.Log("include_content=true PASS")
	t.Log("HeadRevisionID == Content.RevisionID")

	oldContent, err := adapter.ReadContent(ctx, callCtx, skillA.SkillID, originalRevisionID)
	if err != nil {
		t.Fatalf("Adapter.ReadContent explicit original revision: %v", err)
	}
	if oldContent.RevisionID != originalRevisionID || !strings.Contains(oldContent.Text, "original revision content "+prefix) {
		t.Fatalf("explicit revision content = %#v, want original revision content", oldContent)
	}
	if strings.Contains(oldContent.Text, "new head content "+prefix) {
		t.Fatalf("explicit original revision returned current head content: %q", oldContent.Text)
	}
	t.Log("explicit historical revision PASS")

	_, err = adapter.ReadContent(ctx, callCtx, skillA.SkillID, skillB.HeadRevisionID)
	if code, ok := contract.CodeOf(err); !ok || code != contract.NotFound {
		t.Fatalf("cross-skill revision error code = %v, %v, err=%v; want NOT_FOUND", code, ok, err)
	}
	t.Log("cross-skill revision rejected")

	nonOwner := contract.CallContext{UserID: userID + "-non-owner"}
	nonOwnerWithoutContent, err := rt.Skill.Get(ctx, nonOwner, compatskill.GetInput{SkillID: skillA.SkillID})
	assertCompatRejected(t, err, "non-owner Skill.Get include_content=false")
	if nonOwnerWithoutContent.Skill.ID != "" || nonOwnerWithoutContent.Content != nil {
		t.Fatalf("non-owner include_content=false returned data: %#v", nonOwnerWithoutContent)
	}
	nonOwnerWithContent, err := rt.Skill.Get(ctx, nonOwner, compatskill.GetInput{SkillID: skillA.SkillID, IncludeContent: true})
	assertCompatRejected(t, err, "non-owner Skill.Get include_content=true")
	if nonOwnerWithContent.Skill.ID != "" || nonOwnerWithContent.Content != nil {
		t.Fatalf("non-owner include_content=true returned data: %#v", nonOwnerWithContent)
	}
	nonOwnerContent, err := adapter.ReadContent(ctx, nonOwner, skillA.SkillID, nextRevisionID)
	assertCompatRejected(t, err, "non-owner Adapter.ReadContent")
	if nonOwnerContent.Text != "" || nonOwnerContent.RevisionID != "" {
		t.Fatalf("non-owner ReadContent returned content: %#v", nonOwnerContent)
	}
	t.Log("non-owner access rejected")

	_, err = rt.Skill.Get(ctx, callCtx, compatskill.GetInput{SkillID: binarySkill.SkillID, IncludeContent: true})
	if code, ok := contract.CodeOf(err); !ok || code != contract.Unsupported {
		t.Fatalf("binary SKILL.md error code = %v, %v, err=%v; want UNSUPPORTED", code, ok, err)
	}
	t.Log("binary SKILL.md -> UNSUPPORTED")

	t.Logf("postgres compat explicit revision: skillA=%s old=%s head=%s content_revision=%s", skillA.SkillID, originalRevisionID, withContent.Skill.HeadRevisionID, withContent.Content.RevisionID)
	t.Logf("postgres compat cross-skill revision rejected: skillA=%s revisionB=%s", skillA.SkillID, skillB.HeadRevisionID)
	t.Logf("postgres compat binary SKILL.md mapped to UNSUPPORTED: skill=%s revision=%s", binarySkill.SkillID, binarySkill.HeadRevisionID)
}

func assertCompatRejected(t *testing.T, err error, operation string) {
	t.Helper()
	code, ok := contract.CodeOf(err)
	if !ok || code != contract.NotFound {
		t.Fatalf("%s error code = %v, %v, err=%v; want NOT_FOUND", operation, code, ok, err)
	}
}

type integrationSkill struct {
	SkillID        string
	HeadRevisionID string
	Name           string
	Category       string
	Description    string
}

func uniqueIntegrationPrefix(scope string) string {
	return fmt.Sprintf("compat-it-%s-%d", scope, time.Now().UnixNano())
}

func createIntegrationSkill(t *testing.T, ctx context.Context, db *gorm.DB, svc *skillservice.SkillService, userID, prefix, suffix, body string) integrationSkill {
	t.Helper()
	fixture := integrationSkill{
		Name:        prefix + "-" + suffix,
		Category:    prefix + "-category",
		Description: prefix + " " + suffix + " description",
	}
	zipPath := writeIntegrationSkillZip(t, fixture.Name, fixture.Category, fixture.Description, body)
	resp, err := svc.CreateSkill(ctx, skillservice.CreateSkillRequest{
		OwnerUserID:    userID,
		OwnerUserName:  "compat integration",
		CreateUserID:   userID,
		CreateUserName: "compat integration",
		Name:           fixture.Name,
		Category:       fixture.Category,
		Description:    fixture.Description,
		Tags:           []string{prefix, "compat-integration"},
		Source: skillservice.SourceInput{
			Type:       "local_zip",
			StoredPath: zipPath,
			Filename:   filepath.Base(zipPath),
		},
	})
	if err != nil {
		t.Fatalf("CreateSkill(%s): %v", fixture.Name, err)
	}
	fixture.SkillID = resp.SkillID
	fixture.HeadRevisionID = resp.HeadRevisionID
	return fixture
}

func replaceIntegrationSkillContent(t *testing.T, ctx context.Context, svc *skillservice.SkillService, userID string, fixture integrationSkill, body string) string {
	t.Helper()
	zipPath := writeIntegrationSkillZip(t, fixture.Name, fixture.Category, fixture.Description, body)
	resp, err := svc.PatchSkill(ctx, skillservice.PatchSkillRequest{
		SkillID: fixture.SkillID,
		UserID:  userID,
		Source: &skillservice.SourceInput{
			Type:       "local_zip",
			StoredPath: zipPath,
			Filename:   filepath.Base(zipPath),
		},
	})
	if err != nil {
		t.Fatalf("PatchSkill(%s): %v", fixture.SkillID, err)
	}
	return resp.HeadRevisionID
}

func writeIntegrationSkillZip(t *testing.T, name, category, description, body string) string {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), name+".zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("SKILL.md")
	if err != nil {
		_ = writer.Close()
		_ = file.Close()
		t.Fatalf("create SKILL.md zip entry: %v", err)
	}
	content := fmt.Sprintf("---\nname: %s\ncategory: %s\ndescription: %s\n---\n# %s\n\n%s\n", name, category, description, name, body)
	if _, err := entry.Write([]byte(content)); err != nil {
		_ = writer.Close()
		_ = file.Close()
		t.Fatalf("write SKILL.md zip entry: %v", err)
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		t.Fatalf("close zip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}
	return zipPath
}

func makeSkillMDBinary(t *testing.T, ctx context.Context, db *gorm.DB, fixture integrationSkill) {
	t.Helper()
	var row struct {
		BlobHash string `gorm:"column:blob_hash"`
	}
	if err := db.WithContext(ctx).Table("skill_revision_entries").Select("blob_hash").Where("revision_id = ? AND path = ?", fixture.HeadRevisionID, "SKILL.md").Take(&row).Error; err != nil {
		t.Fatalf("find SKILL.md blob hash: %v", err)
	}
	storageKey := fixture.Name + "/binary-skill-md"
	if err := db.WithContext(ctx).Table("skill_blobs").Where("hash = ?", row.BlobHash).Updates(map[string]any{
		"binary":          true,
		"storage_backend": "local_file",
		"storage_key":     storageKey,
		"content":         nil,
		"mime":            "application/octet-stream",
		"file_type":       "binary",
	}).Error; err != nil {
		t.Fatalf("mark SKILL.md blob binary: %v", err)
	}
	if err := db.WithContext(ctx).Table("skill_revision_entries").Where("revision_id = ? AND path = ?", fixture.HeadRevisionID, "SKILL.md").Updates(map[string]any{
		"binary":    true,
		"mime":      "application/octet-stream",
		"file_type": "binary",
	}).Error; err != nil {
		t.Fatalf("mark SKILL.md entry binary: %v", err)
	}
}

func cleanupIntegrationSkills(t *testing.T, ctx context.Context, db *gorm.DB, skillIDs []string) {
	t.Helper()
	if len(skillIDs) == 0 {
		return
	}
	var revisionIDs []string
	if err := db.WithContext(ctx).Table("skill_revisions").Where("skill_id IN ?", skillIDs).Pluck("id", &revisionIDs).Error; err != nil {
		t.Errorf("cleanup collect revisions: %v", err)
		return
	}
	var blobHashes []string
	if len(revisionIDs) > 0 {
		if err := db.WithContext(ctx).Table("skill_revision_entries").Where("revision_id IN ? AND blob_hash IS NOT NULL", revisionIDs).Distinct().Pluck("blob_hash", &blobHashes).Error; err != nil {
			t.Errorf("cleanup collect blobs: %v", err)
			return
		}
	}
	for _, stmt := range []struct {
		table string
		where string
		args  []any
	}{
		{table: "skill_search_indexes", where: "skill_id IN ?", args: []any{skillIDs}},
		{table: "skill_draft_entries", where: "skill_id IN ?", args: []any{skillIDs}},
		{table: "skill_drafts", where: "skill_id IN ?", args: []any{skillIDs}},
		{table: "skill_revision_entries", where: "revision_id IN ?", args: []any{revisionIDs}},
		{table: "skill_revisions", where: "skill_id IN ?", args: []any{skillIDs}},
		{table: "skills", where: "id IN ?", args: []any{skillIDs}},
		{table: "skill_blobs", where: "hash IN ?", args: []any{blobHashes}},
	} {
		if (stmt.table == "skill_revision_entries" && len(revisionIDs) == 0) || (stmt.table == "skill_blobs" && len(blobHashes) == 0) {
			continue
		}
		if err := db.WithContext(ctx).Exec("DELETE FROM "+stmt.table+" WHERE "+stmt.where, stmt.args...).Error; err != nil {
			t.Errorf("cleanup %s: %v", stmt.table, err)
		}
	}
	t.Logf("cleanup completed for compat integration skills: %s", strings.Join(skillIDs, ","))
}

func dbConfigFromCoreEnv(t *testing.T) (string, string) {
	t.Helper()
	driver := strings.TrimSpace(os.Getenv("ACL_DB_DRIVER"))
	dsn := strings.TrimSpace(os.Getenv("ACL_DB_DSN"))
	if driver == "" {
		t.Fatal("ACL_DB_DRIVER is required")
	}
	if dsn == "" {
		t.Fatal("ACL_DB_DSN is required")
	}
	return driver, dsn
}
