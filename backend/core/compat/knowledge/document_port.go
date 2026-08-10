package knowledge

import (
	"context"

	"lazymind/core/compat/contract"
)

type DocumentPort interface {
	GetDocument(ctx context.Context, callCtx contract.CallContext, input GetDocumentInput) (GetDocumentResult, error)
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
