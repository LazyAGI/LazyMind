package skill

import (
	"context"
	"strings"

	"lazymind/core/compat/contract"
)

type Facade struct {
	port Port
}

func NewFacade(port Port) (*Facade, error) {
	if port == nil {
		return nil, contract.NewError(contract.Internal, "skill.facade.new", "skill port is required", false, nil)
	}
	return &Facade{port: port}, nil
}

func (f *Facade) List(ctx context.Context, callCtx contract.CallContext, input ListInput) (ListResult, error) {
	callCtx.UserID = strings.TrimSpace(callCtx.UserID)
	if callCtx.UserID == "" {
		return ListResult{}, contract.InvalidArgumentError("skill.list", "user_id is required")
	}
	input.Keyword = strings.TrimSpace(input.Keyword)
	input.Category = strings.TrimSpace(input.Category)
	input.Page = input.Page.Normalize()
	return f.port.List(ctx, callCtx, input)
}

func (f *Facade) Get(ctx context.Context, callCtx contract.CallContext, input GetInput) (GetResult, error) {
	callCtx.UserID = strings.TrimSpace(callCtx.UserID)
	if callCtx.UserID == "" {
		return GetResult{}, contract.InvalidArgumentError("skill.get", "user_id is required")
	}
	skillID := strings.TrimSpace(input.SkillID)
	if skillID == "" {
		return GetResult{}, contract.InvalidArgumentError("skill.get", "skill_id is required")
	}
	metadata, err := f.port.GetMetadata(ctx, callCtx, skillID)
	if err != nil {
		return GetResult{}, err
	}
	result := GetResult{Skill: metadata}
	if !input.IncludeContent {
		return result, nil
	}
	content, err := f.port.ReadContent(ctx, callCtx, skillID, metadata.HeadRevisionID)
	if err != nil {
		return GetResult{}, err
	}
	if content.RevisionID != metadata.HeadRevisionID {
		return GetResult{}, contract.NewError(contract.Internal, "skill.get", "skill content revision mismatch", false, nil)
	}
	result.Content = &content
	return result, nil
}
