package orm

import (
	"path/filepath"
	"testing"
)

// TestUserConfigModelsAutoMigrate verifies that user configuration tables are created correctly.
func TestUserConfigModelsAutoMigrate(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "user-config.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}

	if err := db.AutoMigrate(&UserChatSettings{}, &UserUIPreferences{}); err != nil {
		t.Fatalf("auto migrate user config models: %v", err)
	}

	// Verify tables exist.
	for _, model := range []any{
		&UserChatSettings{},
		&UserUIPreferences{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

	// Verify UserChatSettings columns.
	if !db.Migrator().HasColumn(&UserChatSettings{}, "user_id") {
		t.Fatal("expected user_chat_settings.user_id column")
	}
	if !db.Migrator().HasColumn(&UserChatSettings{}, "enable_plugin") {
		t.Fatal("expected user_chat_settings.enable_plugin column")
	}
	if !db.Migrator().HasColumn(&UserChatSettings{}, "enable_subagent") {
		t.Fatal("expected user_chat_settings.enable_subagent column")
	}
	if !db.Migrator().HasColumn(&UserChatSettings{}, "plugin_mode") {
		t.Fatal("expected user_chat_settings.plugin_mode column")
	}

	// Verify UserUIPreferences columns.
	if !db.Migrator().HasColumn(&UserUIPreferences{}, "user_id") {
		t.Fatal("expected user_ui_preferences.user_id column")
	}
}

// TestMCPServerModelsAutoMigrate verifies that MCP server and tool tables are created correctly.
func TestMCPServerModelsAutoMigrate(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}

	if err := db.AutoMigrate(&MCPServer{}, &MCPServerTool{}); err != nil {
		t.Fatalf("auto migrate mcp models: %v", err)
	}

	// Verify tables exist.
	for _, model := range []any{
		&MCPServer{},
		&MCPServerTool{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

	// Verify MCPServer columns.
	if !db.Migrator().HasColumn(&MCPServer{}, "id") {
		t.Fatal("expected mcp_servers.id column")
	}
	if !db.Migrator().HasColumn(&MCPServer{}, "name") {
		t.Fatal("expected mcp_servers.name column")
	}
	if !db.Migrator().HasColumn(&MCPServer{}, "url") {
		t.Fatal("expected mcp_servers.url column")
	}

	// Verify MCPServerTool columns.
	if !db.Migrator().HasColumn(&MCPServerTool{}, "mcp_server_id") {
		t.Fatal("expected mcp_server_tools.mcp_server_id column")
	}
	if !db.Migrator().HasColumn(&MCPServerTool{}, "tool_name") {
		t.Fatal("expected mcp_server_tools.tool_name column")
	}
}

// TestSubAgentModelsAutoMigrate verifies that sub-agent tables are created correctly.
func TestSubAgentModelsAutoMigrate(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "subagent.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}

	if err := db.AutoMigrate(&SubAgentTask{}, &SubAgentStep{}, &SubAgentArtifact{}); err != nil {
		t.Fatalf("auto migrate sub-agent models: %v", err)
	}

	// Verify tables exist.
	for _, model := range []any{
		&SubAgentTask{},
		&SubAgentStep{},
		&SubAgentArtifact{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

	// Verify SubAgentTask columns.
	if !db.Migrator().HasColumn(&SubAgentTask{}, "id") {
		t.Fatal("expected sub_agent_tasks.id column")
	}
	if !db.Migrator().HasColumn(&SubAgentTask{}, "conversation_id") {
		t.Fatal("expected sub_agent_tasks.conversation_id column")
	}
	if !db.Migrator().HasColumn(&SubAgentTask{}, "status") {
		t.Fatal("expected sub_agent_tasks.status column")
	}

	// Verify SubAgentArtifact columns.
	if !db.Migrator().HasColumn(&SubAgentArtifact{}, "slot") {
		t.Fatal("expected sub_agent_artifacts.slot column")
	}
	if !db.Migrator().HasColumn(&SubAgentArtifact{}, "content_type") {
		t.Fatal("expected sub_agent_artifacts.content_type column")
	}
}
