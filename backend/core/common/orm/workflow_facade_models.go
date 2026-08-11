package orm

import (
	"encoding/json"
	"time"
)

type WorkflowPreparation struct {
	ID              string          `gorm:"column:id;type:varchar(36);primaryKey" json:"preparation_id"`
	IdempotencyKey  string          `gorm:"column:idempotency_key;type:varchar(255);not null;uniqueIndex:uk_workflow_preparation_owner_key,priority:2" json:"idempotency_key"`
	OwnerUserID     string          `gorm:"column:owner_user_id;type:varchar(255);not null;uniqueIndex:uk_workflow_preparation_owner_key,priority:1;index" json:"owner_user_id"`
	WorkflowID      string          `gorm:"column:workflow_id;type:varchar(255);not null" json:"workflow_id"`
	ContractVersion string          `gorm:"column:contract_version;type:varchar(32);not null" json:"contract_version"`
	RequestJSON     json.RawMessage `gorm:"column:request_json;type:jsonb;not null" json:"request"`
	ResponseJSON    json.RawMessage `gorm:"column:response_json;type:jsonb;not null" json:"response"`
	ConsumedAt      *time.Time      `gorm:"column:consumed_at" json:"consumed_at,omitempty"`
	SessionID       string          `gorm:"column:session_id;type:varchar(36);not null;default:''" json:"session_id,omitempty"`
	CreatedAt       time.Time       `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt       time.Time       `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (WorkflowPreparation) TableName() string { return "workflow_preparations" }

type WorkflowEvent struct {
	ID              int64           `gorm:"column:id;primaryKey;autoIncrement" json:"cursor"`
	SessionID       string          `gorm:"column:session_id;type:varchar(36);not null;index:idx_workflow_events_session_cursor,priority:1" json:"session_id"`
	OwnerUserID     string          `gorm:"column:owner_user_id;type:varchar(255);not null;index" json:"-"`
	ContractVersion string          `gorm:"column:contract_version;type:varchar(32);not null" json:"contract_version"`
	EventType       string          `gorm:"column:event_type;type:varchar(64);not null" json:"type"`
	EntityID        string          `gorm:"column:entity_id;type:varchar(255);not null;default:''" json:"entity_id,omitempty"`
	StateVersion    int64           `gorm:"column:state_version;not null;default:0" json:"state_version"`
	CommandID       string          `gorm:"column:command_id;type:varchar(255);not null;default:'';index" json:"command_id,omitempty"`
	PayloadJSON     json.RawMessage `gorm:"column:payload_json;type:jsonb;not null" json:"payload"`
	CreatedAt       time.Time       `gorm:"column:created_at;not null" json:"created_at"`
}

func (WorkflowEvent) TableName() string { return "workflow_events" }

type WorkflowCommand struct {
	CommandID       string          `gorm:"column:command_id;type:varchar(255);primaryKey"`
	OwnerUserID     string          `gorm:"column:owner_user_id;type:varchar(255);not null;index"`
	SessionID       string          `gorm:"column:session_id;type:varchar(36);not null;index"`
	ContractVersion string          `gorm:"column:contract_version;type:varchar(32);not null"`
	RequestHash     string          `gorm:"column:request_hash;type:varchar(64);not null"`
	HTTPStatus      int             `gorm:"column:http_status;not null"`
	ResponseJSON    json.RawMessage `gorm:"column:response_json;type:jsonb;not null"`
	CreatedAt       time.Time       `gorm:"column:created_at;not null"`
}

func (WorkflowCommand) TableName() string { return "workflow_commands" }
