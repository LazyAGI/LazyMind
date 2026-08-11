package orm

import (
	"encoding/json"
	"time"
)

// ExternalAgentBinding links one LazyMind conversation to a provider-native
// thread. Provider history stays in the provider and is never mirrored here.
type ExternalAgentBinding struct {
	ID                string    `gorm:"column:id;type:varchar(36);primaryKey"`
	ConversationID    string    `gorm:"column:conversation_id;type:varchar(36);not null;uniqueIndex:uk_external_agent_binding_conversation"`
	Provider          string    `gorm:"column:provider;type:varchar(32);not null;uniqueIndex:uk_external_agent_binding_thread,priority:1"`
	ProviderThreadID  string    `gorm:"column:provider_thread_id;type:varchar(128);not null;uniqueIndex:uk_external_agent_binding_thread,priority:2"`
	ManagedByLazyMind bool      `gorm:"column:managed_by_lazymind;not null;default:false"`
	CreatedByUserID   string    `gorm:"column:created_by_user_id;type:varchar(255);not null"`
	CreatedAt         time.Time `gorm:"column:created_at;not null"`
	UpdatedAt         time.Time `gorm:"column:updated_at;not null"`
}

func (ExternalAgentBinding) TableName() string { return "external_agent_bindings" }

// ExternalAgentRun stores control and audit metadata for one LazyMind request.
// Detailed provider events and transcript items remain provider-native.
type ExternalAgentRun struct {
	ID               string    `gorm:"column:id;type:varchar(36);primaryKey"`
	RequestID        string    `gorm:"column:request_id;type:varchar(255);not null;uniqueIndex:uk_external_agent_run_request,priority:2"`
	ConversationID   string    `gorm:"column:conversation_id;type:varchar(36);not null;index"`
	HistoryID        string    `gorm:"column:history_id;type:varchar(36);not null"`
	Provider         string    `gorm:"column:provider;type:varchar(32);not null;uniqueIndex:uk_external_agent_run_request,priority:1"`
	ProviderThreadID string    `gorm:"column:provider_thread_id;type:varchar(128);not null;index"`
	ProviderTurnID   string    `gorm:"column:provider_turn_id;type:varchar(128)"`
	ActorUserID      string    `gorm:"column:actor_user_id;type:varchar(255);not null"`
	Action           string    `gorm:"column:action;type:varchar(32);not null;default:start"`
	Status           string    `gorm:"column:status;type:varchar(32);not null;index"`
	ErrorMessage     string    `gorm:"column:error_message;type:text"`
	ControlRelease   string    `gorm:"column:control_release;type:varchar(32);not null;default:''"`
	ControlError     string    `gorm:"column:control_error;type:text"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null"`
}

func (ExternalAgentRun) TableName() string { return "external_agent_runs" }

// ExternalAgentOperation fences provider mutations that cannot be committed
// atomically with their remote side effect. A pending receipt is never replayed;
// a completed receipt returns its canonical result.
type ExternalAgentOperation struct {
	ID          string          `gorm:"column:id;type:varchar(36);primaryKey"`
	ActorUserID string          `gorm:"column:actor_user_id;type:varchar(255);not null;uniqueIndex:uk_external_agent_operation,priority:1"`
	OperationID string          `gorm:"column:operation_id;type:varchar(255);not null;uniqueIndex:uk_external_agent_operation,priority:2"`
	Kind        string          `gorm:"column:kind;type:varchar(64);not null;uniqueIndex:uk_external_agent_operation,priority:3"`
	RequestHash string          `gorm:"column:request_hash;type:varchar(64);not null"`
	Status      string          `gorm:"column:status;type:varchar(32);not null"`
	Result      json.RawMessage `gorm:"column:result;type:jsonb"`
	CreatedAt   time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time       `gorm:"column:updated_at;not null"`
}

func (ExternalAgentOperation) TableName() string {
	return "external_agent_operations"
}
