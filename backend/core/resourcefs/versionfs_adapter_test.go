package resourcefs

import (
	"context"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/versionfs"
)

// newVersionStoreTestDB creates a SQLite DB and automigrates schemas needed by versionStore.
func newVersionStoreTestDB(t *testing.T) *orm.DB {
	t.Helper()
	return orm.MigrateTestDB(t,
		&orm.PersonalResource{},
		&orm.PersonalResourceBlob{},
		&orm.PersonalResourceRevision{},
		&orm.PersonalResourceDraft{},
		&orm.PersonalResourceReviewSession{},
	)
}

// TestEntryFromDraft converts ORM draft to versionfs Entry.
func TestEntryFromDraft(t *testing.T) {
	draft := orm.PersonalResourceDraft{
		Path:     "memory/memory.md",
		BlobHash: "hash-1",
		Size:     100,
		Mime:     "text/markdown",
		FileType: "markdown",
		Binary:   false,
	}
	entry := entryFromDraft(draft)
	if entry.Path != "memory/memory.md" {
		t.Fatalf("path = %q", entry.Path)
	}
	if entry.EntryType != versionfs.EntryTypeFile {
		t.Fatalf("entry type = %q, want file", entry.EntryType)
	}
	if entry.BlobHash != "hash-1" {
		t.Fatalf("blob hash = %q, want hash-1", entry.BlobHash)
	}
	if !entry.FromDraft {
		t.Fatal("FromDraft should be true")
	}
}

// TestEntryFromRevision converts ORM revision to versionfs Entry.
func TestEntryFromRevision(t *testing.T) {
	rev := orm.PersonalResourceRevision{
		Path:     "memory/user.md",
		BlobHash: "hash-2",
		Size:     200,
		Mime:     "text/markdown",
		FileType: "markdown",
		Binary:   false,
	}
	entry := entryFromRevision(rev)
	if entry.Path != "memory/user.md" {
		t.Fatalf("path = %q", entry.Path)
	}
	if !entry.FromHead {
		t.Fatal("FromHead should be true")
	}
}

// TestSingleFileEntry returns the single entry when exactly one file entry exists.
func TestSingleFileEntry(t *testing.T) {
	// Single file entry
	entries := map[string]versionfs.Entry{
		"memory/memory.md": {
			Path:      "memory/memory.md",
			EntryType: versionfs.EntryTypeFile,
			BlobHash:  "hash-1",
		},
	}
	entry, ok := singleFileEntry(entries)
	if !ok {
		t.Fatal("should return true for single file entry")
	}
	if entry.Path != "memory/memory.md" {
		t.Fatalf("path = %q", entry.Path)
	}

	// Multiple entries
	multi := map[string]versionfs.Entry{
		"a.md": {EntryType: versionfs.EntryTypeFile, BlobHash: "a"},
		"b.md": {EntryType: versionfs.EntryTypeFile, BlobHash: "b"},
	}
	if _, ok := singleFileEntry(multi); ok {
		t.Fatal("should return false for multiple entries")
	}

	// Empty entries
	if _, ok := singleFileEntry(map[string]versionfs.Entry{}); ok {
		t.Fatal("should return false for empty map")
	}

	// Non-file entry type
	nonFile := map[string]versionfs.Entry{
		"dir": {EntryType: versionfs.EntryTypeDir, BlobHash: ""},
	}
	if _, ok := singleFileEntry(nonFile); ok {
		t.Fatal("should return false for directory entry")
	}
}

// TestVersionStoreLoadHeadAndDraft loads a newly created resource head and draft.
func TestVersionStoreLoadHeadAndDraft(t *testing.T) {
	db := newVersionStoreTestDB(t)
	ctx := context.Background()
	store := versionStore{}
	now := time.Now().UTC()
	resourceID := "res-head-draft"
	userID := "user-1"

	seedResource(t, db, resourceID, userID, now)

	head, err := store.LoadHead(ctx, db.DB, resourceID)
	if err != nil {
		t.Fatalf("load head: %v", err)
	}
	if head.RevisionID == "" {
		t.Fatal("expected non-empty revision ID")
	}

	draft, err := store.LoadDraft(ctx, db.DB, resourceID)
	if err != nil {
		t.Fatalf("load draft: %v", err)
	}
	if draft.Version < 1 {
		t.Fatalf("draft version = %d, want >= 1", draft.Version)
	}
}

// TestVersionStoreHasDraftChanges detects no changes on clean draft.
func TestVersionStoreHasDraftChanges(t *testing.T) {
	db := newVersionStoreTestDB(t)
	ctx := context.Background()
	store := versionStore{}
	now := time.Now().UTC()
	resourceID := "res-nodraft"
	userID := "user-1"

	seedResource(t, db, resourceID, userID, now)

	draft, err := store.LoadDraft(ctx, db.DB, resourceID)
	if err != nil {
		t.Fatalf("load draft: %v", err)
	}
	hasChanges, err := store.HasDraftChanges(ctx, db.DB, resourceID, draft)
	if err != nil {
		t.Fatalf("has draft changes: %v", err)
	}
	if hasChanges {
		t.Fatal("clean draft should have no changes")
	}
}

// TestVersionStoreRevsionEntries lists entries for a revision.
func TestVersionStoreRevisionEntries(t *testing.T) {
	db := newVersionStoreTestDB(t)
	ctx := context.Background()
	store := versionStore{}
	now := time.Now().UTC()
	resourceID := "res-rev-entries"
	userID := "user-1"

	seedResource(t, db, resourceID, userID, now)

	entries, err := store.RevisionEntries(ctx, db.DB, resourceID, resourceID+"-rev")
	if err != nil {
		t.Fatalf("revision entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

// TestVersionStoreDraftEntries returns draft entries.
func TestVersionStoreDraftEntries(t *testing.T) {
	db := newVersionStoreTestDB(t)
	ctx := context.Background()
	store := versionStore{}
	now := time.Now().UTC()
	resourceID := "res-draft-entries"
	userID := "user-1"

	seedResource(t, db, resourceID, userID, now)

	entries, err := store.DraftEntries(ctx, db.DB, resourceID, resourceID+"-rev")
	if err != nil {
		t.Fatalf("draft entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

// TestVersionStoreListBlobHashes lists hashes after seeding a resource.
func TestVersionStoreListBlobHashes(t *testing.T) {
	db := newVersionStoreTestDB(t)
	ctx := context.Background()
	store := versionStore{}
	now := time.Now().UTC()

	seedResource(t, db, "res-blob-1", "user-1", now)
	seedResource(t, db, "res-blob-2", "user-2", now)

	hashes, err := store.ListBlobHashes(ctx, db.DB)
	if err != nil {
		t.Fatalf("list blobs: %v", err)
	}
	if len(hashes) < 2 {
		t.Fatalf("expected >= 2 hashes, got %d", len(hashes))
	}
}

// TestVersionStoreBlobReferencedAndDelete verifies blob tracking.
func TestVersionStoreBlobReferencedAndDelete(t *testing.T) {
	db := newVersionStoreTestDB(t)
	ctx := context.Background()
	store := versionStore{}
	now := time.Now().UTC()

	seedResource(t, db, "res-ref", "user-1", now)

	// After seeding, blob should be referenced by revision and draft
	hashes, _ := store.ListBlobHashes(ctx, db.DB)
	if len(hashes) == 0 {
		t.Fatal("expected at least one blob hash")
	}
	refd, err := store.BlobReferenced(ctx, db.DB, hashes[0])
	if err != nil {
		t.Fatalf("blob referenced: %v", err)
	}
	if !refd {
		t.Fatal("blob should be referenced after seeding resource")
	}

	// Insert an orphan blob (not referenced by any revision or draft)
	orphanHash := "orphan-hash-" + resourceIDGen()
	if err := db.DB.Create(&orm.PersonalResourceBlob{
		Hash:           orphanHash,
		Size:           10,
		Mime:           "text/plain",
		FileType:       "text",
		Binary:         false,
		StorageBackend: "postgres",
		Content:        []byte("orphan"),
		CreatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create orphan blob: %v", err)
	}
	orphanRefd, err := store.BlobReferenced(ctx, db.DB, orphanHash)
	if err != nil {
		t.Fatalf("orphan blob referenced: %v", err)
	}
	if orphanRefd {
		t.Fatal("orphan blob should not be referenced")
	}

	// Delete orphan blob
	if err := store.DeleteBlob(ctx, db.DB, orphanHash); err != nil {
		t.Fatalf("delete orphan: %v", err)
	}
}

// TestVersionStoreNextRevisionNo returns sequential numbers for same resource.
func TestVersionStoreNextRevisionNo(t *testing.T) {
	db := newVersionStoreTestDB(t)
	ctx := context.Background()
	store := versionStore{}
	now := time.Now().UTC()

	seedResource(t, db, "res-nextrev", "user-1", now)

	next, err := store.NextRevisionNo(ctx, db.DB, "res-nextrev")
	if err != nil {
		t.Fatalf("next revision no: %v", err)
	}
	if next != 2 {
		t.Fatalf("got %d, want 2", next)
	}
}

// resourceIDGen generates a unique suffix for test resource IDs.
var resourceIDGen = func() func() string {
	counter := 0
	return func() string {
		counter++
		return string(rune('a' + counter%26))
	}
}()

// seedResource creates a minimal resource, blob, revision, and draft for testing versionStore methods.
func seedResource(t *testing.T, db *orm.DB, resourceID, userID string, now time.Time) {
	t.Helper()
	path := "memory/memory.md"
	content := []byte("test content " + resourceID)
	hash := "hash-" + resourceID
	revisionID := resourceID + "-rev"

	if err := db.DB.Create(&orm.PersonalResourceBlob{
		Hash:           hash,
		Size:           int64(len(content)),
		Mime:           "text/markdown; charset=utf-8",
		FileType:       "markdown",
		Binary:         false,
		StorageBackend: "postgres",
		Content:        content,
		CreatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create blob %s: %v", resourceID, err)
	}

	head := revisionID
	if err := db.DB.Create(&orm.PersonalResource{
		ID:             resourceID,
		UserID:         userID,
		ResourceType:   "memory",
		HeadRevisionID: &head,
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create resource %s: %v", resourceID, err)
	}

	if err := db.DB.Create(&orm.PersonalResourceRevision{
		ID:          revisionID,
		ResourceID:  resourceID,
		RevisionNo:  1,
		Path:        path,
		BlobHash:    hash,
		ContentHash: hash,
		Size:        int64(len(content)),
		Mime:        "text/markdown; charset=utf-8",
		FileType:    "markdown",
		Binary:      false,
		Message:     "seed",
		CreatedAt:   now,
	}).Error; err != nil {
		t.Fatalf("create revision %s: %v", resourceID, err)
	}

	if err := db.DB.Create(&orm.PersonalResourceDraft{
		ResourceID:     resourceID,
		BaseRevisionID: &head,
		Path:           path,
		BlobHash:       hash,
		ContentHash:    hash,
		Size:           int64(len(content)),
		Mime:           "text/markdown; charset=utf-8",
		FileType:       "markdown",
		Binary:         false,
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create draft %s: %v", resourceID, err)
	}
}
