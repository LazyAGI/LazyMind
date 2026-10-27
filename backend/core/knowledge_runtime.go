package main

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/capability"
	mcpadapter "lazymind/core/capability/mcp"
	"lazymind/core/chat"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/knowledgeruntime"
	"lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/graphengine"
	"slices"
	"strconv"
)

func knowledgeManagementEnabled() bool {
	return knowledgeruntime.Enabled()
}
func validateKnowledgeExecution(ctx context.Context, e knowledgeruntime.Execution) (knowledgeruntime.Identity, error) {
	return validateKnowledgeExecutionDB(ctx, store.DB(), e, true)
}
func validateKnowledgeCommit(ctx context.Context, tx *gorm.DB, e knowledgeruntime.Execution) (knowledgeruntime.Identity, error) {
	return validateKnowledgeExecutionDB(ctx, tx, e, false)
}
func validateKnowledgeExecutionDB(ctx context.Context, db *gorm.DB, e knowledgeruntime.Execution, checkAccount bool) (knowledgeruntime.Identity, error) {
	invalid := errors.New("knowledge execution is inactive or outside allowed scope")
	if db == nil || store.State() == nil || e.UserID == "" || e.ConversationID == "" {
		return knowledgeruntime.Identity{}, invalid
	}
	var conversation orm.Conversation
	if err := db.WithContext(ctx).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", e.ConversationID, e.UserID).Take(&conversation).Error; err != nil {
		return knowledgeruntime.Identity{}, invalid
	}
	var metadata map[string]any
	var err error
	allowed := []string{}
	if e.TaskID == "" {
		if e.AttemptID != "" || e.LeaseToken != "" {
			return knowledgeruntime.Identity{}, invalid
		}
		metadata, err = chat.ValidateActiveRun(ctx, store.State(), e)
		if err == nil {
			policy, _ := metadata["knowledge_management_policy"].(map[string]any)
			allowed = knowledgeStringList(policy["allowed_tools"])
		}
	} else {
		metadata, err = subagent.ValidateActiveRun(ctx, db, store.State(), e)
		if err == nil && e.AttemptID != "" {
			allowed, err = knowledgeWorkflowTools(ctx, db, e)
		} else if err == nil {
			if e.LeaseToken != "" {
				return knowledgeruntime.Identity{}, invalid
			}
			raw, readErr := store.State().Get(ctx, "knowledge/task/"+e.TaskID+"/"+e.Generation)
			if readErr != nil {
				return knowledgeruntime.Identity{}, invalid
			}
			var policy struct {
				AllowedTools []string `json:"allowed_tools"`
			}
			if json.Unmarshal(raw, &policy) != nil {
				return knowledgeruntime.Identity{}, invalid
			}
			allowed = policy.AllowedTools
		}
	}
	if err != nil {
		return knowledgeruntime.Identity{}, err
	}
	if e.TaskID != "" {
		visibility, _ := metadata["_tool_visibility"].(map[string]any)
		if slices.Contains(knowledgeStringList(visibility["disabled_tools"]), "knowledge_management") {
			allowed = []string{}
		}
	}
	selected := []string{}
	for _, t := range mcpadapter.KnowledgeManagementTools {
		if slices.Contains(allowed, t) {
			selected = append(selected, t)
		}
	}
	if !checkAccount {
		return knowledgeruntime.Identity{AllowedTools: selected}, nil
	}
	response, err := common.GetCurrentIdentity(ctx, e.UserID)
	if err != nil {
		return knowledgeruntime.Identity{}, err
	}
	principal := capability.Principal{UserID: response.Subject, TenantID: response.TenantID, Permissions: capability.NewPermissionSet(response.Permissions...)}
	var datasets []string
	if e.TaskID != "" {
		datasets = knowledgeExecutionDatasetScope(metadata)
	}
	return knowledgeruntime.Identity{Principal: principal, AllowedTools: selected, AllowedDatasetIDs: datasets}, nil
}
func knowledgeStringList(v any) []string {
	switch values := v.(type) {
	case []string:
		return values
	case []any:
		out := []string{}
		for _, v := range values {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
func knowledgeWorkflowTools(ctx context.Context, db *gorm.DB, e knowledgeruntime.Execution) ([]string, error) {
	generation, err := strconv.ParseInt(e.Generation, 10, 64)
	if err != nil || generation <= 0 || e.LeaseToken == "" {
		return nil, errors.New("invalid step execution")
	}
	if err = attempt.New(db, attempt.Config{}).ValidateLease(ctx, e.AttemptID, e.LeaseToken); err != nil {
		return nil, err
	}
	var row struct {
		CompiledGraph json.RawMessage
		StepID        string
	}
	result := db.WithContext(ctx).Table("plugin_session_steps step").Select("revision.compiled_graph, step.step_id").
		Joins("JOIN plugin_sessions session ON session.id=step.session_id").Joins("JOIN plugin_revisions revision ON revision.id=session.plugin_revision_id").
		Where("step.id = ? AND step.task_id = ? AND step.fencing_generation = ? AND step.validity='effective'", e.AttemptID, e.TaskID, generation).
		Where("session.conversation_id = ? AND session.create_user_id = ? AND session.dismissed = ? AND session.status IN ?", e.ConversationID, e.UserID, false, []string{"active", "waiting"}).Take(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	var graph graphengine.CompiledStateGraph
	if json.Unmarshal(row.CompiledGraph, &graph) != nil {
		return nil, errors.New("invalid pinned workflow graph")
	}
	node, ok := graph.Nodes[row.StepID]
	if !ok {
		return nil, errors.New("missing pinned step")
	}
	return subagent.KnowledgeToolsForSelection(append([]string{}, node.LegacyTools...)), nil
}

func persistKnowledgeConfirmation(ctx context.Context, e knowledgeruntime.Execution, p knowledgeruntime.Prepared) error {
	db := store.DB()
	historyID := e.HistoryID
	if e.TaskID != "" {
		var task orm.SubAgentTask
		if err := db.WithContext(ctx).Where("id = ? AND create_user_id = ? AND conversation_id = ?", e.TaskID, e.UserID, e.ConversationID).Take(&task).Error; err != nil {
			return err
		}
		historyID = task.TriggerHistoryID
	}
	if historyID == "" {
		return errors.New("confirmation history unavailable")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&orm.ChatHistory{}).Where("id = ? AND conversation_id = ?", historyID, e.ConversationID).UpdateColumn("update_time", gorm.Expr("update_time")).Error; err != nil {
			return err
		}
		var history orm.ChatHistory
		if err := tx.Where("id = ? AND conversation_id = ?", historyID, e.ConversationID).Take(&history).Error; err != nil {
			return err
		}
		ext := map[string]any{}
		if len(history.Ext) > 0 {
			if err := json.Unmarshal(history.Ext, &ext); err != nil {
				return err
			}
		}
		previous, _ := ext["ask_pending"].(map[string]any)
		if previous["ask_id"] == p.ConfirmationID && ext["ask_answered"] == true {
			return nil // A delayed duplicate prepare cannot reopen a decided card.
		}
		delete(ext, "ask_saved_answers")
		ext["ask_pending"] = map[string]any{"knowledge_management_preview": p.Preview, "ask_id": p.ConfirmationID, "title": p.Preview["title"], "description": p.Preview["description"], "questions": []any{map[string]any{"text": "确认执行此操作？", "type": "boolean", "choices": []string{"__ask_user_yes__", "__ask_user_no__"}}}, "knowledge_management": map[string]string{"confirmation_id": p.ConfirmationID, "arguments_digest": p.ArgumentsDigest, "snapshot": p.Snapshot}}
		ext["ask_answered"] = false
		raw, err := json.Marshal(ext)
		if err != nil {
			return err
		}
		return tx.Model(&orm.ChatHistory{}).Where("id = ?", historyID).UpdateColumn("ext", raw).Error
	})
}

// Only a trusted execution constraint narrows management access. Retrieval
// filters are defaults and must not turn an unselected library into zero access.
func knowledgeExecutionDatasetScope(metadata map[string]any) []string {
	raw, exists := metadata["knowledge_dataset_scope"]
	if !exists {
		return nil
	}
	values := knowledgeStringList(raw)
	if values == nil {
		return []string{}
	}
	return values
}

// persistKnowledgeDecision updates only the exact card the user answered.
func persistKnowledgeDecision(ctx context.Context, e knowledgeruntime.Execution, p knowledgeruntime.Prepared, decision string) error {
	return persistKnowledgeDecisionDB(ctx, store.DB(), e, p, decision)
}
func persistKnowledgeDecisionDB(ctx context.Context, db *gorm.DB, e knowledgeruntime.Execution, p knowledgeruntime.Prepared, decision string) error {
	if db == nil || (decision != "approved" && decision != "rejected") {
		return errors.New("invalid knowledge decision persistence")
	}
	historyID := e.HistoryID
	if e.TaskID != "" {
		var task orm.SubAgentTask
		if err := db.WithContext(ctx).Where("id = ? AND create_user_id = ? AND conversation_id = ?", e.TaskID, e.UserID, e.ConversationID).Take(&task).Error; err != nil {
			return err
		}
		historyID = task.TriggerHistoryID
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&orm.ChatHistory{}).Where("id = ? AND conversation_id = ?", historyID, e.ConversationID).UpdateColumn("update_time", gorm.Expr("update_time")).Error; err != nil {
			return err
		}
		var history orm.ChatHistory
		if err := tx.Where("id = ? AND conversation_id = ?", historyID, e.ConversationID).Take(&history).Error; err != nil {
			return err
		}
		ext := map[string]any{}
		if err := json.Unmarshal(history.Ext, &ext); err != nil {
			return err
		}
		pending, _ := ext["ask_pending"].(map[string]any)
		confirmation, _ := pending["knowledge_management"].(map[string]any)
		// A newer card is authoritative; an old decision must never answer it.
		if pending["ask_id"] != p.ConfirmationID || confirmation["confirmation_id"] != p.ConfirmationID {
			return nil
		}
		if confirmation["arguments_digest"] != p.ArgumentsDigest || confirmation["snapshot"] != p.Snapshot {
			return errors.New("knowledge decision history binding changed")
		}
		answer := "__ask_user_no__"
		if decision == "approved" {
			answer = "__ask_user_yes__"
		}
		ext["ask_answered"] = true
		ext["ask_saved_answers"] = map[string]any{"0": map[string]any{"type": "boolean", "value": answer}}
		raw, err := json.Marshal(ext)
		if err != nil {
			return err
		}
		return tx.Model(&orm.ChatHistory{}).Where("id = ?", history.ID).UpdateColumn("ext", raw).Error
	})
}
