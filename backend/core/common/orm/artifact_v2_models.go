package orm

import (
	"encoding/json"
	"time"
)

type ArtifactV2 struct {
	ID             string     `gorm:"column:id;type:varchar(36);primaryKey"`
	TenantID       string     `gorm:"column:tenant_id;type:varchar(128);not null;index:idx_artifacts_owner_created,priority:1"`
	OwnerUserID    string     `gorm:"column:owner_user_id;type:varchar(255);not null;index:idx_artifacts_owner_created,priority:2"`
	ProjectID      string     `gorm:"column:project_id;type:varchar(36)"`
	Kind           string     `gorm:"column:kind;type:varchar(32);not null"`
	Title          string     `gorm:"column:title;type:varchar(255);not null"`
	LogicalKey     string     `gorm:"column:logical_key;type:varchar(255)"`
	Status         string     `gorm:"column:status;type:varchar(24);not null"`
	Classification string     `gorm:"column:classification;type:varchar(32);not null;default:internal"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null;index:idx_artifacts_owner_created,priority:3"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null"`
	DeletedAt      *time.Time `gorm:"column:deleted_at"`
}

func (ArtifactV2) TableName() string { return "artifacts" }

type ArtifactBlob struct {
	ID               string    `gorm:"column:id;type:varchar(64);primaryKey"`
	TenantID         string    `gorm:"column:tenant_id;type:varchar(128);not null;uniqueIndex:uk_artifact_blobs_tenant_hash_size,priority:1"`
	SHA256           string    `gorm:"column:sha256;type:varchar(64);not null;uniqueIndex:uk_artifact_blobs_tenant_hash_size,priority:2"`
	Size             int64     `gorm:"column:size;not null;uniqueIndex:uk_artifact_blobs_tenant_hash_size,priority:3"`
	MIMEType         string    `gorm:"column:mime_type;type:varchar(255);not null"`
	StorageBackend   string    `gorm:"column:storage_backend;type:varchar(32);not null"`
	StorageKey       string    `gorm:"column:storage_key;type:text;not null"`
	EncryptionKeyRef string    `gorm:"column:encryption_key_ref;type:varchar(255)"`
	State            string    `gorm:"column:state;type:varchar(24);not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
}

func (ArtifactBlob) TableName() string { return "artifact_blobs" }

type ArtifactRevision struct {
	ID                    string          `gorm:"column:id;type:varchar(36);primaryKey"`
	ArtifactID            string          `gorm:"column:artifact_id;type:varchar(36);not null;uniqueIndex:uk_artifact_revision_no,priority:1"`
	RevisionNo            int64           `gorm:"column:revision_no;not null;uniqueIndex:uk_artifact_revision_no,priority:2"`
	ParentRevisionID      string          `gorm:"column:parent_revision_id;type:varchar(36)"`
	MergeParentRevisionID string          `gorm:"column:merge_parent_revision_id;type:varchar(36)"`
	BlobID                string          `gorm:"column:blob_id;type:varchar(64)"`
	InlineJSON            json.RawMessage `gorm:"column:inline_json;type:jsonb"`
	ContentType           string          `gorm:"column:content_type;type:varchar(64);not null"`
	SchemaName            string          `gorm:"column:schema_name;type:varchar(128)"`
	SchemaVersion         string          `gorm:"column:schema_version;type:varchar(32)"`
	ContentHash           string          `gorm:"column:content_hash;type:varchar(80);not null"`
	Size                  int64           `gorm:"column:size;not null"`
	Caption               *string         `gorm:"column:caption"`
	Metadata              json.RawMessage `gorm:"column:metadata;type:jsonb;not null"`
	ProducerType          string          `gorm:"column:producer_type;type:varchar(32);not null"`
	ProducerID            string          `gorm:"column:producer_id;type:varchar(128)"`
	ProducerRunID         string          `gorm:"column:producer_run_id;type:varchar(128)"`
	ProducerEventID       string          `gorm:"column:producer_event_id;type:varchar(128)"`
	CreatedBy             string          `gorm:"column:created_by;type:varchar(255);not null"`
	CreatedAt             time.Time       `gorm:"column:created_at;not null"`
}

func (ArtifactRevision) TableName() string { return "artifact_revisions" }

type ArtifactHead struct {
	ArtifactID string    `gorm:"column:artifact_id;type:varchar(36);primaryKey"`
	Channel    string    `gorm:"column:channel;type:varchar(32);primaryKey"`
	RevisionID string    `gorm:"column:revision_id;type:varchar(36);not null"`
	Version    int64     `gorm:"column:version;not null"`
	UpdatedAt  time.Time `gorm:"column:updated_at;not null"`
}

func (ArtifactHead) TableName() string { return "artifact_heads" }

type ArtifactBinding struct {
	ID          string    `gorm:"column:id;type:varchar(36);primaryKey"`
	ArtifactID  string    `gorm:"column:artifact_id;type:varchar(36);not null"`
	RevisionID  string    `gorm:"column:revision_id;type:varchar(36)"`
	ScopeType   string    `gorm:"column:scope_type;type:varchar(32);not null;index:idx_artifact_bindings_scope,priority:1"`
	ScopeID     string    `gorm:"column:scope_id;type:varchar(128);not null;index:idx_artifact_bindings_scope,priority:2"`
	Role        string    `gorm:"column:role;type:varchar(32);not null;index:idx_artifact_bindings_scope,priority:3"`
	SlotKey     string    `gorm:"column:slot_key;type:varchar(255)"`
	ListItemKey string    `gorm:"column:list_item_key;type:varchar(128)"`
	Position    *int      `gorm:"column:position"`
	Validity    string    `gorm:"column:validity;type:varchar(24);not null"`
	FollowHead  bool      `gorm:"column:follow_head;not null;default:false"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
}

func (ArtifactBinding) TableName() string { return "artifact_bindings" }

type ArtifactDependency struct {
	OutputRevisionID string `gorm:"column:output_revision_id;type:varchar(36);primaryKey"`
	InputRevisionID  string `gorm:"column:input_revision_id;type:varchar(36);primaryKey"`
	Role             string `gorm:"column:role;type:varchar(64);primaryKey"`
	Required         bool   `gorm:"column:required;not null"`
}

func (ArtifactDependency) TableName() string { return "artifact_dependencies" }

type ArtifactIdempotency struct {
	TenantID       string          `gorm:"column:tenant_id;type:varchar(128);primaryKey"`
	IdempotencyKey string          `gorm:"column:idempotency_key;type:varchar(255);primaryKey"`
	Operation      string          `gorm:"column:operation;type:varchar(64);primaryKey"`
	RequestHash    string          `gorm:"column:request_hash;type:varchar(64);not null"`
	ResponseJSON   json.RawMessage `gorm:"column:response_json;type:jsonb;not null"`
	CreatedAt      time.Time       `gorm:"column:created_at;not null"`
}

func (ArtifactIdempotency) TableName() string { return "artifact_idempotency" }

type ArtifactEventOutbox struct {
	ID           string          `gorm:"column:id;type:varchar(36);primaryKey"`
	EventType    string          `gorm:"column:event_type;type:varchar(64);not null"`
	Payload      json.RawMessage `gorm:"column:payload;type:jsonb;not null"`
	Status       string          `gorm:"column:status;type:varchar(24);not null"`
	AttemptCount int             `gorm:"column:attempt_count;not null;default:0"`
	NextAttempt  time.Time       `gorm:"column:next_attempt_at;not null"`
	CreatedAt    time.Time       `gorm:"column:created_at;not null"`
}

func (ArtifactEventOutbox) TableName() string { return "artifact_event_outbox" }
