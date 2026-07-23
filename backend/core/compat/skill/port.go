package skill

import (
	"context"

	"lazymind/core/compat/contract"
)

type Port interface {
	List(ctx context.Context, callCtx contract.CallContext, input ListInput) (ListResult, error)
	Get(ctx context.Context, callCtx contract.CallContext, skillID string) (GetResult, error)
	ReadContent(ctx context.Context, callCtx contract.CallContext, skillID string) (Content, error)
}
