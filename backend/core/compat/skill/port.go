package skill

import (
	"context"

	"lazymind/core/compat/contract"
)

type Port interface {
	List(ctx context.Context, callCtx contract.CallContext, input ListInput) (ListResult, error)
	GetMetadata(ctx context.Context, callCtx contract.CallContext, skillID string) (Summary, error)
	ReadContent(ctx context.Context, callCtx contract.CallContext, skillID, revisionID string) (Content, error)
}
