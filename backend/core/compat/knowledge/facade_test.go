package knowledge

import (
	"context"
	"errors"
	"testing"
	"time"

	"lazymind/core/compat/contract"
)

type fakeCatalogPort struct {
	listCallCtx contract.CallContext
	listInput   ListInput
	getCallCtx  contract.CallContext
	getInput    GetInput
	listResult  ListResult
	getResult   GetResult
	listErr     error
	getErr      error
}

func (p *fakeCatalogPort) List(ctx context.Context, callCtx contract.CallContext, input ListInput) (ListResult, error) {
	p.listCallCtx = callCtx
	p.listInput = input
	if p.listErr != nil {
		return ListResult{}, p.listErr
	}
	return p.listResult, nil
}

func (p *fakeCatalogPort) Get(ctx context.Context, callCtx contract.CallContext, input GetInput) (GetResult, error) {
	p.getCallCtx = callCtx
	p.getInput = input
	if p.getErr != nil {
		return GetResult{}, p.getErr
	}
	return p.getResult, nil
}

func TestFacadeListRequiresUserID(t *testing.T) {
	facade := mustKnowledgeFacade(t, &fakeCatalogPort{})
	_, err := facade.List(context.Background(), contract.CallContext{UserID: "  "}, ListInput{})
	if code, ok := contract.CodeOf(err); !ok || code != contract.InvalidArgument {
		t.Fatalf("error code = %v, %v; want INVALID_ARGUMENT", code, ok)
	}
}

func TestFacadeGetRequiresKnowledgeID(t *testing.T) {
	facade := mustKnowledgeFacade(t, &fakeCatalogPort{})
	_, err := facade.Get(context.Background(), contract.CallContext{UserID: "user"}, GetInput{KnowledgeID: "  "})
	if code, ok := contract.CodeOf(err); !ok || code != contract.InvalidArgument {
		t.Fatalf("error code = %v, %v; want INVALID_ARGUMENT", code, ok)
	}
}

func TestFacadeListNormalizesPaging(t *testing.T) {
	port := &fakeCatalogPort{}
	facade := mustKnowledgeFacade(t, port)
	_, err := facade.List(context.Background(), contract.CallContext{UserID: " user "}, ListInput{
		Keyword: " docs ",
		Page:    contract.PageRequest{PageSize: 500, PageToken: " opaque "},
	})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if port.listCallCtx.UserID != "user" {
		t.Fatalf("UserID = %q, want trimmed user", port.listCallCtx.UserID)
	}
	if port.listInput.Page.PageSize != contract.MaxPageSize {
		t.Fatalf("PageSize = %d, want max %d", port.listInput.Page.PageSize, contract.MaxPageSize)
	}
	if port.listInput.Page.PageToken != " opaque " {
		t.Fatalf("PageToken = %q, want preserved", port.listInput.Page.PageToken)
	}
	if port.listInput.Keyword != "docs" {
		t.Fatalf("Keyword = %q, want trimmed docs", port.listInput.Keyword)
	}
}

func TestFacadeReturnsPortResultsAndErrors(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	port := &fakeCatalogPort{
		listResult: ListResult{
			Items: []Summary{{ID: "ds-1", Name: "Docs", UpdatedAt: now}},
			Page:  contract.PageResult{NextPageToken: "next"},
		},
		getResult: GetResult{Knowledge: Summary{ID: "ds-1", Name: "Docs", UpdatedAt: now}},
	}
	facade := mustKnowledgeFacade(t, port)
	list, err := facade.List(context.Background(), contract.CallContext{UserID: "user"}, ListInput{})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != "ds-1" || list.Page.NextPageToken != "next" {
		t.Fatalf("List = %#v, want ds-1 and next token", list)
	}
	get, err := facade.Get(context.Background(), contract.CallContext{UserID: "user"}, GetInput{KnowledgeID: " ds-1 "})
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if get.Knowledge.ID != "ds-1" || port.getInput.KnowledgeID != "ds-1" {
		t.Fatalf("Get = %#v input=%#v, want trimmed ds-1", get, port.getInput)
	}

	want := errors.New("backend failed")
	facade = mustKnowledgeFacade(t, &fakeCatalogPort{listErr: want})
	_, err = facade.List(context.Background(), contract.CallContext{UserID: "user"}, ListInput{})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func mustKnowledgeFacade(t *testing.T, port CatalogPort) *Facade {
	t.Helper()
	facade, err := NewFacade(port)
	if err != nil {
		t.Fatalf("NewFacade: %v", err)
	}
	return facade
}
