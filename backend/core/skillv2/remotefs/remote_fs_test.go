package remotefs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	skillmetadata "lazymind/core/skillv2/metadata"
	"lazymind/core/skillv2/testutil"
)

func TestRemoteFSExternalSkillMDReturnsStrictRuntimeViewWithoutChangingBlob(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	original := []byte("---\nversion: 1.0.0\n---\n# Runtime skill\n\nUseful runtime description.\n")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").Updates(map[string]any{
		"category":      skillmetadata.ExternalCategory,
		"skill_name":    "runtime-skill",
		"description":   "Useful runtime description.",
		"relative_root": skillmetadata.ExternalCategory + "/runtime-skill",
	}).Error; err != nil {
		t.Fatalf("configure external skill: %v", err)
	}
	if err := db.Model(&testutil.SkillBlobRow{}).Where("hash = ?", "h_skill_rev1").Updates(map[string]any{
		"content": original,
		"size":    len(original),
	}).Error; err != nil {
		t.Fatalf("replace original SKILL.md blob: %v", err)
	}
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	rec := httptest.NewRecorder()
	handler.Content(rec, httptest.NewRequest(http.MethodGet, remoteContentURL("skills/external/runtime-skill/SKILL.md", "user_001", "task1", ""), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("content status=%d body=%s", rec.Code, rec.Body.String())
	}
	meta, err := skillmetadata.ParseRequired(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("runtime SKILL.md is not strictly valid: %v", err)
	}
	if meta.Name != "runtime-skill" || meta.Description != "Useful runtime description." || !strings.Contains(rec.Body.String(), "# Runtime skill") {
		t.Fatalf("runtime SKILL.md = %q", rec.Body.String())
	}
	var blob testutil.SkillBlobRow
	if err := db.Where("hash = ?", "h_skill_rev1").Take(&blob).Error; err != nil {
		t.Fatalf("query original blob: %v", err)
	}
	if !bytes.Equal(blob.Content, original) {
		t.Fatalf("stored SKILL.md changed: %q", blob.Content)
	}
}

func TestRemoteFSWriteText_IsVisibleInSameTask(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	writeReq := httptest.NewRequest(http.MethodPut, remoteContentURL("skills/research/论文精读/references/b.md", "user_001", "task1", ""), strings.NewReader("# B\n"))
	writeRec := httptest.NewRecorder()
	handler.Content(writeRec, writeReq)
	if writeRec.Code != http.StatusOK {
		t.Fatalf("write status = %d, want 200 body=%s", writeRec.Code, writeRec.Body.String())
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ? AND path = ? AND op = ?", "skill1", "references/b.md", "upsert"); got != 1 {
		t.Fatalf("draft upsert count = %d, want 1", got)
	}
	var draft testutil.SkillDraftRow
	if err := db.Where("skill_id = ?", "skill1").Take(&draft).Error; err != nil {
		t.Fatalf("query draft: %v", err)
	}
	if draft.TaskID != "task1" {
		t.Fatalf("draft task_id = %q, want task1", draft.TaskID)
	}

	contentRec := httptest.NewRecorder()
	handler.Content(contentRec, httptest.NewRequest(http.MethodGet, remoteContentURL("skills/research/论文精读/references/b.md", "user_001", "task1", ""), nil))
	if contentRec.Code != http.StatusOK || !strings.Contains(contentRec.Body.String(), "# B") {
		t.Fatalf("same task content status=%d body=%s", contentRec.Code, contentRec.Body.String())
	}
	listRec := httptest.NewRecorder()
	handler.List(listRec, httptest.NewRequest(http.MethodGet, remoteListURL("skills/research/论文精读/references", "user_001", "task1"), nil))
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), "b.md") {
		t.Fatalf("same task list status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	testutil.AssertHeadRevision(t, db, "skill1", "rev1")
}

func TestRemoteFSWriteBinary_SupportsRawAndBase64Read(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	data := testutil.MinimalPNGBytes()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, remoteContentURL("skills/research/论文精读/assets/logo.png", "user_001", "task1", ""), bytes.NewReader(data))
	handler.Content(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("binary write status = %d body=%s", rec.Code, rec.Body.String())
	}
	var blob testutil.SkillBlobRow
	if err := db.Where("binary = ? AND file_type = ?", true, "image").Take(&blob).Error; err != nil {
		t.Fatalf("query binary blob: %v", err)
	}
	if blob.StorageBackend == "postgres" || len(blob.Content) != 0 || blob.StorageKey == nil {
		t.Fatalf("binary blob stored in PG or without storage key: %#v", blob)
	}
	assertBlobStorageState(t, db, blob.Hash, wantBlobStorage{
		Binary:         true,
		StorageBackend: "local_file",
		ContentIsNull:  true,
		StorageKeySet:  true,
		FileType:       "image",
		Mime:           "image/png",
	})

	rawRec := httptest.NewRecorder()
	handler.Content(rawRec, httptest.NewRequest(http.MethodGet, remoteContentURL("skills/research/论文精读/assets/logo.png", "user_001", "task1", "raw"), nil))
	if rawRec.Code != http.StatusOK || !bytes.Equal(rawRec.Body.Bytes(), data) {
		t.Fatalf("raw read status=%d len=%d", rawRec.Code, rawRec.Body.Len())
	}
	base64Rec := httptest.NewRecorder()
	handler.Content(base64Rec, httptest.NewRequest(http.MethodGet, remoteContentURL("skills/research/论文精读/assets/logo.png", "user_001", "task1", "base64"), nil))
	if base64Rec.Code != http.StatusOK || !strings.Contains(base64Rec.Body.String(), base64.StdEncoding.EncodeToString(data)) {
		t.Fatalf("base64 read status=%d body=%s", base64Rec.Code, base64Rec.Body.String())
	}
}

func TestRemoteFSWriteTextThenBinaryStoresCurrentBlobExternally(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	path := "skills/research/论文精读/references/switch.bin"
	text := []byte("plain text first\n")
	binary := testutil.MinimalPNGBytes()

	writeRemoteContent(t, handler, path, "user_001", "task1", text)
	textHash := currentDraftBlobHash(t, db, "skill1", "references/switch.bin")
	assertBlobStorageState(t, db, textHash, wantBlobStorage{
		Binary:         false,
		StorageBackend: "postgres",
		ContentIsNull:  false,
		ContentLen:     int64(len(text)),
		StorageKeySet:  false,
		FileType:       "text",
		Mime:           "text/plain",
	})

	writeRemoteContent(t, handler, path, "user_001", "task1", binary)
	binaryHash := currentDraftBlobHash(t, db, "skill1", "references/switch.bin")
	if binaryHash == textHash {
		t.Fatalf("binary rewrite kept blob hash %q, want new content hash", binaryHash)
	}
	assertBlobStorageState(t, db, binaryHash, wantBlobStorage{
		Binary:         true,
		StorageBackend: "local_file",
		ContentIsNull:  true,
		StorageKeySet:  true,
		FileType:       "binary",
		Mime:           "application/octet-stream",
	})

	rawRec := httptest.NewRecorder()
	handler.Content(rawRec, httptest.NewRequest(http.MethodGet, remoteContentURL(path, "user_001", "task1", "raw"), nil))
	if rawRec.Code != http.StatusOK || !bytes.Equal(rawRec.Body.Bytes(), binary) {
		t.Fatalf("raw binary read status=%d len=%d", rawRec.Code, rawRec.Body.Len())
	}
}

func TestRemoteFSWriteBinaryThenTextStoresCurrentBlobInPostgres(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	path := "skills/research/论文精读/references/switch.bin"
	binary := testutil.MinimalPNGBytes()
	text := []byte("plain text second\n")

	writeRemoteContent(t, handler, path, "user_001", "task1", binary)
	binaryHash := currentDraftBlobHash(t, db, "skill1", "references/switch.bin")
	assertBlobStorageState(t, db, binaryHash, wantBlobStorage{
		Binary:         true,
		StorageBackend: "local_file",
		ContentIsNull:  true,
		StorageKeySet:  true,
		FileType:       "binary",
		Mime:           "application/octet-stream",
	})

	writeRemoteContent(t, handler, path, "user_001", "task1", text)
	textHash := currentDraftBlobHash(t, db, "skill1", "references/switch.bin")
	if textHash == binaryHash {
		t.Fatalf("text rewrite kept blob hash %q, want new content hash", textHash)
	}
	assertBlobStorageState(t, db, textHash, wantBlobStorage{
		Binary:         false,
		StorageBackend: "postgres",
		ContentIsNull:  false,
		ContentLen:     int64(len(text)),
		StorageKeySet:  false,
		FileType:       "text",
		Mime:           "text/plain",
	})

	rawRec := httptest.NewRecorder()
	handler.Content(rawRec, httptest.NewRequest(http.MethodGet, remoteContentURL(path, "user_001", "task1", "raw"), nil))
	if rawRec.Code != http.StatusOK || !bytes.Equal(rawRec.Body.Bytes(), text) {
		t.Fatalf("raw text read status=%d body=%q", rawRec.Code, rawRec.Body.String())
	}
}

func TestBlobDataReadsLocalObjectFileURI(t *testing.T) {
	objects := NewLocalObjectStore(t.TempDir())
	blobs := NewBlobStore(nil, objects)
	handler := NewHandler(HandlerDeps{BlobStore: blobs})
	key := "skillv2/ab/blob"
	want := []byte("binary skill resource")
	if err := objects.service.Put(context.Background(), key, want); err != nil {
		t.Fatalf("put local object: %v", err)
	}

	got, err := handler.blobData(skillBlobRow{Binary: true, StorageKey: &key})
	if err != nil {
		t.Fatalf("read local object: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("read local object = %q, want %q", got, want)
	}
}

func TestRemoteFSWrite_RejectsDifferentTaskWhenDraftExists(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Update("task_id", "task1").Error; err != nil {
		t.Fatalf("seed task_id: %v", err)
	}
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_draft")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, remoteContentURL("skills/research/论文精读/references/b.md", "user_001", "task2", ""), strings.NewReader("# B\n"))
	handler.Content(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 body=%s", rec.Code, rec.Body.String())
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("draft entry count = %d, want 1", got)
	}
}

func writeRemoteContent(t *testing.T, handler *Handler, remotePath, userID, taskID string, data []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.Content(rec, httptest.NewRequest(http.MethodPut, remoteContentURL(remotePath, userID, taskID, ""), bytes.NewReader(data)))
	if rec.Code != http.StatusOK {
		t.Fatalf("write %s status=%d body=%s", remotePath, rec.Code, rec.Body.String())
	}
}

func currentDraftBlobHash(t *testing.T, db *testutil.TestDB, skillID, relPath string) string {
	t.Helper()
	var row struct {
		BlobHash string `gorm:"column:blob_hash"`
	}
	if err := db.Table("skill_draft_entries").
		Select("blob_hash").
		Where("skill_id = ? AND path = ? AND op = ?", skillID, relPath, "upsert").
		Take(&row).Error; err != nil {
		t.Fatalf("query current draft blob hash: %v", err)
	}
	return row.BlobHash
}

type wantBlobStorage struct {
	Binary         bool
	StorageBackend string
	ContentIsNull  bool
	ContentLen     int64
	StorageKeySet  bool
	FileType       string
	Mime           string
}

func assertBlobStorageState(t *testing.T, db *testutil.TestDB, hash string, want wantBlobStorage) {
	t.Helper()
	var got struct {
		Hash           string         `gorm:"column:hash"`
		Binary         bool           `gorm:"column:binary"`
		StorageBackend string         `gorm:"column:storage_backend"`
		StorageKey     sql.NullString `gorm:"column:storage_key"`
		ContentIsNull  bool           `gorm:"column:content_is_null"`
		ContentLen     sql.NullInt64  `gorm:"column:content_len"`
		FileType       string         `gorm:"column:file_type"`
		Mime           string         `gorm:"column:mime"`
	}
	if err := db.Raw(`
		SELECT
			hash,
			binary,
			storage_backend,
			storage_key,
			content IS NULL AS content_is_null,
			length(content) AS content_len,
			file_type,
			mime
		FROM skill_blobs
		WHERE hash = ?
	`, hash).Scan(&got).Error; err != nil {
		t.Fatalf("query blob storage state: %v", err)
	}
	if got.Hash == "" {
		t.Fatalf("blob %q not found", hash)
	}
	if got.Binary != want.Binary ||
		got.StorageBackend != want.StorageBackend ||
		got.ContentIsNull != want.ContentIsNull ||
		got.StorageKey.Valid != want.StorageKeySet ||
		got.FileType != want.FileType ||
		got.Mime != want.Mime {
		t.Fatalf("blob state = %#v, want %#v", got, want)
	}
	if want.StorageKeySet && got.StorageKey.String == "" {
		t.Fatalf("storage_key is empty, want non-empty")
	}
	if !want.ContentIsNull {
		if !got.ContentLen.Valid || got.ContentLen.Int64 != want.ContentLen {
			t.Fatalf("content length = %#v, want %d", got.ContentLen, want.ContentLen)
		}
	}
	if want.ContentIsNull && got.ContentLen.Valid {
		t.Fatalf("content length valid for SQL NULL content: %#v", got.ContentLen)
	}
}

func TestRemoteFSDeletePath_UpdatesTaskView(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "h_a", "# A\n")
	testutil.SeedRevisionEntry(t, db, "rev1", "references", "dir", "", "directory")
	testutil.SeedRevisionEntry(t, db, "rev1", "references/a.md", "file", "h_a", "markdown")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	rec := httptest.NewRecorder()
	handler.DeletePath(rec, httptest.NewRequest(http.MethodDelete, remotePathURL("skills/research/论文精读/references/a.md", "user_001", "task1"), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}
	existsRec := httptest.NewRecorder()
	handler.Exists(existsRec, httptest.NewRequest(http.MethodGet, remoteExistsURL("skills/research/论文精读/references/a.md", "user_001", "task1"), nil))
	if existsRec.Code != http.StatusOK || strings.Contains(existsRec.Body.String(), `"exists":true`) {
		t.Fatalf("exists response after delete status=%d body=%s", existsRec.Code, existsRec.Body.String())
	}
}

func TestRemoteFSMovePath_UpdatesTaskViewAndKeepsBlobHash(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "h1", "# old\n")
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Update("task_id", "task1").Error; err != nil {
		t.Fatalf("seed task_id: %v", err)
	}
	testutil.SeedDraftEntry(t, db, "skill1", "references/old.md", "upsert", "file", "h1")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	body, _ := json.Marshal(map[string]string{
		"from": "skills/research/论文精读/references/old.md",
		"to":   "skills/research/论文精读/references/new.md",
	})
	rec := httptest.NewRecorder()
	handler.Move(rec, httptest.NewRequest(http.MethodPost, remoteMoveURL("user_001", "task1"), bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("move status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ? AND path = ?", "skill1", "references/old.md"); got != 0 {
		t.Fatalf("old path overlay count = %d, want 0", got)
	}
	var entry testutil.SkillDraftEntryRow
	if err := db.Where("skill_id = ? AND path = ?", "skill1", "references/new.md").Take(&entry).Error; err != nil {
		t.Fatalf("query new draft entry: %v", err)
	}
	if entry.BlobHash == nil || *entry.BlobHash != "h1" {
		t.Fatalf("new path blob_hash = %v, want h1", entry.BlobHash)
	}
}

func TestRemoteFS_DoesNotApplySkillBusinessRules(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	for _, path := range []string{
		"skills/research/论文精读/SKILL.md",
		"skills/research/论文精读/scripts/freeform.txt",
		"skills/research/论文精读/references.bin",
		"skills/research/论文精读/assets.txt",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, remoteContentURL(path, "user_001", "task1", ""), strings.NewReader("remote-fs content"))
		handler.Content(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("RemoteFS applied business rule to %q: status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

func remoteContentURL(path, userID, taskID, encoding string) string {
	values := url.Values{"path": {path}, "user_id": {userID}, "task_id": {taskID}}
	if encoding != "" {
		values.Set("encoding", encoding)
	}
	return "/remote-fs/content?" + values.Encode()
}

func remoteListURL(path, userID, taskID string) string {
	return "/remote-fs/list?" + url.Values{"path": {path}, "user_id": {userID}, "task_id": {taskID}}.Encode()
}

func remoteExistsURL(path, userID, taskID string) string {
	return "/remote-fs/exists?" + url.Values{"path": {path}, "user_id": {userID}, "task_id": {taskID}}.Encode()
}

func remotePathURL(path, userID, taskID string) string {
	return "/remote-fs/path?" + url.Values{"path": {path}, "user_id": {userID}, "task_id": {taskID}}.Encode()
}

func remoteMoveURL(userID, taskID string) string {
	return "/remote-fs/move?" + url.Values{"user_id": {userID}, "task_id": {taskID}}.Encode()
}
