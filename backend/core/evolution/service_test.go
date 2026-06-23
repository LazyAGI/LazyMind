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

func TestBuildChatResourceContextCreatesPerUserResourcesAndSnapshots(t *testing.T) {
	db := newTestDB(t)

	relativePath := ParentSkillRelativePath("coding", "git-workflow")
	content := "---\nname: git-workflow\ndescription: git workflow\n---\nbody"

	now := time.Now()
	skill := orm.SkillResource{
		ID:              "skill-1",
		OwnerUserID:     "u1",
		OwnerUserName:   "User 1",
		Category:        "coding",
		ParentSkillName: "git-workflow",
		SkillName:       "git-workflow",
		NodeType:        SkillNodeTypeParent,
		FileExt:         "md",
		RelativePath:    relativePath,
		Content:         content,
		ContentSize:     int64(len([]byte(content))),
		MimeType:        "text/markdown; charset=utf-8",
		ContentHash:     HashContent(content),
		IsEnabled:       true,
		UpdateStatus:    UpdateStatusUpToDate,
		CreateUserID:    "u1",
		CreateUserName:  "User 1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&skill).Error; err != nil {
		t.Fatalf("create skill: %v", err)
	}

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

	var memoryCount int64
	if err := db.Model(&orm.SystemMemory{}).Count(&memoryCount).Error; err != nil {
		t.Fatalf("count system_memories: %v", err)
	}
	if memoryCount != 2 {
		t.Fatalf("expected 2 system memory rows, got %d", memoryCount)
	}

	var preferenceCount int64
	if err := db.Model(&orm.SystemUserPreference{}).Count(&preferenceCount).Error; err != nil {
		t.Fatalf("count system_user_preferences: %v", err)
	}
	if preferenceCount != 2 {
		t.Fatalf("expected 2 system user preference rows, got %d", preferenceCount)
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
	if skillSnapshot.ResourceKey != skill.ID {
		t.Fatalf("expected skill snapshot resource_key to use skill id %q, got %q", skill.ID, skillSnapshot.ResourceKey)
	}
	if skillSnapshot.RelativePath != relativePath {
		t.Fatalf("expected skill snapshot relative_path %q, got %q", relativePath, skillSnapshot.RelativePath)
	}

	var memories []orm.SystemMemory
	if err := db.Order("user_id ASC").Find(&memories).Error; err != nil {
		t.Fatalf("list system_memories: %v", err)
	}
	if len(memories) != 2 || memories[0].UserID != "u1" || memories[1].UserID != "u2" {
		t.Fatalf("expected per-user memory rows for u1/u2, got %#v", memories)
	}
	if ctx.Memory != FormatSystemMemoryForChat(memories[0]) {
		t.Fatalf("expected formatted memory context, got %q", ctx.Memory)
	}

	var prefs []orm.SystemUserPreference
	if err := db.Order("user_id ASC").Find(&prefs).Error; err != nil {
		t.Fatalf("list system_user_preferences: %v", err)
	}
	if len(prefs) != 2 || prefs[0].UserID != "u1" || prefs[1].UserID != "u2" {
		t.Fatalf("expected per-user preference rows for u1/u2, got %#v", prefs)
	}
	if ctx.UserPreference != FormatSystemUserPreferenceForChat(prefs[0]) {
		t.Fatalf("expected formatted preference context, got %q", ctx.UserPreference)
	}
}

func TestBuildChatResourceContextFormatsUserPreferenceForChat(t *testing.T) {
	db := newTestDB(t)

	now := time.Now()
	memory := orm.SystemMemory{
		ID:            "memory-1",
		UserID:        "u1",
		Content:       "memory-content",
		Version:       1,
		UpdatedBy:     "u1",
		UpdatedByName: "User 1",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	memory.ContentHash = HashSystemMemory(memory)
	if err := db.Create(&memory).Error; err != nil {
		t.Fatalf("create memory: %v", err)
	}
	preference := orm.SystemUserPreference{
		ID:            "preference-1",
		UserID:        "u1",
		Content:       "记住用户偏好简洁回答",
		AgentPersona:  "资深研究助理",
		PreferredName: "老师",
		ResponseStyle: "先结论后解释",
		Version:       1,
		UpdatedBy:     "u1",
		UpdatedByName: "User 1",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	preference.ContentHash = HashSystemUserPreference(preference)
	if err := db.Create(&preference).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}

	ctx, err := BuildChatResourceContext(context.Background(), db.DB, "u1", "User 1", "session-memory")
	if err != nil {
		t.Fatalf("build chat resource context: %v", err)
	}

	want := "---\nagent_persona: |-\n 资深研究助理\npreferred_name: |-\n 老师\nresponse_style: |-\n 先结论后解释\n---\n\n记住用户偏好简洁回答"
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
