package coreadapter

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/capability"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

type DocumentService interface {
	GetDocument(context.Context, doc.DocumentReadRequest) (doc.DocumentReadResult, error)
	GetDocumentMetadata(context.Context, doc.DocumentGetRequest) (doc.DocumentMetadata, error)
}

type KnowledgeDocumentReader struct {
	db        *gorm.DB
	datasets  DatasetCatalogService
	documents DocumentService
}

func NewKnowledgeDocumentReader(db *gorm.DB, datasets DatasetCatalogService, documents DocumentService) (*KnowledgeDocumentReader, error) {
	if db == nil {
		return nil, capability.NewError(capability.Internal, "knowledge.document.adapter.new", "gorm db is required", false, nil)
	}
	if datasets == nil || documents == nil {
		return nil, capability.NewError(capability.Internal, "knowledge.document.adapter.new", "dataset and document services are required", false, nil)
	}
	return &KnowledgeDocumentReader{db: db, datasets: datasets, documents: documents}, nil
}

func NewKnowledgeDocumentReaderForDB(db *gorm.DB) (*KnowledgeDocumentReader, error) {
	return NewKnowledgeDocumentReaderForDBs(db, db)
}

func NewKnowledgeDocumentReaderForDBs(coreDB, lazyDB *gorm.DB) (*KnowledgeDocumentReader, error) {
	if coreDB == nil || lazyDB == nil {
		return nil, capability.NewError(capability.Internal, "knowledge.document.adapter.new", "core and readonly gorm databases are required", false, nil)
	}
	datasets, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: coreDB})
	if err != nil {
		return nil, mapDatasetError("knowledge.document.adapter.new", err)
	}
	documents, err := doc.NewDocumentService(doc.DocumentServiceDeps{DB: coreDB, LazyDB: lazyDB})
	if err != nil {
		return nil, mapDocumentError("knowledge.document.adapter.new", err)
	}
	return NewKnowledgeDocumentReader(coreDB, datasets, documents)
}

func (r *KnowledgeDocumentReader) ListKnowledgeDocuments(ctx context.Context, call capability.InvocationContext, query capability.KnowledgeDocumentListQuery) (capability.KnowledgeDocumentListPage, error) {
	const operation = "knowledge.document.list"
	userID := strings.TrimSpace(call.Principal.UserID)
	caller := doc.DatasetCatalogCaller{UserID: userID, TenantID: strings.TrimSpace(call.Principal.TenantID)}
	if _, err := r.datasets.GetDataset(ctx, doc.DatasetGetRequest{UserID: userID, DatasetID: query.KnowledgeID, Caller: caller}); err != nil {
		return capability.KnowledgeDocumentListPage{}, mapDatasetError(operation, err)
	}

	base := r.db.WithContext(ctx).Model(&orm.Document{}).
		Where("dataset_id = ? AND deleted_at IS NULL", query.KnowledgeID).
		Where("UPPER(COALESCE(document_type, '')) <> ?", "FOLDER")
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return capability.KnowledgeDocumentListPage{}, capability.NewError(capability.Unavailable, operation, "query documents failed", true, err)
	}
	var rows []orm.Document
	if err := base.Order("updated_at DESC, id ASC").Offset(query.Offset).Limit(query.Limit).Find(&rows).Error; err != nil {
		return capability.KnowledgeDocumentListPage{}, capability.NewError(capability.Unavailable, operation, "query documents failed", true, err)
	}
	items := make([]capability.KnowledgeDocumentSummary, 0, len(rows))
	for _, row := range rows {
		metadata, err := r.documents.GetDocumentMetadata(ctx, doc.DocumentGetRequest{
			UserID: userID, DatasetID: query.KnowledgeID, DocumentID: row.ID, Caller: caller,
		})
		if err != nil {
			return capability.KnowledgeDocumentListPage{}, mapDocumentError(operation, err)
		}
		items = append(items, mapDocumentSummary(metadata))
	}
	return capability.KnowledgeDocumentListPage{Items: items, Total: total}, nil
}

func (r *KnowledgeDocumentReader) GetKnowledgeDocument(ctx context.Context, call capability.InvocationContext, input capability.GetKnowledgeDocumentInput) (capability.GetKnowledgeDocumentResult, error) {
	const operation = "knowledge.document.get"
	userID := strings.TrimSpace(call.Principal.UserID)
	result, err := r.documents.GetDocument(ctx, doc.DocumentReadRequest{
		UserID: userID, DatasetID: input.KnowledgeID, DocumentID: input.DocumentID,
		IncludeContent: input.IncludeContent, IncludeChunks: input.IncludeChunks,
		PageToken: input.ChunksPage.PageToken, PageSize: input.ChunksPage.PageSize,
		Caller: doc.DatasetCatalogCaller{UserID: userID, TenantID: strings.TrimSpace(call.Principal.TenantID)},
	})
	if err != nil {
		return capability.GetKnowledgeDocumentResult{}, mapDocumentError(operation, err)
	}
	detail := capability.KnowledgeDocumentDetail{
		KnowledgeDocumentSummary: mapDocumentSummary(result.Metadata),
		Source:                   result.Metadata.Source,
		OriginalFile:             mapDocumentFileRef(result.Metadata.OriginalFile),
	}
	if result.Content != nil {
		detail.Content = &capability.KnowledgeDocumentContent{
			Text: result.Content.Text, MIMEType: result.Content.MIMEType, Truncated: result.Content.Truncated,
		}
	}
	if result.Chunks != nil {
		detail.Chunks = make([]capability.KnowledgeDocumentChunk, 0, len(result.Chunks.Chunks))
		for _, chunk := range result.Chunks.Chunks {
			detail.Chunks = append(detail.Chunks, capability.KnowledgeDocumentChunk{ID: chunk.ID, Text: chunk.Text, Number: chunk.Number})
		}
		detail.ChunksPage = &capability.PageInfo{NextPageToken: result.Chunks.NextPageToken, Total: int64(result.Chunks.TotalSize)}
	}
	return capability.GetKnowledgeDocumentResult{Document: detail}, nil
}

func mapDocumentSummary(item doc.DocumentMetadata) capability.KnowledgeDocumentSummary {
	return capability.KnowledgeDocumentSummary{
		ID: item.ID, KnowledgeID: item.DatasetID, Name: item.Name,
		Tags: append([]string(nil), item.Tags...), ParseStatus: item.ParseStatus,
		MIMEType: item.MIMEType, SizeBytes: item.SizeBytes,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, CreatedBy: item.CreatedBy,
	}
}

func mapDocumentFileRef(item *doc.DocumentFileRef) *capability.DocumentFileRef {
	if item == nil {
		return nil
	}
	return &capability.DocumentFileRef{FileName: item.FileName, DownloadURL: item.DownloadURL}
}

func mapDocumentError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var capabilityErr *capability.Error
	if errors.As(err, &capabilityErr) {
		return err
	}
	if mapped := mapContextError(operation, err); mapped != nil {
		return mapped
	}
	var serviceErr *doc.DocumentServiceError
	if errors.As(err, &serviceErr) {
		switch serviceErr.Code {
		case doc.DocumentServiceInvalidArgument:
			return capability.NewError(capability.InvalidArgument, operation, serviceErr.Message, false, err)
		case doc.DocumentServiceNotFound, doc.DocumentServiceForbidden:
			return capability.NewError(capability.NotFound, operation, "document not found", false, err)
		case doc.DocumentServiceUnavailable:
			return capability.NewError(capability.Unavailable, operation, "backend unavailable", true, err)
		case doc.DocumentServiceUnsupported:
			return capability.NewError(capability.Unsupported, operation, serviceErr.Message, false, err)
		default:
			return capability.NewError(capability.Internal, operation, "internal error", false, err)
		}
	}
	return capability.NewError(capability.Internal, operation, "internal error", false, err)
}
