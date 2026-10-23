package orm

import (
	"encoding/json"
	"time"
)

// ConversationFollowUp stores messages submitted while a conversation run is
// active. Queue ordering and steering delivery are server authoritative so a
// browser refresh or a second client cannot lose or duplicate the input.
// Accepted steering rows also provide durable transcript metadata until the
// owning conversation is deleted.
type ConversationFollowUp struct {
	ID              string          `gorm:"column:id;type:varchar(36);primaryKey"`
	ConversationID  string          `gorm:"column:conversation_id;type:varchar(36);not null;index:idx_conversation_followups_queue,priority:1"`
	Content         json.RawMessage `gorm:"column:content;type:json;not null"`
	Text            string          `gorm:"column:text;type:text;not null"`
	Mode            string          `gorm:"column:mode;type:varchar(16);not null;default:'queued'"`
	Status          string          `gorm:"column:status;type:varchar(16);not null;default:'pending';index:idx_conversation_followups_queue,priority:2"`
	SortOrder       int64           `gorm:"column:sort_order;not null;default:0;index:idx_conversation_followups_queue,priority:3"`
	Revision        int64           `gorm:"column:revision;not null;default:1"`
	TargetRunID     string          `gorm:"column:target_run_id;type:varchar(64);not null;default:''"`
	TargetHistoryID string          `gorm:"column:target_history_id;type:varchar(36);not null;default:''"`
	SteerID         string          `gorm:"column:steer_id;type:varchar(64);not null;default:''"`
	IdempotencyKey  string          `gorm:"column:idempotency_key;type:varchar(64);not null"`
	ErrorCode       string          `gorm:"column:error_code;type:varchar(64);not null;default:''"`
	ErrorMessage    string          `gorm:"column:error_message;type:text;not null;default:''"`
	AcceptedAt      *time.Time      `gorm:"column:accepted_at"`

	BaseModel
}

func (ConversationFollowUp) TableName() string { return "conversation_followups" }
