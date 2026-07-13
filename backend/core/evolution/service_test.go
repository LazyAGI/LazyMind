package evolution

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/resourcefs"
)

func newTestDB(t *testing.T) *orm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := orm.Connect(orm.DriverSQLite, dbPath)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(orm.AllModelsForDDL()...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
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

func commitTestPersonalResource(t *testing.T, db *orm.DB, userID string, resourceType resourcefs.ResourceType, content string) {
	t.Helper()
	ctx := context.Background()
	service := resourcefs.NewService(resourcefs.ServiceDeps{DB: db.DB})
	ref := resourcefs.ResourceRef{UserID: userID, ResourceType: resourceType}
	if _, err := service.EnsureResource(ctx, ref, ""); err != nil {
		t.Fatalf("ensure personal resource: %v", err)
	}
	draft, err := service.ReadFile(ctx, resourcefs.ReadFileRequest{Ref: ref, RefType: resourcefs.FileRefDraft})
	if err != nil {
		t.Fatalf("read personal resource draft: %v", err)
	}
	written, err := service.WriteDraft(ctx, resourcefs.WriteDraftRequest{
		Ref:                  ref,
		Content:              content,
		ExpectedDraftVersion: draft.DraftVersion,
		UpdatedBy:            userID,
	})
	if err != nil {
		t.Fatalf("write personal resource draft: %v", err)
	}
	if _, err := service.CommitDraft(ctx, resourcefs.CommitDraftRequest{
		Ref:                  ref,
		Message:              "test commit",
		ExpectedDraftVersion: written.DraftVersion,
		CreatedBy:            userID,
	}); err != nil {
		t.Fatalf("commit personal resource draft: %v", err)
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
	if ctx.Memory != FormatSystemMemoryForChat(orm.SystemMemory{}) || ctx.UserPreference != FormatSystemUserPreferenceForChat(orm.SystemUserPreference{}) {
		t.Fatalf("expected empty user-scoped content, got memory=%q preference=%q", ctx.Memory, ctx.UserPreference)
	}
	if !ctx.UsePersonalization {
		t.Fatalf("expected personalization enabled by default")
	}

	secondCtx, err := BuildChatResourceContext(context.Background(), db.DB, "u2", "User 2", "session-2")
	if err != nil {
		t.Fatalf("build second chat resource context: %v", err)
	}
	if secondCtx.Memory != FormatSystemMemoryForChat(orm.SystemMemory{}) || secondCtx.UserPreference != FormatSystemUserPreferenceForChat(orm.SystemUserPreference{}) {
		t.Fatalf("expected empty second user-scoped content, got memory=%q preference=%q", secondCtx.Memory, secondCtx.UserPreference)
	}
	if !secondCtx.UsePersonalization {
		t.Fatalf("expected second user personalization enabled by default")
	}

	var personalResourceCount int64
	if err := db.Model(&orm.PersonalResource{}).Count(&personalResourceCount).Error; err != nil {
		t.Fatalf("count personal resources: %v", err)
	}
	if personalResourceCount != 4 {
		t.Fatalf("expected 4 personal resource rows, got %d", personalResourceCount)
	}

	var snapshotCount int64
	if err := db.Model(&orm.ResourceSessionSnapshot{}).Where("session_id = ?", "session-1").Count(&snapshotCount).Error; err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if snapshotCount != 3 {
		t.Fatalf("expected 3 snapshots, got %d", snapshotCount)
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

	var resources []orm.PersonalResource
	if err := db.Order("user_id ASC, resource_type ASC").Find(&resources).Error; err != nil {
		t.Fatalf("list personal resources: %v", err)
	}
	if len(resources) != 4 || resources[0].UserID != "u1" || resources[2].UserID != "u2" {
		t.Fatalf("expected per-user personal resource rows for u1/u2, got %#v", resources)
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

func TestBuildChatResourceContextFormatsUserPreferenceForChat(t *testing.T) {
	db := newTestDB(t)

	preference := orm.SystemUserPreference{
		Content:       "记住用户偏好简洁回答",
		AgentPersona:  "资深研究助理",
		PreferredName: "老师",
		ResponseStyle: "先结论后解释",
	}
	want := FormatSystemUserPreferenceForChat(preference)
	commitTestPersonalResource(t, db, "u1", resourcefs.ResourceTypeMemory, "memory-content")
	commitTestPersonalResource(t, db, "u1", resourcefs.ResourceTypeUserPreference, want)

	ctx, err := BuildChatResourceContext(context.Background(), db.DB, "u1", "User 1", "session-memory")
	if err != nil {
		t.Fatalf("build chat resource context: %v", err)
	}

	if ctx.Memory != "memory-content" {
		t.Fatalf("unexpected memory context: %q", ctx.Memory)
	}
	if ctx.UserPreference != want {
		t.Fatalf("unexpected formatted preference:\n%s", ctx.UserPreference)
	}

	var snapshot orm.ResourceSessionSnapshot
	if err := db.Where("session_id = ? AND resource_type = ?", "session-memory", ResourceTypeUserPreference).Take(&snapshot).Error; err != nil {
		t.Fatalf("query preference snapshot: %v", err)
	}
	if snapshot.SnapshotHash != HashContent(want) {
		t.Fatalf("expected snapshot hash to use formatted preference, got %q want %q", snapshot.SnapshotHash, HashContent(want))
	}
}

func TestParseSystemUserPreferenceContentRequiresFrontmatterFields(t *testing.T) {
	parsed, err := ParseSystemUserPreferenceContent("---\nagent_persona: 角色\npreferred_name: 用户称谓\nresponse_style: 回复风格\n---\n")
	if err != nil {
		t.Fatalf("parse metadata-only preference: %v", err)
	}
	if parsed.Content != "" || parsed.AgentPersona != "角色" || parsed.PreferredName != "用户称谓" || parsed.ResponseStyle != "回复风格" {
		t.Fatalf("unexpected parsed preference: %#v", parsed)
	}

	if _, err := ParseSystemUserPreferenceContent("---\nagent_persona: 角色\npreferred_name: 用户称谓\n---\n正文"); err == nil {
		t.Fatal("expected missing response_style to fail")
	}
}

func TestResolveRequestUserIgnoresFallbackAndUsesSessionSnapshot(t *testing.T) {
	db := newTestDB(t)

	now := time.Now()
	snapshot := orm.ResourceSessionSnapshot{
		ID:           "snapshot-1",
		SessionID:    "session-1",
		UserID:       "session-user",
		ResourceType: ResourceTypeMemory,
		ResourceKey:  SystemResourceKey(ResourceTypeMemory),
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
