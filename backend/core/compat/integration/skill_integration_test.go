package integration_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"lazymind/core/common/orm"
	"lazymind/core/compat/contract"
	adaptercore "lazymind/core/compat/internal/adapters/core"
	compatruntime "lazymind/core/compat/runtime"
	compatskill "lazymind/core/compat/skill"
	skillremotefs "lazymind/core/skillv2/remotefs"
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

func TestSkillRuntimeKeywordSpecialCharactersWithRealPostgres(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	prefix := uniqueIntegrationPrefix("keyword")
	var createdSkillIDs []string
	t.Cleanup(func() { cleanupIntegrationSkills(t, context.Background(), db.DB, createdSkillIDs) })
	for _, tc := range []struct {
		name    string
		keyword string
		similar string
	}{
		{name: "underscore", keyword: "a_b", similar: "axb"},
		{name: "percent", keyword: "a%b", similar: "axxb"},
		{name: "backslash", keyword: `a\b`, similar: "ab"},
	} {
		contentSkill := createIntegrationSkillWithTags(t, ctx, db.DB, svc, userID, prefix+"-"+tc.name, "content", "content literal "+tc.keyword, []string{prefix, "content"})
		tagSkill := createIntegrationSkillWithTags(t, ctx, db.DB, svc, userID, prefix+"-"+tc.name, "tag", "tag-only content", []string{prefix, tc.keyword})
		similarSkill := createIntegrationSkillWithTags(t, ctx, db.DB, svc, userID, prefix+"-"+tc.name, "similar", "similar literal "+tc.similar, []string{prefix, tc.similar})
		createdSkillIDs = append(createdSkillIDs, contentSkill.SkillID, tagSkill.SkillID, similarSkill.SkillID)

		got, err := rt.Skill.List(ctx, contract.CallContext{UserID: userID}, compatskill.ListInput{
			Keyword: tc.keyword,
			Page:    contract.PageRequest{PageSize: 10},
		})
		if err != nil {
			t.Fatalf("Skill.List keyword %q: %v", tc.keyword, err)
		}
		if got.Page.Total == nil || *got.Page.Total < 2 {
			t.Fatalf("keyword %q total = %v, want at least content+tag matches", tc.keyword, got.Page.Total)
		}
		ids := map[string]bool{}
		for _, item := range got.Items {
			ids[item.ID] = true
		}
		if !ids[contentSkill.SkillID] || !ids[tagSkill.SkillID] {
			t.Fatalf("keyword %q ids = %#v, want content and tag skills", tc.keyword, ids)
		}
		if ids[similarSkill.SkillID] {
			t.Fatalf("keyword %q matched similar skill %s", tc.keyword, similarSkill.SkillID)
		}
		t.Logf("postgres keyword literal %s PASS: keyword=%q total=%d content=%s tag=%s", tc.name, tc.keyword, *got.Page.Total, contentSkill.SkillID, tagSkill.SkillID)
	}
}

func TestSkillRuntimeDraftOnlyEmptyHeadWithRealPostgres(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	prefix := uniqueIntegrationPrefix("draft-only")
	fixture := createDraftOnlyIntegrationSkill(t, ctx, db.DB, userID, prefix)
	t.Cleanup(func() { cleanupIntegrationSkills(t, context.Background(), db.DB, []string{fixture.SkillID}) })

	metadata, err := rt.Skill.Get(ctx, contract.CallContext{UserID: userID}, compatskill.GetInput{SkillID: fixture.SkillID})
	if err != nil {
		t.Fatalf("Skill.Get draft-only metadata: %v", err)
	}
	if metadata.Skill.HeadRevisionID != "" || metadata.Content != nil {
		t.Fatalf("draft-only metadata = %#v, want empty head and no content", metadata)
	}
	_, err = rt.Skill.Get(ctx, contract.CallContext{UserID: userID}, compatskill.GetInput{SkillID: fixture.SkillID, IncludeContent: true})
	if code, ok := contract.CodeOf(err); !ok || code != contract.NotFound {
		t.Fatalf("draft-only include_content error code = %v, %v, err=%v; want NOT_FOUND", code, ok, err)
	}
	t.Log("draft-only nil head behavior PASS")
}

func TestSkillRemoteFSBinaryWriteWithRealPostgres(t *testing.T) {
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
	handler := skillremotefs.NewHandler(skillremotefs.HandlerDeps{
		DB:        db.DB,
		BlobStore: skillremotefs.NewBlobStore(db.DB, skillremotefs.NewLocalObjectStore(objectRoot)),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	prefix := uniqueIntegrationPrefix("remote-binary")
	fixture := createIntegrationSkill(t, ctx, db.DB, svc, userID, prefix, "skill", "remote fs seed "+prefix)
	t.Cleanup(func() { cleanupIntegrationSkills(t, context.Background(), db.DB, []string{fixture.SkillID}) })

	data := minimalIntegrationPNGBytes()
	remotePath := "skills/" + fixture.Category + "/" + fixture.Name + "/assets/logo.png"
	relPath := "assets/logo.png"
	taskID := prefix + "-task"
	put := httptest.NewRecorder()
	handler.Content(put, httptest.NewRequest(http.MethodPut, remoteFSContentURL(remotePath, userID, taskID, ""), bytes.NewReader(data)))
	if put.Code != http.StatusOK {
		t.Fatalf("remote fs binary PUT status=%d body=%s", put.Code, put.Body.String())
	}
	t.Log("remote fs binary PUT PASS")

	blobHash := currentIntegrationDraftBlobHash(t, ctx, db.DB, fixture.SkillID, relPath)
	state := integrationBlobStorageStateForHash(t, ctx, db.DB, blobHash)
	if !state.Binary ||
		state.StorageBackend != "local_file" ||
		!state.ContentIsNull ||
		!state.StorageKey.Valid ||
		state.StorageKey.String == "" ||
		state.FileType != "image" ||
		state.Mime != "image/png" {
		t.Fatalf("remote fs binary blob state = %#v, want external binary SQL NULL content", state)
	}
	t.Log("remote fs binary blob storage PASS")

	raw := httptest.NewRecorder()
	handler.Content(raw, httptest.NewRequest(http.MethodGet, remoteFSContentURL(remotePath, userID, taskID, "raw"), nil))
	if raw.Code != http.StatusOK || !bytes.Equal(raw.Body.Bytes(), data) {
		t.Fatalf("remote fs binary raw read status=%d len=%d", raw.Code, raw.Body.Len())
	}
	t.Log("remote fs binary raw read PASS")

	encoded := httptest.NewRecorder()
	handler.Content(encoded, httptest.NewRequest(http.MethodGet, remoteFSContentURL(remotePath, userID, taskID, "base64"), nil))
	if encoded.Code != http.StatusOK || !strings.Contains(encoded.Body.String(), base64.StdEncoding.EncodeToString(data)) {
		t.Fatalf("remote fs binary base64 read status=%d body=%s", encoded.Code, encoded.Body.String())
	}
	t.Log("remote fs binary base64 read PASS")
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
	return createIntegrationSkillWithTags(t, ctx, db, svc, userID, prefix, suffix, body, []string{prefix, "compat-integration"})
}

func createIntegrationSkillWithTags(t *testing.T, ctx context.Context, db *gorm.DB, svc *skillservice.SkillService, userID, prefix, suffix, body string, tags []string) integrationSkill {
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
		Tags:           append([]string(nil), tags...),
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

func createDraftOnlyIntegrationSkill(t *testing.T, ctx context.Context, db *gorm.DB, userID, prefix string) integrationSkill {
	t.Helper()
	now := time.Now().UTC()
	fixture := integrationSkill{
		SkillID:     uuid.NewString(),
		Name:        prefix + "-draft",
		Category:    prefix + "-category",
		Description: prefix + " draft-only description",
	}
	tags, err := json.Marshal([]string{prefix, "compat-integration"})
	if err != nil {
		t.Fatalf("marshal draft tags: %v", err)
	}
	if err := db.WithContext(ctx).Table("skills").Create(map[string]any{
		"id":                    fixture.SkillID,
		"owner_user_id":         userID,
		"owner_user_name":       "compat integration",
		"create_user_id":        userID,
		"create_user_name":      "compat integration",
		"category":              fixture.Category,
		"skill_name":            fixture.Name,
		"description":           fixture.Description,
		"tags":                  string(tags),
		"relative_root":         fixture.Category + "/" + fixture.Name,
		"skill_md_path":         "SKILL.md",
		"version":               1,
		"auto_evo":              false,
		"auto_evo_apply_status": "idle",
		"auto_evo_generation":   0,
		"auto_evo_error":        "",
		"is_enabled":            false,
		"update_status":         "up_to_date",
		"ext":                   "{}",
		"created_at":            now,
		"updated_at":            now,
	}).Error; err != nil {
		t.Fatalf("create draft-only skill: %v", err)
	}
	if err := db.WithContext(ctx).Table("skill_drafts").Create(map[string]any{
		"skill_id":         fixture.SkillID,
		"draft_status":     "pending_confirm",
		"task_id":          prefix + "-task",
		"version":          1,
		"draft_updated_at": now,
		"created_at":       now,
		"updated_at":       now,
	}).Error; err != nil {
		t.Fatalf("create draft-only draft: %v", err)
	}
	return fixture
}

func remoteFSContentURL(pathValue, userID, taskID, encoding string) string {
	values := url.Values{"path": {pathValue}, "user_id": {userID}, "task_id": {taskID}}
	if encoding != "" {
		values.Set("encoding", encoding)
	}
	return "/remote-fs/content?" + values.Encode()
}

func currentIntegrationDraftBlobHash(t *testing.T, ctx context.Context, db *gorm.DB, skillID, relPath string) string {
	t.Helper()
	var row struct {
		BlobHash string `gorm:"column:blob_hash"`
	}
	if err := db.WithContext(ctx).Table("skill_draft_entries").
		Select("blob_hash").
		Where("skill_id = ? AND path = ? AND op = ?", skillID, relPath, "upsert").
		Take(&row).Error; err != nil {
		t.Fatalf("query current draft blob hash: %v", err)
	}
	return row.BlobHash
}

type integrationBlobStorageState struct {
	Hash           string         `gorm:"column:hash"`
	Binary         bool           `gorm:"column:binary"`
	StorageBackend string         `gorm:"column:storage_backend"`
	StorageKey     sql.NullString `gorm:"column:storage_key"`
	ContentIsNull  bool           `gorm:"column:content_is_null"`
	ContentLen     sql.NullInt64  `gorm:"column:content_len"`
	FileType       string         `gorm:"column:file_type"`
	Mime           string         `gorm:"column:mime"`
}

func integrationBlobStorageStateForHash(t *testing.T, ctx context.Context, db *gorm.DB, hash string) integrationBlobStorageState {
	t.Helper()
	var state integrationBlobStorageState
	if err := db.WithContext(ctx).Raw(`
		SELECT
			hash,
			"binary",
			storage_backend,
			storage_key,
			content IS NULL AS content_is_null,
			length(content) AS content_len,
			file_type,
			mime
		FROM skill_blobs
		WHERE hash = ?
	`, hash).Scan(&state).Error; err != nil {
		t.Fatalf("query blob storage state: %v", err)
	}
	if state.Hash == "" {
		t.Fatalf("blob %q not found", hash)
	}
	return state
}

func minimalIntegrationPNGBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
		0x42, 0x60, 0x82,
	}
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
	var draftBlobHashes []string
	if err := db.WithContext(ctx).Table("skill_draft_entries").Where("skill_id IN ? AND blob_hash IS NOT NULL", skillIDs).Distinct().Pluck("blob_hash", &draftBlobHashes).Error; err != nil {
		t.Errorf("cleanup collect draft blobs: %v", err)
		return
	}
	blobHashes = appendUniqueStrings(blobHashes, draftBlobHashes...)
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

func appendUniqueStrings(values []string, candidates ...string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		values = append(values, candidate)
	}
	return values
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
