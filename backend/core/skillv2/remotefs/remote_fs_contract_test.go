package remotefs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestRemoteFSContent_MissingPath_Returns400(t *testing.T) {
	db := testutil.NewTestDB(t)
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	rec := httptest.NewRecorder()
	handler.Content(rec, httptest.NewRequest(http.MethodPut, "/remote-fs/content?user_id=user_001&task_id=task1", strings.NewReader("body")))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "path") {
		t.Fatalf("status=%d body=%s, want 400 mentioning path", rec.Code, rec.Body.String())
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", ""); got != 0 {
		t.Fatalf("draft entry count = %d, want 0", got)
	}
}

func TestRemoteFSMove_InvalidJSON_Returns400(t *testing.T) {
	db := testutil.NewTestDB(t)
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	rec := httptest.NewRecorder()
	handler.Move(rec, httptest.NewRequest(http.MethodPost, remoteMoveURL("user_001", "task1"), strings.NewReader("{invalid json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", ""); got != 0 {
		t.Fatalf("draft entry count = %d, want 0", got)
	}
}

func TestRemoteFS_Unauthenticated_Returns401(t *testing.T) {
	db := testutil.NewTestDB(t)
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	rec := httptest.NewRecorder()
	handler.List(rec, httptest.NewRequest(http.MethodGet, "/remote-fs/list?path=skills/research/论文精读&task_id=task1", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s, want 401", rec.Code, rec.Body.String())
	}
}

func TestRemoteFS_ForbiddenOtherUserSkill_Returns404Or403(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	rec := httptest.NewRecorder()
	handler.Content(rec, httptest.NewRequest(http.MethodGet, remoteContentURL("skills/research/论文精读/SKILL.md", "user_002", "task1", ""), nil))
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s, want 403 or 404", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "# 论文精读") {
		t.Fatalf("forbidden response leaked file content: %s", rec.Body.String())
	}
}

func TestRemoteFSPath_InvalidSegments_Returns400(t *testing.T) {
	db := testutil.NewTestDB(t)
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	for _, path := range []string{
		"skills/research/论文精读/../evil.md",
		"/skills/research/论文精读/SKILL.md",
		"skills/research//SKILL.md",
		`skills\research\论文精读\SKILL.md`,
	} {
		rec := httptest.NewRecorder()
		handler.Content(rec, httptest.NewRequest(http.MethodPut, remoteContentURL(path, "user_001", "task1", ""), strings.NewReader("bad")))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("path %q status=%d body=%s, want 400", path, rec.Code, rec.Body.String())
		}
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", ""); got != 0 {
		t.Fatalf("draft entry count = %d, want 0", got)
	}
}

func TestRemoteFSList_NamespaceLevels(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedSkillWithRevision(t, db, "skill2", "rev2")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill2").Updates(map[string]any{
		"category":      "coding",
		"skill_name":    "git-workflow",
		"relative_root": "coding/git-workflow",
	}).Error; err != nil {
		t.Fatalf("update second skill: %v", err)
	}
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	for _, tc := range []struct {
		path string
		want []string
	}{
		{path: "skills", want: []string{"research", "coding"}},
		{path: "skills/research", want: []string{"论文精读"}},
		{path: "skills/research/论文精读", want: []string{"SKILL.md"}},
	} {
		rec := httptest.NewRecorder()
		handler.List(rec, httptest.NewRequest(http.MethodGet, remoteListURL(tc.path, "user_001", "task1"), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("list %q status=%d body=%s", tc.path, rec.Code, rec.Body.String())
		}
		for _, want := range tc.want {
			if !strings.Contains(rec.Body.String(), want) {
				t.Fatalf("list %q missing %q: %s", tc.path, want, rec.Body.String())
			}
		}
	}
}

func TestRemoteFSList_TaskIDRequiredOnlyInsidePackage(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	root := httptest.NewRecorder()
	handler.List(root, httptest.NewRequest(http.MethodGet, "/remote-fs/list?path=skills&user_id=user_001", nil))
	if root.Code != http.StatusOK {
		t.Fatalf("root list status=%d body=%s, want 200", root.Code, root.Body.String())
	}
	category := httptest.NewRecorder()
	handler.List(category, httptest.NewRequest(http.MethodGet, "/remote-fs/list?path=skills/research&user_id=user_001", nil))
	if category.Code != http.StatusOK {
		t.Fatalf("category list status=%d body=%s, want 200", category.Code, category.Body.String())
	}
	pkg := httptest.NewRecorder()
	handler.List(pkg, httptest.NewRequest(http.MethodGet, "/remote-fs/list?path=skills/research/论文精读&user_id=user_001", nil))
	if pkg.Code != http.StatusBadRequest {
		t.Fatalf("package list status=%d body=%s, want 400", pkg.Code, pkg.Body.String())
	}
}

func TestRemoteFSContent_DirectoryReturns400(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedRevisionEntry(t, db, "rev1", "references", "dir", "", "directory")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	rec := httptest.NewRecorder()
	handler.Content(rec, httptest.NewRequest(http.MethodGet, remoteContentURL("skills/research/论文精读/references", "user_001", "task1", ""), nil))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "directory") {
		t.Fatalf("status=%d body=%s, want 400 directory error", rec.Code, rec.Body.String())
	}
}

func TestRemoteFSDeleteMissingPath_Returns404(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	rec := httptest.NewRecorder()
	handler.DeletePath(rec, httptest.NewRequest(http.MethodDelete, remotePathURL("skills/research/论文精读/references/missing.md", "user_001", "task1"), nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s, want 404", rec.Code, rec.Body.String())
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); got != 0 {
		t.Fatalf("draft entry count = %d, want 0", got)
	}
}

func TestRemoteFSMoveToExistingPath_Returns409(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "h_a", "# A\n")
	testutil.SeedTextBlob(t, db, "h_b", "# B\n")
	testutil.SeedDraftEntry(t, db, "skill1", "references/a.md", "upsert", "file", "h_a")
	testutil.SeedDraftEntry(t, db, "skill1", "references/b.md", "upsert", "file", "h_b")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	body, _ := json.Marshal(map[string]string{"from": "skills/research/论文精读/references/a.md", "to": "skills/research/论文精读/references/b.md"})
	rec := httptest.NewRecorder()
	handler.Move(rec, httptest.NewRequest(http.MethodPost, remoteMoveURL("user_001", "task1"), bytes.NewReader(body)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}
	var b testutil.SkillDraftEntryRow
	if err := db.Where("skill_id = ? AND path = ?", "skill1", "references/b.md").Take(&b).Error; err != nil {
		t.Fatalf("query b.md overlay: %v", err)
	}
	if b.BlobHash == nil || *b.BlobHash != "h_b" {
		t.Fatalf("b.md blob changed: %#v", b)
	}
}

func TestRemoteFSMoveToBaseExistingPath_Returns409(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "h_a", "# A\n")
	testutil.SeedTextBlob(t, db, "h_b", "# B\n")
	testutil.SeedRevisionEntry(t, db, "rev1", "references/a.md", "file", "h_a", "markdown")
	testutil.SeedRevisionEntry(t, db, "rev1", "references/b.md", "file", "h_b", "markdown")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	body, _ := json.Marshal(map[string]string{"from": "skills/research/论文精读/references/a.md", "to": "skills/research/论文精读/references/b.md"})
	rec := httptest.NewRecorder()
	handler.Move(rec, httptest.NewRequest(http.MethodPost, remoteMoveURL("user_001", "task1"), bytes.NewReader(body)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); got != 0 {
		t.Fatalf("draft entry count = %d, want 0", got)
	}
}

func TestRemoteFSMoveToMissingParent_MaterializesParent(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Update("task_id", "task1").Error; err != nil {
		t.Fatalf("seed task_id: %v", err)
	}
	testutil.SeedDraftEntry(t, db, "skill1", "references/a.md", "upsert", "file", "h_a")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	body, _ := json.Marshal(map[string]string{"from": "skills/research/论文精读/references/a.md", "to": "skills/research/论文精读/missing-dir/a.md"})
	rec := httptest.NewRecorder()
	handler.Move(rec, httptest.NewRequest(http.MethodPost, remoteMoveURL("user_001", "task1"), bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ? AND path = ? AND entry_type = ?", "skill1", "missing-dir", "dir"); got != 1 {
		t.Fatalf("materialized parent count = %d, want 1", got)
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ? AND path = ?", "skill1", "missing-dir/a.md"); got != 1 {
		t.Fatalf("move overlay count = %d, want 1", got)
	}
}

func TestRemoteFSMoveDirectoryIntoChild_Returns400(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedRevisionEntry(t, db, "rev1", "references", "dir", "", "directory")
	testutil.SeedRevisionEntry(t, db, "rev1", "references/a.md", "file", "h_a", "markdown")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	body, _ := json.Marshal(map[string]string{"from": "skills/research/论文精读/references", "to": "skills/research/论文精读/references/nested"})
	rec := httptest.NewRecorder()
	handler.Move(rec, httptest.NewRequest(http.MethodPost, remoteMoveURL("user_001", "task1"), bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); got != 0 {
		t.Fatalf("draft entry count = %d, want 0", got)
	}
}
