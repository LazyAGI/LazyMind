package executor

import (
	"context"
	"encoding/json"
)

// AttemptContext is the immutable Host-neutral execution snapshot used by
// trusted Executor boundaries. It never contains a model configuration, API
// credential or Host-local path. Public adapters must redact Metadata.
type AttemptContext struct {
	ContractVersion     string            `json:"contract_version"`
	SessionID           string            `json:"session_id"`
	AttemptID           string            `json:"attempt_id"`
	StepID              string            `json:"step_id"`
	AttemptNo           int               `json:"attempt_no"`
	Operation           string            `json:"operation"`
	Objective           string            `json:"objective,omitempty"`
	Prompt              string            `json:"prompt,omitempty"`
	Acceptance          []string          `json:"acceptance_criteria,omitempty"`
	Instruction         string            `json:"instruction,omitempty"`
	PartialSelector     map[string][]int  `json:"partial_selector,omitempty"`
	WorkflowRevision    string            `json:"workflow_revision"`
	Inputs              map[string]any    `json:"inputs,omitempty"`
	DeclaredOutputs     []string          `json:"declared_outputs,omitempty"`
	DeclaredOutputTypes map[string]string `json:"declared_output_types,omitempty"`
	RequiredOutputs     []string          `json:"required_outputs,omitempty"`
	OutputCardinality   map[string]string `json:"output_cardinality,omitempty"`
	Capabilities        []string          `json:"capabilities,omitempty"`
	LegacyTools         []string          `json:"legacy_tools,omitempty"`
	Metadata            map[string]string `json:"metadata,omitempty"`
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

type ContextLoader interface {
	LoadAttemptContext(context.Context, string) (AttemptContext, error)
}

type ArtifactSink interface {
	Save(context.Context, AttemptContext, Artifact) error
}
