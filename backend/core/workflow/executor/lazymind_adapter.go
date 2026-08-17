package executor

import (
	"context"
	"encoding/json"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/graphengine"

	"gorm.io/gorm"
)

// DBContextLoader freezes the neutral Attempt Context from the durable outbox
// payload. Runtime-specific paths, models and credentials never enter it.
type DBContextLoader struct{ DB *gorm.DB }

func (loader DBContextLoader) LoadAttemptContext(ctx context.Context, id string) (AttemptContext, error) {
	var row orm.WorkflowSessionStep
	if err := loader.DB.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return AttemptContext{}, err
	}
	var outbox orm.WorkflowOutbox
	if err := loader.DB.WithContext(ctx).Where("attempt_id = ?", id).First(&outbox).Error; err != nil {
		return AttemptContext{}, err
	}
	value := AttemptContext{ContractVersion: attempt.ContractVersion, SessionID: row.SessionID, AttemptID: row.ID, StepID: row.StepID, AttemptNo: row.Attempt}
	if len(outbox.PayloadJSON) != 0 {
		if err := json.Unmarshal(outbox.PayloadJSON, &value); err != nil {
			return AttemptContext{}, err
		}
	}
	value.ContractVersion, value.SessionID, value.AttemptID, value.StepID, value.AttemptNo = attempt.ContractVersion, row.SessionID, row.ID, row.StepID, row.Attempt
	var session orm.WorkflowSession
	if err := loader.DB.WithContext(ctx).Where("id = ?", row.SessionID).First(&session).Error; err != nil {
		return AttemptContext{}, err
	}
	value.WorkflowRevision = session.WorkflowRevisionID
	if value.Metadata == nil {
		value.Metadata = map[string]string{}
	}
	value.Metadata["controller_host"] = session.ControllerHost
	value.Metadata["origin_host"] = session.OriginHost
	value.Metadata["conversation_id"] = session.ConversationID
	value.Metadata["owner_user_id"] = session.CreateUserID
	value.Metadata["task_id"] = row.TaskID
	listMaterials := map[string]bool{}
	if session.WorkflowRevisionID != "" {
		var revision orm.WorkflowRevision
		if err := loader.DB.WithContext(ctx).Where("id = ?", session.WorkflowRevisionID).First(&revision).Error; err == nil {
			var graph graphengine.CompiledStateGraph
			if json.Unmarshal(revision.CompiledGraph, &graph) == nil {
				for materialID, cardinality := range graph.MaterialCardinalities {
					listMaterials[materialID] = cardinality == "list"
				}
				if node, ok := graph.Nodes[row.StepID]; ok {
					value.Prompt, value.Acceptance = node.Prompt, node.Acceptance
					value.DeclaredOutputs, value.RequiredOutputs = node.Outputs, node.RequiredOutputs
					value.Capabilities, value.LegacyTools = node.Capabilities, node.LegacyTools
					value.OutputCardinalities = map[string]string{}
					for _, output := range node.Outputs {
						cardinality := graph.MaterialCardinalities[output]
						if cardinality != "list" {
							cardinality = "single"
						}
						value.OutputCardinalities[output] = cardinality
					}
				}
			}
		}
	}
	var bindings []orm.WorkflowAttemptInputBinding
	if err := loader.DB.WithContext(ctx).
		Table("plugin_attempt_input_bindings AS input_bindings").
		Select("input_bindings.*").
		Joins("LEFT JOIN plugin_slot_revisions AS slot_revisions ON slot_revisions.id = input_bindings.material_revision_id").
		Where("input_bindings.attempt_id = ?", row.ID).
		Order("input_bindings.material_id ASC").
		Order("CASE WHEN slot_revisions.list_index IS NULL THEN 0 ELSE 1 END ASC").
		Order("slot_revisions.list_index ASC").
		Order("input_bindings.created_at ASC, input_bindings.id ASC").
		Find(&bindings).Error; err == nil {
		if value.Inputs == nil {
			value.Inputs = map[string]any{}
		}
		loadedMaterials := map[string]bool{}
		for _, binding := range bindings {
			item := map[string]any{"source_type": binding.SourceType,
				"source_id": binding.SourceID, "source_revision": binding.SourceRevision,
				"source_revision_id": binding.MaterialRevisionID, "content_hash": binding.ContentHash,
				"bind_as": binding.BindAs}
			// A list material legitimately contributes several frozen revision
			// witnesses to one Attempt. Keep the historical scalar shape for a
			// single binding, and promote it to an ordered list only when a second
			// revision exists. This is backward compatible for every existing
			// single-cardinality executor while preventing list inputs from being
			// silently overwritten by map assignment.
			if !loadedMaterials[binding.MaterialID] {
				// Durable bindings remain authoritative over any provisional input
				// value carried in the original outbox payload, matching the prior
				// single-binding overwrite behavior.
				if listMaterials[binding.MaterialID] {
					value.Inputs[binding.MaterialID] = []map[string]any{item}
				} else {
					value.Inputs[binding.MaterialID] = item
				}
				loadedMaterials[binding.MaterialID] = true
				continue
			}
			switch current := value.Inputs[binding.MaterialID].(type) {
			case nil:
				value.Inputs[binding.MaterialID] = item
			case map[string]any:
				value.Inputs[binding.MaterialID] = []map[string]any{current, item}
			case []map[string]any:
				value.Inputs[binding.MaterialID] = append(current, item)
			}
		}
	}
	return value, nil
}
