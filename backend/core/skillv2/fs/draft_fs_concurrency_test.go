package fs

import (
	"context"
	"sync"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestDraftFSConcurrentWriteSamePath_UsesDraftVersion(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Update("version", 3).Error; err != nil {
		t.Fatalf("update draft version: %v", err)
	}
	draftFS := NewDraftFS(DraftFSDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, content := range []string{"# A\n", "# B\n"} {
		wg.Add(1)
		go func(content string) {
			defer wg.Done()
			_, err := draftFS.WriteText(context.Background(), WriteTextRequest{
				SkillID:              "skill1",
				Path:                 "SKILL.md",
				Content:              content,
				ExpectedDraftVersion: 3,
				UserID:               "user_001",
			})
			results <- err
		}(content)
	}
	wg.Wait()
	close(results)

	successes := 0
	failures := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent write successes=%d failures=%d, want 1/1", successes, failures)
	}
	var draft testutil.SkillDraftRow
	if err := db.Where("skill_id = ?", "skill1").Take(&draft).Error; err != nil {
		t.Fatalf("query draft: %v", err)
	}
	if draft.Version != 4 {
		t.Fatalf("draft version = %d, want 4", draft.Version)
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ? AND path = ?", "skill1", "SKILL.md"); got != 1 {
		t.Fatalf("SKILL.md draft overlay count = %d, want 1", got)
	}
}
