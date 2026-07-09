package remotefs

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"lazymind/core/skillv2/testutil"
)

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
