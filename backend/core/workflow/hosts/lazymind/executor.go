package lazymind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/state"
	"lazymind/core/subagent"
	workflowexecutor "lazymind/core/workflow/executor"
)

// Executor is LazyMind Chat's Host-specific bridge. Workflow Runtime depends
// only on workflowexecutor.HostExecutor and does not import this package.
type Executor struct {
	DB    *gorm.DB
	State state.Store
}

func (adapter Executor) BuildRunSpec(ctx context.Context, value workflowexecutor.AttemptContext) (workflowexecutor.HostRunSpec, error) {
	if value.AttemptID == "" || value.Operation == "" {
		return workflowexecutor.HostRunSpec{}, errors.New("attempt_id and operation are required")
	}
	var step orm.WorkflowSessionStep
	if err := adapter.DB.WithContext(ctx).Where("id = ?", value.AttemptID).First(&step).Error; err != nil {
		return workflowexecutor.HostRunSpec{}, err
	}
	var session orm.WorkflowSession
	if err := adapter.DB.WithContext(ctx).Where("id = ?", value.SessionID).First(&session).Error; err != nil {
		return workflowexecutor.HostRunSpec{}, err
	}
	var task orm.SubAgentTask
	if err := adapter.DB.WithContext(ctx).Where("id = ?", step.TaskID).First(&task).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			return workflowexecutor.HostRunSpec{}, err
		}
		now := time.Now().UTC()
		outputs, _ := json.Marshal(value.RequiredOutputs)
		task = orm.SubAgentTask{ID: step.TaskID, ConversationID: session.ConversationID,
			AgentType: "workflow_step", Title: value.StepID, Objective: value.Objective, Mode: "manual",
			Status: "pending", InputSlots: json.RawMessage(`[]`), OutputSlots: outputs,
			CreateUserID: session.CreateUserID, LastHeartbeat: now, CreatedAt: now, UpdatedAt: now}
		if err := adapter.DB.WithContext(ctx).Create(&task).Error; err != nil {
			return workflowexecutor.HostRunSpec{}, err
		}
	}
	llmConfig, err := modelconfig.LoadLLMConfig(ctx, adapter.DB, session.CreateUserID)
	if err != nil {
		return workflowexecutor.HostRunSpec{}, err
	}
	return workflowexecutor.HostRunSpec{Attempt: value, Params: map[string]any{
		"operation": value.Operation, "objective": value.Objective, "inputs": value.Inputs,
		"capabilities": value.Capabilities, "prompt": value.Prompt,
		"legacy_tools": value.LegacyTools, "user_id": session.CreateUserID,
		"runtime_instruction": value.Instruction, "partial_indices": value.PartialSelector,
		"_host_task_id": step.TaskID, "_host_workspace_path": task.WorkspacePath,
		"_host_llm_config": llmConfig,
	}}, nil
}

func (adapter Executor) RunSubAgent(ctx context.Context, spec workflowexecutor.HostRunSpec,
	callbacks workflowexecutor.Callbacks) (workflowexecutor.Result, error) {
	taskID, _ := spec.Params["_host_task_id"].(string)
	workspacePath, _ := spec.Params["_host_workspace_path"].(string)
	llmConfig, _ := spec.Params["_host_llm_config"].(map[string]any)
	params := map[string]any{}
	for key, value := range spec.Params {
		if len(key) == 0 || key[0] != '_' {
			params[key] = value
		}
	}
	request := subagent.RunRequest{TaskID: taskID, AgentType: "workflow_step", Params: params,
		WorkspacePath: workspacePath, DBDSN: subagent.DBDSN(), LLMConfig: llmConfig}
	result := workflowexecutor.Result{ExecutorRef: request.TaskID}
	err := subagent.RunObserved(ctx, adapter.DB, adapter.State, request, func(event subagent.TaskEvent) error {
		switch event.Type {
		case "progress", "task_start":
			if callbacks.Progress != nil {
				value, _ := json.Marshal(map[string]any{"progress": event.Progress, "phase": event.CurrentPhase})
				return callbacks.Progress(value)
			}
		case "artifact":
			artifact := workflowexecutor.Artifact{Slot: event.ArtifactKey, ContentType: event.ContentType,
				Value: event.Value, Seq: event.Seq}
			result.Artifacts = append(result.Artifacts, artifact)
			if callbacks.Artifact != nil {
				return callbacks.Artifact(artifact)
			}
		case "done":
			result.Summary = event.Summary
		case "error":
			return fmt.Errorf("lazymind host execution failed: %s", event.Message)
		}
		return nil
	})
	return result, err
}

func (adapter Executor) Cancel(_ context.Context, _ string) error { return nil }
