package chat

import (
	"encoding/json"
	"errors"
	"strings"
)

const (
	RuntimeEventModelRetryScheduled = "model_retry_scheduled"
	RuntimeEventModelCallFinished   = "model_call_finished"
	RuntimeEventRunFinished         = "run_finished"
)

type ChatRuntimeEvent struct {
	SchemaVersion int             `json:"schema_version"`
	EventID       string          `json:"event_id"`
	RunID         string          `json:"run_id"`
	Type          string          `json:"type"`
	Data          json.RawMessage `json:"data"`
}

type RunTerminal struct {
	Status        string `json:"status"`
	Reason        string `json:"reason"`
	Code          string `json:"code,omitempty"`
	PartialOutput bool   `json:"partial_output"`
	ModelCallID   string `json:"model_call_id,omitempty"`
	DiagnosticID  string `json:"diagnostic_id,omitempty"`
}

func (e *ChatRuntimeEvent) Validate(expectedRunID string) error {
	if e == nil {
		return errors.New("runtime event is nil")
	}
	if e.SchemaVersion != 1 || strings.TrimSpace(e.EventID) == "" || strings.TrimSpace(e.RunID) == "" {
		return errors.New("invalid runtime event envelope")
	}
	if expectedRunID != "" && e.RunID != expectedRunID {
		return errors.New("runtime event run_id mismatch")
	}
	switch e.Type {
	case RuntimeEventModelRetryScheduled, RuntimeEventModelCallFinished:
		var data map[string]any
		if err := json.Unmarshal(e.Data, &data); err != nil || data == nil {
			return errors.New("runtime event data must be an object")
		}
	case RuntimeEventRunFinished:
		_, err := e.Terminal()
		return err
	default:
		return errors.New("unsupported runtime event type")
	}
	return nil
}

func (e *ChatRuntimeEvent) Terminal() (*RunTerminal, error) {
	if e == nil || e.Type != RuntimeEventRunFinished {
		return nil, errors.New("runtime event is not run_finished")
	}
	var terminal RunTerminal
	if err := json.Unmarshal(e.Data, &terminal); err != nil {
		return nil, errors.New("invalid run_finished data")
	}
	switch terminal.Status {
	case "completed", "interrupted", "failed", "cancelled":
	default:
		return nil, errors.New("invalid run status")
	}
	switch terminal.Reason {
	case "normal", "awaiting_user_input", "model_incomplete", "model_failure", "runtime_failure", "user_cancelled":
	default:
		return nil, errors.New("invalid run reason")
	}
	return &terminal, nil
}

func failedRunEvent(runID, code string, partialOutput bool) *ChatRuntimeEvent {
	terminal := RunTerminal{Status: "failed", Reason: "runtime_failure", Code: code, PartialOutput: partialOutput}
	return runFinishedEvent(runID, terminal)
}

func completedRunEvent(runID string, partialOutput bool) *ChatRuntimeEvent {
	return runFinishedEvent(runID, RunTerminal{Status: "completed", Reason: "normal", PartialOutput: partialOutput})
}

func runFinishedEvent(runID string, terminal RunTerminal) *ChatRuntimeEvent {
	data, _ := json.Marshal(terminal)
	return &ChatRuntimeEvent{
		SchemaVersion: 1,
		EventID:       newID("evt_"),
		RunID:         runID,
		Type:          RuntimeEventRunFinished,
		Data:          data,
	}
}

func cancelledRunEvent(runID string, partialOutput bool) *ChatRuntimeEvent {
	terminal := RunTerminal{Status: "cancelled", Reason: "user_cancelled", PartialOutput: partialOutput}
	return runFinishedEvent(runID, terminal)
}

func externalRunTerminalEvent(runID, status string, partialOutput bool) *ChatRuntimeEvent {
	switch status {
	case "completed":
		return completedRunEvent(runID, partialOutput)
	case "stopped":
		return cancelledRunEvent(runID, partialOutput)
	default:
		return failedRunEvent(runID, "external_agent_failed", partialOutput)
	}
}

func terminalJSON(terminal *RunTerminal) json.RawMessage {
	if terminal == nil {
		return nil
	}
	data, _ := json.Marshal(terminal)
	return data
}

func storedRunEvent(runID string, raw json.RawMessage) *ChatRuntimeEvent {
	var terminal RunTerminal
	if strings.TrimSpace(runID) == "" || json.Unmarshal(raw, &terminal) != nil {
		if strings.TrimSpace(runID) == "" {
			runID = newID("run_")
		}
		return failedRunEvent(runID, "missing_persisted_terminal", false)
	}
	return &ChatRuntimeEvent{SchemaVersion: 1, EventID: newID("evt_"), RunID: runID, Type: RuntimeEventRunFinished, Data: raw}
}

func hasBusinessStreamPayload(chunk UpstreamStreamChunk) bool {
	return chunk.Text != "" || chunk.ReasoningText != "" || len(chunk.Sources) > 0 ||
		chunk.TaskCreated != nil || chunk.ArtifactCreated != nil || chunk.AskPending != nil ||
		chunk.ToolLimitPending != nil || chunk.IntentUpdated != nil || chunk.WorkflowPreflightUpdated != nil ||
		chunk.Heartbeat
}
