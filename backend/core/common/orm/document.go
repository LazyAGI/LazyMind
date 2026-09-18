package orm

import (
	"encoding/json"
	"time"
)

// ----- Readonly-diff tables (Core-maintained schema A) -----

// Document is the Core-maintained diff table for documents.
// It stores only fields that Core needs to own; the base fields are read from schema-B (lazy_llm_server).
//
// ID is the Core resource id (API document_id / path {document}).
// LazyllmDocID matches readonlyorm.LazyLLMDocRow.DocID (external lazyllm_documents.doc_id).
// DatasetID matches core datasets.id / readonlyorm kb_id style (varchar(255)).
type Document struct {
	ID           string `gorm:"column:id;type:varchar(128);primaryKey"`
	LazyllmDocID string `gorm:"column:lazyllm_doc_id;type:varchar(128);not null;default:'';index"`

	DatasetID        string          `gorm:"column:dataset_id;type:varchar(255);not null;index"`
	DisplayName      string          `gorm:"column:display_name;type:varchar(512);not null;default:''"`
	DocumentType     string          `gorm:"column:document_type;type:varchar(64)"`
	PID              string          `gorm:"column:p_id;type:varchar(255);not null;default:'';index"`
	Tags             json.RawMessage `gorm:"column:tags;type:json"`
	FileID           string          `gorm:"column:file_id;type:varchar(128);not null;default:''"`
	PDFConvertResult string          `gorm:"column:pdf_convert_result;type:varchar(64);not null;default:''"`

	Ext json.RawMessage `gorm:"column:ext;type:json"`

	BaseModel
}

func (Document) TableName() string { return "documents" }

// DocumentProcessingState records independently retryable processing stages.
// EffectiveLevel is derived by the service and is intentionally not persisted.
type DocumentProcessingState struct {
	DatasetID  string `gorm:"column:dataset_id;type:varchar(255);primaryKey"`
	DocumentID string `gorm:"column:document_id;type:varchar(128);primaryKey"`

	ParseStatus       string `gorm:"column:parse_status;type:varchar(16);not null;default:'pending'"`
	ChunkStatus       string `gorm:"column:chunk_status;type:varchar(16);not null;default:'pending'"`
	IndexStatus       string `gorm:"column:index_status;type:varchar(16);not null;default:'pending'"`
	ParseErrorCode    string `gorm:"column:parse_error_code;type:varchar(64);not null;default:''"`
	ParseErrorMessage string `gorm:"column:parse_error_message;type:text;not null;default:''"`
	ChunkErrorCode    string `gorm:"column:chunk_error_code;type:varchar(64);not null;default:''"`
	ChunkErrorMessage string `gorm:"column:chunk_error_message;type:text;not null;default:''"`
	IndexErrorCode    string `gorm:"column:index_error_code;type:varchar(64);not null;default:''"`
	IndexErrorMessage string `gorm:"column:index_error_message;type:text;not null;default:''"`

	SourceFingerprint string    `gorm:"column:source_fingerprint;type:varchar(128);not null;default:''"`
	ParseFingerprint  string    `gorm:"column:parse_fingerprint;type:varchar(128);not null;default:''"`
	ChunkFingerprint  string    `gorm:"column:chunk_fingerprint;type:varchar(128);not null;default:''"`
	IndexFingerprint  string    `gorm:"column:index_fingerprint;type:varchar(128);not null;default:''"`
	ParserVersion     string    `gorm:"column:parser_version;type:varchar(128);not null;default:''"`
	ChunkerVersion    string    `gorm:"column:chunker_version;type:varchar(128);not null;default:''"`
	EmbeddingVersion  string    `gorm:"column:embedding_version;type:varchar(128);not null;default:''"`
	ParseArtifactRef  string    `gorm:"column:parse_artifact_ref;type:text;not null;default:''"`
	ChunkArtifactRef  string    `gorm:"column:chunk_artifact_ref;type:text;not null;default:''"`
	IndexArtifactRef  string    `gorm:"column:index_artifact_ref;type:text;not null;default:''"`
	Revision          int64     `gorm:"column:revision;not null;default:1"`
	UpdatedAt         time.Time `gorm:"column:updated_at;not null"`
}

func (DocumentProcessingState) TableName() string { return "document_processing_states" }
