package knowledge

import (
	"context"
	"strings"

	"lazymind/core/compat/contract"
)

type Facade struct {
	port CatalogPort
}

func NewFacade(port CatalogPort) (*Facade, error) {
	if port == nil {
		return nil, contract.NewError(contract.Internal, "knowledge.facade.new", "catalog port is required", false, nil)
	}
	return &Facade{port: port}, nil
}

func (f *Facade) List(ctx context.Context, callCtx contract.CallContext, input ListInput) (ListResult, error) {
	callCtx.UserID = strings.TrimSpace(callCtx.UserID)
	if callCtx.UserID == "" {
		return ListResult{}, contract.InvalidArgumentError("knowledge.list", "user_id is required")
	}
	input.Keyword = strings.TrimSpace(input.Keyword)
	input.Page = input.Page.Normalize()
	return f.port.List(ctx, callCtx, input)
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
	return f.port.Get(ctx, callCtx, input)
}
