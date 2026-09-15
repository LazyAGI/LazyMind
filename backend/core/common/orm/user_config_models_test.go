package orm

import (
	"testing"
)

func TestUserConfigModelsAutoMigrate(t *testing.T) {
	db := MigrateTestDB(t, &UserChatSettings{}, &UserUIPreferences{})

	for _, model := range []any{
		&UserChatSettings{},
		&UserUIPreferences{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

	if !db.Migrator().HasColumn(&UserChatSettings{}, "user_id") {
		t.Fatal("expected user_chat_settings.user_id column")
	}
	if !db.Migrator().HasColumn(&UserChatSettings{}, "enable_workflow") {
		t.Fatal("expected user_chat_settings.enable_workflow column")
	}
	if !db.Migrator().HasColumn(&UserChatSettings{}, "enable_subagent") {
		t.Fatal("expected user_chat_settings.enable_subagent column")
	}
	if !db.Migrator().HasColumn(&UserChatSettings{}, "plugin_mode") {
		t.Fatal("expected user_chat_settings.plugin_mode column")
	}
	for _, column := range []string{"quick_question_defaults", "new_task_defaults"} {
		if !db.Migrator().HasColumn(&UserChatSettings{}, column) {
			t.Fatalf("expected user_chat_settings.%s column", column)
		}
	}

	if !db.Migrator().HasColumn(&UserUIPreferences{}, "user_id") {
		t.Fatal("expected user_ui_preferences.user_id column")
	}
	if !db.Migrator().HasColumn(&UserUIPreferences{}, "schedules_enabled") {
		t.Fatal("expected user_ui_preferences.schedules_enabled column")
	}
	if !db.Migrator().HasColumn(&UserUIPreferences{}, "performance_stats_enabled") {
		t.Fatal("expected user_ui_preferences.performance_stats_enabled column")
	}
}

func TestConversationThinkingDepthAutoMigrate(t *testing.T) {
	db := MigrateTestDB(t, &Conversation{})
	if !db.Migrator().HasColumn(&Conversation{}, "thinking_depth") {
		t.Fatal("expected conversations.thinking_depth column")
	}
}

func TestMCPServerModelsAutoMigrate(t *testing.T) {
	db := MigrateTestDB(t, &MCPServer{}, &MCPServerTool{})

	for _, model := range []any{
		&MCPServer{},
		&MCPServerTool{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

	if !db.Migrator().HasColumn(&MCPServer{}, "id") {
		t.Fatal("expected mcp_servers.id column")
	}
	if !db.Migrator().HasColumn(&MCPServer{}, "name") {
		t.Fatal("expected mcp_servers.name column")
	}
	if !db.Migrator().HasColumn(&MCPServer{}, "url") {
		t.Fatal("expected mcp_servers.url column")
	}

	if !db.Migrator().HasColumn(&MCPServerTool{}, "mcp_server_id") {
		t.Fatal("expected mcp_server_tools.mcp_server_id column")
	}
	if !db.Migrator().HasColumn(&MCPServerTool{}, "tool_name") {
		t.Fatal("expected mcp_server_tools.tool_name column")
	}
}

func TestSubAgentModelsAutoMigrate(t *testing.T) {
	db := MigrateTestDB(t, &SubAgentTask{}, &SubAgentStep{}, &SubAgentArtifact{})

	for _, model := range []any{
		&SubAgentTask{},
		&SubAgentStep{},
		&SubAgentArtifact{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

	if !db.Migrator().HasColumn(&SubAgentTask{}, "id") {
		t.Fatal("expected sub_agent_tasks.id column")
	}
	if !db.Migrator().HasColumn(&SubAgentTask{}, "conversation_id") {
		t.Fatal("expected sub_agent_tasks.conversation_id column")
	}
	if !db.Migrator().HasColumn(&SubAgentTask{}, "status") {
		t.Fatal("expected sub_agent_tasks.status column")
	}

	if !db.Migrator().HasColumn(&SubAgentArtifact{}, "slot") {
		t.Fatal("expected sub_agent_artifacts.slot column")
	}
	if !db.Migrator().HasColumn(&SubAgentArtifact{}, "content_type") {
		t.Fatal("expected sub_agent_artifacts.content_type column")
	}
}
