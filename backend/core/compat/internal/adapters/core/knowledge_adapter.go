package core

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/compat/contract"
	compatknowledge "lazymind/core/compat/knowledge"
	"lazymind/core/doc"
)

type DatasetCatalogService interface {
	ListDatasets(ctx context.Context, req doc.DatasetListRequest) (doc.DatasetListResult, error)
	GetDataset(ctx context.Context, req doc.DatasetGetRequest) (doc.Dataset, error)
}

type KnowledgeCatalogAdapter struct {
	service DatasetCatalogService
}

func NewKnowledgeCatalogAdapter(service DatasetCatalogService) (*KnowledgeCatalogAdapter, error) {
	if service == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.adapter.new", "dataset service is required", false, nil)
	}
	return &KnowledgeCatalogAdapter{service: service}, nil
}

func NewKnowledgeCatalogAdapterForDB(db *gorm.DB) (*KnowledgeCatalogAdapter, error) {
	if db == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.adapter.new", "gorm db is required", false, nil)
	}
	service, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: db})
	if err != nil {
		return nil, mapDatasetServiceError("knowledge.adapter.new", err)
	}
	return NewKnowledgeCatalogAdapter(service)
}

func (a *KnowledgeCatalogAdapter) List(ctx context.Context, callCtx contract.CallContext, input compatknowledge.ListInput) (compatknowledge.ListResult, error) {
	userID := strings.TrimSpace(callCtx.UserID)
	if userID == "" {
		return compatknowledge.ListResult{}, contract.InvalidArgumentError("knowledge.list", "user_id is required")
	}
	page := input.Page.Normalize()
	offset, err := contract.DecodeOffsetPageToken(page.PageToken)
	if err != nil {
		return compatknowledge.ListResult{}, contract.NewError(contract.InvalidArgument, "knowledge.list", "invalid page token", false, err)
	}
	resp, err := a.service.ListDatasets(ctx, doc.DatasetListRequest{
		UserID:  userID,
		Keyword: strings.TrimSpace(input.Keyword),
		Tags:    append([]string(nil), input.Tags...),
		Offset:  offset,
		Limit:   page.PageSize,
		Caller:  doc.DatasetCatalogCaller{UserID: userID, TenantID: strings.TrimSpace(callCtx.TenantID)},
	})
	if err != nil {
		return compatknowledge.ListResult{}, mapDatasetServiceError("knowledge.list", err)
	}
	total := resp.TotalSize
	result := compatknowledge.ListResult{
		Items: mapKnowledgeSummaries(resp.Datasets),
		Page:  contract.PageResult{Total: &total},
	}
	if resp.HasMore {
		result.Page.NextPageToken = contract.EncodeOffsetPageToken(resp.NextOffset)
	}
	return result, nil
}

func (a *KnowledgeCatalogAdapter) Get(ctx context.Context, callCtx contract.CallContext, input compatknowledge.GetInput) (compatknowledge.GetResult, error) {
	userID := strings.TrimSpace(callCtx.UserID)
	if userID == "" {
		return compatknowledge.GetResult{}, contract.InvalidArgumentError("knowledge.get", "user_id is required")
	}
	datasetID := strings.TrimSpace(input.KnowledgeID)
	if datasetID == "" {
		return compatknowledge.GetResult{}, contract.InvalidArgumentError("knowledge.get", "knowledge_id is required")
	}
	resp, err := a.service.GetDataset(ctx, doc.DatasetGetRequest{
		UserID:    userID,
		DatasetID: datasetID,
		Caller:    doc.DatasetCatalogCaller{UserID: userID, TenantID: strings.TrimSpace(callCtx.TenantID)},
	})
	if err != nil {
		return compatknowledge.GetResult{}, mapDatasetServiceError("knowledge.get", err)
	}
	return compatknowledge.GetResult{Knowledge: mapKnowledgeSummary(resp)}, nil
}

func mapKnowledgeSummaries(items []doc.Dataset) []compatknowledge.Summary {
	out := make([]compatknowledge.Summary, 0, len(items))
	for _, item := range items {
		out = append(out, mapKnowledgeSummary(item))
	}
	return out
}

func mapKnowledgeSummary(item doc.Dataset) compatknowledge.Summary {
	return compatknowledge.Summary{
		ID:                item.DatasetID,
		Name:              item.DisplayName,
		Description:       item.Desc,
		Tags:              append([]string(nil), item.Tags...),
		UpdatedAt:         item.UpdateTime,
		DocumentSizeBytes: item.DocumentSize,
		DocumentCount:     item.DocumentCount,
	}
}

func mapDatasetServiceError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var compatErr *contract.Error
	if errors.As(err, &compatErr) {
		return err
	}
	var svcErr *doc.DatasetServiceError
	if errors.As(err, &svcErr) {
		switch svcErr.Code {
		case doc.DatasetServiceInvalidArgument:
			return contract.NewError(contract.InvalidArgument, operation, svcErr.Message, false, err)
		case doc.DatasetServiceNotFound, doc.DatasetServiceForbidden:
			return contract.NewError(contract.NotFound, operation, "knowledge not found", false, err)
		case doc.DatasetServiceUnavailable:
			return contract.NewError(contract.BackendUnavailable, operation, "backend unavailable", true, err)
		default:
			return contract.NewError(contract.Internal, operation, "internal error", false, err)
		}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return contract.NewError(contract.NotFound, operation, "knowledge not found", false, err)
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(msg, "not found"):
		return contract.NewError(contract.NotFound, operation, "knowledge not found", false, err)
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "timeout"):
		return contract.NewError(contract.BackendUnavailable, operation, "backend unavailable", true, err)
	default:
		return contract.NewError(contract.Internal, operation, "internal error", false, err)
	}
}
