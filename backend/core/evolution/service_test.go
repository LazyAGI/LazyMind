package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
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
		UserAddress:   "老师",
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

	want := "---\nagent_persona: |-\n  资深研究助理\nuser_address: |-\n  老师\nresponse_style: |-\n  先结论后解释\n---\n\n记住用户偏好简洁回答"
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
	parsed, err := ParseSystemUserPreferenceContent("---\nagent_persona: 角色\nuser_address: 用户称谓\nresponse_style: 回复风格\n---\n")
	if err != nil {
		t.Fatalf("parse metadata-only preference: %v", err)
	}
	if parsed.Content != "" || parsed.AgentPersona != "角色" || parsed.UserAddress != "用户称谓" || parsed.ResponseStyle != "回复风格" {
		t.Fatalf("unexpected parsed preference: %#v", parsed)
	}

	if _, err := ParseSystemUserPreferenceContent("---\nagent_persona: 角色\nuser_address: 用户称谓\n---\n正文"); err == nil {
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

func TestLoadApprovedSuggestionsFiltersByUser(t *testing.T) {
	db := newTestDB(t)

	now := time.Now()
	rows := []orm.ResourceSuggestion{
		{
			ID:           "s-u1",
			UserID:       "u1",
			ResourceType: ResourceTypeSkill,
			ResourceKey:  "skill-1",
			Action:       SuggestionActionModify,
			SessionID:    "session-u1",
			Title:        "u1 accepted",
			Content:      "update skill for u1",
			Status:       SuggestionStatusAccepted,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "s-u2",
			UserID:       "u2",
			ResourceType: ResourceTypeSkill,
			ResourceKey:  "skill-1",
			Action:       SuggestionActionModify,
			SessionID:    "session-u2",
			Title:        "u2 accepted",
			Content:      "update skill for u2",
			Status:       SuggestionStatusAccepted,
			CreatedAt:    now.Add(time.Second),
			UpdatedAt:    now.Add(time.Second),
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create suggestions: %v", err)
	}

	got, err := LoadApprovedSuggestions(context.Background(), db.DB, "u1", ResourceTypeSkill, "skill-1", nil)
	if err != nil {
		t.Fatalf("load accepted suggestions: %v", err)
	}
	if len(got) != 1 || got[0].ID != "s-u1" {
		t.Fatalf("expected only u1 suggestion, got %#v", got)
	}
}

func TestLoadAutoApplicableSuggestionsIncludesPendingAndAccepted(t *testing.T) {
	db := newTestDB(t)

	now := time.Now()
	rows := []orm.ResourceSuggestion{
		{
			ID:           "s-pending",
			UserID:       "u1",
			ResourceType: ResourceTypeUserPreference,
			ResourceKey:  SystemResourceKey(ResourceTypeUserPreference),
			Action:       SuggestionActionModify,
			Title:        "pending",
			Content:      "pending change",
			Status:       SuggestionStatusPendingReview,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "s-accepted",
			UserID:       "u1",
			ResourceType: ResourceTypeUserPreference,
			ResourceKey:  SystemResourceKey(ResourceTypeUserPreference),
			Action:       SuggestionActionModify,
			Title:        "accepted",
			Content:      "accepted change",
			Status:       SuggestionStatusAccepted,
			CreatedAt:    now.Add(time.Second),
			UpdatedAt:    now.Add(time.Second),
		},
		{
			ID:           "s-applied",
			UserID:       "u1",
			ResourceType: ResourceTypeUserPreference,
			ResourceKey:  SystemResourceKey(ResourceTypeUserPreference),
			Action:       SuggestionActionModify,
			Title:        "applied",
			Content:      "applied change",
			Status:       SuggestionStatusApplied,
			CreatedAt:    now.Add(2 * time.Second),
			UpdatedAt:    now.Add(2 * time.Second),
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create suggestions: %v", err)
	}

	got, err := LoadAutoApplicableSuggestions(context.Background(), db.DB, "u1", ResourceTypeUserPreference, SystemResourceKey(ResourceTypeUserPreference))
	if err != nil {
		t.Fatalf("load auto applicable suggestions: %v", err)
	}
	if len(got) != 2 || got[0].ID != "s-pending" || got[1].ID != "s-accepted" {
		t.Fatalf("expected pending and accepted suggestions in created order, got %#v", got)
	}
}

func TestApplyManagedPreferenceAutoEvolutionAppliesPendingAndAcceptedSuggestions(t *testing.T) {
	db := newTestDB(t)

	var algoBody map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/rewrite" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&algoBody); err != nil {
			t.Fatalf("decode algorithm request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"content": "generated preference"},
		})
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listener unavailable in current test environment: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Shutdown(context.Background()) }()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", fmt.Sprintf("http://%s", listener.Addr().String()))

	now := time.Now()
	row := orm.SystemUserPreference{
		ID:                 "preference-1",
		UserID:             "u1",
		Content:            "current preference",
		ContentHash:        HashContent("current preference"),
		Version:            3,
		DraftContent:       "old draft",
		DraftSourceVersion: 3,
		DraftStatus:        "pending_confirm",
		AutoEvo:            true,
		AutoEvoGeneration:  2,
		Ext:                WithDraftSuggestionIDs(nil, []string{"old-draft-suggestion"}),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}
	suggestions := []orm.ResourceSuggestion{
		{
			ID:           "s-pending",
			UserID:       "u1",
			ResourceType: ResourceTypeUserPreference,
			ResourceKey:  SystemResourceKey(ResourceTypeUserPreference),
			Action:       SuggestionActionModify,
			Title:        "pending",
			Content:      "pending change",
			Status:       SuggestionStatusPendingReview,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "s-accepted",
			UserID:       "u1",
			ResourceType: ResourceTypeUserPreference,
			ResourceKey:  SystemResourceKey(ResourceTypeUserPreference),
			Action:       SuggestionActionModify,
			Title:        "accepted",
			Content:      "accepted change",
			Status:       SuggestionStatusAccepted,
			CreatedAt:    now.Add(time.Second),
			UpdatedAt:    now.Add(time.Second),
		},
	}
	if err := db.Create(&suggestions).Error; err != nil {
		t.Fatalf("create suggestions: %v", err)
	}

	applied, err := applyManagedPreferenceAutoEvolution(context.Background(), db.DB, row)
	if err != nil {
		t.Fatalf("apply auto evolution: %v", err)
	}
	if applied {
		t.Fatalf("expected auto evolution not to apply suggestions through generate")
	}
	if len(algoBody) != 0 {
		t.Fatalf("algorithm should not be called by auto evolution, got %#v", algoBody)
	}
	var updated orm.SystemUserPreference
	if err := db.Where("id = ?", row.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query updated preference: %v", err)
	}
	if updated.Content != row.Content {
		t.Fatalf("expected content to stay unchanged, got %q", updated.Content)
	}
	if updated.Version != row.Version {
		t.Fatalf("expected version %d, got %d", row.Version, updated.Version)
	}
	if updated.DraftStatus != row.DraftStatus || updated.DraftContent != row.DraftContent || updated.DraftSourceVersion != row.DraftSourceVersion {
		t.Fatalf("expected draft to stay unchanged, got status=%q content=%q source=%d", updated.DraftStatus, updated.DraftContent, updated.DraftSourceVersion)
	}
	if gotIDs := DraftSuggestionIDs(updated.Ext); len(gotIDs) != 1 || gotIDs[0] != "old-draft-suggestion" {
		t.Fatalf("expected draft suggestion ids to stay unchanged, got %#v", gotIDs)
	}
	var updatedSuggestions []orm.ResourceSuggestion
	if err := db.Where("id IN ?", []string{"s-pending", "s-accepted"}).Order("created_at ASC").Find(&updatedSuggestions).Error; err != nil {
		t.Fatalf("query updated suggestions: %v", err)
	}
	if len(updatedSuggestions) != 2 || updatedSuggestions[0].Status != SuggestionStatusPendingReview || updatedSuggestions[1].Status != SuggestionStatusAccepted {
		t.Fatalf("expected suggestions to stay unchanged, got %#v", updatedSuggestions)
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
