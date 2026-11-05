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
	tools := mcpadapter.KnowledgeManagementTools
	if e.TaskID == "" {
		tools = mcpadapter.ChatKnowledgeManagementTools()
	}
	for _, t := range tools {
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
	principal := capability.Principal{
		UserID:      response.Subject,
		UserName:    response.Username,
		TenantID:    response.TenantID,
		Permissions: capability.NewPermissionSet(response.Permissions...),
	}
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
	return persistKnowledgeCard(ctx, store.DB(), e, p, "pending")
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

func persistKnowledgeDecision(ctx context.Context, e knowledgeruntime.Execution, p knowledgeruntime.Prepared, decision string) error {
	return persistKnowledgeDecisionDB(ctx, store.DB(), e, p, decision)
}

func persistKnowledgeDecisionDB(ctx context.Context, db *gorm.DB, e knowledgeruntime.Execution, p knowledgeruntime.Prepared, decision string) error {
	return persistKnowledgeCard(ctx, db, e, p, decision)
}

func persistKnowledgeCard(ctx context.Context, db *gorm.DB, e knowledgeruntime.Execution, p knowledgeruntime.Prepared, status string) error {
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
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&orm.ChatHistory{}).Where("id = ? AND conversation_id = ?", historyID, e.ConversationID)
		if err := query.Session(&gorm.Session{}).UpdateColumn("update_time", gorm.Expr("update_time")).Error; err != nil {
			return err
		}
		var history orm.ChatHistory
		if err := query.Session(&gorm.Session{}).Take(&history).Error; err != nil {
			return err
		}
		ext := map[string]json.RawMessage{}
		if len(history.Ext) > 0 {
			if err := json.Unmarshal(history.Ext, &ext); err != nil {
				return err
			}
		}
		var cards []knowledgeruntime.Confirmation
		if raw := ext["knowledge_confirmations"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &cards); err != nil {
				return err
			}
		}
		i := slices.IndexFunc(cards, func(card knowledgeruntime.Confirmation) bool { return card.ID == p.ConfirmationID })
		if i >= 0 {
			card := &cards[i]
			if card.ArgumentsDigest != p.ArgumentsDigest || card.Snapshot != p.Snapshot {
				return errors.New("knowledge decision history binding changed")
			}
			if card.Status != "pending" {
				return nil // A late prepare or repeated decision cannot reopen the card.
			}
			card.Status = status
		} else if p.Action != "" {
			cards = append(cards, knowledgeruntime.Confirmation{
				ID: p.ConfirmationID, ArgumentsDigest: p.ArgumentsDigest, Snapshot: p.Snapshot,
				Action: p.Action, Status: status, Preview: p.Preview, Segment: p.Segment, ExpiresAt: p.ExpiresAt,
			})
		} else {
			if status == "pending" {
				return nil
			}
			// A prepared operation from the previous protocol may still have a
			// live decision slot during a rolling upgrade.
			var legacy struct {
				ID      string            `json:"ask_id"`
				Binding map[string]string `json:"knowledge_management"`
			}
			_ = json.Unmarshal(ext["ask_pending"], &legacy)
			if legacy.ID != p.ConfirmationID || legacy.Binding["confirmation_id"] != p.ConfirmationID {
				return nil
			}
			if legacy.Binding["arguments_digest"] != p.ArgumentsDigest || legacy.Binding["snapshot"] != p.Snapshot {
				return errors.New("knowledge decision history binding changed")
			}
			answer := "__ask_user_no__"
			if status == "approved" {
				answer = "__ask_user_yes__"
			}
			ext["ask_answered"] = json.RawMessage("true")
			ext["ask_saved_answers"], _ = json.Marshal(map[string]any{"0": map[string]string{"type": "boolean", "value": answer}})
		}
		var err error
		if len(cards) > 0 {
			ext["knowledge_confirmations"], err = json.Marshal(cards)
			if err != nil {
				return err
			}
		}
		raw, err := json.Marshal(ext)
		if err != nil {
			return err
		}
		return query.Session(&gorm.Session{}).UpdateColumn("ext", raw).Error
	})
	if err != nil {
		return err
	}
	return chat.PublishConversationEvent(ctx, store.State(), e.ConversationID, historyID, "knowledge_confirmation", map[string]any{
		"confirmation_id": p.ConfirmationID,
	})
}
