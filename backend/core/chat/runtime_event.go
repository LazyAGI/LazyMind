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
	Status        string      `json:"status"`
	Reason        string      `json:"reason"`
	Code          string      `json:"code,omitempty"`
	PartialOutput bool        `json:"partial_output"`
	ModelInvoked  *bool       `json:"model_invoked,omitempty"`
	ModelCallID   string      `json:"model_call_id,omitempty"`
	DiagnosticID  string      `json:"diagnostic_id,omitempty"`
	Metrics       *RunMetrics `json:"metrics,omitempty"`
}

type RunMetrics struct {
	SchemaVersion   int             `json:"schema_version,omitempty"`
	TurnSeq         *int            `json:"turn_seq,omitempty"`
	Steps           int             `json:"steps"`
	ModelSteps      int             `json:"model_steps,omitempty"`
	ToolSteps       int             `json:"tool_steps,omitempty"`
	ModelMs         int64           `json:"model_ms,omitempty"`
	ToolMs          int64           `json:"tool_ms,omitempty"`
	Model           string          `json:"model,omitempty"`
	InputTokens     *int64          `json:"input_tokens,omitempty"`
	OutputTokens    *int64          `json:"output_tokens,omitempty"`
	TotalTokens     *int64          `json:"total_tokens,omitempty"`
	CachedTokens    *int64          `json:"cached_tokens,omitempty"`
	ReasoningTokens *int64          `json:"reasoning_tokens,omitempty"`
	CacheHitRate    *float64        `json:"cache_hit_rate,omitempty"`
	ProviderUsages  json.RawMessage `json:"provider_usages,omitempty"`
	TtftMs          *int64          `json:"ttft_ms,omitempty"`
	TokS            *float64        `json:"tok_s,omitempty"`
	MaxInputTokens  *int64          `json:"max_input_tokens,omitempty"`
	ContextRatio    *float64        `json:"context_ratio,omitempty"`
}

func (terminal *RunTerminal) modelWasInvoked() bool {
	// Older producers did not report invocation. Preserve their routing behavior;
	// an explicit false identifies a host response that never called the model.
	return terminal != nil && (terminal.ModelInvoked == nil || *terminal.ModelInvoked)
}

type ModelRetryScheduledData struct {
	ModelCallID string `json:"model_call_id"`
	RetryIndex  *int   `json:"retry_index"`
	MaxAttempts *int   `json:"max_attempts"`
	DelayMS     *int   `json:"delay_ms"`
}

type ModelFailureData struct {
	Origin       string `json:"origin"`
	Code         string `json:"code"`
	DiagnosticID string `json:"diagnostic_id,omitempty"`
}

type ModelCallFinishedData struct {
	ModelCallID       string            `json:"model_call_id"`
	AttemptCount      *int              `json:"attempt_count"`
	Kind              string            `json:"kind"`
	HasSemanticOutput *bool             `json:"has_semantic_output"`
	Finish            *string           `json:"finish,omitempty"`
	Failure           *ModelFailureData `json:"failure,omitempty"`
}

var validModelFinishes = map[string]struct{}{
	"stop": {}, "tool_calls": {}, "length": {}, "content_filter": {},
	"insufficient_system_resource": {}, "unknown": {},
}

var validIncompleteModelFinishes = map[string]struct{}{
	"length": {}, "content_filter": {}, "insufficient_system_resource": {}, "unknown": {},
}

var validModelFailureOrigins = map[string]struct{}{
	"transport": {}, "http": {}, "provider": {}, "protocol": {},
}

var validModelFailureCodes = map[string]struct{}{
	"invalid_request": {}, "authentication_failed": {}, "permission_denied": {}, "not_found": {},
	"rate_limited": {}, "usage_limit_exceeded": {}, "concurrency_limited": {}, "quota_exhausted": {},
	"balance_exhausted": {}, "organization_spend_limit_exceeded": {}, "project_spend_limit_exceeded": {},
	"input_filtered": {}, "output_filtered": {}, "token_limit": {}, "request_timeout": {},
	"provider_overloaded": {}, "service_unavailable": {}, "provider_internal_error": {},
	"provider_rejected": {}, "conflict": {}, "unprocessable_entity": {}, "protocol_error": {},
	"transport_error": {},
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
	case RuntimeEventModelRetryScheduled:
		return validateModelRetryScheduled(e.Data)
	case RuntimeEventModelCallFinished:
		return validateModelCallFinished(e.Data)
	case RuntimeEventRunFinished:
		_, err := e.Terminal()
		return err
	default:
		return errors.New("unsupported runtime event type")
	}
}

func validateModelRetryScheduled(raw json.RawMessage) error {
	var data ModelRetryScheduledData
	if err := json.Unmarshal(raw, &data); err != nil {
		return errors.New("invalid model_retry_scheduled data")
	}
	if strings.TrimSpace(data.ModelCallID) == "" || data.RetryIndex == nil || data.MaxAttempts == nil || data.DelayMS == nil {
		return errors.New("model_retry_scheduled fields are required")
	}
	if *data.RetryIndex < 1 || *data.MaxAttempts <= *data.RetryIndex || *data.DelayMS < 0 {
		return errors.New("invalid model_retry_scheduled values")
	}
	return nil
}

func validateModelCallFinished(raw json.RawMessage) error {
	var data ModelCallFinishedData
	if err := json.Unmarshal(raw, &data); err != nil {
		return errors.New("invalid model_call_finished data")
	}
	if strings.TrimSpace(data.ModelCallID) == "" || data.AttemptCount == nil || *data.AttemptCount < 1 || data.HasSemanticOutput == nil {
		return errors.New("model_call_finished fields are required")
	}
	switch data.Kind {
	case "finish":
		if data.Finish == nil || data.Failure != nil {
			return errors.New("model_call_finished finish outcome is invalid")
		}
		if _, ok := validModelFinishes[*data.Finish]; !ok {
			return errors.New("unsupported model finish")
		}
	case "failure":
		if data.Finish != nil || data.Failure == nil {
			return errors.New("model_call_finished failure outcome is invalid")
		}
		if _, ok := validModelFailureOrigins[data.Failure.Origin]; !ok {
			return errors.New("unsupported model failure origin")
		}
		if _, ok := validModelFailureCodes[data.Failure.Code]; !ok {
			return errors.New("unsupported model failure code")
		}
	default:
		return errors.New("unsupported model_call_finished kind")
	}
	return nil
}

func (e *ChatRuntimeEvent) Terminal() (*RunTerminal, error) {
	if e == nil || e.Type != RuntimeEventRunFinished {
		return nil, errors.New("runtime event is not run_finished")
	}
	return parseRunTerminal(e.Data)
}

func parseRunTerminal(raw json.RawMessage) (*RunTerminal, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, errors.New("invalid run_finished data")
	}
	partialOutput, exists := fields["partial_output"]
	if !exists {
		return nil, errors.New("run_finished partial_output is required")
	}
	var explicitPartialOutput *bool
	if err := json.Unmarshal(partialOutput, &explicitPartialOutput); err != nil || explicitPartialOutput == nil {
		return nil, errors.New("run_finished partial_output must be boolean")
	}
	var terminal RunTerminal
	if err := json.Unmarshal(raw, &terminal); err != nil {
		return nil, errors.New("invalid run_finished data")
	}
	if _, exists := fields["model_invoked"]; exists && terminal.ModelInvoked == nil {
		return nil, errors.New("run_finished model_invoked must be boolean")
	}
	codeRaw, codePresent := fields["code"]
	if codePresent {
		var code string
		if err := json.Unmarshal(codeRaw, &code); err != nil {
			return nil, errors.New("run_finished code must be a string")
		}
	}
	valid := false
	switch terminal.Status {
	case "completed":
		valid = (terminal.Reason == "normal" || terminal.Reason == "awaiting_user_input") && !codePresent
	case "interrupted":
		switch terminal.Reason {
		case "model_incomplete":
			_, valid = validIncompleteModelFinishes[terminal.Code]
		case "model_failure":
			_, valid = validModelFailureCodes[terminal.Code]
		}
	case "failed":
		switch terminal.Reason {
		case "model_failure":
			_, valid = validModelFailureCodes[terminal.Code]
		case "runtime_failure":
			valid = strings.TrimSpace(terminal.Code) != ""
		}
	case "cancelled":
		valid = terminal.Reason == "user_cancelled" && !codePresent
	}
	if !valid {
		return nil, errors.New("invalid run status/reason combination")
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
	terminal, err := parseRunTerminal(raw)
	if strings.TrimSpace(runID) == "" || err != nil {
		if strings.TrimSpace(runID) == "" {
			runID = newID("run_")
		}
		return failedRunEvent(runID, "missing_persisted_terminal", false)
	}
	return &ChatRuntimeEvent{
		SchemaVersion: 1,
		EventID:       newID("evt_"),
		RunID:         runID,
		Type:          RuntimeEventRunFinished,
		Data:          terminalJSON(terminal),
	}
}

func hasBusinessStreamPayload(chunk UpstreamStreamChunk) bool {
	return chunk.Text != "" || chunk.ReasoningText != "" || len(chunk.Sources) > 0 ||
		chunk.TaskCreated != nil || chunk.ArtifactCreated != nil || chunk.AskPending != nil ||
		chunk.ToolLimitPending != nil || chunk.IntentUpdated != nil || chunk.WorkflowPreflightUpdated != nil ||
		chunk.Heartbeat
}
