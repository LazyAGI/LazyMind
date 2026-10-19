package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/taskdisplay"
	"lazymind/core/modelconfig"
)

// InheritedModelOrigin is supplied only by Core after confirming the parent route.
// It is not an authorization grant; every execution rebuilds its owner's pool.
type InheritedModelOrigin struct {
	Identity       common.ModelSwitchIdentity
	MaxInputTokens *int64
}

var errTaskModelsExhausted = errors.New("task model configuration unavailable")

type taskModelCall struct {
	cursor      common.ModelOutput
	pending     []TaskEvent
	currentID   string
	switchIndex int
	seen        map[string]string
}

// Closed records contain public facts only, never the candidate pool or credentials.
type taskModelRecord struct {
	ModelOutput    common.ModelOutput         `json:"model_output"`
	CurrentModel   common.ModelSwitchIdentity `json:"current_model"`
	MaxInputTokens *int64                     `json:"max_input_tokens"`
	ModelSwitch    *common.ModelSwitchRecord  `json:"model_switch,omitempty"`
}

func prepareTaskModelInput(ctx context.Context, db *gorm.DB, task *orm.SubAgentTask, req *RunRequest) error {
	req.ModelFallbackCurrentID, req.ModelFallbackCandidates, req.ModelFallbackInitialSwitch = "", nil, nil
	prepared, err := modelconfig.LoadChatRuntimeConfig(ctx, db, task.CreateUserID)
	if err != nil {
		return errors.New("task model configuration unavailable")
	}
	origin := req.InheritedModelOrigin
	// Explicit resume can inherit only the last fact from this task's previous execution.
	if origin == nil && req.Resume {
		var row orm.SubAgentStep
		err := db.WithContext(ctx).Where("task_id = ? AND execution_id = ? AND role = ?", task.ID, task.ExecutionID, "model_runtime").Order("seq DESC").Take(&row).Error
		if err == nil {
			var record taskModelRecord
			if json.Unmarshal(row.Content, &record) == nil && record.CurrentModel.CandidateID != "" {
				origin = &InheritedModelOrigin{record.CurrentModel, record.MaxInputTokens}
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	var current *modelconfig.ChatModelCandidate
	if origin != nil {
		model := modelconfig.FindChatModel(prepared.Models, origin.Identity.ModelID, origin.Identity.Source)
		if model != nil && common.SameModelSwitchIdentity(origin.Identity, taskModelIdentity(*model)) {
			current = prepared.PrepareCandidate(ctx, *model)
		}
	} else {
		model, err := modelconfig.ResolveDefaultChatModel(ctx, db, task.CreateUserID, prepared.Models)
		if err != nil {
			return errors.New("task model configuration unavailable")
		}
		if model != nil {
			current = prepared.PrepareCandidate(ctx, *model)
			if current == nil {
				origin = &InheritedModelOrigin{Identity: taskModelIdentity(*model), MaxInputTokens: modelconfig.ConfiguredChatWindow(model.MaxInputTokens)}
			}
		}
	}
	if current == nil {
		var window *int64
		if origin != nil {
			window = origin.MaxInputTokens
		}
		_, current = modelconfig.InitialChatCandidate(prepared, window)
		if current == nil {
			return errTaskModelsExhausted
		}
		if origin != nil {
			req.ModelFallbackInitialSwitch = &common.ModelFallbackInitialSwitch{From: origin.Identity, ReasonCode: "model_config_unavailable", FromMaxInputTokens: window}
		}
	}
	req.LLMConfig = cloneTaskConfig(prepared.Config)
	if req.LLMConfig == nil {
		req.LLMConfig = map[string]any{}
	}
	req.LLMConfig["llm"] = cloneTaskConfig(current.LLMConfig)["llm"]
	req.ModelFallbackCurrentID = current.CandidateID
	req.ModelFallbackCandidates = prepared.Candidates
	req.modelModels = prepared.Models
	return nil
}

func cloneTaskConfig(config map[string]any) map[string]any {
	if config == nil {
		return nil
	}
	raw, _ := json.Marshal(config)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func taskModelIdentity(model modelconfig.AvailableChatModel) common.ModelSwitchIdentity {
	return common.ModelSwitchIdentity{CandidateID: modelconfig.ChatCandidateID(model.Source, model.ID), Source: model.Source, ModelID: model.ID, ProviderGroupID: model.ProviderGroupID, ProviderName: model.ProviderName, ModelName: model.ModelName}
}
func taskModelCandidate(req *RunRequest, id string) *modelconfig.ChatModelCandidate {
	for i := range req.ModelFallbackCandidates {
		if req.ModelFallbackCandidates[i].CandidateID == id {
			return &req.ModelFallbackCandidates[i]
		}
	}
	return nil
}
func taskModelPublicIdentity(req *RunRequest, id string) common.ModelSwitchIdentity {
	for _, model := range req.modelModels {
		if modelconfig.ChatCandidateID(model.Source, model.ID) == id {
			return taskModelIdentity(model)
		}
	}
	return common.ModelSwitchIdentity{}
}

// Called inside the existing execution transaction on a copy of the owner state.
// The caller installs that copy only after the transaction commits.
func persistTaskModelEvent(ctx context.Context, tx *gorm.DB, req *RunRequest, call *taskModelCall, ev *TaskEvent) (bool, error) {
	m := ev.ModelOutput
	if m == nil {
		if ev.RuntimeEvent != nil {
			return false, errors.New("task switch requires a model commit")
		}
		if ev.ProcessStep != nil && ev.ProcessStep.ModelSwitch != nil {
			return false, errors.New("unconfirmed task model switch")
		}
		return persistTaskEventWithRecord(ctx, tx, ev)
	}
	if m.RunID != call.cursor.RunID {
		return false, ErrStaleExecution
	}
	if m.CallIndex < 1 || (m.Phase != "" && m.Phase != "begin" && m.Phase != "commit" && m.Phase != "rollback") {
		return false, errors.New("invalid task model output")
	}
	if m.Phase == "" {
		if (ev.Type != "text" && ev.Type != "think") || ev.RuntimeEvent != nil || ev.ProcessStep != nil || len(ev.Sources) > 0 || len(ev.Value) > 0 || len(ev.ToolCalls) > 0 || len(ev.ToolResults) > 0 {
			return false, errors.New("invalid task model content")
		}
		if call.cursor.Phase != "begin" || m.CallIndex != call.cursor.CallIndex {
			return false, nil
		}
		call.pending = append(call.pending, *ev)
		return true, nil
	}
	if ev.Type != "model_output" || ev.Text != "" || ev.Think != "" || ev.ProcessStep != nil || len(ev.Sources) > 0 || len(ev.Value) > 0 || len(ev.ToolCalls) > 0 || len(ev.ToolResults) > 0 || len(ev.DurableToolResults) > 0 || ev.Delta != "" || ev.ArtifactKey != "" || ev.ContentType != "" || ev.V2ArtifactID != "" || ev.V2RevisionID != "" || len(ev.Steps) > 0 || len(ev.WritingSubtasks) > 0 || ev.StreamID != "" || ev.Status != "" || ev.Summary != "" || ev.Message != "" {
		return false, errors.New("task model boundary contains content")
	}
	if ev.RuntimeEvent != nil && (m.Phase != "commit" || ev.RuntimeEvent.Type != "model_switched") {
		return false, errors.New("invalid task runtime event")
	}
	if m.CallIndex < call.cursor.CallIndex {
		return false, nil
	}
	if m.Phase == "begin" {
		if m.CallIndex == call.cursor.CallIndex {
			return false, nil
		}
		if call.cursor.Phase == "begin" || m.CallIndex != call.cursor.CallIndex+1 {
			return false, errors.New("invalid task call sequence")
		}
		call.cursor = *m
		call.pending = nil
		return true, nil
	}
	if m.CallIndex != call.cursor.CallIndex {
		return false, errors.New("invalid task call close")
	}
	if call.cursor.Phase != "begin" {
		if event := ev.RuntimeEvent; event != nil {
			if call.seen[event.EventID] != event.Type+string(event.Data) {
				return false, ErrPublicEventConflict
			}
		}
		return false, nil
	}
	var switched *common.ModelSwitchRecord
	if event := ev.RuntimeEvent; event != nil {
		if event.SchemaVersion != 1 || strings.TrimSpace(event.EventID) == "" || len(event.EventID) > 128 || event.RunID != call.cursor.RunID || common.ValidateModelSwitched(event.Data) != nil {
			return false, errors.New("invalid task switch envelope")
		}
		var data common.ModelSwitchedData
		_ = json.Unmarshal(event.Data, &data)
		expected := taskModelPublicIdentity(req, call.currentID)
		var fromWindow *int64
		if from := taskModelCandidate(req, call.currentID); from != nil {
			fromWindow = from.MaxInputTokens
		}
		if initial := req.ModelFallbackInitialSwitch; initial != nil && call.switchIndex == 0 {
			expected = initial.From
			fromWindow = initial.FromMaxInputTokens
			if data.ReasonCode != initial.ReasonCode {
				return false, errors.New("task initial switch reason mismatch")
			}
		}
		to := taskModelCandidate(req, data.To.CandidateID)
		if to == nil || !common.SameModelSwitchIdentity(data.From, expected) || !common.SameModelSwitchIdentity(data.To, taskModelPublicIdentity(req, to.CandidateID)) || data.SwitchIndex != call.switchIndex+1 {
			return false, errors.New("task switch candidate mismatch")
		}
		data.From, data.To = expected, taskModelPublicIdentity(req, to.CandidateID)
		data.FromMaxInputTokens, data.ToMaxInputTokens = fromWindow, to.MaxInputTokens
		fingerprint := event.Type + string(event.Data)
		event.Data, _ = json.Marshal(data)
		if _, exists := call.seen[event.EventID]; exists {
			return false, ErrPublicEventConflict
		}
		switched = &common.ModelSwitchRecord{ModelSwitchedData: data, EventID: event.EventID}
		call.currentID, call.switchIndex = to.CandidateID, data.SwitchIndex
		call.seen[event.EventID] = fingerprint
	}
	if m.Phase == "commit" {
		if req.ModelFallbackInitialSwitch != nil && call.switchIndex == 0 {
			return false, errors.New("task initial model requires successful switch")
		}
		for _, pending := range call.pending {
			role, content := remoteStepContent(pending)
			if _, err := appendExecutionStep(ctx, tx, ev.TaskID, ev.ExecutionID, role, content); err != nil {
				return false, err
			}
		}
	}
	call.pending = nil
	call.cursor = *m
	candidate := taskModelCandidate(req, call.currentID)
	identity := taskModelPublicIdentity(req, call.currentID)
	var window *int64
	if candidate != nil {
		window = candidate.MaxInputTokens
	}
	// Before the first bootstrap success, the confirmed model remains the origin.
	if req.ModelFallbackInitialSwitch != nil && call.switchIndex == 0 {
		identity = req.ModelFallbackInitialSwitch.From
		window = req.ModelFallbackInitialSwitch.FromMaxInputTokens
	}
	record := taskModelRecord{ModelOutput: *m, CurrentModel: identity, MaxInputTokens: window, ModelSwitch: switched}
	raw, _ := json.Marshal(record)
	if _, err := appendExecutionStep(ctx, tx, ev.TaskID, ev.ExecutionID, "model_runtime", raw); err != nil {
		return false, err
	}
	if switched != nil {
		step := taskdisplay.PublicProcessStep{StepID: fmt.Sprintf("model-switch-%d", switched.SwitchIndex), Revision: 1, Order: switched.SwitchIndex, Title: "模型自动切换", Status: "succeeded", ModelSwitch: switched}
		if _, err := PersistPublicProcessStep(ctx, tx, ev.TaskID, ev.ExecutionID, switched.EventID, step); err != nil {
			return false, err
		}
		ev.ProcessStep = &step // published as the same confirmed fact; never supplied by Python.
	}
	return true, nil
}

func appendExecutionStep(ctx context.Context, tx *gorm.DB, taskID, executionID, role string, content json.RawMessage) (int, error) {
	var seq int
	if err := tx.Model(&orm.SubAgentStep{}).Where("task_id = ?", taskID).Select("COALESCE(MAX(seq), -1)").Scan(&seq).Error; err != nil {
		return 0, err
	}
	err := tx.WithContext(ctx).Create(&orm.SubAgentStep{ID: "sas_" + common.GenerateID(), TaskID: taskID, ExecutionID: executionID, Seq: seq + 1, Role: role, Content: content, CreatedAt: time.Now().UTC()}).Error
	return seq + 1, err
}
