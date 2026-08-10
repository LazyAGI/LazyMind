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

type DocumentService interface {
	GetDocument(ctx context.Context, req doc.DocumentReadRequest) (doc.DocumentReadResult, error)
	GetDocumentMetadata(ctx context.Context, req doc.DocumentGetRequest) (doc.DocumentMetadata, error)
	ReadDocumentContent(ctx context.Context, req doc.DocumentContentRequest) (doc.DocumentContent, error)
	ListDocumentChunks(ctx context.Context, req doc.DocumentChunksRequest) (doc.DocumentChunksResult, error)
}

type KnowledgeDocumentAdapter struct {
	service DocumentService
}

func (a *KnowledgeDocumentAdapter) GetDocument(ctx context.Context, callCtx contract.CallContext, input compatknowledge.GetDocumentInput) (compatknowledge.GetDocumentResult, error) {
	userID, datasetID, documentID, err := validateDocumentRequest(callCtx, input.KnowledgeID, input.DocumentID)
	if err != nil {
		return compatknowledge.GetDocumentResult{}, err
	}
	page := input.ChunksPage
	if input.IncludeChunks {
		page = page.Normalize()
	}
	resp, err := a.service.GetDocument(ctx, doc.DocumentReadRequest{
		UserID:         userID,
		DatasetID:      datasetID,
		DocumentID:     documentID,
		IncludeContent: input.IncludeContent,
		IncludeChunks:  input.IncludeChunks,
		PageToken:      page.PageToken,
		PageSize:       page.PageSize,
		Caller:         doc.DatasetCatalogCaller{UserID: userID},
	})
	if err != nil {
		return compatknowledge.GetDocumentResult{}, mapDocumentServiceError("knowledge.document.get", err)
	}
	detail := mapDocumentMetadata(resp.Metadata)
	if resp.Content != nil {
		content := compatknowledge.DocumentContent{MIMEType: resp.Content.MIMEType, Text: resp.Content.Text, Truncated: resp.Content.Truncated}
		detail.Content = &content
	}
	if resp.Chunks != nil {
		detail.Chunks = mapDocumentChunks(resp.Chunks.Chunks)
		total := int64(resp.Chunks.TotalSize)
		detail.ChunksPage = &contract.PageResult{NextPageToken: resp.Chunks.NextPageToken, Total: &total}
	}
	return compatknowledge.GetDocumentResult{Document: detail}, nil
}

func NewKnowledgeDocumentAdapter(service DocumentService) (*KnowledgeDocumentAdapter, error) {
	if service == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.document.adapter.new", "document service is required", false, nil)
	}
	return &KnowledgeDocumentAdapter{service: service}, nil
}

func NewKnowledgeDocumentAdapterForDB(db *gorm.DB) (*KnowledgeDocumentAdapter, error) {
	if db == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.document.adapter.new", "gorm db is required", false, nil)
	}
	return NewKnowledgeDocumentAdapterForDBs(db, db)
}

func NewKnowledgeDocumentAdapterForDBs(coreDB, lazyDB *gorm.DB) (*KnowledgeDocumentAdapter, error) {
	if coreDB == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.document.adapter.new", "core gorm db is required", false, nil)
	}
	if lazyDB == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.document.adapter.new", "lazy gorm db is required", false, nil)
	}
	service, err := doc.NewDocumentService(doc.DocumentServiceDeps{DB: coreDB, LazyDB: lazyDB})
	if err != nil {
		return nil, mapDocumentServiceError("knowledge.document.adapter.new", err)
	}
	return NewKnowledgeDocumentAdapter(service)
}

func (a *KnowledgeDocumentAdapter) GetDocumentMetadata(ctx context.Context, callCtx contract.CallContext, input compatknowledge.GetDocumentMetadataInput) (compatknowledge.DocumentDetail, error) {
	userID, datasetID, documentID, err := validateDocumentRequest(callCtx, input.KnowledgeID, input.DocumentID)
	if err != nil {
		return compatknowledge.DocumentDetail{}, err
	}
	resp, err := a.service.GetDocumentMetadata(ctx, doc.DocumentGetRequest{
		UserID:     userID,
		DatasetID:  datasetID,
		DocumentID: documentID,
		Caller:     doc.DatasetCatalogCaller{UserID: userID},
	})
	if err != nil {
		return compatknowledge.DocumentDetail{}, mapDocumentServiceError("knowledge.document.get", err)
	}
	return mapDocumentMetadata(resp), nil
}

func (a *KnowledgeDocumentAdapter) ReadDocumentContent(ctx context.Context, callCtx contract.CallContext, input compatknowledge.ReadDocumentContentInput) (compatknowledge.DocumentContent, error) {
	userID, datasetID, documentID, err := validateDocumentRequest(callCtx, input.KnowledgeID, input.DocumentID)
	if err != nil {
		return compatknowledge.DocumentContent{}, err
	}
	resp, err := a.service.ReadDocumentContent(ctx, doc.DocumentContentRequest{
		UserID:     userID,
		DatasetID:  datasetID,
		DocumentID: documentID,
		Caller:     doc.DatasetCatalogCaller{UserID: userID},
	})
	if err != nil {
		return compatknowledge.DocumentContent{}, mapDocumentServiceError("knowledge.document.get", err)
	}
	return compatknowledge.DocumentContent{MIMEType: resp.MIMEType, Text: resp.Text, Truncated: resp.Truncated}, nil
}

func (a *KnowledgeDocumentAdapter) ListDocumentChunks(ctx context.Context, callCtx contract.CallContext, input compatknowledge.ListDocumentChunksInput) (compatknowledge.ListDocumentChunksResult, error) {
	userID, datasetID, documentID, err := validateDocumentRequest(callCtx, input.KnowledgeID, input.DocumentID)
	if err != nil {
		return compatknowledge.ListDocumentChunksResult{}, err
	}
	page := input.Page.Normalize()
	resp, err := a.service.ListDocumentChunks(ctx, doc.DocumentChunksRequest{
		UserID:     userID,
		DatasetID:  datasetID,
		DocumentID: documentID,
		PageToken:  page.PageToken,
		PageSize:   page.PageSize,
		Caller:     doc.DatasetCatalogCaller{UserID: userID},
	})
	if err != nil {
		return compatknowledge.ListDocumentChunksResult{}, mapDocumentServiceError("knowledge.document.get", err)
	}
	total := int64(resp.TotalSize)
	return compatknowledge.ListDocumentChunksResult{
		Chunks: mapDocumentChunks(resp.Chunks),
		Page:   contract.PageResult{NextPageToken: resp.NextPageToken, Total: &total},
	}, nil
}

func validateDocumentRequest(callCtx contract.CallContext, knowledgeID, documentID string) (string, string, string, error) {
	userID := strings.TrimSpace(callCtx.UserID)
	if userID == "" {
		return "", "", "", contract.InvalidArgumentError("knowledge.document.get", "user_id is required")
	}
	datasetID := strings.TrimSpace(knowledgeID)
	if datasetID == "" {
		return "", "", "", contract.InvalidArgumentError("knowledge.document.get", "knowledge_id is required")
	}
	documentID = strings.TrimSpace(documentID)
	if documentID == "" {
		return "", "", "", contract.InvalidArgumentError("knowledge.document.get", "document_id is required")
	}
	return userID, datasetID, documentID, nil
}

func mapDocumentMetadata(item doc.DocumentMetadata) compatknowledge.DocumentDetail {
	return compatknowledge.DocumentDetail{
		ID:           item.ID,
		KnowledgeID:  item.DatasetID,
		Name:         item.Name,
		Source:       item.Source,
		Tags:         append([]string(nil), item.Tags...),
		ParseStatus:  item.ParseStatus,
		MIMEType:     item.MIMEType,
		SizeBytes:    item.SizeBytes,
		CreatedAt:    item.CreatedAt,
		UpdatedAt:    item.UpdatedAt,
		CreatedBy:    item.CreatedBy,
		OriginalFile: mapDocumentFileRef(item.OriginalFile),
	}
}

func mapDocumentFileRef(item *doc.DocumentFileRef) *compatknowledge.FileRef {
	if item == nil {
		return nil
	}
	return &compatknowledge.FileRef{FileName: item.FileName, DownloadURL: item.DownloadURL}
}

func mapDocumentChunks(items []doc.DocumentChunk) []compatknowledge.DocumentChunk {
	out := make([]compatknowledge.DocumentChunk, 0, len(items))
	for _, item := range items {
		out = append(out, compatknowledge.DocumentChunk{ID: item.ID, Text: item.Text, Number: item.Number})
	}
	return out
}

func mapDocumentServiceError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var compatErr *contract.Error
	if errors.As(err, &compatErr) {
		return err
	}
	var svcErr *doc.DocumentServiceError
	if errors.As(err, &svcErr) {
		switch svcErr.Code {
		case doc.DocumentServiceInvalidArgument:
			return contract.NewError(contract.InvalidArgument, operation, svcErr.Message, false, err)
		case doc.DocumentServiceNotFound, doc.DocumentServiceForbidden:
			return contract.NewError(contract.NotFound, operation, "document not found", false, err)
		case doc.DocumentServiceUnavailable:
			return contract.NewError(contract.BackendUnavailable, operation, "backend unavailable", true, err)
		case doc.DocumentServiceUnsupported:
			return contract.NewError(contract.Unsupported, operation, svcErr.Message, false, err)
		default:
			return contract.NewError(contract.Internal, operation, "internal error", false, err)
		}
	}
	return contract.NewError(contract.Internal, operation, "internal error", false, err)
}
