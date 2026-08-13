package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/subagent"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/executor"
	workflowstore "lazymind/core/workflow/store"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

func declaredWorkflowOutputTypes(ctx context.Context, db *gorm.DB, owner, refOrID, revisionID string, outputs []string) map[string]string {
	pkg, err := workflowstore.New(db).GetWorkflowPackage(ctx, owner, refOrID, revisionID)
	if err != nil {
		return nil
	}
	var spec struct {
		Slots []struct {
			ID   string `yaml:"id"`
			Type string `yaml:"type"`
		} `yaml:"slots"`
	}
	if yaml.Unmarshal(pkg.Files["workflow.yaml"], &spec) != nil {
		return nil
	}
	allowed := make(map[string]bool, len(outputs))
	for _, output := range outputs {
		allowed[output] = true
	}
	types := map[string]string{}
	for _, slot := range spec.Slots {
		if allowed[slot.ID] && strings.TrimSpace(slot.Type) != "" {
			types[slot.ID] = strings.TrimSpace(slot.Type)
		}
	}
	return types
}

func enqueueCanonicalAttempt(ctx context.Context, db *gorm.DB, request subagent.RunRequest) error {
	var step orm.WorkflowSessionStep
	if err := db.WithContext(ctx).Where("task_id = ?", request.TaskID).First(&step).Error; err != nil {
		return err
	}
	var task orm.SubAgentTask
	if err := db.WithContext(ctx).Select("objective", "output_slots", "params").Where("id = ?", request.TaskID).First(&task).Error; err != nil {
		return err
	}
	operation, _ := request.Params["operation"].(string)
	if operation == "" {
		operation = "execute"
	}
	value := executor.AttemptContext{ContractVersion: attempt.ContractVersion, SessionID: step.SessionID,
		AttemptID: step.ID, StepID: step.StepID, AttemptNo: step.Attempt, Operation: operation, Objective: task.Objective}
	_ = json.Unmarshal(task.OutputSlots, &value.DeclaredOutputs)
	var taskParams struct {
		OutputTypes map[string]string `json:"output_slot_types"`
	}
	_ = json.Unmarshal(task.Params, &taskParams)
	value.DeclaredOutputTypes = taskParams.OutputTypes
	if required, ok := request.Params["required_output_artifact_keys"].([]string); ok {
		value.RequiredOutputs = required
	}
	if objective, ok := request.Params["objective"].(string); ok {
		value.Objective = objective
	}
	if inputs, ok := request.Params["inputs"].(map[string]any); ok {
		value.Inputs = inputs
	}
	if instruction, ok := request.Params["retry_hint"].(string); ok {
		value.Instruction = instruction
	}
	if selector, ok := request.Params["partial_indices"].(map[string][]int); ok {
		value.PartialSelector = selector
	} else if raw, ok := request.Params["partial_indices"].(map[string]any); ok {
		value.PartialSelector = map[string][]int{}
		for slot, item := range raw {
			if values, ok := item.([]int); ok {
				value.PartialSelector[slot] = values
			}
		}
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updated := tx.Model(&orm.WorkflowSessionStep{}).Where("id = ? AND status IN ?", step.ID, []string{StepStatusPending, "queued"}).
			Updates(map[string]any{"status": "queued", "updated_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return gorm.ErrInvalidData
		}
		return tx.Create(&orm.WorkflowOutbox{ID: uuid.NewString(), AttemptID: step.ID, SessionID: step.SessionID,
			PayloadJSON: payload, Status: "pending", CreatedAt: now, UpdatedAt: now}).Error
	})
}
