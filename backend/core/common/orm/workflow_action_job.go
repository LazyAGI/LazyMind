package orm

import "time"

// WorkflowActionJob persists model-only work separately from workflow steps.
// Only the human confirmation endpoint may commit its preview.
type WorkflowActionJob struct {
	ID               string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	SessionID        string    `gorm:"type:varchar(36);not null;index" json:"session_id"`
	SlotID           string    `gorm:"type:varchar(128);not null" json:"slot_id"`
	ListIndex        int       `gorm:"not null" json:"list_index"`
	BaseRevision     int       `gorm:"not null" json:"base_revision"`
	BaseDraftVersion *int64    `json:"base_draft_version,omitempty"`
	Status           string    `gorm:"type:varchar(24);not null" json:"status"`
	Version          int64     `gorm:"not null" json:"version"`
	RequestJSON      string    `gorm:"type:text;not null" json:"-"`
	ResultJSON       string    `gorm:"type:text;not null" json:"-"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (WorkflowActionJob) TableName() string { return "workflow_action_jobs" }
