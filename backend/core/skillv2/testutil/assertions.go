package testutil

import "testing"

type ExpectedEntry struct {
	Path      string
	EntryType string
	Binary    bool
	FileType  string
	HasBlob   bool
}

type BlobExpectation struct {
	Binary         bool
	FileType       string
	StorageBackend string
	ContentInPG    bool
	HasStorageKey  bool
}

func AssertHeadRevision(t *testing.T, db *TestDB, skillID, revisionID string) {
	t.Helper()

	var row SkillRow
	if err := db.Where("id = ?", skillID).Take(&row).Error; err != nil {
		t.Fatalf("query skill %s: %v", skillID, err)
	}
	if row.HeadRevisionID == nil || *row.HeadRevisionID != revisionID {
		t.Fatalf("head_revision_id = %v, want %q", row.HeadRevisionID, revisionID)
	}
}

func AssertNoDraftEntries(t *testing.T, db *TestDB, skillID string) {
	t.Helper()
	if got := CountRows(t, db, "skill_draft_entries", "skill_id = ?", skillID); got != 0 {
		t.Fatalf("skill_draft_entries count = %d, want 0", got)
	}
}

func AssertRevisionEntries(t *testing.T, db *TestDB, revisionID string, expected []ExpectedEntry) {
	t.Helper()

	var rows []SkillRevisionEntryRow
	if err := db.Where("revision_id = ?", revisionID).Find(&rows).Error; err != nil {
		t.Fatalf("query revision entries: %v", err)
	}
	byPath := map[string]SkillRevisionEntryRow{}
	for _, row := range rows {
		byPath[row.Path] = row
	}
	for _, want := range expected {
		got, ok := byPath[want.Path]
		if !ok {
			t.Fatalf("missing revision entry %q", want.Path)
		}
		if got.EntryType != want.EntryType {
			t.Fatalf("%s entry_type = %q, want %q", want.Path, got.EntryType, want.EntryType)
		}
		if got.Binary != want.Binary {
			t.Fatalf("%s binary = %v, want %v", want.Path, got.Binary, want.Binary)
		}
		if want.FileType != "" && got.FileType != want.FileType {
			t.Fatalf("%s file_type = %q, want %q", want.Path, got.FileType, want.FileType)
		}
		hasBlob := got.BlobHash != nil && *got.BlobHash != ""
		if hasBlob != want.HasBlob {
			t.Fatalf("%s has blob = %v, want %v", want.Path, hasBlob, want.HasBlob)
		}
	}
}
