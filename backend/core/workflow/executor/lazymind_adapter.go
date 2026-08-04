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
	if session.WorkflowRevisionID != "" {
		var revision orm.WorkflowRevision
		if err := loader.DB.WithContext(ctx).Where("id = ?", session.WorkflowRevisionID).First(&revision).Error; err == nil {
			var graph graphengine.CompiledStateGraph
			if json.Unmarshal(revision.CompiledGraph, &graph) == nil {
				if node, ok := graph.Nodes[row.StepID]; ok {
					value.Prompt, value.Acceptance = node.Prompt, node.Acceptance
					value.RequiredOutputs, value.Capabilities, value.LegacyTools = node.RequiredOutputs, node.Capabilities, node.LegacyTools
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
				"content_hash": binding.ContentHash, "bind_as": binding.BindAs}
		}
	}
	return value, nil
}

// Mode is an explicit rollback switch. legacy performs no canonical claim;
// shadow compares output but leaves legacy authoritative; canary enables it for
// the configured percentage; canonical enables all eligible Attempts.
type Mode string

const (
	ModeLegacy    Mode = "legacy"
	ModeShadow    Mode = "shadow"
	ModeCanary    Mode = "canary"
	ModeCanonical Mode = "canonical"
)

func UseCanonical(mode Mode, canaryPercent, bucket int, schemaCapable bool) bool {
	if !schemaCapable || mode == ModeLegacy || mode == ModeShadow {
		return false
	}
	if mode == ModeCanonical {
		return true
	}
	return mode == ModeCanary && canaryPercent > 0 && bucket >= 0 && bucket%100 < canaryPercent
}
