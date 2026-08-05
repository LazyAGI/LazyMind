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

type fakeDocumentPort struct {
	metadataCallCtx contract.CallContext
	metadataInput   GetDocumentMetadataInput
	contentCallCtx  contract.CallContext
	contentInput    ReadDocumentContentInput
	chunksCallCtx   contract.CallContext
	chunksInput     ListDocumentChunksInput
	metadataCalls   int
	contentCalls    int
	chunksCalls     int
	metadataResult  DocumentDetail
	contentResult   DocumentContent
	chunksResult    ListDocumentChunksResult
	metadataErr     error
	contentErr      error
	chunksErr       error
}

type fakeSearchPort struct {
	callCtx contract.CallContext
	input   SearchInput
	result  SearchResult
	err     error
	calls   int
}

func (p *fakeSearchPort) Search(ctx context.Context, callCtx contract.CallContext, input SearchInput) (SearchResult, error) {
	p.calls++
	p.callCtx = callCtx
	p.input = input
	if p.err != nil {
		return SearchResult{}, p.err
	}
	return p.result, nil
}

func (p *fakeDocumentPort) GetDocumentMetadata(ctx context.Context, callCtx contract.CallContext, input GetDocumentMetadataInput) (DocumentDetail, error) {
	p.metadataCalls++
	p.metadataCallCtx = callCtx
	p.metadataInput = input
	if p.metadataErr != nil {
		return DocumentDetail{}, p.metadataErr
	}
	return p.metadataResult, nil
}

func (p *fakeDocumentPort) ReadDocumentContent(ctx context.Context, callCtx contract.CallContext, input ReadDocumentContentInput) (DocumentContent, error) {
	p.contentCalls++
	p.contentCallCtx = callCtx
	p.contentInput = input
	if p.contentErr != nil {
		return DocumentContent{}, p.contentErr
	}
	return p.contentResult, nil
}

func (p *fakeDocumentPort) ListDocumentChunks(ctx context.Context, callCtx contract.CallContext, input ListDocumentChunksInput) (ListDocumentChunksResult, error) {
	p.chunksCalls++
	p.chunksCallCtx = callCtx
	p.chunksInput = input
	if p.chunksErr != nil {
		return ListDocumentChunksResult{}, p.chunksErr
	}
	return p.chunksResult, nil
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

func TestFacadeGetDocumentValidationAndUnsupported(t *testing.T) {
	facade := mustKnowledgeFacade(t, &fakeCatalogPort{})
	cases := []struct {
		name    string
		callCtx contract.CallContext
		input   GetDocumentInput
		code    contract.ErrorCode
	}{
		{name: "user", callCtx: contract.CallContext{UserID: " "}, input: GetDocumentInput{KnowledgeID: "ds-1", DocumentID: "doc-1"}, code: contract.InvalidArgument},
		{name: "knowledge", callCtx: contract.CallContext{UserID: "user"}, input: GetDocumentInput{DocumentID: "doc-1"}, code: contract.InvalidArgument},
		{name: "document", callCtx: contract.CallContext{UserID: "user"}, input: GetDocumentInput{KnowledgeID: "ds-1"}, code: contract.InvalidArgument},
		{name: "unsupported", callCtx: contract.CallContext{UserID: "user"}, input: GetDocumentInput{KnowledgeID: "ds-1", DocumentID: "doc-1"}, code: contract.Unsupported},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := facade.GetDocument(context.Background(), tt.callCtx, tt.input)
			if code, ok := contract.CodeOf(err); !ok || code != tt.code {
				t.Fatalf("error code = %v, %v; want %s", code, ok, tt.code)
			}
		})
	}
}

func TestFacadeGetDocumentMetadataOnly(t *testing.T) {
	port := &fakeDocumentPort{metadataResult: DocumentDetail{ID: "doc-1", KnowledgeID: "ds-1", Name: "Doc"}}
	facade := mustKnowledgeFacadeWithDeps(t, FacadeDeps{Catalog: &fakeCatalogPort{}, Document: port})

	result, err := facade.GetDocument(context.Background(), contract.CallContext{UserID: " user "}, GetDocumentInput{
		KnowledgeID: " ds-1 ",
		DocumentID:  " doc-1 ",
	})
	if err != nil {
		t.Fatalf("GetDocument returned error: %v", err)
	}
	if result.Document.ID != "doc-1" || result.Document.Content != nil || result.Document.ChunksPage != nil {
		t.Fatalf("unexpected result: %#v", result)
	}
	if port.metadataCalls != 1 || port.contentCalls != 0 || port.chunksCalls != 0 {
		t.Fatalf("calls metadata=%d content=%d chunks=%d", port.metadataCalls, port.contentCalls, port.chunksCalls)
	}
	if port.metadataCallCtx.UserID != "user" || port.metadataInput.KnowledgeID != "ds-1" || port.metadataInput.DocumentID != "doc-1" {
		t.Fatalf("unexpected metadata call ctx=%#v input=%#v", port.metadataCallCtx, port.metadataInput)
	}
}

func TestFacadeGetDocumentIncludesContentAndChunks(t *testing.T) {
	total := int64(2)
	port := &fakeDocumentPort{
		metadataResult: DocumentDetail{ID: "doc-1", KnowledgeID: "ds-1", Name: "Doc"},
		contentResult:  DocumentContent{MIMEType: "text/plain", Text: "hello", Truncated: true},
		chunksResult: ListDocumentChunksResult{
			Chunks: []DocumentChunk{{ID: "chunk-1", Text: "part", Number: 1}},
			Page:   contract.PageResult{NextPageToken: "next", Total: &total},
		},
	}
	facade := mustKnowledgeFacadeWithDeps(t, FacadeDeps{Catalog: &fakeCatalogPort{}, Document: port})
	result, err := facade.GetDocument(context.Background(), contract.CallContext{UserID: "user"}, GetDocumentInput{
		KnowledgeID:    "ds-1",
		DocumentID:     "doc-1",
		IncludeContent: true,
		IncludeChunks:  true,
		ChunksPage:     contract.PageRequest{PageSize: 500, PageToken: "tok"},
	})
	if err != nil {
		t.Fatalf("GetDocument returned error: %v", err)
	}
	if result.Document.Content == nil || result.Document.Content.Text != "hello" || !result.Document.Content.Truncated {
		t.Fatalf("unexpected content: %#v", result.Document.Content)
	}
	if len(result.Document.Chunks) != 1 || result.Document.Chunks[0].ID != "chunk-1" || result.Document.ChunksPage == nil || result.Document.ChunksPage.NextPageToken != "next" {
		t.Fatalf("unexpected chunks: %#v page=%#v", result.Document.Chunks, result.Document.ChunksPage)
	}
	if port.chunksInput.Page.PageSize != contract.MaxPageSize || port.chunksInput.Page.PageToken != "tok" {
		t.Fatalf("chunks page = %#v, want max page size and token", port.chunksInput.Page)
	}
	if port.contentCalls != 1 || port.chunksCalls != 1 {
		t.Fatalf("content calls=%d chunks calls=%d", port.contentCalls, port.chunksCalls)
	}
}

func TestFacadeGetDocumentDefaultChunkPage(t *testing.T) {
	port := &fakeDocumentPort{metadataResult: DocumentDetail{ID: "doc-1", KnowledgeID: "ds-1"}}
	facade := mustKnowledgeFacadeWithDeps(t, FacadeDeps{Catalog: &fakeCatalogPort{}, Document: port})
	_, err := facade.GetDocument(context.Background(), contract.CallContext{UserID: "user"}, GetDocumentInput{
		KnowledgeID:   "ds-1",
		DocumentID:    "doc-1",
		IncludeChunks: true,
	})
	if err != nil {
		t.Fatalf("GetDocument returned error: %v", err)
	}
	if port.chunksInput.Page.PageSize != contract.DefaultPageSize {
		t.Fatalf("PageSize = %d, want default %d", port.chunksInput.Page.PageSize, contract.DefaultPageSize)
	}
}

func TestFacadeGetDocumentPropagatesOptionalErrors(t *testing.T) {
	wantContent := contract.NewError(contract.BackendUnavailable, "content", "down", true, nil)
	port := &fakeDocumentPort{metadataResult: DocumentDetail{ID: "doc-1"}, contentErr: wantContent}
	facade := mustKnowledgeFacadeWithDeps(t, FacadeDeps{Catalog: &fakeCatalogPort{}, Document: port})
	_, err := facade.GetDocument(context.Background(), contract.CallContext{UserID: "user"}, GetDocumentInput{
		KnowledgeID:    "ds-1",
		DocumentID:     "doc-1",
		IncludeContent: true,
	})
	if !errors.Is(err, wantContent) {
		t.Fatalf("content err = %v, want %v", err, wantContent)
	}

	wantChunks := contract.NewError(contract.Internal, "chunks", "bad", false, nil)
	port = &fakeDocumentPort{metadataResult: DocumentDetail{ID: "doc-1"}, chunksErr: wantChunks}
	facade = mustKnowledgeFacadeWithDeps(t, FacadeDeps{Catalog: &fakeCatalogPort{}, Document: port})
	_, err = facade.GetDocument(context.Background(), contract.CallContext{UserID: "user"}, GetDocumentInput{
		KnowledgeID:   "ds-1",
		DocumentID:    "doc-1",
		IncludeChunks: true,
	})
	if !errors.Is(err, wantChunks) {
		t.Fatalf("chunks err = %v, want %v", err, wantChunks)
	}
}

func TestFacadeGetDocumentMetadataErrorStopsOptionalCalls(t *testing.T) {
	want := contract.NewError(contract.NotFound, "metadata", "missing", false, nil)
	port := &fakeDocumentPort{metadataErr: want}
	facade := mustKnowledgeFacadeWithDeps(t, FacadeDeps{Catalog: &fakeCatalogPort{}, Document: port})
	_, err := facade.GetDocument(context.Background(), contract.CallContext{UserID: "user"}, GetDocumentInput{
		KnowledgeID:    "ds-1",
		DocumentID:     "doc-1",
		IncludeContent: true,
		IncludeChunks:  true,
	})
	if !errors.Is(err, want) {
		t.Fatalf("metadata err = %v, want %v", err, want)
	}
	if port.contentCalls != 0 || port.chunksCalls != 0 {
		t.Fatalf("content calls=%d chunks calls=%d, want 0 after metadata failure", port.contentCalls, port.chunksCalls)
	}
}

func TestFacadeCatalogListGetUnchangedWithDocumentPort(t *testing.T) {
	catalog := &fakeCatalogPort{
		listResult: ListResult{Items: []Summary{{ID: "ds-1"}}},
		getResult:  GetResult{Knowledge: Summary{ID: "ds-1"}},
	}
	facade := mustKnowledgeFacadeWithDeps(t, FacadeDeps{Catalog: catalog, Document: &fakeDocumentPort{}})
	if _, err := facade.List(context.Background(), contract.CallContext{UserID: "user"}, ListInput{}); err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if _, err := facade.Get(context.Background(), contract.CallContext{UserID: "user"}, GetInput{KnowledgeID: "ds-1"}); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if catalog.listInput.Page.PageSize != contract.DefaultPageSize || catalog.getInput.KnowledgeID != "ds-1" {
		t.Fatalf("catalog calls changed: list=%#v get=%#v", catalog.listInput, catalog.getInput)
	}
}

func TestFacadeSearchValidationAndUnsupported(t *testing.T) {
	facade := mustKnowledgeFacade(t, &fakeCatalogPort{})
	cases := []struct {
		name    string
		callCtx contract.CallContext
		input   SearchInput
		code    contract.ErrorCode
	}{
		{name: "user", callCtx: contract.CallContext{UserID: " "}, input: SearchInput{Query: "q", KnowledgeIDs: []string{"ds-1"}}, code: contract.InvalidArgument},
		{name: "query", callCtx: contract.CallContext{UserID: "user"}, input: SearchInput{Query: " ", KnowledgeIDs: []string{"ds-1"}}, code: contract.InvalidArgument},
		{name: "knowledge ids", callCtx: contract.CallContext{UserID: "user"}, input: SearchInput{Query: "q", KnowledgeIDs: []string{" "}}, code: contract.InvalidArgument},
		{name: "unsupported", callCtx: contract.CallContext{UserID: "user"}, input: SearchInput{Query: "q", KnowledgeIDs: []string{"ds-1"}}, code: contract.Unsupported},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := facade.Search(context.Background(), tt.callCtx, tt.input)
			if code, ok := contract.CodeOf(err); !ok || code != tt.code {
				t.Fatalf("error code = %v, %v; want %s", code, ok, tt.code)
			}
		})
	}
}

func TestFacadeSearchNormalizesInputAndReturnsResult(t *testing.T) {
	port := &fakeSearchPort{result: SearchResult{
		Hits: []SearchHit{{KnowledgeID: "ds-1", DocumentID: "doc-1", Text: "source", Score: 0.8}},
	}}
	facade := mustKnowledgeFacadeWithDeps(t, FacadeDeps{Search: port})
	result, err := facade.Search(context.Background(), contract.CallContext{UserID: " user "}, SearchInput{
		Query:        "  hello  ",
		KnowledgeIDs: []string{" ds-1 ", "", "ds-2", "ds-1"},
		TopK:         500,
	})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(result.Hits) != 1 || result.Hits[0].DocumentID != "doc-1" || result.Hits[0].Score != 0.8 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if port.calls != 1 || port.callCtx.UserID != "user" {
		t.Fatalf("unexpected search calls=%d callCtx=%#v", port.calls, port.callCtx)
	}
	if port.input.Query != "hello" || port.input.TopK != MaxSearchTopK {
		t.Fatalf("input not normalized: %#v", port.input)
	}
	if len(port.input.KnowledgeIDs) != 2 || port.input.KnowledgeIDs[0] != "ds-1" || port.input.KnowledgeIDs[1] != "ds-2" {
		t.Fatalf("knowledge ids not normalized: %#v", port.input.KnowledgeIDs)
	}
}

func TestFacadeSearchDefaultTopK(t *testing.T) {
	port := &fakeSearchPort{result: SearchResult{Hits: []SearchHit{}}}
	facade := mustKnowledgeFacadeWithDeps(t, FacadeDeps{Search: port})
	if _, err := facade.Search(context.Background(), contract.CallContext{UserID: "user"}, SearchInput{Query: "q", KnowledgeIDs: []string{"ds-1"}, TopK: -1}); err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if port.input.TopK != DefaultSearchTopK {
		t.Fatalf("TopK = %d, want default %d", port.input.TopK, DefaultSearchTopK)
	}
}

func TestFacadeSearchPropagatesPortTypedError(t *testing.T) {
	want := contract.NewError(contract.BackendUnavailable, "search", "down", true, errors.New("cause"))
	port := &fakeSearchPort{err: want}
	facade := mustKnowledgeFacadeWithDeps(t, FacadeDeps{Search: port})
	_, err := facade.Search(context.Background(), contract.CallContext{UserID: "user"}, SearchInput{Query: "q", KnowledgeIDs: []string{"ds-1"}})
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

func mustKnowledgeFacadeWithDeps(t *testing.T, deps FacadeDeps) *Facade {
	t.Helper()
	facade, err := NewFacadeWithDeps(deps)
	if err != nil {
		t.Fatalf("NewFacadeWithDeps: %v", err)
	}
	return facade
}
