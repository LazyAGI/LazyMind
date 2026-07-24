package orm

import "encoding/json"

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
	PID              string          `gorm:"column:p_id;type:varchar(255);not null;default:'';index"`
	Tags             json.RawMessage `gorm:"column:tags;type:json"`
	FileID           string          `gorm:"column:file_id;type:varchar(128);not null;default:''"`
	PDFConvertResult string          `gorm:"column:pdf_convert_result;type:varchar(64);not null;default:''"`

	Ext json.RawMessage `gorm:"column:ext;type:json"`

	BaseModel
}

func (Document) TableName() string { return "documents" }
