package orm

import (
	"encoding/json"
	"time"
)

// ExternalAgentBinding is the durable one-to-one correspondence between one
// provider-native thread and one LazyMind conversation.
type ExternalAgentBinding struct {
	ID                string    `gorm:"column:id;type:varchar(36);primaryKey" json:"binding_id"`
	ConversationID    string    `gorm:"column:conversation_id;type:varchar(36);not null;uniqueIndex:uk_external_agent_binding_conversation" json:"conversation_id"`
	Provider          string    `gorm:"column:provider;type:varchar(32);not null;uniqueIndex:uk_external_agent_binding_thread,priority:1" json:"provider"`
	HostID            string    `gorm:"column:host_id;type:varchar(128);not null;uniqueIndex:uk_external_agent_binding_thread,priority:2" json:"host_id"`
	ProviderThreadID  string    `gorm:"column:provider_thread_id;type:varchar(128);not null;uniqueIndex:uk_external_agent_binding_thread,priority:3" json:"provider_thread_id"`
	ManagedByLazyMind bool      `gorm:"column:managed_by_lazymind;not null;default:false" json:"managed_by_lazymind"`
	CreatedByUserID   string    `gorm:"column:created_by_user_id;type:varchar(255);not null" json:"-"`
	CreatedAt         time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (ExternalAgentBinding) TableName() string { return "external_agent_bindings" }

// ExternalAgentSession is the provider-native catalog projection. It proves a
// session exists in the local provider store, independently of whether that
// session has a LazyMind Conversation binding.
type ExternalAgentSession struct {
	ID               string     `gorm:"column:id;type:varchar(36);primaryKey" json:"session_id"`
	OwnerUserID      string     `gorm:"column:owner_user_id;type:varchar(255);not null;uniqueIndex:uk_external_agent_session,priority:1;index" json:"-"`
	Provider         string     `gorm:"column:provider;type:varchar(32);not null;uniqueIndex:uk_external_agent_session,priority:2;index" json:"provider"`
	HostID           string     `gorm:"column:host_id;type:varchar(128);not null;uniqueIndex:uk_external_agent_session,priority:3;index" json:"host_id"`
	ProviderThreadID string     `gorm:"column:provider_thread_id;type:varchar(128);not null;uniqueIndex:uk_external_agent_session,priority:4" json:"provider_thread_id"`
	ProjectKey       string     `gorm:"column:project_key;type:varchar(128);not null;default:''" json:"project_key"`
	ProjectName      string     `gorm:"column:project_name;type:varchar(200);not null;default:''" json:"project_name"`
	DisplayName      string     `gorm:"column:display_name;type:varchar(255);not null;default:''" json:"display_name"`
	TurnCount        int        `gorm:"column:turn_count;not null;default:0" json:"turn_count"`
	Active           bool       `gorm:"column:active;not null;default:true;index" json:"active"`
	NativeUpdatedAt  *time.Time `gorm:"column:native_updated_at" json:"native_updated_at,omitempty"`
	LastSeenAt       time.Time  `gorm:"column:last_seen_at;not null;index" json:"last_seen_at"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt        time.Time  `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (ExternalAgentSession) TableName() string { return "external_agent_sessions" }

// ExternalChatRun is the operational record for one external Agent turn. The
// same read model covers LazyMind-managed turns and observed MCP activity.
type ExternalChatRun struct {
	ID                string          `gorm:"column:id;type:varchar(36);primaryKey" json:"run_id"`
	RequestID         string          `gorm:"column:request_id;type:varchar(255);not null;uniqueIndex:uk_external_agent_run_request,priority:2" json:"request_id"`
	ConversationID    string          `gorm:"column:conversation_id;type:varchar(36);not null;index" json:"conversation_id"`
	HistoryID         string          `gorm:"column:history_id;type:varchar(36);not null;index" json:"history_id"`
	Provider          string          `gorm:"column:provider;type:varchar(32);not null;uniqueIndex:uk_external_agent_run_request,priority:1" json:"provider"`
	ProviderThreadID  string          `gorm:"column:provider_thread_id;type:varchar(128);not null;default:'';index" json:"provider_thread_id,omitempty"`
	ProviderTurnID    string          `gorm:"column:provider_turn_id;type:varchar(128)" json:"provider_turn_id,omitempty"`
	ActorUserID       string          `gorm:"column:actor_user_id;type:varchar(255);not null;index" json:"-"`
	Action            string          `gorm:"column:action;type:varchar(32);not null;default:'start'" json:"action"`
	Status            string          `gorm:"column:status;type:varchar(32);not null;index" json:"status"`
	ErrorMessage      string          `gorm:"column:error_message;type:text" json:"error_message,omitempty"`
	Prompt            string          `gorm:"column:prompt;type:text;not null;default:''" json:"-"`
	Query             string          `gorm:"column:query;type:text;not null;default:''" json:"-"`
	Sequence          int             `gorm:"column:sequence;not null;default:0" json:"sequence"`
	HistoryExt        json.RawMessage `gorm:"column:history_ext;type:json" json:"-"`
	HostID            string          `gorm:"column:host_id;type:varchar(128);not null;default:''" json:"host_id,omitempty"`
	LeaseToken        string          `gorm:"column:lease_token;type:varchar(64);not null;default:''" json:"-"`
	LeaseExpiresAt    *time.Time      `gorm:"column:lease_expires_at;index" json:"lease_expires_at,omitempty"`
	ClaimedAt         *time.Time      `gorm:"column:claimed_at" json:"claimed_at,omitempty"`
	LastHeartbeatAt   *time.Time      `gorm:"column:last_heartbeat_at" json:"last_heartbeat_at,omitempty"`
	StopRequested     bool            `gorm:"column:stop_requested;not null;default:false" json:"stop_requested"`
	ClaimCount        int             `gorm:"column:claim_count;not null;default:0" json:"claim_count"`
	NextEventSequence int64           `gorm:"column:next_event_sequence;not null;default:0" json:"event_count"`
	CompletedAt       *time.Time      `gorm:"column:completed_at" json:"completed_at,omitempty"`
	CreatedAt         time.Time       `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt         time.Time       `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (ExternalChatRun) TableName() string { return "external_agent_runs" }

// ExternalChatRunEvent is an append-only transport journal. It is not a
// domain-event source: conversation, Workflow and artifact tables remain the
// product authorities for their own state.
type ExternalChatRunEvent struct {
	ID               string    `gorm:"column:id;type:varchar(64);primaryKey" json:"event_id"`
	RunID            string    `gorm:"column:run_id;type:varchar(36);not null;uniqueIndex:uk_external_chat_run_event_sequence,priority:1;index" json:"run_id"`
	Sequence         int64     `gorm:"column:sequence;not null;uniqueIndex:uk_external_chat_run_event_sequence,priority:2" json:"sequence"`
	Type             string    `gorm:"column:type;type:varchar(32);not null" json:"type"`
	Text             string    `gorm:"column:text;type:text" json:"text,omitempty"`
	ProviderThreadID string    `gorm:"column:provider_thread_id;type:varchar(128);not null;default:''" json:"provider_thread_id,omitempty"`
	ErrorMessage     string    `gorm:"column:error_message;type:text" json:"error,omitempty"`
	CreatedAt        time.Time `gorm:"column:created_at;not null" json:"created_at"`
}

func (ExternalChatRunEvent) TableName() string { return "external_chat_run_events" }

// ExternalChatHost is a durable presence projection. Availability is still
// determined by its short TTL, so a crashed connector is never shown online.
type ExternalChatHost struct {
	ActorUserID       string    `gorm:"column:actor_user_id;type:varchar(255);primaryKey" json:"-"`
	Provider          string    `gorm:"column:provider;type:varchar(32);primaryKey" json:"provider"`
	HostID            string    `gorm:"column:host_id;type:varchar(128);primaryKey" json:"host_id"`
	Installed         bool      `gorm:"column:installed;not null" json:"installed"`
	Ready             bool      `gorm:"column:ready;not null" json:"ready"`
	UnavailableReason string    `gorm:"column:unavailable_reason;type:varchar(512);not null;default:''" json:"unavailable_reason,omitempty"`
	LastSeen          time.Time `gorm:"column:last_seen;not null;index" json:"last_seen"`
	UpdatedAt         time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (ExternalChatHost) TableName() string { return "external_chat_hosts" }
