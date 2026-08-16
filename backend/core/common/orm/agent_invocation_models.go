package orm

import (
	"encoding/json"
	"time"
)

// AgentInvocation records one logical MCP tools/call received by the local
// LazyMind connector. Workflow state and artifact content remain in their
// authoritative Runtime tables; this row only carries provenance and links.
type AgentInvocation struct {
	ID                  string          `gorm:"column:id;type:varchar(80);primaryKey" json:"invocation_id"`
	OwnerUserID         string          `gorm:"column:owner_user_id;type:varchar(255);not null;index:idx_agent_invocations_owner_started,priority:1" json:"-"`
	ClientName          string          `gorm:"column:client_name;type:varchar(128);not null;default:'';index" json:"client_name"`
	ClientVersion       string          `gorm:"column:client_version;type:varchar(128);not null;default:''" json:"client_version,omitempty"`
	ConnectorName       string          `gorm:"column:connector_name;type:varchar(128);not null;default:''" json:"connector_name"`
	ConnectorVersion    string          `gorm:"column:connector_version;type:varchar(64);not null;default:''" json:"connector_version,omitempty"`
	ConnectorInstanceID string          `gorm:"column:connector_instance_id;type:varchar(80);not null;default:'';index" json:"connector_instance_id"`
	ProtocolVersion     string          `gorm:"column:protocol_version;type:varchar(64);not null;default:''" json:"protocol_version,omitempty"`
	Transport           string          `gorm:"column:transport;type:varchar(32);not null;default:'stdio'" json:"transport"`
	ToolName            string          `gorm:"column:tool_name;type:varchar(128);not null;index" json:"tool_name"`
	ReadOnly            bool            `gorm:"column:read_only;not null;default:false" json:"read_only"`
	Status              string          `gorm:"column:status;type:varchar(32);not null;index" json:"status"`
	RequestHash         string          `gorm:"column:request_hash;type:varchar(64);not null" json:"request_hash"`
	RequestSummary      json.RawMessage `gorm:"column:request_summary;type:jsonb;not null;default:'{}'" json:"request_summary,omitempty"`
	ResultSummary       json.RawMessage `gorm:"column:result_summary;type:jsonb;not null;default:'{}'" json:"result_summary,omitempty"`
	ErrorCode           string          `gorm:"column:error_code;type:varchar(128);not null;default:''" json:"error_code,omitempty"`
	Retryable           bool            `gorm:"column:retryable;not null;default:false" json:"retryable,omitempty"`
	WorkflowID          string          `gorm:"column:workflow_id;type:varchar(255);not null;default:'';index" json:"workflow_id,omitempty"`
	SessionID           string          `gorm:"column:session_id;type:varchar(80);not null;default:'';index" json:"session_id,omitempty"`
	StepID              string          `gorm:"column:step_id;type:varchar(128);not null;default:''" json:"step_id,omitempty"`
	AttemptID           string          `gorm:"column:attempt_id;type:varchar(80);not null;default:'';index" json:"attempt_id,omitempty"`
	ResourceID          string          `gorm:"column:resource_id;type:varchar(128);not null;default:''" json:"resource_id,omitempty"`
	ArtifactID          string          `gorm:"column:artifact_id;type:varchar(80);not null;default:''" json:"artifact_id,omitempty"`
	CommandID           string          `gorm:"column:command_id;type:varchar(128);not null;default:''" json:"command_id,omitempty"`
	ExternalRef         string          `gorm:"column:external_ref;type:varchar(255);not null;default:''" json:"external_ref,omitempty"`
	StartedAt           time.Time       `gorm:"column:started_at;not null;index:idx_agent_invocations_owner_started,priority:2,sort:desc" json:"started_at"`
	FinishedAt          *time.Time      `gorm:"column:finished_at" json:"finished_at,omitempty"`
	CreatedAt           time.Time       `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt           time.Time       `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (AgentInvocation) TableName() string { return "agent_invocations" }
