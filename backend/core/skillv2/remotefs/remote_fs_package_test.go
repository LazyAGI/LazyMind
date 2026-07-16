package remotefs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestRemoteFSDir_CreatesDraftPackage(t *testing.T) {
	db := testutil.NewTestDB(t)
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	body, _ := json.Marshal(map[string]any{"path": "skills/research/new-skill", "recursive": true})
	rec := httptest.NewRecorder()
	handler.Dir(rec, httptest.NewRequest(http.MethodPost, remoteDirURL("user_001", "task1"), bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("mkdir package status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := testutil.CountRows(t, db, "skills", "owner_user_id = ? AND relative_root = ? AND deleted_at IS NULL", "user_001", "research/new-skill"); got != 1 {
		t.Fatalf("active skill count = %d, want 1", got)
	}
	var skill testutil.SkillRow
	if err := db.Where("relative_root = ?", "research/new-skill").Take(&skill).Error; err != nil {
		t.Fatalf("query created skill: %v", err)
	}
	if skill.IsEnabled {
		t.Fatal("remote-fs empty package is_enabled = true, want false")
	}
	if skill.HeadRevisionID != nil {
		t.Fatalf("remote-fs draft package head_revision_id = %v, want nil", skill.HeadRevisionID)
	}
	if got := testutil.CountRows(t, db, "skill_revisions", "skill_id = ?", skill.ID); got != 0 {
		t.Fatalf("draft package revision count = %d, want 0", got)
	}
	if got := testutil.CountRows(t, db, "skill_drafts", "skill_id = ?", skill.ID); got != 1 {
		t.Fatalf("draft count = %d, want 1", got)
	}
	var draft testutil.SkillDraftRow
	if err := db.Where("skill_id = ?", skill.ID).Take(&draft).Error; err != nil {
		t.Fatalf("query created draft: %v", err)
	}
	if draft.BaseRevisionID != nil || draft.TaskID != "task1" || draft.DraftStatus != "pending_confirm" {
		t.Fatalf("created draft = %#v, want create draft without base", draft)
	}

	list := httptest.NewRecorder()
	handler.List(list, httptest.NewRequest(http.MethodGet, remoteListURL("skills/research", "user_001", "task1"), nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "new-skill") {
		t.Fatalf("list category status=%d body=%s", list.Code, list.Body.String())
	}
}

func TestRemoteFSDir_AutoModeMarksDraftForAutoCommit(t *testing.T) {
	db := testutil.NewTestDB(t)
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	body, _ := json.Marshal(map[string]any{"path": "skills/research/auto-skill", "recursive": true})
	rec := httptest.NewRecorder()
	handler.Dir(rec, httptest.NewRequest(http.MethodPost, remoteDirURL("user_001", "session-auto")+"&mode=auto", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("mkdir auto package status=%d body=%s", rec.Code, rec.Body.String())
	}
	var skill testutil.SkillRow
	if err := db.Where("relative_root = ?", "research/auto-skill").Take(&skill).Error; err != nil {
		t.Fatalf("query auto skill: %v", err)
	}
	if !skill.AutoEvo || skill.HeadRevisionID != nil {
		t.Fatalf("auto skill = %#v, want auto_evo draft without head", skill)
	}
	var draft testutil.SkillDraftRow
	if err := db.Where("skill_id = ?", skill.ID).Take(&draft).Error; err != nil {
		t.Fatalf("query auto draft: %v", err)
	}
	if draft.DraftStatus != "auto_pending" || draft.TaskID != "session-auto" {
		t.Fatalf("auto draft = %#v", draft)
	}
}

func TestRemoteFSNewPackage_FirstCommitCreatesRevisionOne(t *testing.T) {
	db := testutil.NewTestDB(t)
	objects := NewLocalObjectStore(t.TempDir())
	blobs := NewBlobStore(db.DB, objects)
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: blobs})
	committer := NewCommitter(CommitterDeps{DB: db.DB, BlobStore: blobs})

	createPackage(t, handler, "skills/research/new-skill")
	write := httptest.NewRecorder()
	handler.Content(write, httptest.NewRequest(
		http.MethodPut,
		remoteContentURL("skills/research/new-skill/SKILL.md", "user_001", "task-mkdir", ""),
		strings.NewReader("# New Skill\n"),
	))
	if write.Code != http.StatusOK {
		t.Fatalf("write SKILL.md status=%d body=%s", write.Code, write.Body.String())
	}

	var skill testutil.SkillRow
	if err := db.Where("relative_root = ?", "research/new-skill").Take(&skill).Error; err != nil {
		t.Fatalf("query created skill: %v", err)
	}
	if got := testutil.CountRows(t, db, "skill_revisions", "skill_id = ?", skill.ID); got != 0 {
		t.Fatalf("revision count before commit = %d, want 0", got)
	}
	var draft testutil.SkillDraftRow
	if err := db.Where("skill_id = ?", skill.ID).Take(&draft).Error; err != nil {
		t.Fatalf("query draft: %v", err)
	}

	resp, err := committer.CommitDraft(context.Background(), CommitDraftRequest{
		SkillID:      skill.ID,
		UserID:       "user_001",
		DraftVersion: draft.Version,
	})
	if err != nil {
		t.Fatalf("commit initial draft: %v", err)
	}
	if resp.RevisionNo != 1 {
		t.Fatalf("revision_no = %d, want 1", resp.RevisionNo)
	}
	testutil.AssertHeadRevision(t, db, skill.ID, resp.RevisionID)
	if got := testutil.CountRows(t, db, "skill_revisions", "skill_id = ? AND parent_revision_id IS NULL", skill.ID); got != 1 {
		t.Fatalf("initial revision count = %d, want 1", got)
	}
	testutil.AssertRevisionEntries(t, db, resp.RevisionID, []testutil.ExpectedEntry{
		{Path: "SKILL.md", EntryType: "file", FileType: "markdown", HasBlob: true},
	})
	testutil.AssertNoDraftEntries(t, db, skill.ID)
}

func TestRemoteFSDirAndWrite_MaterializeParents(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	body, _ := json.Marshal(map[string]any{"path": "skills/research/论文精读/a/b/c", "recursive": true})
	dirRec := httptest.NewRecorder()
	handler.Dir(dirRec, httptest.NewRequest(http.MethodPost, remoteDirURL("user_001", "task1"), bytes.NewReader(body)))
	if dirRec.Code != http.StatusOK {
		t.Fatalf("mkdir status=%d body=%s", dirRec.Code, dirRec.Body.String())
	}
	for _, p := range []string{"a", "a/b", "a/b/c"} {
		if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ? AND path = ? AND entry_type = ?", "skill1", p, "dir"); got != 1 {
			t.Fatalf("dir %q draft count = %d, want 1", p, got)
		}
	}

	write := httptest.NewRecorder()
	handler.Content(write, httptest.NewRequest(http.MethodPut, remoteContentURL("skills/research/论文精读/x/y/file.md", "user_001", "task1", ""), strings.NewReader("# file\n")))
	if write.Code != http.StatusOK {
		t.Fatalf("write status=%d body=%s", write.Code, write.Body.String())
	}
	for _, p := range []string{"x", "x/y"} {
		if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ? AND path = ? AND entry_type = ?", "skill1", p, "dir"); got != 1 {
			t.Fatalf("auto dir %q draft count = %d, want 1", p, got)
		}
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ? AND path = ? AND entry_type = ?", "skill1", "x/y/file.md", "file"); got != 1 {
		t.Fatalf("written file draft count = %d, want 1", got)
	}
}

func TestRemoteFSCopy_ReusesBlobButEntriesAreIndependent(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	createPackage(t, handler, "skills/research/target-skill")

	copyBody, _ := json.Marshal(map[string]any{
		"from":      "skills/research/论文精读/SKILL.md",
		"to":        "skills/research/target-skill/docs/copied.md",
		"overwrite": false,
	})
	copyRec := httptest.NewRecorder()
	handler.Copy(copyRec, httptest.NewRequest(http.MethodPost, remoteCopyURL("user_001", "task-copy"), bytes.NewReader(copyBody)))
	if copyRec.Code != http.StatusOK {
		t.Fatalf("copy status=%d body=%s", copyRec.Code, copyRec.Body.String())
	}
	var copied testutil.SkillDraftEntryRow
	if err := db.Where("path = ?", "docs/copied.md").Take(&copied).Error; err != nil {
		t.Fatalf("query copied entry: %v", err)
	}
	if copied.BlobHash == nil || *copied.BlobHash != "h_skill_rev1" {
		t.Fatalf("copied blob_hash = %v, want h_skill_rev1", copied.BlobHash)
	}

	write := httptest.NewRecorder()
	handler.Content(write, httptest.NewRequest(http.MethodPut, remoteContentURL("skills/research/论文精读/SKILL.md", "user_001", "task-src", ""), strings.NewReader("# changed\n")))
	if write.Code != http.StatusOK {
		t.Fatalf("source write status=%d body=%s", write.Code, write.Body.String())
	}
	var sourceDraft testutil.SkillDraftEntryRow
	if err := db.Where("skill_id = ? AND path = ?", "skill1", "SKILL.md").Take(&sourceDraft).Error; err != nil {
		t.Fatalf("query source draft: %v", err)
	}
	if sourceDraft.BlobHash == nil || *sourceDraft.BlobHash == "h_skill_rev1" {
		t.Fatalf("source blob_hash did not change: %v", sourceDraft.BlobHash)
	}
	readTarget := httptest.NewRecorder()
	handler.Content(readTarget, httptest.NewRequest(http.MethodGet, remoteContentURL("skills/research/target-skill/docs/copied.md", "user_001", "task-copy", ""), nil))
	if readTarget.Code != http.StatusOK || strings.Contains(readTarget.Body.String(), "# changed") || !strings.Contains(readTarget.Body.String(), "# 论文精读") {
		t.Fatalf("target read status=%d body=%s", readTarget.Code, readTarget.Body.String())
	}
}

func TestRemoteFSMove_PackageRootRenameAndCrossPackageFile(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	renameBody, _ := json.Marshal(map[string]string{
		"from": "skills/research/论文精读",
		"to":   "skills/coding/renamed",
	})
	rename := httptest.NewRecorder()
	handler.Move(rename, httptest.NewRequest(http.MethodPost, remoteMoveURL("user_001", "task1"), bytes.NewReader(renameBody)))
	if rename.Code != http.StatusOK {
		t.Fatalf("package move status=%d body=%s", rename.Code, rename.Body.String())
	}
	if got := testutil.CountRows(t, db, "skills", "id = ? AND category = ? AND skill_name = ? AND relative_root = ?", "skill1", "coding", "renamed", "coding/renamed"); got != 1 {
		t.Fatalf("renamed skill count = %d, want 1", got)
	}
	oldExists := httptest.NewRecorder()
	handler.Exists(oldExists, httptest.NewRequest(http.MethodGet, remoteExistsURL("skills/research/论文精读", "user_001", "task1"), nil))
	if oldExists.Code != http.StatusOK || !strings.Contains(oldExists.Body.String(), `"exists":false`) {
		t.Fatalf("old exists status=%d body=%s", oldExists.Code, oldExists.Body.String())
	}

	createPackage(t, handler, "skills/research/target-skill")
	moveBody, _ := json.Marshal(map[string]string{
		"from": "skills/coding/renamed/SKILL.md",
		"to":   "skills/research/target-skill/moved/SKILL.md",
	})
	move := httptest.NewRecorder()
	handler.Move(move, httptest.NewRequest(http.MethodPost, remoteMoveURL("user_001", "task2"), bytes.NewReader(moveBody)))
	if move.Code != http.StatusOK {
		t.Fatalf("cross-package move status=%d body=%s", move.Code, move.Body.String())
	}
	sourceExists := httptest.NewRecorder()
	handler.Exists(sourceExists, httptest.NewRequest(http.MethodGet, remoteExistsURL("skills/coding/renamed/SKILL.md", "user_001", "task2"), nil))
	if sourceExists.Code != http.StatusOK || !strings.Contains(sourceExists.Body.String(), `"exists":false`) {
		t.Fatalf("source exists status=%d body=%s", sourceExists.Code, sourceExists.Body.String())
	}
	targetRead := httptest.NewRecorder()
	handler.Content(targetRead, httptest.NewRequest(http.MethodGet, remoteContentURL("skills/research/target-skill/moved/SKILL.md", "user_001", "task2", ""), nil))
	if targetRead.Code != http.StatusOK || !strings.Contains(targetRead.Body.String(), "# 论文精读") {
		t.Fatalf("target read status=%d body=%s", targetRead.Code, targetRead.Body.String())
	}
}

func TestRemoteFSTrashAndPurge_PreservesSharedBlob(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	createPackage(t, handler, "skills/research/target-skill")

	copyBody, _ := json.Marshal(map[string]any{
		"from":      "skills/research/论文精读/SKILL.md",
		"to":        "skills/research/target-skill/shared.md",
		"overwrite": false,
	})
	copyRec := httptest.NewRecorder()
	handler.Copy(copyRec, httptest.NewRequest(http.MethodPost, remoteCopyURL("user_001", "task-copy"), bytes.NewReader(copyBody)))
	if copyRec.Code != http.StatusOK {
		t.Fatalf("copy status=%d body=%s", copyRec.Code, copyRec.Body.String())
	}

	trashBody, _ := json.Marshal(map[string]string{"path": "skills/research/论文精读"})
	trash := httptest.NewRecorder()
	handler.Trash(trash, httptest.NewRequest(http.MethodPost, remoteTrashURL("user_001"), bytes.NewReader(trashBody)))
	if trash.Code != http.StatusOK {
		t.Fatalf("trash status=%d body=%s", trash.Code, trash.Body.String())
	}
	list := httptest.NewRecorder()
	handler.List(list, httptest.NewRequest(http.MethodGet, remoteListURL("skills/research", "user_001", "task1"), nil))
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "论文精读") {
		t.Fatalf("trashed skill leaked in list status=%d body=%s", list.Code, list.Body.String())
	}
	if got := testutil.CountRows(t, db, "skill_revisions", "skill_id = ?", "skill1"); got == 0 {
		t.Fatal("trash deleted revisions")
	}
	if got := testutil.CountRows(t, db, "skill_blobs", "hash = ?", "h_skill_rev1"); got != 1 {
		t.Fatalf("trash blob count = %d, want 1", got)
	}

	createPackage(t, handler, "skills/research/论文精读")
	if got := testutil.CountRows(t, db, "skills", "owner_user_id = ? AND relative_root = ?", "user_001", "research/论文精读"); got != 2 {
		t.Fatalf("same-name active recreate total count = %d, want 2", got)
	}

	purge := httptest.NewRecorder()
	handler.DeletePath(purge, httptest.NewRequest(http.MethodDelete, remotePermanentPathURL("skills/research/论文精读", "user_001"), nil))
	if purge.Code != http.StatusOK {
		t.Fatalf("purge status=%d body=%s", purge.Code, purge.Body.String())
	}
	if got := testutil.CountRows(t, db, "skills", "id = ?", "skill1"); got != 0 {
		t.Fatalf("purged skill row count = %d, want 0", got)
	}
	if got := testutil.CountRows(t, db, "skills", "owner_user_id = ? AND relative_root = ? AND deleted_at IS NULL", "user_001", "research/论文精读"); got != 1 {
		t.Fatalf("recreated active skill count = %d, want 1", got)
	}
	if got := testutil.CountRows(t, db, "skill_blobs", "hash = ?", "h_skill_rev1"); got != 1 {
		t.Fatalf("shared blob count after purge = %d, want 1", got)
	}
}

func createPackage(t *testing.T, handler *Handler, path string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"path": path, "recursive": true})
	rec := httptest.NewRecorder()
	handler.Dir(rec, httptest.NewRequest(http.MethodPost, remoteDirURL("user_001", "task-mkdir"), bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("create package %q status=%d body=%s", path, rec.Code, rec.Body.String())
	}
}

func remoteDirURL(userID, taskID string) string {
	return "/remote-fs/dir?" + url.Values{"user_id": {userID}, "task_id": {taskID}}.Encode()
}

func remoteCopyURL(userID, taskID string) string {
	return "/remote-fs/copy?" + url.Values{"user_id": {userID}, "task_id": {taskID}}.Encode()
}

func remoteTrashURL(userID string) string {
	return "/remote-fs/trash?" + url.Values{"user_id": {userID}}.Encode()
}

func remotePermanentPathURL(path, userID string) string {
	return "/remote-fs/path?" + url.Values{"path": {path}, "user_id": {userID}, "permanent": {"true"}, "confirm": {"true"}}.Encode()
}
