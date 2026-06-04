package orm

import (
	"encoding/json"
	"time"
)

type EvalSet struct {
	ID             string          `gorm:"column:id;type:varchar(64);primaryKey"`
	Name           string          `gorm:"column:name;type:varchar(255);not null"`
	Description    string          `gorm:"column:description;type:text;not null;default:''"`
	DatasetIDs     json.RawMessage `gorm:"column:dataset_ids;type:jsonb;not null;default:'[]'"`
	OwnerID        string          `gorm:"column:owner_id;type:varchar(255);not null;index"`
	GroupID        string          `gorm:"column:group_id;type:varchar(255);not null;default:'';index"`
	ShardID        string          `gorm:"column:shard_id;type:varchar(64);not null;index"`
	Status         string          `gorm:"column:status;type:varchar(32);not null;default:'active';index"`
	ItemCount      int64           `gorm:"column:item_count;not null;default:0"`
	CreateUserID   string          `gorm:"column:create_user_id;type:varchar(255);not null"`
	CreateUserName string          `gorm:"column:create_user_name;type:varchar(255);not null;default:''"`
	CreatedAt      time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time       `gorm:"column:updated_at;not null"`
}

func (EvalSet) TableName() string { return "eval_sets" }

type EvalSetShard struct {
	ID                     string     `gorm:"column:id;type:varchar(64);primaryKey"`
	Status                 string     `gorm:"column:status;type:varchar(32);not null;default:'open';index"`
	RowLimit               int64      `gorm:"column:row_limit;not null;default:200000"`
	RowOpenThreshold       int64      `gorm:"column:row_open_threshold;not null;default:120000"`
	SizeLimitBytes         int64      `gorm:"column:size_limit_bytes;not null;default:8589934592"`
	SizeOpenThresholdBytes int64      `gorm:"column:size_open_threshold_bytes;not null;default:5368709120"`
	ActualRows             int64      `gorm:"column:actual_rows;not null;default:0"`
	EstimatedBytes         int64      `gorm:"column:estimated_bytes;not null;default:0"`
	CreatedAt              time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt              time.Time  `gorm:"column:updated_at;not null"`
	SealedAt               *time.Time `gorm:"column:sealed_at"`
}

func (EvalSetShard) TableName() string { return "eval_set_shards" }

type EvalSetItem struct {
	ShardID           string    `gorm:"column:shard_id;type:varchar(64);primaryKey;not null;index:idx_eval_set_items_set_created,priority:1;index:idx_eval_set_items_set_source,priority:1;index:idx_eval_set_items_set_type,priority:1;index:idx_eval_set_items_set_updated,priority:1"`
	ID                string    `gorm:"column:id;type:varchar(64);primaryKey"`
	EvalSetID         string    `gorm:"column:eval_set_id;type:varchar(64);not null;index:idx_eval_set_items_set_created,priority:2;index:idx_eval_set_items_set_source,priority:2;index:idx_eval_set_items_set_type,priority:2;index:idx_eval_set_items_set_updated,priority:2"`
	CaseID            string    `gorm:"column:case_id;type:varchar(255);not null;default:''"`
	Question          string    `gorm:"column:question;type:text;not null"`
	GroundTruth       string    `gorm:"column:ground_truth;type:text;not null"`
	QuestionType      string    `gorm:"column:question_type;type:varchar(128);not null;index:idx_eval_set_items_set_type,priority:3"`
	GenerateReason    string    `gorm:"column:generate_reason;type:text;not null;default:''"`
	KeyPoints         string    `gorm:"column:key_points;type:text;not null;default:''"`
	ReferenceChunkIDs string    `gorm:"column:reference_chunk_ids;type:text;not null;default:''"`
	ReferenceContext  string    `gorm:"column:reference_context;type:text;not null;default:''"`
	ReferenceDoc      string    `gorm:"column:reference_doc;type:text;not null;default:''"`
	ReferenceDocIDs   string    `gorm:"column:reference_doc_ids;type:text;not null;default:''"`
	IsDeleted         bool      `gorm:"column:is_deleted;not null;default:false;comment:template field, not logical delete"`
	EstimatedBytes    int64     `gorm:"column:estimated_bytes;not null;default:0"`
	Source            string    `gorm:"column:source;type:varchar(32);not null;index:idx_eval_set_items_set_source,priority:3"`
	SourceSessionID   string    `gorm:"column:source_session_id;type:varchar(128);not null;default:''"`
	SourceHistoryID   string    `gorm:"column:source_history_id;type:varchar(128);not null;default:''"`
	CreateUserID      string    `gorm:"column:create_user_id;type:varchar(255);not null"`
	CreateUserName    string    `gorm:"column:create_user_name;type:varchar(255);not null;default:''"`
	CreatedAt         time.Time `gorm:"column:created_at;not null;index:idx_eval_set_items_set_created,priority:4"`
	UpdatedAt         time.Time `gorm:"column:updated_at;not null;index:idx_eval_set_items_set_updated,priority:3"`
}

func (EvalSetItem) TableName() string { return "eval_set_items" }
