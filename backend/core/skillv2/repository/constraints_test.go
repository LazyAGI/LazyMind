package repository

import (
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestSkillBlobConstraint_TextMustStoreContentInPostgres(t *testing.T) {
	db := testutil.NewTestDB(t)
	key := "objects/h_text_bad"

	err := db.Create(&testutil.SkillBlobRow{
		Hash:           "h_text_bad",
		Size:           12,
		Mime:           "text/markdown",
		FileType:       "markdown",
		Binary:         false,
		StorageBackend: "local_file",
		StorageKey:     &key,
		CreatedAt:      testutil.TimeFixture(),
	}).Error
	if err == nil {
		t.Fatal("expected DB constraint failure for text blob outside postgres content")
	}
}

func TestSkillBlobConstraint_BinaryMustNotStoreContentInPostgres(t *testing.T) {
	db := testutil.NewTestDB(t)

	err := db.Create(&testutil.SkillBlobRow{
		Hash:           "h_binary_bad",
		Size:           8,
		Mime:           "image/png",
		FileType:       "image",
		Binary:         true,
		StorageBackend: "postgres",
		Content:        testutil.MinimalPNGBytes(),
		CreatedAt:      testutil.TimeFixture(),
	}).Error
	if err == nil {
		t.Fatal("expected DB constraint failure for binary blob persisted in postgres content")
	}
}

func TestRevisionEntryConstraint_FileRequiresBlobDirForbidsBlob(t *testing.T) {
	db := testutil.NewTestDB(t)

	err := db.Create(&testutil.SkillRevisionEntryRow{
		RevisionID: "rev1",
		Path:       "SKILL.md",
		EntryType:  "file",
		FileType:   "markdown",
	}).Error
	if err == nil {
		t.Fatal("expected DB constraint failure for file entry without blob")
	}

	hash := "h_dir_bad"
	err = db.Create(&testutil.SkillRevisionEntryRow{
		RevisionID: "rev1",
		Path:       "references",
		EntryType:  "dir",
		BlobHash:   &hash,
		FileType:   "directory",
	}).Error
	if err == nil {
		t.Fatal("expected DB constraint failure for dir entry with blob")
	}
}

func TestDraftEntryConstraint_DeleteDoesNotRequireBlobUpsertRequiresType(t *testing.T) {
	db := testutil.NewTestDB(t)

	err := db.Create(&testutil.SkillDraftEntryRow{
		SkillID:   "skill1",
		Path:      "references/a.md",
		Op:        "delete",
		UpdatedAt: testutil.TimeFixture(),
	}).Error
	if err != nil {
		t.Fatalf("delete draft entry should not require blob: %v", err)
	}

	err = db.Create(&testutil.SkillDraftEntryRow{
		SkillID:   "skill1",
		Path:      "SKILL.md",
		Op:        "upsert",
		UpdatedAt: testutil.TimeFixture(),
	}).Error
	if err == nil {
		t.Fatal("expected DB constraint failure for upsert without entry_type")
	}
}
