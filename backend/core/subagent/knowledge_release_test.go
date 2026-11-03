package subagent

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"slices"
	"testing"
)

func TestKnowledgeDefaultReleaseRespectsSelectionAndRollback(t *testing.T) {
	t.Setenv("LAZYMIND_KNOWLEDGE_MANAGEMENT_ENABLED", "")
	configs := KnowledgeSystemConfig(nil)
	if len(configs) != 1 {
		t.Fatalf("default configs=%v", configs)
	}
	cfg := configs[0].(map[string]any)
	if len(cfg["allowed_tools"].([]string)) != 10 {
		t.Fatalf("management tools=%v", cfg["allowed_tools"])
	}
	if slices.Contains(cfg["allowed_tools"].([]string), "knowledge.options") ||
		len(KnowledgeToolsForSelection([]string{"knowledge.options"})) != 0 {
		t.Fatal("removed options tool remains selectable")
	}
	if !slices.Equal(KnowledgeToolsForSelection([]string{"kb"}), []string{"knowledge.list", "knowledge.document.list", "knowledge.document.aggregate"}) {
		t.Fatal("read-only knowledge selection changed")
	}
	empty := KnowledgeSystemConfig([]string{})[0].(map[string]any)
	if len(empty["allowed_tools"].([]string)) != 0 {
		t.Fatal("explicit empty selection granted tools")
	}
	t.Setenv("LAZYMIND_KNOWLEDGE_MANAGEMENT_ENABLED", "false")
	if len(KnowledgeSystemConfig(nil)) != 0 {
		t.Fatal("rollback still loads management")
	}
}

func TestTaskVisibilityIntersectsParentAndUserAtLaunch(t *testing.T) {
	db := newTestDB(t)
	task := &orm.SubAgentTask{CreateUserID: "alice", ConversationID: "conv", TriggerHistoryID: "parent"}
	if err := db.Create(&orm.UserDisabledTool{CreateUserID: "alice", ToolName: "calculator"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ChatHistory{ID: "parent", ConversationID: "conv", Ext: json.RawMessage(`{"disabled_tools":["knowledge_management"]}`)}).Error; err != nil {
		t.Fatal(err)
	}
	names, err := taskDisabledTools(t.Context(), db.DB, task)
	if err != nil || len(names) != 2 {
		t.Fatalf("visibility=%v err=%v", names, err)
	}
	if len(visibleKnowledgeConfig(nil, names)) != 0 {
		t.Fatal("child bypassed parent visibility")
	}
	task.CreateUserID, task.TriggerHistoryID = "bob", ""
	names, err = taskDisabledTools(t.Context(), db.DB, task)
	if err != nil || len(names) != 0 {
		t.Fatalf("another user inherited visibility: %v %v", names, err)
	}
}

func TestTaskVisibilityChangesOnlyOnNextExecution(t *testing.T) {
	db := newTestDB(t)
	task, err := CreateTask(t.Context(), db.DB, CreateTaskInput{TaskID: "visibility-task", ConversationID: "conv", CreateUserID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	originalParams := string(task.Params)
	disabled, err := captureTaskVisibility(t.Context(), db.DB, task)
	if err != nil || len(disabled.Disabled) != 0 {
		t.Fatalf("initial visibility: %v %v", disabled, err)
	}
	if string(task.Params) != originalParams {
		t.Fatal("visibility capture mutated caller parameters")
	}
	if disabled.ExecutionID != task.ExecutionID {
		t.Fatal("visibility returned wrong execution")
	}

	if err := db.Create(&orm.UserDisabledTool{CreateUserID: "alice", ToolName: "calculator"}).Error; err != nil {
		t.Fatal(err)
	}
	disabled, err = captureTaskVisibility(t.Context(), db.DB, task)
	if err != nil || len(disabled.Disabled) != 0 {
		t.Fatalf("running execution changed visibility: %v %v", disabled, err)
	}
	if err := db.Model(task).Update("execution_id", "resumed").Error; err != nil {
		t.Fatal(err)
	}
	task, _ = GetTask(t.Context(), db.DB, task.ID)
	disabled, err = captureTaskVisibility(t.Context(), db.DB, task)
	if err != nil || len(disabled.Disabled) != 1 || disabled.Disabled[0] != "calculator" {
		t.Fatalf("resume missed visibility update: %v %v", disabled, err)
	}
}
