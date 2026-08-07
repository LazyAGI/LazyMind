package domain

import "time"

// Session is the public Workflow domain view. Its persistence mapping keeps the
// historical physical table and columns so rolling deployments need no rewrite.
type Session struct {
	ID               string    `gorm:"column:id;type:varchar(36);primaryKey" json:"id"`
	ConversationID   string    `gorm:"column:conversation_id;type:varchar(36);not null" json:"conversation_id"`
	WorkflowID       string    `gorm:"column:plugin_id;type:varchar(64);not null" json:"workflow_id"`
	WorkflowRef      string    `gorm:"column:plugin_ref;type:varchar(512);not null;default:''" json:"workflow_ref"`
	WorkflowRevision string    `gorm:"column:plugin_revision_id;type:varchar(36);not null;default:''" json:"workflow_revision_id"`
	OriginHost       string    `gorm:"column:origin_host;type:varchar(32);not null;default:'lazymind'" json:"origin_host"`
	OriginRef        string    `gorm:"column:origin_ref;type:varchar(255);not null;default:''" json:"origin_ref"`
	ControllerHost   string    `gorm:"column:controller_host;type:varchar(32);not null;default:'lazymind'" json:"controller_host"`
	Status           string    `gorm:"column:status;type:varchar(16);not null;default:active" json:"status"`
	StateVersion     int64     `gorm:"column:state_version;not null;default:0" json:"state_version"`
	CreatedAt        time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (Session) TableName() string { return "plugin_sessions" }

type SchemaCapabilities struct {
	HostNeutralSessionRefs bool `json:"host_neutral_session_refs"`
}
