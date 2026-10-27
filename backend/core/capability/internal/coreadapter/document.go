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
	caller := managementCaller(call)

	ids := query.KnowledgeIDs
	if len(ids) == 0 {
		ids = []string{query.KnowledgeID}
	}
	catalog := doc.NewDocumentCatalog(r.db, r.datasets)
	page, err := catalog.List(ctx, caller, doc.DocumentCatalogQuery{OrderBy: query.OrderBy, DatasetIDs: ids, ResourceIDs: query.ResourceIDs, ResourceType: query.ResourceType, ParentID: query.ParentID, Recursive: query.Recursive, Match: query.Match, Name: query.Name, Path: query.Path, Offset: query.Offset, Limit: query.Limit})
	if err != nil {
		return capability.KnowledgeDocumentListPage{}, mapResourceError(operation, err)
	}
	rows := page.Rows
	items := make([]capability.KnowledgeDocumentSummary, 0, len(rows))
	for _, row := range rows {
		metadata := doc.DocumentMetadata{ID: row.ID, DatasetID: row.DatasetID, Name: row.DisplayName, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
		var err error
		if !strings.EqualFold(row.DocumentType, "FOLDER") {
			metadata, err = r.documents.GetDocumentMetadata(ctx, doc.DocumentGetRequest{
				UserID: userID, DatasetID: row.DatasetID, DocumentID: row.ID, Caller: caller,
			})
		}
		if err != nil {
			return capability.KnowledgeDocumentListPage{}, mapDocumentError(operation, err)
		}
		item := mapDocumentSummary(metadata)
		item.ParentID = row.PID
		item.LogicalPath = page.LogicalPaths[row.ID]
		dataset := page.Datasets[row.DatasetID]
		item.AllowedActions = append([]string(nil), dataset.AllowedActions...)
		if strings.EqualFold(row.DocumentType, "FOLDER") {
			item.ResourceType = "folder"
			item.ProcessingStatus = "not_requested"
			item.ResourceRef = capability.ResourceRef{Kind: "folder", KnowledgeBaseID: row.DatasetID, FolderID: row.ID}
		}
		items = append(items, item)
	}
	return capability.KnowledgeDocumentListPage{Items: items, Total: page.Total}, nil
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
	var diagnostic *capability.ProcessingDiagnostic
	if d := item.ProcessingError; d != nil {
		diagnostic = &capability.ProcessingDiagnostic{Stage: d.Stage, Code: d.Code, Message: d.Message, TaskID: d.TaskID, Recovery: d.Recovery}
	}
	return capability.KnowledgeDocumentSummary{
		ID: item.ID, KnowledgeID: item.DatasetID, Name: item.Name,
		ResourceType: "document", ResourceRef: capability.ResourceRef{Kind: "document", KnowledgeBaseID: item.DatasetID, DocumentID: item.ID},
		RelativePath:     item.RelativePath,
		ProcessingError:  diagnostic,
		ProcessingStatus: documentProcessingStatus(item.ProcessingLevel, item.ProcessingState),
		Tags:             append([]string(nil), item.Tags...), ParseStatus: item.ParseStatus,
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

func documentProcessingStatus(level string, state orm.DocumentProcessingState) string {
	return doc.DocumentProcessingStatus(level, state)
}
