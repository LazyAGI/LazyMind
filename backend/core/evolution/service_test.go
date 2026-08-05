package evolution

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func newTestDB(t *testing.T) *orm.DB {
	t.Helper()

	return orm.MigrateAllModelsForTest(t)
}

func createPublishedV2Skill(t *testing.T, db *orm.DB, id, userID, userName, category, skillName, content string) {
	t.Helper()
	now := time.Now()
	revisionID := id + "-rev-1"
	hash := HashContent(content)
	if err := db.Create(&orm.SkillV2Skill{
		ID:                 id,
		OwnerUserID:        userID,
		OwnerUserName:      userName,
		CreateUserID:       userID,
		CreateUserName:     userName,
		Category:           category,
		SkillName:          skillName,
		Tags:               []byte("[]"),
		RelativeRoot:       filepath.ToSlash(filepath.Join(category, skillName)),
		SkillMDPath:        "SKILL.md",
		HeadRevisionID:     &revisionID,
		Version:            1,
		AutoEvoApplyStatus: "idle",
		IsEnabled:          true,
		UpdateStatus:       "up_to_date",
		CreatedAt:          now,
		UpdatedAt:          now,
	}).Error; err != nil {
		t.Fatalf("create v2 skill: %v", err)
	}
	if err := db.Create(&orm.SkillV2Revision{
		ID:           revisionID,
		SkillID:      id,
		RevisionNo:   1,
		TreeHash:     hash,
		ChangeSource: "create",
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create v2 revision: %v", err)
	}
	if err := db.Create(&orm.SkillV2Blob{
		Hash:           hash,
		Size:           int64(len([]byte(content))),
		Mime:           "text/markdown; charset=utf-8",
		FileType:       "markdown",
		Binary:         false,
		StorageBackend: "postgres",
		Content:        []byte(content),
		CreatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create v2 blob: %v", err)
	}
	if err := db.Create(&orm.SkillV2RevisionEntry{
		RevisionID: revisionID,
		Path:       "SKILL.md",
		EntryType:  "file",
		BlobHash:   &hash,
		Size:       int64(len([]byte(content))),
		Mime:       "text/markdown; charset=utf-8",
		FileType:   "markdown",
		Mode:       420,
	}).Error; err != nil {
		t.Fatalf("create v2 revision entry: %v", err)
	}
}

func TestBuildChatResourceContextCreatesPerUserResourcesAndSnapshots(t *testing.T) {
	db := newTestDB(t)

	relativePath := ParentSkillRelativePath("coding", "git-workflow")
	content := "---\nname: git-workflow\ndescription: git workflow\n---\nbody"
	createPublishedV2Skill(t, db, "skill-1", "u1", "User 1", "coding", "git-workflow", content)

	ctx, err := BuildChatResourceContext(context.Background(), db.DB, "u1", "User 1", "session-1")
	if err != nil {
		t.Fatalf("build chat resource context: %v", err)
	}
	if len(ctx.DisabledTools) != 0 {
		t.Fatalf("unexpected disabled_tools: %#v", ctx.DisabledTools)
	}
	if len(ctx.AvailableSkills) != 1 || ctx.AvailableSkills[0] != "coding/git-workflow" {
		t.Fatalf("unexpected available_skills: %#v", ctx.AvailableSkills)
	}
	if !ctx.UsePersonalization {
		t.Fatalf("expected personalization enabled by default")
	}

	secondCtx, err := BuildChatResourceContext(context.Background(), db.DB, "u2", "User 2", "session-2")
	if err != nil {
		t.Fatalf("build second chat resource context: %v", err)
	}
	if !secondCtx.UsePersonalization {
		t.Fatalf("expected second user personalization enabled by default")
	}

	var snapshotCount int64
	if err := db.Model(&orm.ResourceSessionSnapshot{}).Where("session_id = ?", "session-1").Count(&snapshotCount).Error; err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if snapshotCount != 1 {
		t.Fatalf("expected only the skill snapshot, got %d", snapshotCount)
	}
	var skillSnapshot orm.ResourceSessionSnapshot
	if err := db.Where("session_id = ? AND resource_type = ?", "session-1", ResourceTypeSkill).Take(&skillSnapshot).Error; err != nil {
		t.Fatalf("query skill snapshot: %v", err)
	}
	if skillSnapshot.ResourceKey != "skill-1" {
		t.Fatalf("expected skill snapshot resource_key to use skill id %q, got %q", "skill-1", skillSnapshot.ResourceKey)
	}
	if skillSnapshot.RelativePath != relativePath {
		t.Fatalf("expected skill snapshot relative_path %q, got %q", relativePath, skillSnapshot.RelativePath)
	}

}

func TestBuildChatResourceContextSkipsInvalidEnabledV2Skill(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()

	validRevisionID := "rev-valid"
	validHash := "hash-valid"
	if err := db.Create(&orm.SkillV2Skill{
		ID:                 "skill-valid",
		OwnerUserID:        "u1",
		OwnerUserName:      "User 1",
		CreateUserID:       "u1",
		CreateUserName:     "User 1",
		Category:           "research",
		SkillName:          "valid-skill",
		Tags:               []byte("[]"),
		RelativeRoot:       "research/valid-skill",
		SkillMDPath:        "SKILL.md",
		HeadRevisionID:     &validRevisionID,
		Version:            1,
		AutoEvoApplyStatus: "idle",
		IsEnabled:          true,
		UpdateStatus:       "up_to_date",
		CreatedAt:          now,
		UpdatedAt:          now,
	}).Error; err != nil {
		t.Fatalf("create valid v2 skill: %v", err)
	}
	if err := db.Create(&orm.SkillV2Revision{
		ID:           validRevisionID,
		SkillID:      "skill-valid",
		RevisionNo:   1,
		TreeHash:     "tree-valid",
		ChangeSource: "create",
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create valid revision: %v", err)
	}
	if err := db.Create(&orm.SkillV2Blob{
		Hash:           validHash,
		Size:           int64(len([]byte("# valid\n"))),
		Mime:           "text/markdown",
		FileType:       "markdown",
		Binary:         false,
		StorageBackend: "postgres",
		Content:        []byte("# valid\n"),
		CreatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create valid blob: %v", err)
	}
	if err := db.Create(&orm.SkillV2RevisionEntry{
		RevisionID: validRevisionID,
		Path:       "SKILL.md",
		EntryType:  "file",
		BlobHash:   &validHash,
		Size:       int64(len([]byte("# valid\n"))),
		Mime:       "text/markdown",
		FileType:   "markdown",
		Mode:       420,
	}).Error; err != nil {
		t.Fatalf("create valid revision entry: %v", err)
	}

	invalidRevisionID := "rev-invalid"
	if err := db.Create(&orm.SkillV2Skill{
		ID:                 "skill-invalid",
		OwnerUserID:        "u1",
		OwnerUserName:      "User 1",
		CreateUserID:       "u1",
		CreateUserName:     "User 1",
		Category:           "research",
		SkillName:          "invalid-skill",
		Tags:               []byte("[]"),
		RelativeRoot:       "research/invalid-skill",
		SkillMDPath:        "SKILL.md",
		HeadRevisionID:     &invalidRevisionID,
		Version:            1,
		AutoEvoApplyStatus: "idle",
		IsEnabled:          true,
		UpdateStatus:       "up_to_date",
		CreatedAt:          now,
		UpdatedAt:          now,
	}).Error; err != nil {
		t.Fatalf("create invalid v2 skill: %v", err)
	}
	if err := db.Create(&orm.SkillV2Revision{
		ID:           invalidRevisionID,
		SkillID:      "skill-invalid",
		RevisionNo:   1,
		TreeHash:     "tree-invalid",
		ChangeSource: "create",
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create invalid revision: %v", err)
	}

	ctx, err := BuildChatResourceContext(context.Background(), db.DB, "u1", "User 1", "session-v2")
	if err != nil {
		t.Fatalf("build chat resource context: %v", err)
	}
	if len(ctx.AvailableSkills) != 1 || ctx.AvailableSkills[0] != "research/valid-skill" {
		t.Fatalf("unexpected available_skills: %#v", ctx.AvailableSkills)
	}

	var skillSnapshotCount int64
	if err := db.Model(&orm.ResourceSessionSnapshot{}).Where("session_id = ? AND resource_type = ?", "session-v2", ResourceTypeSkill).Count(&skillSnapshotCount).Error; err != nil {
		t.Fatalf("count skill snapshots: %v", err)
	}
	if skillSnapshotCount != 1 {
		t.Fatalf("skill snapshot count = %d, want 1", skillSnapshotCount)
	}
}

func TestResolveRequestUserIgnoresFallbackAndUsesSessionSnapshot(t *testing.T) {
	db := newTestDB(t)

	now := time.Now()
	snapshot := orm.ResourceSessionSnapshot{
		ID:           "snapshot-1",
		SessionID:    "session-1",
		UserID:       "session-user",
		ResourceType: "memory",
		ResourceKey:  "memory",
		SnapshotHash: HashContent(""),
		CreatedAt:    now,
	}
	if err := db.Create(&snapshot).Error; err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	userID, userName, err := ResolveRequestUser(context.Background(), db.DB, "session-1", "header-user", "Header User")
	if err != nil {
		t.Fatalf("resolve request user: %v", err)
	}
	if userID != "session-user" {
		t.Fatalf("expected session user, got %q", userID)
	}
	if userName != "" {
		t.Fatalf("expected empty user name when conversation is absent, got %q", userName)
	}
}

func TestResolveRequestUserFallsBackToConversationOwner(t *testing.T) {
	db := newTestDB(t)

	now := time.Now()
	conversation := orm.Conversation{
		ID:          "conv-2",
		DisplayName: "Conversation 2",
		BaseModel: orm.BaseModel{
			CreateUserID:   "conversation-user",
			CreateUserName: "Conversation User",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	userID, userName, err := ResolveRequestUser(context.Background(), db.DB, "conv-2_1710000000000", "header-user", "Header User")
	if err != nil {
		t.Fatalf("resolve request user: %v", err)
	}
	if userID != "conversation-user" {
		t.Fatalf("expected conversation owner, got %q", userID)
	}
	if userName != "Conversation User" {
		t.Fatalf("expected conversation owner name, got %q", userName)
	}
}

func TestResolveRequestUserOnlyStripsTimestampSuffix(t *testing.T) {
	db := newTestDB(t)

	now := time.Now()
	conversation := orm.Conversation{
		ID:          "conv_with_under_score",
		DisplayName: "Conversation with underscore",
		BaseModel: orm.BaseModel{
			CreateUserID:   "conversation-user",
			CreateUserName: "Conversation User",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	userID, userName, err := ResolveRequestUser(context.Background(), db.DB, "conv_with_under_score_1710000000000", "header-user", "Header User")
	if err != nil {
		t.Fatalf("resolve request user: %v", err)
	}
	if userID != "conversation-user" || userName != "Conversation User" {
		t.Fatalf("expected conversation owner, got user_id=%q user_name=%q", userID, userName)
	}
	if got := conversationIDFromSessionID("conv_with_under_score_notatime"); got != "conv_with_under_score_notatime" {
		t.Fatalf("expected non-timestamp suffix to be preserved, got %q", got)
	}
}
