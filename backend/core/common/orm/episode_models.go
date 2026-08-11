package orm

// EpisodeMemory stores the current authoritative Episode Memory rows. Episode
// identity is user- and conversation-scoped; NormalizedSummary is persisted so
// both PostgreSQL and SQLite enforce the same idempotency contract.
type EpisodeMemory struct {
	RowID             uint64 `gorm:"column:row_id;primaryKey;autoIncrement"`
	ID                string `gorm:"column:id;type:varchar(36);not null;uniqueIndex:uk_episode_memories_user_id,priority:2;index:idx_episode_memories_user_recorded,priority:3,sort:desc;index:idx_episode_memories_user_conversation_recorded,priority:4"`
	UserID            string `gorm:"column:user_id;type:varchar(255);not null;uniqueIndex:uk_episode_memories_user_id,priority:1;uniqueIndex:uk_episode_memories_identity,priority:1;index:idx_episode_memories_user_recorded,priority:1;index:idx_episode_memories_user_conversation_recorded,priority:1"`
	ConversationID    string `gorm:"column:conversation_id;type:varchar(255);not null;uniqueIndex:uk_episode_memories_identity,priority:2;index:idx_episode_memories_user_conversation_recorded,priority:2"`
	SourceKind        string `gorm:"column:source_kind;type:varchar(32);not null"`
	EpisodeType       string `gorm:"column:episode_type;type:varchar(16);not null"`
	Summary           string `gorm:"column:summary;type:text;not null"`
	NormalizedSummary string `gorm:"column:normalized_summary;type:text;not null;uniqueIndex:uk_episode_memories_identity,priority:3"`
	SearchText        string `gorm:"column:search_text;type:text;not null"`
	TokenizerVersion  string `gorm:"column:tokenizer_version;type:varchar(64);not null"`
	OccurredAtMS      int64  `gorm:"column:occurred_at_ms;not null"`
	RecordedAtMS      int64  `gorm:"column:recorded_at_ms;not null;index:idx_episode_memories_user_recorded,priority:2,sort:desc;index:idx_episode_memories_user_conversation_recorded,priority:3"`
	HitCount          int64  `gorm:"column:hit_count;not null;default:0"`
}

func (EpisodeMemory) TableName() string { return "episode_memories" }
