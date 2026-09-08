package orm

import "time"

// Fork provenance deliberately has no cascading relationship to its source.
type ConversationForkOrigin struct {
	ConversationID        string    `gorm:"column:conversation_id;type:varchar(36);primaryKey" json:"conversation_id"`
	SourceConversationID  string    `gorm:"column:source_conversation_id;type:varchar(36);not null;index" json:"source_conversation_id"`
	SourceHistoryID       string    `gorm:"column:source_history_id;type:varchar(36);not null" json:"source_history_id"`
	SourceSeq             int       `gorm:"column:source_seq;not null" json:"source_seq"`
	SourceHistoryRevision string    `gorm:"column:source_history_revision;type:varchar(80);not null" json:"source_history_revision"`
	SourcePrefixRevision  string    `gorm:"column:source_prefix_revision;type:varchar(80);not null" json:"source_prefix_revision"`
	SourceTitleSnapshot   string    `gorm:"column:source_title_snapshot;type:varchar(255);not null" json:"source_title_snapshot"`
	ForkedAt              time.Time `gorm:"column:forked_at;not null" json:"forked_at"`
}

func (ConversationForkOrigin) TableName() string { return "conversation_fork_origins" }

// Successful operation receipts survive deletion of either conversation.
type ConversationForkRequest struct {
	ActorUserID    string    `gorm:"column:actor_user_id;type:varchar(255);primaryKey"`
	IdempotencyKey string    `gorm:"column:idempotency_key;type:varchar(128);primaryKey"`
	RequestHash    string    `gorm:"column:request_hash;type:varchar(80);not null"`
	ConversationID string    `gorm:"column:conversation_id;type:varchar(36);not null"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
}

func (ConversationForkRequest) TableName() string { return "conversation_fork_requests" }
