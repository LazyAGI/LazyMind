// Package taskdisplay defines the public, non-debug task presentation contract.
package taskdisplay

import "time"

const SchemaVersion = 1
const PageLimit = 100
const MaxProcessSteps = 1000

type CollectionPage struct {
	Total      int     `json:"total"`
	NextCursor *string `json:"next_cursor" nullable:"true" required:"true"`
	Revision   int64   `json:"revision"`
}
type Pages struct {
	ProcessSteps   CollectionPage `json:"process_steps"`
	Sources        CollectionPage `json:"sources"`
	StageArtifacts CollectionPage `json:"stage_artifacts"`
}
type Timing struct {
	StartedAt          *time.Time `json:"started_at" nullable:"true" required:"true"`
	FinishedAt         *time.Time `json:"finished_at" nullable:"true" required:"true"`
	ExecutionElapsedMS *int64     `json:"execution_elapsed_ms" nullable:"true" required:"true"`
	ThinkingElapsedMS  *int64     `json:"thinking_elapsed_ms" nullable:"true" required:"true"`
	MeasuredAt         time.Time  `json:"measured_at"`
}
type PublicProcessStep struct {
	StepID     string     `json:"step_id"`
	Revision   int64      `json:"revision"`
	Order      int        `json:"order"`
	Title      string     `json:"title"`
	Status     string     `json:"status"`
	Summary    string     `json:"summary,omitempty"`
	StartedAt  *time.Time `json:"started_at" nullable:"true" required:"true"`
	FinishedAt *time.Time `json:"finished_at" nullable:"true" required:"true"`
	ElapsedMS  *int64     `json:"elapsed_ms" nullable:"true" required:"true"`
}
type PublicSource struct {
	SourceID   string  `json:"source_id"`
	Platform   string  `json:"platform"`
	Domain     *string `json:"domain" nullable:"true" required:"true"`
	Title      string  `json:"title"`
	Snippet    string  `json:"snippet,omitempty"`
	URL        string  `json:"url,omitempty"`
	ResourceID string  `json:"resource_id,omitempty"`
	DatasetID  string  `json:"dataset_id,omitempty"`
	Kind       string  `json:"kind"`
}
type Capabilities struct {
	Preview  bool `json:"preview"`
	Open     bool `json:"open"`
	Download bool `json:"download"`
}
type PublicArtifact struct {
	ArtifactID         string       `json:"artifact_id"`
	Revision           int64        `json:"revision"`
	ProducerDisplayKey string       `json:"producer_display_key"`
	Name               string       `json:"name"`
	ContentType        string       `json:"content_type"`
	SizeBytes          *int64       `json:"size_bytes" nullable:"true" required:"true"`
	State              string       `json:"state"`
	PreviewKind        *string      `json:"preview_kind" nullable:"true" required:"true"`
	Capabilities       Capabilities `json:"capabilities"`
	PreviewURL         string       `json:"preview_url,omitempty"`
	OpenURL            string       `json:"open_url,omitempty"`
	DownloadURL        string       `json:"download_url,omitempty"`
	InlineContent      string       `json:"inline_content,omitempty"`
	CreatedAt          time.Time    `json:"created_at"`
}
type OrdinaryTaskView struct {
	SchemaVersion    int                 `json:"schema_version" enum:"1"`
	DisplayKey       string              `json:"display_key"`
	TaskID           *string             `json:"task_id" nullable:"true" required:"true"`
	SessionID        *string             `json:"session_id" nullable:"true" required:"true"`
	WorkflowStepID   *string             `json:"workflow_step_id" nullable:"true" required:"true"`
	AttemptID        *string             `json:"attempt_id" nullable:"true" required:"true"`
	ConversationID   string              `json:"conversation_id"`
	TriggerHistoryID string              `json:"trigger_history_id,omitempty"`
	AgentType        string              `json:"agent_type,omitempty"`
	RunID            string              `json:"run_id"`
	ExecutionID      string              `json:"execution_id"`
	ParallelGroupID  *string             `json:"parallel_group_id" nullable:"true" required:"true"`
	Order            int                 `json:"order"`
	Title            string              `json:"title"`
	Status           string              `json:"status"`
	Revision         int64               `json:"revision"`
	ProcessState     string              `json:"process_state"`
	ProcessSteps     []PublicProcessStep `json:"process_steps"`
	PlanSteps        []string            `json:"plan_steps,omitempty"`
	ProgressPct      *int                `json:"progress_pct,omitempty"`
	Sources          []PublicSource      `json:"sources"`
	StageArtifacts   []PublicArtifact    `json:"stage_artifacts"`
	Pages            Pages               `json:"pages"`
	Timing           Timing              `json:"timing"`

	CapabilityDependency *CapabilityDependency `json:"capability_dependency,omitempty"`
}
type OrdinaryRunView struct {
	RunID           string           `json:"run_id"`
	Revision        int64            `json:"revision"`
	FinalOutputRefs []string         `json:"final_output_refs"`
	FinalArtifacts  []PublicArtifact `json:"final_artifacts"`
}
type SnapshotEvent struct {
	Type          string           `json:"type"`
	SchemaVersion int              `json:"schema_version" enum:"1"`
	EventID       string           `json:"event_id"`
	DisplayKey    string           `json:"display_key"`
	Revision      int64            `json:"revision"`
	EmittedAt     time.Time        `json:"emitted_at"`
	Data          OrdinaryTaskView `json:"data"`
}

func String(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func NewTask() OrdinaryTaskView {
	return OrdinaryTaskView{SchemaVersion: SchemaVersion, ProcessState: "not_provided", ProcessSteps: []PublicProcessStep{}, Sources: []PublicSource{}, StageArtifacts: []PublicArtifact{}, Timing: Timing{MeasuredAt: time.Now().UTC()}}
}
