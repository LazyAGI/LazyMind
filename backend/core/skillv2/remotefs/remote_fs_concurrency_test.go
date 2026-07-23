package remotefs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestRemoteFSWriteAndCommit_TaskConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	committer := NewCommitter(CommitterDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	write1 := httptest.NewRecorder()
	handler.Content(write1, httptest.NewRequest(http.MethodPut, remoteContentURL("skills/research/论文精读/SKILL.md", "user_001", "task1", ""), strings.NewReader(testutil.SkillMD("论文精读", "task1"))))
	if write1.Code != http.StatusOK {
		t.Fatalf("task1 write status=%d body=%s", write1.Code, write1.Body.String())
	}

	write2 := httptest.NewRecorder()
	handler.Content(write2, httptest.NewRequest(http.MethodPut, remoteContentURL("skills/research/论文精读/references/b.md", "user_001", "task2", ""), strings.NewReader("# task2\n")))
	if write2.Code != http.StatusConflict {
		t.Fatalf("task2 conflict status=%d body=%s, want 409", write2.Code, write2.Body.String())
	}

	if _, err := committer.CommitDraft(httptest.NewRequest(http.MethodPost, "/skills/skill1/commit", nil).Context(), CommitDraftRequest{
		SkillID:      "skill1",
		UserID:       "user_001",
		DraftVersion: 2,
	}); err != nil {
		t.Fatalf("CommitDraft returned error: %v", err)
	}

	write2AfterCommit := httptest.NewRecorder()
	handler.Content(write2AfterCommit, httptest.NewRequest(http.MethodPut, remoteContentURL("skills/research/论文精读/references/b.md", "user_001", "task2", ""), strings.NewReader("# task2\n")))
	if write2AfterCommit.Code != http.StatusOK {
		t.Fatalf("task2 write after commit status=%d body=%s, want 200", write2AfterCommit.Code, write2AfterCommit.Body.String())
	}
	var draft testutil.SkillDraftRow
	if err := db.Where("skill_id = ?", "skill1").Take(&draft).Error; err != nil {
		t.Fatalf("query draft: %v", err)
	}
	if draft.TaskID != "task2" {
		t.Fatalf("draft task_id = %q, want task2", draft.TaskID)
	}
}
