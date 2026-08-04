package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"lazymind/core/workflow/attempt"
)

type AttemptContext struct {
	ContractVersion  string            `json:"contract_version"`
	SessionID        string            `json:"session_id"`
	AttemptID        string            `json:"attempt_id"`
	StepID           string            `json:"step_id"`
	AttemptNo        int               `json:"attempt_no"`
	Operation        string            `json:"operation"`
	Objective        string            `json:"objective,omitempty"`
	Prompt           string            `json:"prompt,omitempty"`
	Acceptance       []string          `json:"acceptance_criteria,omitempty"`
	Instruction      string            `json:"instruction,omitempty"`
	PartialSelector  map[string][]int  `json:"partial_selector,omitempty"`
	WorkflowRevision string            `json:"workflow_revision"`
	Inputs           map[string]any    `json:"inputs,omitempty"`
	RequiredOutputs  []string          `json:"required_outputs,omitempty"`
	Capabilities     []string          `json:"capabilities,omitempty"`
	LegacyTools      []string          `json:"legacy_tools,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

type HostRunSpec struct {
	Attempt AttemptContext `json:"attempt"`
	Params  map[string]any `json:"params,omitempty"`
}

type Artifact struct {
	Slot        string          `json:"slot"`
	ContentType string          `json:"content_type"`
	Value       json.RawMessage `json:"value"`
	Seq         int             `json:"seq"`
}

type Result struct {
	Summary     string         `json:"summary,omitempty"`
	ExecutorRef string         `json:"executor_ref,omitempty"`
	Artifacts   []Artifact     `json:"artifacts,omitempty"`
	Projection  map[string]any `json:"projection,omitempty"`
}

type Callbacks struct {
	Progress func(json.RawMessage) error
	Artifact func(Artifact) error
}

// HostExecutor is the only pluggable execution boundary used by Workflow
// Runtime. Concrete Hosts implement it in their own integration package; the
// Runtime never selects a model provider or imports Host-specific state.
type HostExecutor interface {
	BuildRunSpec(context.Context, AttemptContext) (HostRunSpec, error)
	RunSubAgent(context.Context, HostRunSpec, Callbacks) (Result, error)
	Cancel(context.Context, string) error
}

type ContextLoader interface {
	LoadAttemptContext(context.Context, string) (AttemptContext, error)
}

type ArtifactSink interface {
	Save(context.Context, AttemptContext, Artifact) error
}

type AttemptService interface {
	Claim(context.Context, string) (attempt.Claim, error)
	Heartbeat(context.Context, string, string) (time.Time, error)
	Progress(context.Context, string, string, json.RawMessage) error
	Complete(context.Context, string, string, json.RawMessage) error
	Fail(context.Context, string, string, string, json.RawMessage) error
	Cancel(context.Context, string, string) error
}

type Config struct {
	ExecutorID        string
	Host              string
	HeartbeatInterval time.Duration
}

type Supervisor struct {
	Attempts  AttemptService
	Contexts  ContextLoader
	Executor  HostExecutor
	Artifacts ArtifactSink
	Config    Config
}

type HandoffAck struct {
	AttemptID         string `json:"attempt_id"`
	FencingGeneration int64  `json:"fencing_generation"`
	Owned             bool   `json:"owned"`
}

type executorError string

func (err executorError) Error() string { return string(err) }

func (s *Supervisor) claim(ctx context.Context) (attempt.Claim, error) {
	if s.Attempts == nil || s.Contexts == nil || s.Executor == nil {
		return attempt.Claim{}, executorError("executor supervisor is not configured")
	}
	if service, ok := s.Attempts.(interface {
		ClaimForHost(context.Context, string, string) (attempt.Claim, error)
	}); ok {
		return service.ClaimForHost(ctx, s.Config.ExecutorID, s.Config.Host)
	}
	return s.Attempts.Claim(ctx, s.Config.ExecutorID)
}

// ExecuteSync and Handoff share claim and runClaimed; their only difference is
// whether the caller waits for the terminal transition.
func (s *Supervisor) ExecuteSync(ctx context.Context) (Result, error) {
	claim, err := s.claim(ctx)
	if err != nil {
		return Result{}, err
	}
	return s.runClaimed(ctx, claim)
}

func (s *Supervisor) Handoff(ctx context.Context) (HandoffAck, error) {
	claim, err := s.claim(ctx)
	if err != nil {
		return HandoffAck{}, err
	}
	owned := make(chan struct{})
	go func() {
		close(owned) // claim is already durable; recovery may reclaim its lease.
		_, _ = s.runClaimed(context.Background(), claim)
	}()
	select {
	case <-owned:
		return HandoffAck{AttemptID: claim.AttemptID, FencingGeneration: claim.FencingGeneration, Owned: true}, nil
	case <-ctx.Done():
		return HandoffAck{}, ctx.Err()
	}
}

func (s *Supervisor) runClaimed(ctx context.Context, claim attempt.Claim) (result Result, returned error) {
	attemptCtx, err := s.Contexts.LoadAttemptContext(ctx, claim.AttemptID)
	if err != nil {
		s.fail(context.Background(), claim, "CONTEXT_LOAD_FAILED", err)
		return result, err
	}
	interval := s.Config.HeartbeatInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	heartbeatErr := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				if _, err := s.Attempts.Heartbeat(runCtx, claim.AttemptID, claim.LeaseToken); err != nil {
					select {
					case heartbeatErr <- err:
					default:
					}
					cancel()
					return
				}
			}
		}
	}()

	terminal := sync.Once{}
	finish := func(status, code string, payload json.RawMessage) error {
		var finishErr error
		terminal.Do(func() {
			switch status {
			case "succeeded":
				finishErr = s.Attempts.Complete(context.Background(), claim.AttemptID, claim.LeaseToken, payload)
			case "cancelled":
				finishErr = s.Attempts.Cancel(context.Background(), claim.AttemptID, claim.LeaseToken)
			default:
				finishErr = s.Attempts.Fail(context.Background(), claim.AttemptID, claim.LeaseToken, code, payload)
			}
		})
		return finishErr
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err := executorError(fmt.Sprintf("executor panic: %v", recovered))
			_ = finish("failed", "EXECUTOR_PANIC", errorJSON(err))
			returned = err
		}
	}()

	spec, err := s.Executor.BuildRunSpec(runCtx, attemptCtx)
	if err != nil {
		_ = finish("failed", "RUN_SPEC_INVALID", errorJSON(err))
		return result, err
	}
	seen := map[string]bool{}
	callbacks := Callbacks{
		Progress: func(value json.RawMessage) error {
			return s.Attempts.Progress(runCtx, claim.AttemptID, claim.LeaseToken, value)
		},
		Artifact: func(value Artifact) error {
			if value.Slot == "" {
				return executorError("artifact slot is required")
			}
			if value.Seq < 1 {
				value.Seq = 1
			}
			if s.Artifacts != nil {
				if err := s.Artifacts.Save(runCtx, attemptCtx, value); err != nil {
					return err
				}
			}
			seen[value.Slot] = true
			return nil
		},
	}
	result, err = s.Executor.RunSubAgent(runCtx, spec, callbacks)
	select {
	case heartbeat := <-heartbeatErr:
		if err == nil {
			err = heartbeat
		}
	default:
	}
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			_ = s.Executor.Cancel(context.Background(), claim.AttemptID)
			_ = finish("cancelled", "CANCELLED", nil)
		} else {
			_ = finish("failed", "EXECUTION_FAILED", errorJSON(err))
		}
		return result, err
	}
	for _, artifact := range result.Artifacts {
		if !seen[artifact.Slot] {
			if err = callbacks.Artifact(artifact); err != nil {
				_ = finish("failed", "ARTIFACT_WRITE_FAILED", errorJSON(err))
				return result, err
			}
		}
	}
	for _, required := range attemptCtx.RequiredOutputs {
		if !seen[required] {
			err = executorError(fmt.Sprintf("required output %q missing", required))
			_ = finish("failed", "REQUIRED_OUTPUT_MISSING", errorJSON(err))
			return result, err
		}
	}
	payload, err := json.Marshal(result)
	if err != nil {
		_ = finish("failed", "RESULT_INVALID", errorJSON(err))
		return result, err
	}
	if err = finish("succeeded", "", payload); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Supervisor) fail(ctx context.Context, claim attempt.Claim, code string, err error) {
	_ = s.Attempts.Fail(ctx, claim.AttemptID, claim.LeaseToken, code, errorJSON(err))
}
func errorJSON(err error) json.RawMessage {
	value, _ := json.Marshal(map[string]string{"error": err.Error()})
	return value
}
