package coreadapter

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/capability"
	"lazymind/core/common/orm"
)

type DocumentIDMapper interface {
	MapCoreDocumentIDs(context.Context, []string, []string) (map[documentMapKey]string, error)
}

type documentMapKey struct {
	DatasetID string
	LazyDocID string
}

type DBBackedDocumentIDMapper struct{ db *gorm.DB }

func NewDBBackedDocumentIDMapper(db *gorm.DB) (*DBBackedDocumentIDMapper, error) {
	if db == nil {
		return nil, capability.NewError(capability.Internal, "knowledge.search.documents.new", "gorm db is required", false, nil)
	}
	return &DBBackedDocumentIDMapper{db: db}, nil
}

func (m *DBBackedDocumentIDMapper) MapCoreDocumentIDs(ctx context.Context, datasetIDs, lazyDocIDs []string) (map[documentMapKey]string, error) {
	if len(datasetIDs) == 0 || len(lazyDocIDs) == 0 {
		return map[documentMapKey]string{}, nil
	}
	var rows []orm.Document
	if err := m.db.WithContext(ctx).Select("id, dataset_id, lazyllm_doc_id").
		Where("dataset_id IN ? AND (id IN ? OR lazyllm_doc_id IN ?) AND deleted_at IS NULL", datasetIDs, lazyDocIDs, lazyDocIDs).Find(&rows).Error; err != nil {
		return nil, capability.NewError(capability.Unavailable, "knowledge.search.documents", "query documents failed", true, err)
	}
	out := make(map[documentMapKey]string, len(rows))
	for _, row := range rows {
		datasetID, documentID := strings.TrimSpace(row.DatasetID), strings.TrimSpace(row.ID)
		out[documentMapKey{DatasetID: datasetID, LazyDocID: documentID}] = documentID
		if lazyID := strings.TrimSpace(row.LazyllmDocID); lazyID != "" {
			out[documentMapKey{DatasetID: datasetID, LazyDocID: lazyID}] = documentID
		}
	}
	return out, nil
}
