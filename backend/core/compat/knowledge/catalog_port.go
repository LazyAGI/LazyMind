package knowledge

import (
	"context"

	"lazymind/core/compat/contract"
)

type CatalogPort interface {
	List(ctx context.Context, callCtx contract.CallContext, input ListInput) (ListResult, error)
	Get(ctx context.Context, callCtx contract.CallContext, input GetInput) (GetResult, error)
}
