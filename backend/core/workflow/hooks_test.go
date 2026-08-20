package workflow

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

// TestLoadWorkflowChatContextFromDB_MissingTask returns nil for nonexistent task.
func TestLoadWorkflowChatContextFromDB_MissingTask(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	got := loadWorkflowChatContextFromDB(ctx, db.DB, "nonexistent")
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestWorkflowStepParamsExposePinnedScriptTools(t *testing.T) {
	params := WorkflowStepParams{
		WorkflowID: "test-workflow", RevisionID: "revision-1", TreeHash: "tree-1",
		LegacyTools: []string{"create_list_fixtures"},
		Runtime:     graphengine.RuntimePolicy{PublisherOwnedSlots: []string{"report"}},
	}
	got := params.asMap()
	if got["revision_id"] != "revision-1" || got["tree_hash"] != "tree-1" {
		t.Fatalf("pinned revision metadata missing: %#v", got)
	}
	tools, ok := got["legacy_tools"].([]string)
	if !ok || len(tools) != 1 || tools[0] != "create_list_fixtures" {
		t.Fatalf("compiled script tools missing: %#v", got)
	}
	runtime, ok := got["workflow_runtime"].(graphengine.RuntimePolicy)
	if !ok || len(runtime.PublisherOwnedSlots) != 1 || runtime.PublisherOwnedSlots[0] != "report" {
		t.Fatalf("compiled runtime policy missing: %#v", got)
	}
}

// TestLoadWorkflowChatContextFromDB_WrongAgentType returns nil.
func TestLoadWorkflowChatContextFromDB_WrongAgentType(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	db.DB.Create(&orm.SubAgentTask{
		ID: "t1", ConversationID: "c1", AgentType: "code-gen", Title: "Test",
		Objective: "test", Mode: "auto", Status: "running",
		CreateUserID: "u1", CreatedAt: now, UpdatedAt: now, LastHeartbeat: now,
		InputSlots: json.RawMessage("[]"), OutputSlots: json.RawMessage("[]"),
	})
	got := loadWorkflowChatContextFromDB(ctx, db.DB, "t1")
	if got != nil {
		t.Fatalf("got %v, want nil for non-workflow_step agent type", got)
	}
}

// TestLoadWorkflowChatContextFromDB_ValidTask returns WorkflowChatContext with fields.
func TestLoadWorkflowChatContextFromDB_ValidTask(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	params := WorkflowStepParams{
		WorkflowID:    "test-plugin",
		SessionID:     "sess-1",
		StepID:        "step_a",
		WorkflowMode:  "auto",
		ChatSessionID: "chat-sess-1",
	}
	paramsBytes, _ := json.Marshal(params)
	db.DB.Create(&orm.SubAgentTask{
		ID: "t2", ConversationID: "c2", AgentType: "workflow_step", Title: "Workflow",
		Objective: "run", Mode: "auto", Status: "running",
		CreateUserID: "u1", TriggerHistoryID: "hist-1",
		Params:    json.RawMessage(paramsBytes),
		CreatedAt: now, UpdatedAt: now, LastHeartbeat: now,
		InputSlots: json.RawMessage("[]"), OutputSlots: json.RawMessage("[]"),
	})
	got := loadWorkflowChatContextFromDB(ctx, db.DB, "t2")
	if got == nil {
		t.Fatal("expected non-nil context")
	}
	if got.WorkflowID != "test-plugin" || got.SessionID != "sess-1" {
		t.Fatalf("unexpected context: %+v", got)
	}
	if got.ConvID != "c2" || got.UserID != "u1" {
		t.Fatalf("conv/user mismatch: %+v", got)
	}
}

// TestLoadWorkflowChatContextFromDB_MissingParams returns nil.
func TestLoadWorkflowChatContextFromDB_MissingParams(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	db.DB.Create(&orm.SubAgentTask{
		ID: "t3", ConversationID: "c3", AgentType: "workflow_step", Title: "Workflow",
		Objective: "run", Mode: "auto", Status: "running",
		CreateUserID: "u1", CreatedAt: now, UpdatedAt: now, LastHeartbeat: now,
		InputSlots: json.RawMessage("[]"), OutputSlots: json.RawMessage("[]"),
	})
	got := loadWorkflowChatContextFromDB(ctx, db.DB, "t3")
	if got != nil {
		t.Fatalf("got %v, want nil for missing params", got)
	}
}
