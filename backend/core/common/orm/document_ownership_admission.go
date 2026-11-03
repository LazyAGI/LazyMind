package orm

import "time"

// DocumentOwnershipBackfill freezes the historical index boundary once. Live
// documents must obtain their Core association through task publication.
type DocumentOwnershipBackfill struct {
	DatasetID        string     `gorm:"column:dataset_id;primaryKey;type:varchar(255)"`
	KbID             string     `gorm:"column:kb_id;type:varchar(255);not null"`
	TenantID         string     `gorm:"column:tenant_id;type:varchar(128);not null"`
	MaxAssociationID int64      `gorm:"column:max_association_id;not null"`
	CompletedAt      *time.Time `gorm:"column:completed_at"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null"`
}

func (DocumentOwnershipBackfill) TableName() string { return "document_ownership_backfills" }

// RejectedDocumentPublication survives the failed publication transaction so an
// external worker's late side effect can never be admitted as historical data.
type RejectedDocumentPublication struct {
	DatasetID     string `gorm:"column:dataset_id;primaryKey;type:varchar(255)"`
	ExternalDocID string `gorm:"column:external_doc_id;primaryKey;type:varchar(128)"`
}

func (RejectedDocumentPublication) TableName() string { return "rejected_document_publications" }
