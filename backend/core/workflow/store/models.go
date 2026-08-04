package store

import (
	"encoding/json"
	"time"

	"lazymind/core/common/orm"
)

// Preparation is an idempotent, owner-scoped Workflow start plan. It is an
// expand-only table and does not replace any legacy Runtime table.
type Preparation = orm.WorkflowPreparation

// Event is the persistent, monotonically ordered Workflow stream record.
type Event = orm.WorkflowEvent

// Command stores a facade result. Existing plugin_transition_commands remain
// untouched because legacy binaries continue to own that compatibility table.
type Command = orm.WorkflowCommand

type InputResource = orm.WorkflowInputResource
type InputBinding = orm.WorkflowInputBinding

// WorkflowPackage is the immutable, Host-neutral representation of one
// published Workflow revision. Files are addressed by package-relative paths;
// callers never receive a filesystem root or Host-private path.
type WorkflowPackage struct {
	WorkflowRef     string            `json:"workflow_ref"`
	WorkflowID      string            `json:"workflow_id"`
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	WhenToUse       string            `json:"when_to_use"`
	SourceType      string            `json:"source_type"`
	RevisionID      string            `json:"revision_id"`
	RevisionNo      int64             `json:"revision_no"`
	TreeHash        string            `json:"tree_hash"`
	GraphHash       string            `json:"graph_hash"`
	GraphVersion    string            `json:"graph_schema_version"`
	CompiledGraph   json.RawMessage   `json:"compiled_graph,omitempty"`
	ContainsScripts bool              `json:"contains_scripts"`
	Files           map[string][]byte `json:"files,omitempty"`
}

type Artifact struct {
	ID                string          `json:"artifact_id"`
	SessionID         string          `json:"session_id"`
	SlotID            string          `json:"slot_id"`
	Slot              string          `json:"slot"`
	StepID            string          `json:"step_id"`
	Attempt           int             `json:"attempt"`
	ProducerAttemptID string          `json:"producer_attempt_id,omitempty"`
	Revision          int             `json:"revision"`
	ListIndex         *int            `json:"list_index,omitempty"`
	Selected          bool            `json:"selected"`
	Validity          string          `json:"validity"`
	ChangeSource      string          `json:"change_source"`
	ContentType       string          `json:"content_type"`
	Value             json.RawMessage `json:"value,omitempty"`
	Caption           *string         `json:"caption,omitempty"`
	Deleted           bool            `json:"deleted"`
	CreatedAt         time.Time       `json:"created_at"`
}
