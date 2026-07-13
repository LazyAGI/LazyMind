package service

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCreateSkillFromUploadedZip_CreatesInitialRevisionAndRoutesBlobs(t *testing.T) {
	ctx := context.Background()

	db := newSkillV2TestDB(t)
	localBlobDir := t.TempDir()
	uploadDir := t.TempDir()

	zipPath := filepath.Join(uploadDir, "skill.zip")
	writeSkillZip(t, zipPath, map[string][]byte{
		"SKILL.md":        []byte("# 论文精读\n\n用于阅读和总结论文。\n"),
		"references/a.md": []byte("# 参考资料\n\n这是参考资料。\n"),
		"scripts/run.py":  []byte("print(\"hello skill\")\n"),
		"assets/logo.png": minimalPNGBytes(),
	})

	uploadStore := newFakeUploadStore()
	uploadStore.Put(UploadSession{
		UploadID:    "upload_skill_zip_001",
		OwnerUserID: "user_001",
		State:       "completed",
		StoredPath:  zipPath,
		Filename:    "skill.zip",
	})

	svc := NewSkillService(SkillServiceDeps{
		DB:          db,
		UploadStore: uploadStore,
		BlobStore:   NewBlobStore(db, NewLocalObjectStore(localBlobDir)),
		Clock:       fixedClock(),
	})

	resp, err := svc.CreateSkill(ctx, CreateSkillRequest{
		OwnerUserID:    "user_001",
		OwnerUserName:  "张三",
		CreateUserID:   "user_001",
		CreateUserName: "张三",
		Name:           "论文精读",
		Category:       "research",
		Description:    "用于阅读和总结论文的技能",
		Tags:           []string{"paper", "research"},
		AutoEvo:        false,
		IsEnabled:      boolPtr(true),
		Source: SourceInput{
			Type:     "uploaded_zip",
			UploadID: "upload_skill_zip_001",
			Filename: "skill.zip",
		},
	})
	if err != nil {
		t.Fatalf("CreateSkill returned error: %v", err)
	}
	if strings.TrimSpace(resp.SkillID) == "" {
		t.Fatal("CreateSkill returned empty skill id")
	}
	if strings.TrimSpace(resp.HeadRevisionID) == "" {
		t.Fatal("CreateSkill returned empty head revision id")
	}

	assertSkillMetadata(t, db, resp.SkillID, resp.HeadRevisionID)
	assertInitialRevision(t, db, resp.SkillID, resp.HeadRevisionID)
	assertRevisionEntries(t, db, resp.HeadRevisionID)
	assertDraftInitialized(t, db, resp.SkillID, resp.HeadRevisionID)
	assertNoDraftEntries(t, db, resp.SkillID)
	assertBlobRouting(t, db, resp.HeadRevisionID)
	assertTreeReadable(t, svc, resp.SkillID)
	assertTextAndBinaryReadBehavior(t, svc, resp.SkillID)
}

type fakeUploadStore struct {
	sessions map[string]UploadSession
}

func newFakeUploadStore() *fakeUploadStore {
	return &fakeUploadStore{sessions: map[string]UploadSession{}}
}

func (s *fakeUploadStore) Put(session UploadSession) {
	s.sessions[session.UploadID] = session
}

func (s *fakeUploadStore) Get(ctx context.Context, uploadID string) (UploadSession, error) {
	session, ok := s.sessions[uploadID]
	if !ok {
		return UploadSession{}, errors.New("upload session not found")
	}
	return session, nil
}

type testClock struct {
	now time.Time
}

func fixedClock() testClock {
	return testClock{now: time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)}
}

func (c testClock) Now() time.Time {
	return c.now
}

func boolPtr(v bool) *bool {
	return &v
}

func newSkillV2TestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "skillv2.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("connect sqlite test db: %v", err)
	}
	if err := db.AutoMigrate(
		&testSkillV2SkillRow{},
		&testSkillV2BlobRow{},
		&testSkillV2RevisionRow{},
		&testSkillV2RevisionEntryRow{},
		&testSkillV2DraftRow{},
		&testSkillV2DraftEntryRow{},
	); err != nil {
		t.Fatalf("auto migrate skill v2 test tables: %v", err)
	}
	return db
}

func writeSkillZip(t *testing.T, path string, files map[string][]byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create zip dir: %v", err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	defer writer.Close()

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", name, err)
		}
		if _, err := entry.Write(files[name]); err != nil {
			t.Fatalf("write zip entry %q: %v", name, err)
		}
	}
}

func minimalPNGBytes() []byte {
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

func assertSkillMetadata(t *testing.T, db *gorm.DB, skillID, headRevisionID string) {
	t.Helper()

	var row testSkillV2SkillRow
	if err := db.Where("id = ?", skillID).Take(&row).Error; err != nil {
		t.Fatalf("query created skill: %v", err)
	}
	if row.OwnerUserID != "user_001" {
		t.Fatalf("owner_user_id = %q, want user_001", row.OwnerUserID)
	}
	if row.OwnerUserName != "张三" {
		t.Fatalf("owner_user_name = %q, want 张三", row.OwnerUserName)
	}
	if row.CreateUserID != "user_001" {
		t.Fatalf("create_user_id = %q, want user_001", row.CreateUserID)
	}
	if row.CreateUserName != "张三" {
		t.Fatalf("create_user_name = %q, want 张三", row.CreateUserName)
	}
	if row.SkillName != "论文精读" {
		t.Fatalf("skill_name = %q, want 论文精读", row.SkillName)
	}
	if row.Category != "research" {
		t.Fatalf("category = %q, want research", row.Category)
	}
	if row.RelativeRoot != "research/论文精读" {
		t.Fatalf("relative_root = %q, want research/论文精读", row.RelativeRoot)
	}
	if row.SkillMDPath != "SKILL.md" {
		t.Fatalf("skill_md_path = %q, want SKILL.md", row.SkillMDPath)
	}
	if row.Description != "用于阅读和总结论文的技能" {
		t.Fatalf("description = %q, want 用于阅读和总结论文的技能", row.Description)
	}
	assertJSONTags(t, row.Tags, []string{"paper", "research"})
	if row.AutoEvo {
		t.Fatal("auto_evo = true, want false")
	}
	if !row.IsEnabled {
		t.Fatal("is_enabled = false, want true")
	}
	if row.HeadRevisionID == nil || *row.HeadRevisionID != headRevisionID {
		t.Fatalf("head_revision_id = %v, want %q", row.HeadRevisionID, headRevisionID)
	}
	assertNoLegacySkillColumns(t, db)
}

func assertInitialRevision(t *testing.T, db *gorm.DB, skillID, revisionID string) {
	t.Helper()

	var row testSkillV2RevisionRow
	if err := db.Where("id = ?", revisionID).Take(&row).Error; err != nil {
		t.Fatalf("query initial revision: %v", err)
	}
	if row.SkillID != skillID {
		t.Fatalf("revision skill_id = %q, want %q", row.SkillID, skillID)
	}
	if row.RevisionNo != 1 {
		t.Fatalf("revision_no = %d, want 1", row.RevisionNo)
	}
	if row.ParentRevisionID != nil {
		t.Fatalf("parent_revision_id = %v, want nil", *row.ParentRevisionID)
	}
	if row.TreeHash == "" {
		t.Fatal("tree_hash is empty")
	}
	if row.ChangeSource != "create" && row.ChangeSource != "direct_import" {
		t.Fatalf("change_source = %q, want create or direct_import", row.ChangeSource)
	}
}

func assertRevisionEntries(t *testing.T, db *gorm.DB, revisionID string) {
	t.Helper()

	entries := listRevisionEntries(t, db, revisionID)
	want := map[string]string{
		"SKILL.md":        "file",
		"references":      "dir",
		"references/a.md": "file",
		"scripts":         "dir",
		"scripts/run.py":  "file",
		"assets":          "dir",
		"assets/logo.png": "file",
	}
	if len(entries) != len(want) {
		t.Fatalf("revision entries count = %d, want %d: %#v", len(entries), len(want), entries)
	}
	for path, entryType := range want {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing revision entry %q", path)
		}
		if entry.EntryType != entryType {
			t.Fatalf("entry %q type = %q, want %q", path, entry.EntryType, entryType)
		}
		if entryType == "file" && (entry.BlobHash == nil || *entry.BlobHash == "") {
			t.Fatalf("file entry %q has empty blob_hash", path)
		}
		if entryType == "dir" && entry.BlobHash != nil {
			t.Fatalf("dir entry %q blob_hash = %q, want nil", path, *entry.BlobHash)
		}
	}
}

func assertDraftInitialized(t *testing.T, db *gorm.DB, skillID, baseRevisionID string) {
	t.Helper()

	var row testSkillV2DraftRow
	if err := db.Where("skill_id = ?", skillID).Take(&row).Error; err != nil {
		t.Fatalf("query initialized draft: %v", err)
	}
	if row.BaseRevisionID == nil || *row.BaseRevisionID != baseRevisionID {
		t.Fatalf("draft base_revision_id = %v, want %q", row.BaseRevisionID, baseRevisionID)
	}
	if row.Version != 1 {
		t.Fatalf("draft version = %d, want 1", row.Version)
	}
	if row.TaskID != "" {
		t.Fatalf("draft task_id = %q, want empty", row.TaskID)
	}
	if row.ConversationID != nil {
		t.Fatalf("draft conversation_id = %q, want nil", *row.ConversationID)
	}
}

func assertNoDraftEntries(t *testing.T, db *gorm.DB, skillID string) {
	t.Helper()

	if got := countRows(t, db, "skill_draft_entries", "skill_id = ?", skillID); got != 0 {
		t.Fatalf("skill_draft_entries count = %d, want 0", got)
	}
}

func assertBlobRouting(t *testing.T, db *gorm.DB, revisionID string) {
	t.Helper()

	skillBlob := getBlobByPath(t, db, revisionID, "SKILL.md")
	if skillBlob.Binary {
		t.Fatal("SKILL.md binary = true, want false")
	}
	if skillBlob.FileType != "markdown" {
		t.Fatalf("SKILL.md file_type = %q, want markdown", skillBlob.FileType)
	}
	if skillBlob.StorageBackend != "postgres" {
		t.Fatalf("SKILL.md storage_backend = %q, want postgres", skillBlob.StorageBackend)
	}
	if len(skillBlob.Content) == 0 {
		t.Fatal("SKILL.md content is empty")
	}
	if skillBlob.StorageKey != nil {
		t.Fatalf("SKILL.md storage_key = %q, want nil", *skillBlob.StorageKey)
	}

	refBlob := getBlobByPath(t, db, revisionID, "references/a.md")
	if refBlob.Binary || refBlob.FileType != "markdown" || refBlob.StorageBackend != "postgres" || len(refBlob.Content) == 0 || refBlob.StorageKey != nil {
		t.Fatalf("references/a.md blob routing invalid: %#v", refBlob)
	}

	scriptBlob := getBlobByPath(t, db, revisionID, "scripts/run.py")
	if scriptBlob.Binary || scriptBlob.FileType != "text" || scriptBlob.StorageBackend != "postgres" || len(scriptBlob.Content) == 0 || scriptBlob.StorageKey != nil {
		t.Fatalf("scripts/run.py blob routing invalid: %#v", scriptBlob)
	}

	logoBlob := getBlobByPath(t, db, revisionID, "assets/logo.png")
	if !logoBlob.Binary {
		t.Fatal("assets/logo.png binary = false, want true")
	}
	if logoBlob.FileType != "image" {
		t.Fatalf("assets/logo.png file_type = %q, want image", logoBlob.FileType)
	}
	if logoBlob.StorageBackend == "postgres" {
		t.Fatal("assets/logo.png storage_backend = postgres, want local_file or s3")
	}
	if logoBlob.StorageBackend != "local_file" && logoBlob.StorageBackend != "s3" {
		t.Fatalf("assets/logo.png storage_backend = %q, want local_file or s3", logoBlob.StorageBackend)
	}
	if len(logoBlob.Content) != 0 {
		t.Fatal("assets/logo.png content persisted in PG, want nil/empty")
	}
	if logoBlob.StorageKey == nil || strings.TrimSpace(*logoBlob.StorageKey) == "" {
		t.Fatal("assets/logo.png storage_key is empty")
	}
}

func assertTreeReadable(t *testing.T, svc *SkillService, skillID string) {
	t.Helper()

	tree, err := svc.GetTree(context.Background(), TreeRef{
		SkillID: skillID,
		RefType: "head",
	})
	if err != nil {
		t.Fatalf("GetTree returned error: %v", err)
	}

	nodes := map[string]TreeNode{}
	collectTreeNodes(nodes, tree.Children)
	for _, path := range []string{"SKILL.md", "references", "references/a.md", "scripts", "scripts/run.py", "assets", "assets/logo.png"} {
		if _, ok := nodes[path]; !ok {
			t.Fatalf("tree missing path %q", path)
		}
	}

	logo := nodes["assets/logo.png"]
	if logo.Type != "file" {
		t.Fatalf("assets/logo.png tree type = %q, want file", logo.Type)
	}
	if logo.Mime != "image/png" {
		t.Fatalf("assets/logo.png mime = %q, want image/png", logo.Mime)
	}
	if logo.FileType != "image" {
		t.Fatalf("assets/logo.png file_type = %q, want image", logo.FileType)
	}
	if !logo.Binary {
		t.Fatal("assets/logo.png binary = false, want true")
	}
	if strings.TrimSpace(logo.BlobHash) == "" {
		t.Fatal("assets/logo.png blob_hash is empty")
	}
}

func assertTextAndBinaryReadBehavior(t *testing.T, svc *SkillService, skillID string) {
	t.Helper()

	textFile, err := svc.ReadFile(context.Background(), FileRef{
		SkillID: skillID,
		RefType: "head",
		Path:    "SKILL.md",
	})
	if err != nil {
		t.Fatalf("ReadFile SKILL.md returned error: %v", err)
	}
	if textFile.Binary {
		t.Fatal("SKILL.md Binary = true, want false")
	}
	if !strings.Contains(textFile.Content, "# 论文精读") {
		t.Fatalf("SKILL.md content = %q, want it to contain heading", textFile.Content)
	}
	if textFile.DownloadURL != "" {
		t.Fatalf("SKILL.md download_url = %q, want empty", textFile.DownloadURL)
	}

	imageFile, err := svc.ReadFile(context.Background(), FileRef{
		SkillID: skillID,
		RefType: "head",
		Path:    "assets/logo.png",
	})
	if err != nil {
		t.Fatalf("ReadFile assets/logo.png returned error: %v", err)
	}
	if !imageFile.Binary {
		t.Fatal("assets/logo.png Binary = false, want true")
	}
	if imageFile.Content != "" {
		t.Fatalf("assets/logo.png content = %q, want empty", imageFile.Content)
	}
	if imageFile.DownloadURL == "" {
		t.Fatal("assets/logo.png download_url is empty")
	}
	if imageFile.StorageKey != "" {
		t.Fatalf("assets/logo.png storage_key = %q, want empty", imageFile.StorageKey)
	}
}

func collectTreeNodes(out map[string]TreeNode, nodes []TreeNode) {
	for _, node := range nodes {
		out[node.Path] = node
		collectTreeNodes(out, node.Children)
	}
}

func listRevisionEntries(t *testing.T, db *gorm.DB, revisionID string) map[string]testSkillV2RevisionEntryRow {
	t.Helper()

	var rows []testSkillV2RevisionEntryRow
	if err := db.Where("revision_id = ?", revisionID).Find(&rows).Error; err != nil {
		t.Fatalf("query revision entries: %v", err)
	}
	out := make(map[string]testSkillV2RevisionEntryRow, len(rows))
	for _, row := range rows {
		out[row.Path] = row
	}
	return out
}

func getBlobByPath(t *testing.T, db *gorm.DB, revisionID, path string) testSkillV2BlobRow {
	t.Helper()

	var entry testSkillV2RevisionEntryRow
	if err := db.Where("revision_id = ? AND path = ?", revisionID, path).Take(&entry).Error; err != nil {
		t.Fatalf("query revision entry %q: %v", path, err)
	}
	if entry.BlobHash == nil || *entry.BlobHash == "" {
		t.Fatalf("revision entry %q has no blob hash", path)
	}

	var blob testSkillV2BlobRow
	if err := db.Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
		t.Fatalf("query blob for %q hash %q: %v", path, *entry.BlobHash, err)
	}
	return blob
}

func countRows(t *testing.T, db *gorm.DB, table, where string, args ...any) int64 {
	t.Helper()

	var count int64
	query := db.Table(table)
	if strings.TrimSpace(where) != "" {
		query = query.Where(where, args...)
	}
	if err := query.Count(&count).Error; err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	return count
}

func assertJSONTags(t *testing.T, raw []byte, want []string) {
	t.Helper()

	var got []string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode skill tags: %v; raw=%s", err, string(raw))
	}
	if len(got) != len(want) {
		t.Fatalf("tags = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags = %#v, want %#v", got, want)
		}
	}
}

func assertNoLegacySkillColumns(t *testing.T, db *gorm.DB) {
	t.Helper()

	type sqliteColumn struct {
		Name string `gorm:"column:name"`
	}
	var columns []sqliteColumn
	if err := db.Raw("PRAGMA table_info(skills)").Scan(&columns).Error; err != nil {
		t.Fatalf("inspect skills columns: %v", err)
	}
	seen := map[string]bool{}
	for _, column := range columns {
		seen[column.Name] = true
	}
	for _, legacy := range []string{"content", "draft_content", "node_type", "parent_skill_id", "parent_skill_name"} {
		if seen[legacy] {
			t.Fatalf("skills table contains legacy column %q", legacy)
		}
	}
}

type testSkillV2SkillRow struct {
	ID                    string     `gorm:"column:id;type:varchar(36);primaryKey"`
	OwnerUserID           string     `gorm:"column:owner_user_id;type:text;not null"`
	OwnerUserName         string     `gorm:"column:owner_user_name;type:text;not null;default:''"`
	CreateUserID          string     `gorm:"column:create_user_id;type:text;not null"`
	CreateUserName        string     `gorm:"column:create_user_name;type:text;not null;default:''"`
	Category              string     `gorm:"column:category;type:text;not null"`
	SkillName             string     `gorm:"column:skill_name;type:text;not null"`
	OriginBuiltinSkillUID string     `gorm:"column:origin_builtin_skill_uid;type:text;not null;default:''"`
	Description           string     `gorm:"column:description;type:text"`
	Tags                  []byte     `gorm:"column:tags;type:json"`
	RelativeRoot          string     `gorm:"column:relative_root;type:text;not null"`
	SkillMDPath           string     `gorm:"column:skill_md_path;type:text;not null;default:'SKILL.md'"`
	HeadRevisionID        *string    `gorm:"column:head_revision_id;type:varchar(36)"`
	Version               int64      `gorm:"column:version;not null;default:1"`
	AutoEvo               bool       `gorm:"column:auto_evo;not null;default:false"`
	AutoEvoApplyStatus    string     `gorm:"column:auto_evo_apply_status;type:text;not null;default:'idle'"`
	AutoEvoGeneration     int64      `gorm:"column:auto_evo_generation;not null;default:0"`
	AutoEvoStartedAt      *time.Time `gorm:"column:auto_evo_started_at"`
	AutoEvoFinishedAt     *time.Time `gorm:"column:auto_evo_finished_at"`
	AutoEvoError          string     `gorm:"column:auto_evo_error;type:text;not null;default:''"`
	IsEnabled             bool       `gorm:"column:is_enabled;not null;default:true"`
	UpdateStatus          string     `gorm:"column:update_status;type:text;not null;default:'up_to_date'"`
	Ext                   []byte     `gorm:"column:ext;type:json"`
	DeletedAt             *time.Time `gorm:"column:deleted_at"`
	DeletedBy             *string    `gorm:"column:deleted_by;type:text"`
	CreatedAt             time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt             time.Time  `gorm:"column:updated_at;not null"`
}

func (testSkillV2SkillRow) TableName() string {
	return "skills"
}

type testSkillV2BlobRow struct {
	Hash           string    `gorm:"column:hash;type:text;primaryKey"`
	Size           int64     `gorm:"column:size;not null"`
	Mime           string    `gorm:"column:mime;type:text"`
	FileType       string    `gorm:"column:file_type;type:text;not null;default:'unknown'"`
	Binary         bool      `gorm:"column:binary;not null;default:false"`
	StorageBackend string    `gorm:"column:storage_backend;type:text;not null"`
	StorageKey     *string   `gorm:"column:storage_key;type:text"`
	Content        []byte    `gorm:"column:content;type:blob"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
}

func (testSkillV2BlobRow) TableName() string {
	return "skill_blobs"
}

type testSkillV2RevisionRow struct {
	ID               string    `gorm:"column:id;type:varchar(36);primaryKey"`
	SkillID          string    `gorm:"column:skill_id;type:varchar(36);not null"`
	ParentRevisionID *string   `gorm:"column:parent_revision_id;type:varchar(36)"`
	RevisionNo       int64     `gorm:"column:revision_no;not null"`
	TreeHash         string    `gorm:"column:tree_hash;type:text;not null"`
	Message          string    `gorm:"column:message;type:text"`
	ChangeSource     string    `gorm:"column:change_source;type:text;not null;default:'draft_commit'"`
	SourceRefType    string    `gorm:"column:source_ref_type;type:text;not null;default:''"`
	SourceRefID      string    `gorm:"column:source_ref_id;type:text;not null;default:''"`
	CreatedBy        *string   `gorm:"column:created_by;type:varchar(36)"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
}

func (testSkillV2RevisionRow) TableName() string {
	return "skill_revisions"
}

type testSkillV2RevisionEntryRow struct {
	RevisionID string  `gorm:"column:revision_id;type:varchar(36);primaryKey"`
	Path       string  `gorm:"column:path;type:text;primaryKey"`
	EntryType  string  `gorm:"column:entry_type;type:text;not null"`
	BlobHash   *string `gorm:"column:blob_hash;type:text"`
	Size       int64   `gorm:"column:size"`
	Mime       string  `gorm:"column:mime;type:text"`
	FileType   string  `gorm:"column:file_type;type:text;not null;default:'unknown'"`
	Binary     bool    `gorm:"column:binary;not null;default:false"`
	Mode       int     `gorm:"column:mode;not null;default:420"`
}

func (testSkillV2RevisionEntryRow) TableName() string {
	return "skill_revision_entries"
}

type testSkillV2DraftRow struct {
	SkillID        string     `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	BaseRevisionID *string    `gorm:"column:base_revision_id;type:varchar(36)"`
	DraftStatus    string     `gorm:"column:draft_status;type:text;not null;default:''"`
	DraftUpdatedAt *time.Time `gorm:"column:draft_updated_at"`
	TaskID         string     `gorm:"column:task_id;type:text;not null;default:''"`
	ConversationID *string    `gorm:"column:conversation_id;type:varchar(36)"`
	UpdatedBy      *string    `gorm:"column:updated_by;type:varchar(36)"`
	Version        int64      `gorm:"column:version;not null;default:1"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null"`
}

func (testSkillV2DraftRow) TableName() string {
	return "skill_drafts"
}

type testSkillV2DraftEntryRow struct {
	SkillID   string    `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	Path      string    `gorm:"column:path;type:text;primaryKey"`
	Op        string    `gorm:"column:op;type:text;not null"`
	EntryType string    `gorm:"column:entry_type;type:text"`
	BlobHash  *string   `gorm:"column:blob_hash;type:text"`
	Size      int64     `gorm:"column:size"`
	Mime      string    `gorm:"column:mime;type:text"`
	FileType  string    `gorm:"column:file_type;type:text"`
	Binary    bool      `gorm:"column:binary"`
	Mode      int       `gorm:"column:mode"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (testSkillV2DraftEntryRow) TableName() string {
	return "skill_draft_entries"
}

func (r testSkillV2BlobRow) GoString() string {
	return fmt.Sprintf(
		"testSkillV2BlobRow{Hash:%q Size:%d Mime:%q FileType:%q Binary:%v StorageBackend:%q StorageKey:%v ContentLen:%d}",
		r.Hash,
		r.Size,
		r.Mime,
		r.FileType,
		r.Binary,
		r.StorageBackend,
		r.StorageKey,
		len(r.Content),
	)
}
