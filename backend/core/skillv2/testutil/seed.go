package testutil

import (
	"encoding/json"
	"fmt"
	"testing"
)

func SeedSkillWithRevision(t *testing.T, db *TestDB, skillID, revisionID string) {
	t.Helper()

	now := TimeFixture()
	tags, _ := json.Marshal([]string{"paper"})
	head := revisionID
	category := "research"
	skillName := "论文精读"
	if skillID != "skill1" {
		skillName = skillName + "-" + skillID
	}
	MustCreate(t, db, &SkillRow{
		ID:                 skillID,
		OwnerUserID:        "user_001",
		OwnerUserName:      "张三",
		CreateUserID:       "user_001",
		CreateUserName:     "张三",
		Category:           category,
		SkillName:          skillName,
		Description:        "用于阅读和总结论文的技能",
		Tags:               tags,
		RelativeRoot:       category + "/" + skillName,
		SkillMDPath:        "SKILL.md",
		HeadRevisionID:     &head,
		Version:            1,
		AutoEvoApplyStatus: "idle",
		IsEnabled:          true,
		UpdateStatus:       "up_to_date",
		CreatedAt:          now,
		UpdatedAt:          now,
	})
	MustCreate(t, db, &SkillRevisionRow{
		ID:           revisionID,
		SkillID:      skillID,
		RevisionNo:   1,
		TreeHash:     "tree_" + revisionID,
		ChangeSource: "create",
		CreatedAt:    now,
	})
	content := SkillMD(skillName, "用于阅读和总结论文的技能")
	SeedTextBlob(t, db, "h_skill_"+revisionID, content)
	hash := "h_skill_" + revisionID
	MustCreate(t, db, &SkillRevisionEntryRow{
		RevisionID: revisionID,
		Path:       "SKILL.md",
		EntryType:  "file",
		BlobHash:   &hash,
		Size:       int64(len([]byte(content))),
		Mime:       "text/markdown",
		FileType:   "markdown",
		Mode:       420,
	})
	MustCreate(t, db, &SkillDraftRow{
		SkillID:        skillID,
		BaseRevisionID: &head,
		TaskID:         "",
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
}

func SkillMD(name, description string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n# %s\n", name, description, name)
}

func SeedTextBlob(t *testing.T, db *TestDB, hash, content string) {
	t.Helper()
	MustCreate(t, db, &SkillBlobRow{
		Hash:           hash,
		Size:           int64(len([]byte(content))),
		Mime:           "text/markdown",
		FileType:       "markdown",
		Binary:         false,
		StorageBackend: "postgres",
		Content:        []byte(content),
		CreatedAt:      TimeFixture(),
	})
}

func SeedBinaryBlob(t *testing.T, db *TestDB, hash string) {
	t.Helper()
	key := "objects/" + hash
	MustCreate(t, db, &SkillBlobRow{
		Hash:           hash,
		Size:           int64(len(MinimalPNGBytes())),
		Mime:           "image/png",
		FileType:       "image",
		Binary:         true,
		StorageBackend: "local_file",
		StorageKey:     &key,
		CreatedAt:      TimeFixture(),
	})
}

func SeedRevisionEntry(t *testing.T, db *TestDB, revisionID, path, entryType, blobHash, fileType string) {
	t.Helper()
	var hashPtr *string
	if blobHash != "" {
		hashPtr = &blobHash
	}
	MustCreate(t, db, &SkillRevisionEntryRow{
		RevisionID: revisionID,
		Path:       path,
		EntryType:  entryType,
		BlobHash:   hashPtr,
		Size:       10,
		Mime:       "text/markdown",
		FileType:   fileType,
		Mode:       420,
	})
}

func SeedDraftEntry(t *testing.T, db *TestDB, skillID, path, op, entryType, blobHash string) {
	t.Helper()
	var hashPtr *string
	if blobHash != "" {
		hashPtr = &blobHash
	}
	MustCreate(t, db, &SkillDraftEntryRow{
		SkillID:   skillID,
		Path:      path,
		Op:        op,
		EntryType: entryType,
		BlobHash:  hashPtr,
		Size:      10,
		Mime:      "text/markdown",
		FileType:  "markdown",
		Mode:      420,
		UpdatedAt: TimeFixture(),
	})
}
