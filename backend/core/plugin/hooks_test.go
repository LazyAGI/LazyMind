package plugin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

// TestLoadPluginChatContextFromDB_MissingTask returns nil for nonexistent task.
func TestLoadPluginChatContextFromDB_MissingTask(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	got := loadPluginChatContextFromDB(ctx, db.DB, "nonexistent")
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestLoadPluginChatContextFromDB_WrongAgentType returns nil.
func TestLoadPluginChatContextFromDB_WrongAgentType(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	db.DB.Create(&orm.SubAgentTask{
		ID: "t1", ConversationID: "c1", AgentType: "code-gen", Title: "Test",
		Objective: "test", Mode: "auto", Status: "running",
		CreateUserID: "u1", CreatedAt: now, UpdatedAt: now, LastHeartbeat: now,
		InputSlots: json.RawMessage("[]"), OutputSlots: json.RawMessage("[]"),
	})
	got := loadPluginChatContextFromDB(ctx, db.DB, "t1")
	if got != nil {
		t.Fatalf("got %v, want nil for non-plugin_step agent type", got)
	}
}

// TestLoadPluginChatContextFromDB_ValidTask returns PluginChatContext with fields.
func TestLoadPluginChatContextFromDB_ValidTask(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	params := PluginStepParams{
		PluginID:      "test-plugin",
		SessionID:     "sess-1",
		StepID:        "step_a",
		PluginMode:    "auto",
		ChatSessionID: "chat-sess-1",
	}
	paramsBytes, _ := json.Marshal(params)
	db.DB.Create(&orm.SubAgentTask{
		ID: "t2", ConversationID: "c2", AgentType: "plugin_step", Title: "Plugin",
		Objective: "run", Mode: "auto", Status: "running",
		CreateUserID: "u1", TriggerHistoryID: "hist-1",
		Params:    json.RawMessage(paramsBytes),
		CreatedAt: now, UpdatedAt: now, LastHeartbeat: now,
		InputSlots: json.RawMessage("[]"), OutputSlots: json.RawMessage("[]"),
	})
	got := loadPluginChatContextFromDB(ctx, db.DB, "t2")
	if got == nil {
		t.Fatal("expected non-nil context")
	}
	if got.PluginID != "test-plugin" || got.SessionID != "sess-1" {
		t.Fatalf("unexpected context: %+v", got)
	}
	if got.ConvID != "c2" || got.UserID != "u1" {
		t.Fatalf("conv/user mismatch: %+v", got)
	}
}

// TestLoadPluginChatContextFromDB_MissingParams returns nil.
func TestLoadPluginChatContextFromDB_MissingParams(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	db.DB.Create(&orm.SubAgentTask{
		ID: "t3", ConversationID: "c3", AgentType: "plugin_step", Title: "Plugin",
		Objective: "run", Mode: "auto", Status: "running",
		CreateUserID: "u1", CreatedAt: now, UpdatedAt: now, LastHeartbeat: now,
		InputSlots: json.RawMessage("[]"), OutputSlots: json.RawMessage("[]"),
	})
	got := loadPluginChatContextFromDB(ctx, db.DB, "t3")
	if got != nil {
		t.Fatalf("got %v, want nil for missing params", got)
	}
}
