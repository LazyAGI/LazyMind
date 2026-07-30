package knowledge

import (
	"context"

	"lazymind/core/compat/contract"
)

type SearchPort interface {
	Search(ctx context.Context, callCtx contract.CallContext, input SearchInput) (SearchResult, error)
}
