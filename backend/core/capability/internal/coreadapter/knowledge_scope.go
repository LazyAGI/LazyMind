package coreadapter

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/capability"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

type KnowledgeScopeResolver interface {
	ResolveKnowledge(context.Context, string, string, []string) (KnowledgeScope, error)
}

type KnowledgeScope struct {
	DatasetIDToKBID map[string]string
	KBIDToDatasetID map[string]string
}

type DBBackedKnowledgeScopeResolver struct {
	db       *gorm.DB
	datasets DatasetCatalogService
}

func NewDBBackedKnowledgeScopeResolver(db *gorm.DB, datasets DatasetCatalogService) (*DBBackedKnowledgeScopeResolver, error) {
	if db == nil {
		return nil, capability.NewError(capability.Internal, "knowledge.search.scope.new", "gorm db is required", false, nil)
	}
	if datasets == nil {
		return nil, capability.NewError(capability.Internal, "knowledge.search.scope.new", "dataset service is required", false, nil)
	}
	return &DBBackedKnowledgeScopeResolver{db: db, datasets: datasets}, nil
}

func (r *DBBackedKnowledgeScopeResolver) ResolveKnowledge(ctx context.Context, userID, tenantID string, datasetIDs []string) (KnowledgeScope, error) {
	for _, datasetID := range datasetIDs {
		if _, err := r.datasets.GetDataset(ctx, doc.DatasetGetRequest{
			UserID: userID, DatasetID: datasetID,
			Caller: doc.DatasetCatalogCaller{UserID: userID, TenantID: tenantID},
		}); err != nil {
			return KnowledgeScope{}, mapDatasetError("knowledge.search.scope", err)
		}
	}
	var rows []orm.Dataset
	if err := r.db.WithContext(ctx).Where("id IN ? AND deleted_at IS NULL", datasetIDs).Find(&rows).Error; err != nil {
		return KnowledgeScope{}, capability.NewError(capability.Unavailable, "knowledge.search.scope", "query datasets failed", true, err)
	}
	rowByID := make(map[string]orm.Dataset, len(rows))
	for _, row := range rows {
		rowByID[row.ID] = row
	}
	scope := KnowledgeScope{
		DatasetIDToKBID: make(map[string]string, len(datasetIDs)),
		KBIDToDatasetID: make(map[string]string, len(datasetIDs)),
	}
	for _, datasetID := range datasetIDs {
		row, ok := rowByID[datasetID]
		if !ok {
			return KnowledgeScope{}, capability.NewError(capability.NotFound, "knowledge.search.scope", "knowledge not found", false, gorm.ErrRecordNotFound)
		}
		kbID := strings.TrimSpace(row.KbID)
		if kbID == "" {
			return KnowledgeScope{}, capability.NewError(capability.Internal, "knowledge.search.scope", "knowledge backend id is empty", false, nil)
		}
		scope.DatasetIDToKBID[datasetID] = kbID
		scope.KBIDToDatasetID[kbID] = datasetID
	}
	return scope, nil
}
