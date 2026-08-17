package coreadapter

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/capability"
	"lazymind/core/doc"
)

type DatasetCatalogService interface {
	ListDatasets(context.Context, doc.DatasetListRequest) (doc.DatasetListResult, error)
	GetDataset(context.Context, doc.DatasetGetRequest) (doc.Dataset, error)
}

type KnowledgeCatalog struct {
	service DatasetCatalogService
}

func NewKnowledgeCatalog(service DatasetCatalogService) (*KnowledgeCatalog, error) {
	if service == nil {
		return nil, capability.NewError(capability.Internal, "knowledge.adapter.new", "dataset service is required", false, nil)
	}
	return &KnowledgeCatalog{service: service}, nil
}

func NewKnowledgeCatalogForDB(db *gorm.DB) (*KnowledgeCatalog, error) {
	if db == nil {
		return nil, capability.NewError(capability.Internal, "knowledge.adapter.new", "gorm db is required", false, nil)
	}
	service, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: db})
	if err != nil {
		return nil, mapDatasetError("knowledge.adapter.new", err)
	}
	return NewKnowledgeCatalog(service)
}

func (c *KnowledgeCatalog) ListKnowledge(ctx context.Context, call capability.InvocationContext, query capability.KnowledgeListQuery) (capability.KnowledgeListPage, error) {
	userID := strings.TrimSpace(call.Principal.UserID)
	resp, err := c.service.ListDatasets(ctx, doc.DatasetListRequest{
		UserID:  userID,
		Keyword: query.Keyword,
		Tags:    append([]string(nil), query.Tags...),
		Offset:  query.Offset,
		Limit:   query.Limit,
		Caller:  doc.DatasetCatalogCaller{UserID: userID, TenantID: strings.TrimSpace(call.Principal.TenantID)},
	})
	if err != nil {
		return capability.KnowledgeListPage{}, mapDatasetError("knowledge.list", err)
	}
	items := make([]capability.KnowledgeSummary, 0, len(resp.Datasets))
	for _, item := range resp.Datasets {
		items = append(items, capability.KnowledgeSummary{
			ID: item.DatasetID, Name: item.DisplayName, Description: item.Desc,
			Tags: append([]string(nil), item.Tags...), UpdatedAt: item.UpdateTime,
			DocumentSizeBytes: item.DocumentSize, DocumentCount: item.DocumentCount,
		})
	}
	return capability.KnowledgeListPage{
		Items: items, Total: resp.TotalSize, HasMore: resp.HasMore, NextOffset: resp.NextOffset,
	}, nil
}
