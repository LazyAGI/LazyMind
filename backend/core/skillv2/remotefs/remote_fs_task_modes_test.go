package remotefs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestRemoteFSReadView_TaskModes(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "h_draft", "# draft\n")
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Update("task_id", "session_a").Error; err != nil {
		t.Fatalf("seed task_id: %v", err)
	}
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_draft")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	for _, tc := range []struct {
		name        string
		taskID      string
		wantContent string
	}{
		{name: "review reads existing draft", taskID: "review_123", wantContent: "# draft"},
		{name: "org reads publish when draft belongs to another task", taskID: "org_123", wantContent: "# 论文精读"},
		{name: "editor reads own draft", taskID: "session_a", wantContent: "# draft"},
		{name: "editor reads publish when draft belongs to another task", taskID: "session_b", wantContent: "# 论文精读"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.Content(rec, httptest.NewRequest(http.MethodGet, remoteContentURL("skills/research/论文精读/SKILL.md", "user_001", tc.taskID, ""), nil))
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.wantContent) {
				t.Fatalf("status=%d body=%s, want content %q", rec.Code, rec.Body.String(), tc.wantContent)
			}
		})
	}
}

func TestRemoteFSReviewWritesExistingDraftAndTakesOwnership(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	seedRunningMaintenanceTask(t, db, "review_123", "user_001")
	testutil.SeedTextBlob(t, db, "h_draft", "# draft\n")
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Update("task_id", "session_a").Error; err != nil {
		t.Fatalf("seed task_id: %v", err)
	}
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_draft")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	write := httptest.NewRecorder()
	handler.Content(write, httptest.NewRequest(http.MethodPut, remoteContentURL("skills/research/论文精读/references/review.md", "user_001", "review_123", ""), strings.NewReader("# review\n")))
	if write.Code != http.StatusOK {
		t.Fatalf("review write status=%d body=%s", write.Code, write.Body.String())
	}
	var draft testutil.SkillDraftRow
	if err := db.Where("skill_id = ?", "skill1").Take(&draft).Error; err != nil {
		t.Fatalf("query draft: %v", err)
	}
	if draft.TaskID != "review_123" {
		t.Fatalf("draft task_id = %q, want review_123", draft.TaskID)
	}
	read := httptest.NewRecorder()
	handler.Content(read, httptest.NewRequest(http.MethodGet, remoteContentURL("skills/research/论文精读/references/review.md", "user_001", "review_123", ""), nil))
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), "# review") {
		t.Fatalf("review read status=%d body=%s", read.Code, read.Body.String())
	}
}

func TestRemoteFSReviewCreatesDraftWhenNoneExists(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	seedRunningMaintenanceTask(t, db, "review_123", "user_001")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	write := httptest.NewRecorder()
	handler.Content(write, httptest.NewRequest(http.MethodPut, remoteContentURL("skills/research/论文精读/references/review.md", "user_001", "review_123", ""), strings.NewReader("# review\n")))
	if write.Code != http.StatusOK {
		t.Fatalf("review write status=%d body=%s", write.Code, write.Body.String())
	}
	var draft testutil.SkillDraftRow
	if err := db.Where("skill_id = ?", "skill1").Take(&draft).Error; err != nil {
		t.Fatalf("query draft: %v", err)
	}
	if draft.TaskID != "review_123" {
		t.Fatalf("draft task_id = %q, want review_123", draft.TaskID)
	}
	read := httptest.NewRecorder()
	handler.Content(read, httptest.NewRequest(http.MethodGet, remoteContentURL("skills/research/论文精读/references/review.md", "user_001", "review_123", ""), nil))
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), "# review") {
		t.Fatalf("review read status=%d body=%s", read.Code, read.Body.String())
	}
}

func TestRemoteFSOrgWritesOwnDraftAndBlocksOtherDraft(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	seedRunningMaintenanceTask(t, db, "org_123", "user_001")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	write := httptest.NewRecorder()
	handler.Content(write, httptest.NewRequest(http.MethodPut, remoteContentURL("skills/research/论文精读/references/org.md", "user_001", "org_123", ""), strings.NewReader("# org\n")))
	if write.Code != http.StatusOK {
		t.Fatalf("org write status=%d body=%s", write.Code, write.Body.String())
	}
	readOwn := httptest.NewRecorder()
	handler.Content(readOwn, httptest.NewRequest(http.MethodGet, remoteContentURL("skills/research/论文精读/references/org.md", "user_001", "org_123", ""), nil))
	if readOwn.Code != http.StatusOK || !strings.Contains(readOwn.Body.String(), "# org") {
		t.Fatalf("org own read status=%d body=%s", readOwn.Code, readOwn.Body.String())
	}
	readOther := httptest.NewRecorder()
	handler.Exists(readOther, httptest.NewRequest(http.MethodGet, remoteExistsURL("skills/research/论文精读/references/org.md", "user_001", "org_456"), nil))
	if readOther.Code != http.StatusOK || !strings.Contains(readOther.Body.String(), `"exists":false`) {
		t.Fatalf("other org exists status=%d body=%s", readOther.Code, readOther.Body.String())
	}

	blocked := httptest.NewRecorder()
	handler.Content(blocked, httptest.NewRequest(http.MethodPut, remoteContentURL("skills/research/论文精读/references/other.md", "user_001", "org_456", ""), strings.NewReader("# other\n")))
	if blocked.Code != http.StatusConflict {
		t.Fatalf("other org write status=%d body=%s, want 409", blocked.Code, blocked.Body.String())
	}
}

func seedRunningMaintenanceTask(t *testing.T, db *testutil.TestDB, taskID, userID string) {
	t.Helper()
	if err := db.Table("skill_review_stats").Create(map[string]any{
		"id":          taskID,
		"requestid":   taskID,
		"userid":      userID,
		"status":      "running",
		"started_at":  "2026-07-13T10:00:00Z",
		"duration_ms": 0,
		"summary":     "{}",
	}).Error; err != nil {
		t.Fatalf("seed running maintenance task: %v", err)
	}
}

func TestRemoteFSEditorUsesTaskIDAndIgnoresSessionIDParam(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	missingTask := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/remote-fs/content?path=skills/research/%E8%AE%BA%E6%96%87%E7%B2%BE%E8%AF%BB/SKILL.md&user_id=user_001&session_id=session_a", strings.NewReader("# session\n"))
	handler.Content(missingTask, req)
	if missingTask.Code != http.StatusBadRequest {
		t.Fatalf("session_id-only write status=%d body=%s, want 400", missingTask.Code, missingTask.Body.String())
	}

	write := httptest.NewRecorder()
	handler.Content(write, httptest.NewRequest(http.MethodPut, remoteContentURL("skills/research/论文精读/references/editor.md", "user_001", "session_a", ""), strings.NewReader("# editor\n")))
	if write.Code != http.StatusOK {
		t.Fatalf("editor write status=%d body=%s", write.Code, write.Body.String())
	}
	readOwn := httptest.NewRecorder()
	handler.Content(readOwn, httptest.NewRequest(http.MethodGet, remoteContentURL("skills/research/论文精读/references/editor.md", "user_001", "session_a", ""), nil))
	if readOwn.Code != http.StatusOK || !strings.Contains(readOwn.Body.String(), "# editor") {
		t.Fatalf("editor own read status=%d body=%s", readOwn.Code, readOwn.Body.String())
	}
	blocked := httptest.NewRecorder()
	handler.Content(blocked, httptest.NewRequest(http.MethodPut, remoteContentURL("skills/research/论文精读/references/other.md", "user_001", "session_b", ""), strings.NewReader("# other\n")))
	if blocked.Code != http.StatusConflict {
		t.Fatalf("other editor write status=%d body=%s, want 409", blocked.Code, blocked.Body.String())
	}
}
