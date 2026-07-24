package orm

import (
	"encoding/json"
	"time"
)

// ConversationArtifact is produced directly by the main ChatAgent and belongs
// to the exact assistant history that emitted it. SubAgent artifacts remain
// task-owned in SubAgentArtifact.
type ConversationArtifact struct {
	ID             string          `gorm:"column:id;type:varchar(36);primaryKey"`
	ConversationID string          `gorm:"column:conversation_id;type:varchar(36);not null;index:idx_conversation_artifacts_owner_conversation_created,priority:2"`
	HistoryID      string          `gorm:"column:history_id;type:varchar(36);not null;index"`
	Filename       string          `gorm:"column:filename;type:varchar(255);not null"`
	Slot           string          `gorm:"column:slot;type:varchar(255);not null"`
	ContentType    string          `gorm:"column:content_type;type:varchar(32);not null"`
	Value          json.RawMessage `gorm:"column:value;type:jsonb;not null"`
	Caption        *string         `gorm:"column:caption"`
	CreateUserID   string          `gorm:"column:create_user_id;type:varchar(255);not null;index:idx_conversation_artifacts_owner_conversation_created,priority:1"`
	CreatedAt      time.Time       `gorm:"column:created_at;not null;index:idx_conversation_artifacts_owner_conversation_created,priority:3"`
}

func (ConversationArtifact) TableName() string { return "conversation_artifacts" }
