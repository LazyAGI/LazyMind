package orm

import (
	"encoding/json"
	"time"
)

// AcademicWork is the canonical identity of a scholarly work. File versions
// stored in knowledge bases are linked through AcademicWorkDocument.
type AcademicWork struct {
	ID                    string          `gorm:"column:id;type:varchar(36);primaryKey"`
	CanonicalTitle        string          `gorm:"column:canonical_title;type:text;not null"`
	NormalizedTitle       string          `gorm:"column:normalized_title;type:text;not null;index"`
	AuthorsJSON           json.RawMessage `gorm:"column:authors_json;type:json;not null"`
	FirstAuthorNormalized string          `gorm:"column:first_author_normalized;type:varchar(255);not null;default:'';index"`
	PublicationYear       int             `gorm:"column:publication_year;not null;default:0;index"`
	Venue                 string          `gorm:"column:venue;type:text;not null;default:''"`
	Abstract              string          `gorm:"column:abstract;type:text;not null;default:''"`
	DOINormalized         string          `gorm:"column:doi_normalized;type:varchar(512);not null;default:'';index"`
	ArxivIDBase           string          `gorm:"column:arxiv_id_base;type:varchar(64);not null;default:'';index"`
	ExternalIDsJSON       json.RawMessage `gorm:"column:external_ids_json;type:json;not null"`
	ProvenanceJSON        json.RawMessage `gorm:"column:metadata_provenance_json;type:json;not null"`
	ResolutionConfidence  float64         `gorm:"column:resolution_confidence;not null;default:0"`
	CreatedAt             time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt             time.Time       `gorm:"column:updated_at;not null"`
}

func (AcademicWork) TableName() string { return "academic_works" }

type AcademicWorkDocument struct {
	AcademicWorkID     string    `gorm:"column:academic_work_id;type:varchar(36);primaryKey"`
	DatasetID          string    `gorm:"column:dataset_id;type:varchar(255);primaryKey;index"`
	DocumentID         string    `gorm:"column:document_id;type:varchar(128);primaryKey;index"`
	VersionKind        string    `gorm:"column:version_kind;type:varchar(32);not null;default:'unknown'"`
	SourceProvider     string    `gorm:"column:source_provider;type:varchar(64);not null;default:''"`
	SourceLocator      string    `gorm:"column:source_locator;type:text;not null;default:''"`
	SourceVersion      string    `gorm:"column:source_version;type:varchar(64);not null;default:''"`
	ContentSHA256      string    `gorm:"column:content_sha256;type:varchar(64);not null;default:'';index"`
	MatchMethod        string    `gorm:"column:match_method;type:varchar(64);not null;default:''"`
	MatchConfidence    float64   `gorm:"column:match_confidence;not null;default:0"`
	IsPreferredVersion bool      `gorm:"column:is_preferred_version;not null;default:false"`
	CreatedAt          time.Time `gorm:"column:created_at;not null"`
	UpdatedAt          time.Time `gorm:"column:updated_at;not null"`
}

func (AcademicWorkDocument) TableName() string { return "academic_work_documents" }

type AcademicReference struct {
	ID                   string          `gorm:"column:id;type:varchar(36);primaryKey"`
	SourceDocumentID     string          `gorm:"column:source_document_id;type:varchar(128);not null;index"`
	SourceWorkID         string          `gorm:"column:source_work_id;type:varchar(36);not null;default:'';index"`
	ReferenceKey         string          `gorm:"column:reference_key;type:varchar(64);not null;default:''"`
	RawText              string          `gorm:"column:raw_text;type:text;not null"`
	Title                string          `gorm:"column:title;type:text;not null;default:''"`
	AuthorsJSON          json.RawMessage `gorm:"column:authors_json;type:json;not null"`
	PublicationYear      int             `gorm:"column:publication_year;not null;default:0"`
	DOINormalized        string          `gorm:"column:doi_normalized;type:varchar(512);not null;default:'';index"`
	ArxivIDBase          string          `gorm:"column:arxiv_id_base;type:varchar(64);not null;default:'';index"`
	ResolvedWorkID       string          `gorm:"column:resolved_work_id;type:varchar(36);not null;default:'';index"`
	ResolutionStatus     string          `gorm:"column:resolution_status;type:varchar(32);not null;default:'unresolved'"`
	ResolutionMethod     string          `gorm:"column:resolution_method;type:varchar(64);not null;default:''"`
	ResolutionConfidence float64         `gorm:"column:resolution_confidence;not null;default:0"`
	Page                 int             `gorm:"column:page;not null;default:0"`
	BBoxJSON             json.RawMessage `gorm:"column:bbox_json;type:json;not null"`
	SegmentIDsJSON       json.RawMessage `gorm:"column:segment_ids_json;type:json;not null"`
	ExtractorName        string          `gorm:"column:extractor_name;type:varchar(128);not null;default:''"`
	ExtractorVersion     string          `gorm:"column:extractor_version;type:varchar(64);not null;default:''"`
	SourceFingerprint    string          `gorm:"column:source_fingerprint;type:varchar(128);not null;default:''"`
	CreatedAt            time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt            time.Time       `gorm:"column:updated_at;not null"`
}

func (AcademicReference) TableName() string { return "academic_references" }

type PaperImportBatch struct {
	ID                    string          `gorm:"column:id;type:varchar(36);primaryKey"`
	EntryType             string          `gorm:"column:entry_type;type:varchar(32);not null"`
	TargetDatasetID       string          `gorm:"column:target_dataset_id;type:varchar(255);not null;index"`
	TargetPID             string          `gorm:"column:target_pid;type:varchar(255);not null;default:''"`
	SourceDocumentIDsJSON json.RawMessage `gorm:"column:source_document_ids_json;type:json;not null"`
	PolicySnapshotJSON    json.RawMessage `gorm:"column:policy_snapshot_json;type:json;not null"`
	Status                string          `gorm:"column:status;type:varchar(32);not null;index"`
	TotalItems            int             `gorm:"column:total_items;not null;default:0"`
	CompletedItems        int             `gorm:"column:completed_items;not null;default:0"`
	FailedItems           int             `gorm:"column:failed_items;not null;default:0"`
	SkippedItems          int             `gorm:"column:skipped_items;not null;default:0"`
	NeedsActionItems      int             `gorm:"column:needs_action_items;not null;default:0"`
	AsyncJobID            string          `gorm:"column:async_job_id;type:varchar(36);not null;default:''"`
	IdempotencyKey        string          `gorm:"column:idempotency_key;type:varchar(128);not null;default:'';index"`
	CreatedBy             string          `gorm:"column:created_by;type:varchar(255);not null;index"`
	CreatedAt             time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt             time.Time       `gorm:"column:updated_at;not null"`
}

func (PaperImportBatch) TableName() string { return "paper_import_batches" }

type PaperImportItem struct {
	ID                    string          `gorm:"column:id;type:varchar(36);primaryKey"`
	BatchID               string          `gorm:"column:batch_id;type:varchar(36);not null;index"`
	AcademicWorkID        string          `gorm:"column:academic_work_id;type:varchar(36);not null;index"`
	ReferenceIDsJSON      json.RawMessage `gorm:"column:reference_ids_json;type:json;not null"`
	PresenceSnapshotJSON  json.RawMessage `gorm:"column:presence_snapshot_json;type:json;not null"`
	SelectedCandidateJSON json.RawMessage `gorm:"column:selected_candidate_json;type:json;not null"`
	Stage                 string          `gorm:"column:stage;type:varchar(32);not null"`
	Status                string          `gorm:"column:status;type:varchar(32);not null;index"`
	ContentSHA256         string          `gorm:"column:content_sha256;type:varchar(64);not null;default:''"`
	DocumentID            string          `gorm:"column:document_id;type:varchar(128);not null;default:''"`
	DocumentTaskID        string          `gorm:"column:document_task_id;type:varchar(128);not null;default:''"`
	AttemptCount          int             `gorm:"column:attempt_count;not null;default:0"`
	ErrorCode             string          `gorm:"column:error_code;type:varchar(64);not null;default:''"`
	ErrorDetailsJSON      json.RawMessage `gorm:"column:error_details_json;type:json;not null"`
	CreatedAt             time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt             time.Time       `gorm:"column:updated_at;not null"`
}

func (PaperImportItem) TableName() string { return "paper_import_items" }
