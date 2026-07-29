package knowledge

import (
	"context"

	"lazymind/core/compat/contract"
)

type DocumentPort interface {
	GetDocumentMetadata(ctx context.Context, callCtx contract.CallContext, input GetDocumentMetadataInput) (DocumentDetail, error)
	ReadDocumentContent(ctx context.Context, callCtx contract.CallContext, input ReadDocumentContentInput) (DocumentContent, error)
	ListDocumentChunks(ctx context.Context, callCtx contract.CallContext, input ListDocumentChunksInput) (ListDocumentChunksResult, error)
}

type GetDocumentMetadataInput struct {
	KnowledgeID string
	DocumentID  string
}

type ReadDocumentContentInput struct {
	KnowledgeID string
	DocumentID  string
}

type ListDocumentChunksInput struct {
	KnowledgeID string
	DocumentID  string
	Page        contract.PageRequest
}

type ListDocumentChunksResult struct {
	Chunks []DocumentChunk
	Page   contract.PageResult
}
