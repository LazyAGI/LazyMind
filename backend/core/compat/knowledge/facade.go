package knowledge

import (
	"context"
	"strings"

	"lazymind/core/compat/contract"
)

type Facade struct {
	catalog  CatalogPort
	document DocumentPort
}

type FacadeDeps struct {
	Catalog  CatalogPort
	Document DocumentPort
}

func NewFacade(port CatalogPort) (*Facade, error) {
	if port == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.facade.new", "catalog port is required", false, nil)
	}
	return NewFacadeWithDeps(FacadeDeps{Catalog: port})
}

func NewFacadeWithDeps(deps FacadeDeps) (*Facade, error) {
	if deps.Catalog == nil && deps.Document == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.facade.new", "catalog or document port is required", false, nil)
	}
	return &Facade{catalog: deps.Catalog, document: deps.Document}, nil
}

func (f *Facade) List(ctx context.Context, callCtx contract.CallContext, input ListInput) (ListResult, error) {
	callCtx.UserID = strings.TrimSpace(callCtx.UserID)
	if callCtx.UserID == "" {
		return ListResult{}, contract.InvalidArgumentError("knowledge.list", "user_id is required")
	}
	input.Keyword = strings.TrimSpace(input.Keyword)
	input.Page = input.Page.Normalize()
	if f.catalog == nil {
		return ListResult{}, contract.NewError(contract.Unsupported, "knowledge.list", "knowledge catalog is not configured", false, nil)
	}
	return f.catalog.List(ctx, callCtx, input)
}

func (f *Facade) Get(ctx context.Context, callCtx contract.CallContext, input GetInput) (GetResult, error) {
	callCtx.UserID = strings.TrimSpace(callCtx.UserID)
	if callCtx.UserID == "" {
		return GetResult{}, contract.InvalidArgumentError("knowledge.get", "user_id is required")
	}
	input.KnowledgeID = strings.TrimSpace(input.KnowledgeID)
	if input.KnowledgeID == "" {
		return GetResult{}, contract.InvalidArgumentError("knowledge.get", "knowledge_id is required")
	}
	if f.catalog == nil {
		return GetResult{}, contract.NewError(contract.Unsupported, "knowledge.get", "knowledge catalog is not configured", false, nil)
	}
	return f.catalog.Get(ctx, callCtx, input)
}

func (f *Facade) GetDocument(ctx context.Context, callCtx contract.CallContext, input GetDocumentInput) (GetDocumentResult, error) {
	callCtx.UserID = strings.TrimSpace(callCtx.UserID)
	if callCtx.UserID == "" {
		return GetDocumentResult{}, contract.InvalidArgumentError("knowledge.document.get", "user_id is required")
	}
	input.KnowledgeID = strings.TrimSpace(input.KnowledgeID)
	if input.KnowledgeID == "" {
		return GetDocumentResult{}, contract.InvalidArgumentError("knowledge.document.get", "knowledge_id is required")
	}
	input.DocumentID = strings.TrimSpace(input.DocumentID)
	if input.DocumentID == "" {
		return GetDocumentResult{}, contract.InvalidArgumentError("knowledge.document.get", "document_id is required")
	}
	if f.document == nil {
		return GetDocumentResult{}, contract.NewError(contract.Unsupported, "knowledge.document.get", "knowledge document is not configured", false, nil)
	}

	detail, err := f.document.GetDocumentMetadata(ctx, callCtx, GetDocumentMetadataInput{
		KnowledgeID: input.KnowledgeID,
		DocumentID:  input.DocumentID,
	})
	if err != nil {
		return GetDocumentResult{}, err
	}
	if input.IncludeContent {
		content, err := f.document.ReadDocumentContent(ctx, callCtx, ReadDocumentContentInput{
			KnowledgeID: input.KnowledgeID,
			DocumentID:  input.DocumentID,
		})
		if err != nil {
			return GetDocumentResult{}, err
		}
		detail.Content = &content
	}
	if input.IncludeChunks {
		page := input.ChunksPage.Normalize()
		chunks, err := f.document.ListDocumentChunks(ctx, callCtx, ListDocumentChunksInput{
			KnowledgeID: input.KnowledgeID,
			DocumentID:  input.DocumentID,
			Page:        page,
		})
		if err != nil {
			return GetDocumentResult{}, err
		}
		detail.Chunks = chunks.Chunks
		detail.ChunksPage = &chunks.Page
	}
	return GetDocumentResult{Document: detail}, nil
}
