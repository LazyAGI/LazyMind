package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	mcpadapter "lazymind/core/capability/mcp"
	"lazymind/core/common/orm"
	"lazymind/core/knowledgeruntime"
	"lazymind/core/state"
	"lazymind/core/toolpolicy"
	"slices"
)

func KnowledgeToolsForSelection(selection []string) []string {
	if selection == nil {
		selection = []string{"knowledge_management"}
	}
	result := []string{}
	for _, wire := range mcpadapter.KnowledgeManagementTools {
		if slices.Contains(selection, "knowledge_management") || slices.Contains(selection, wire) ||
			(slices.Contains(selection, "kb") && (wire == "knowledge.list" || wire == "knowledge.document.list" || wire == "knowledge.document.aggregate")) {
			result = append(result, wire)
		}
	}
	return result
}
func KnowledgeSystemConfig(selection []string) []any {
	if !knowledgeruntime.Enabled() {
		return []any{}
	}
	return []any{map[string]any{"id": "lazymind-knowledge", "name": "lazymind-knowledge", "transport": "streamable-http", "result_mode": "structured", "allowed_tools": KnowledgeToolsForSelection(selection)}}
}
func saveKnowledgeRunPolicy(ctx context.Context, st state.Store, req RunRequest, generation string) error {
	if st == nil {
		return nil
	}
	allowed := []string{}
	for _, v := range req.SystemMCPConfig {
		if cfg, ok := v.(map[string]any); ok && cfg["id"] == "lazymind-knowledge" {
			allowed, _ = cfg["allowed_tools"].([]string)
		}
	}
	data, _ := json.Marshal(map[string]any{"allowed_tools": allowed})
	return st.Set(ctx, "knowledge/task/"+req.TaskID+"/"+generation, data, subagentRunTimeout)
}

// Task launches intersect current user visibility with the parent run snapshot.
func taskDisabledTools(ctx context.Context, db *gorm.DB, task *orm.SubAgentTask) ([]string, error) {
	current, err := toolpolicy.Disabled(ctx, db, task.CreateUserID)
	if err != nil {
		return nil, err
	}
	if task.TriggerHistoryID == "" {
		return current, nil
	}
	var parent orm.ChatHistory
	err = db.WithContext(ctx).Select("ext").Where("id = ? AND conversation_id = ?", task.TriggerHistoryID, task.ConversationID).Take(&parent).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var answer orm.MultiAnswersChatHistory
		err = db.WithContext(ctx).Select("ext").Where("id = ? AND conversation_id = ?", task.TriggerHistoryID, task.ConversationID).Take(&answer).Error
		parent.Ext = answer.Ext
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return current, nil
	}
	if err != nil {
		return nil, err
	}
	var snapshot struct {
		Disabled []string `json:"disabled_tools"`
	}
	if err = json.Unmarshal(parent.Ext, &snapshot); err != nil && len(parent.Ext) > 0 {
		return nil, err
	}
	return toolpolicy.Merge(current, snapshot.Disabled), nil
}

func visibleKnowledgeConfig(selection, disabled []string) []any {
	if slices.Contains(disabled, "knowledge_management") {
		return []any{}
	}
	return KnowledgeSystemConfig(selection)
}

type taskVisibilitySnapshot struct {
	ExecutionID string   `json:"execution_id"`
	Disabled    []string `json:"disabled_tools"`
}

// Capture once per execution. Resume creates a new execution ID and reads settings again.
func captureTaskVisibility(ctx context.Context, db *gorm.DB, task *orm.SubAgentTask) (taskVisibilitySnapshot, error) {
	var snapshot taskVisibilitySnapshot
	err := taskTransaction(ctx, db, func(tx *gorm.DB) error {
		if err := tx.Model(&orm.SubAgentTask{}).Where("id = ?", task.ID).UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
			return err
		}
		current, err := GetTask(ctx, tx, task.ID)
		if err != nil {
			return err
		}
		if current.ExecutionID != task.ExecutionID {
			return ErrStaleExecution
		}
		params := map[string]json.RawMessage{}
		if len(current.Params) > 0 {
			if err := json.Unmarshal(current.Params, &params); err != nil {
				return err
			}
		}
		if raw := params["_tool_visibility"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				return err
			}
		}
		if snapshot.ExecutionID != current.ExecutionID || current.ExecutionID == "" {
			disabled, err := taskDisabledTools(ctx, tx, current)
			if err != nil {
				return err
			}
			snapshot.ExecutionID, snapshot.Disabled = current.ExecutionID, disabled
			params["_tool_visibility"], err = json.Marshal(snapshot)
			if err != nil {
				return err
			}
			current.Params, err = json.Marshal(params)
			if err != nil {
				return err
			}
			if err = tx.Model(current).UpdateColumn("params", current.Params).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return snapshot, err
}
