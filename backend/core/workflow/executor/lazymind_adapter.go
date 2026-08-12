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
	if session.WorkflowRevisionID != "" {
		var revision orm.WorkflowRevision
		if err := loader.DB.WithContext(ctx).Where("id = ?", session.WorkflowRevisionID).First(&revision).Error; err == nil {
			var graph graphengine.CompiledStateGraph
			if json.Unmarshal(revision.CompiledGraph, &graph) == nil {
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
	if err := loader.DB.WithContext(ctx).Where("attempt_id = ?", row.ID).Find(&bindings).Error; err == nil {
		if value.Inputs == nil {
			value.Inputs = map[string]any{}
		}
		for _, binding := range bindings {
			value.Inputs[binding.MaterialID] = map[string]any{"source_type": binding.SourceType,
				"source_id": binding.SourceID, "source_revision": binding.SourceRevision,
				"source_revision_id": binding.MaterialRevisionID, "content_hash": binding.ContentHash,
				"bind_as": binding.BindAs}
		}
	}
	return value, nil
}
