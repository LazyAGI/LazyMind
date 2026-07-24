package fs

import (
	"context"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestDraftFSWriteText_StoresOverlayOnly(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	draftFS := NewDraftFS(DraftFSDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	resp, err := draftFS.WriteText(context.Background(), WriteTextRequest{
		SkillID:              "skill1",
		Path:                 "SKILL.md",
		Content:              "# 新内容\n",
		ExpectedDraftVersion: 1,
		UserID:               "user_001",
	})
	if err != nil {
		t.Fatalf("WriteText returned error: %v", err)
	}
	if resp.DraftVersion != 2 {
		t.Fatalf("DraftVersion = %d, want 2", resp.DraftVersion)
	}
	testutil.AssertHeadRevision(t, db, "skill1", "rev1")
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ? AND path = ? AND op = ?", "skill1", "SKILL.md", "upsert"); got != 1 {
		t.Fatalf("SKILL.md draft upsert count = %d, want 1", got)
	}
	if got := testutil.CountRows(t, db, "skill_revisions", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("skill_revisions count = %d, want 1", got)
	}
}

func TestDraftFSMultipleWrites_AreVisibleInDraftView(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	draftFS := NewDraftFS(DraftFSDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	headFS := NewHeadFS(HeadFSDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	_, err := draftFS.WriteText(context.Background(), WriteTextRequest{SkillID: "skill1", Path: "SKILL.md", Content: "# 草稿\n", ExpectedDraftVersion: 1, UserID: "user_001"})
	if err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	_, err = draftFS.WriteText(context.Background(), WriteTextRequest{SkillID: "skill1", Path: "references/a.md", Content: "# 草稿资料\n", ExpectedDraftVersion: 2, UserID: "user_001"})
	if err != nil {
		t.Fatalf("write references/a.md: %v", err)
	}
	_, err = draftFS.Mkdir(context.Background(), MkdirRequest{SkillID: "skill1", Path: "assets/images", ExpectedDraftVersion: 3, UserID: "user_001"})
	if err != nil {
		t.Fatalf("mkdir assets/images: %v", err)
	}

	draftTree, err := draftFS.Tree(context.Background(), TreeRequest{SkillID: "skill1"})
	if err != nil {
		t.Fatalf("draft tree: %v", err)
	}
	assertTreeHasPaths(t, draftTree, "SKILL.md", "references/a.md", "assets/images")
	headTree, err := headFS.Tree(context.Background(), TreeRequest{SkillID: "skill1"})
	if err != nil {
		t.Fatalf("head tree: %v", err)
	}
	assertTreeMissingPaths(t, headTree, "references/a.md", "assets/images")
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); got != 3 {
		t.Fatalf("skill_draft_entries count = %d, want 3", got)
	}
}

func TestDraftFSMkdirEmptyDir_IsVisibleAndCommitable(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	draftFS := NewDraftFS(DraftFSDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	revisions := NewRevisionService(RevisionServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	resp, err := draftFS.Mkdir(context.Background(), MkdirRequest{SkillID: "skill1", Path: "empty-notes", ExpectedDraftVersion: 1, UserID: "user_001"})
	if err != nil {
		t.Fatalf("Mkdir returned error: %v", err)
	}
	if resp.DraftVersion != 2 {
		t.Fatalf("DraftVersion = %d, want 2", resp.DraftVersion)
	}
	tree, err := draftFS.Tree(context.Background(), TreeRequest{SkillID: "skill1"})
	if err != nil {
		t.Fatalf("draft tree: %v", err)
	}
	assertTreeHasPaths(t, tree, "empty-notes")

	commit, err := revisions.CommitDraft(context.Background(), CommitDraftRequest{SkillID: "skill1", UserID: "user_001", DraftVersion: 2})
	if err != nil {
		t.Fatalf("CommitDraft returned error: %v", err)
	}
	testutil.AssertRevisionEntries(t, db, commit.RevisionID, []testutil.ExpectedEntry{
		{Path: "empty-notes", EntryType: "dir", FileType: "directory", HasBlob: false},
	})
}

func TestDraftFSDeleteDraftOnlyEmptyDir_RemovesOverlay(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedDraftEntry(t, db, "skill1", "notes", "upsert", "dir", "")
	draftFS := NewDraftFS(DraftFSDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if _, err := draftFS.Delete(context.Background(), DeleteRequest{SkillID: "skill1", Path: "notes", Recursive: false, ExpectedDraftVersion: 1, UserID: "user_001"}); err != nil {
		t.Fatalf("Delete draft-only empty dir returned error: %v", err)
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ? AND path = ?", "skill1", "notes"); got != 0 {
		t.Fatalf("notes draft overlay count = %d, want 0", got)
	}
}

func TestDraftFSDeleteBaseEmptyDir_WritesDeleteOverlay(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedRevisionEntry(t, db, "rev1", "notes", "dir", "", "directory")
	draftFS := NewDraftFS(DraftFSDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if _, err := draftFS.Delete(context.Background(), DeleteRequest{SkillID: "skill1", Path: "notes", Recursive: false, ExpectedDraftVersion: 1, UserID: "user_001"}); err != nil {
		t.Fatalf("Delete base empty dir returned error: %v", err)
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ? AND path = ? AND op = ?", "skill1", "notes", "delete"); got != 1 {
		t.Fatalf("notes delete overlay count = %d, want 1", got)
	}
}

func TestDraftFSDeleteNonEmptyDirectory_RequiresRecursive(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "h_a", "# A\n")
	testutil.SeedRevisionEntry(t, db, "rev1", "references", "dir", "", "directory")
	testutil.SeedRevisionEntry(t, db, "rev1", "references/a.md", "file", "h_a", "markdown")
	draftFS := NewDraftFS(DraftFSDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if _, err := draftFS.Delete(context.Background(), DeleteRequest{SkillID: "skill1", Path: "references", Recursive: false, ExpectedDraftVersion: 1, UserID: "user_001"}); err == nil {
		t.Fatal("Delete non-empty directory without recursive succeeded")
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ? AND path = ?", "skill1", "references"); got != 0 {
		t.Fatalf("references draft overlay count = %d, want 0", got)
	}
}

func TestDraftFSFileDirectoryTypeConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedRevisionEntry(t, db, "rev1", "references", "dir", "", "directory")
	draftFS := NewDraftFS(DraftFSDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if _, err := draftFS.WriteText(context.Background(), WriteTextRequest{SkillID: "skill1", Path: "references", Content: "bad", ExpectedDraftVersion: 1, UserID: "user_001"}); err == nil {
		t.Fatal("WriteText over existing directory succeeded")
	}
	if _, err := draftFS.Mkdir(context.Background(), MkdirRequest{SkillID: "skill1", Path: "SKILL.md", ExpectedDraftVersion: 1, UserID: "user_001"}); err == nil {
		t.Fatal("Mkdir over existing file succeeded")
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); got != 0 {
		t.Fatalf("skill_draft_entries count = %d, want 0", got)
	}
}

func TestDraftExists_ServiceDetectsOverlay(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	store := NewDraftStore(DraftStoreDeps{DB: db.DB})

	state, err := store.HasUncommittedDraft(context.Background(), "skill1")
	if err != nil {
		t.Fatalf("HasUncommittedDraft returned error: %v", err)
	}
	if state.HasUncommittedDraft {
		t.Fatalf("HasUncommittedDraft = true with no overlay: %#v", state)
	}

	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Updates(map[string]any{
		"task_id":         "task1",
		"conversation_id": "conv1",
		"version":         3,
	}).Error; err != nil {
		t.Fatalf("update draft state: %v", err)
	}
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_draft")

	state, err = store.HasUncommittedDraft(context.Background(), "skill1")
	if err != nil {
		t.Fatalf("HasUncommittedDraft returned error after overlay: %v", err)
	}
	if !state.HasUncommittedDraft || state.DraftVersion != 3 || state.BaseRevisionID != "rev1" || state.TaskID != "task1" || state.ConversationID != "conv1" {
		t.Fatalf("unexpected draft state: %#v", state)
	}
}

func TestDraftFSUploadBinary_DoesNotStoreBytesInPG(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	draftFS := NewDraftFS(DraftFSDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	resp, err := draftFS.WriteFile(context.Background(), WriteFileRequest{
		SkillID:              "skill1",
		Path:                 "assets/logo.png",
		Data:                 testutil.MinimalPNGBytes(),
		ExpectedDraftVersion: 1,
		UserID:               "user_001",
	})
	if err != nil {
		t.Fatalf("WriteFile binary returned error: %v", err)
	}
	var blob testutil.SkillBlobRow
	if err := db.Where("hash = ?", resp.BlobHash).Take(&blob).Error; err != nil {
		t.Fatalf("query binary blob: %v", err)
	}
	if !blob.Binary || blob.StorageBackend == "postgres" || len(blob.Content) != 0 || blob.StorageKey == nil {
		t.Fatalf("binary blob stored incorrectly: %#v", blob)
	}
}

func assertTreeHasPaths(t *testing.T, tree TreeNode, paths ...string) {
	t.Helper()
	nodes := map[string]TreeNode{}
	collectTreeNodes(nodes, tree.Children)
	for _, path := range paths {
		if _, ok := nodes[path]; !ok {
			t.Fatalf("tree missing path %q", path)
		}
	}
}

func assertTreeMissingPaths(t *testing.T, tree TreeNode, paths ...string) {
	t.Helper()
	nodes := map[string]TreeNode{}
	collectTreeNodes(nodes, tree.Children)
	for _, path := range paths {
		if _, ok := nodes[path]; ok {
			t.Fatalf("tree unexpectedly contains path %q", path)
		}
	}
}

func collectTreeNodes(out map[string]TreeNode, nodes []TreeNode) {
	for _, node := range nodes {
		out[node.Path] = node
		collectTreeNodes(out, node.Children)
	}
}
