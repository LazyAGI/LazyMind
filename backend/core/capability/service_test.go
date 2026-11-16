package capability

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakePorts struct {
	contentReadCalls  int
	skillQuery        SkillListQuery
	knowledgeQuery    KnowledgeListQuery
	documentListQuery KnowledgeDocumentListQuery
	searchInput       SearchKnowledgeInput
	call              InvocationContext
	metadata          SkillMetadata
	content           SkillContent
	document          GetKnowledgeDocumentResult
	searchResult      SearchKnowledgeResult
	cloudGetInput     GetCloudDocumentInput
}

func (f *fakePorts) ListVocabularyWordbooks(context.Context, InvocationContext) (ListVocabularyWordbooksResult, error) {
	return ListVocabularyWordbooksResult{}, nil
}
func (f *fakePorts) ListVocabularyWords(context.Context, InvocationContext, ListVocabularyWordsInput) (ListVocabularyWordsResult, error) {
	return ListVocabularyWordsResult{}, nil
}
func (f *fakePorts) NextVocabularyReview(context.Context, InvocationContext, NextVocabularyReviewInput) (NextVocabularyReviewResult, error) {
	return NextVocabularyReviewResult{}, nil
}
func (f *fakePorts) StartVocabularyReview(context.Context, InvocationContext, StartVocabularyReviewInput) (StartVocabularyReviewResult, error) {
	return StartVocabularyReviewResult{}, nil
}
func (f *fakePorts) AnswerVocabularyReview(context.Context, InvocationContext, AnswerVocabularyReviewInput) (AnswerVocabularyReviewResult, error) {
	return AnswerVocabularyReviewResult{Accepted: true}, nil
}
func (f *fakePorts) VocabularyReviewReport(context.Context, InvocationContext, VocabularyReviewReportInput) (VocabularyReviewReportResult, error) {
	return VocabularyReviewReportResult{}, nil
}

func (f *fakePorts) ListCloudDocuments(_ context.Context, call InvocationContext, _ CloudDocumentListQuery) (CloudDocumentListPage, error) {
	f.call = call
	return CloudDocumentListPage{}, nil
}
func (f *fakePorts) GetCloudDocument(_ context.Context, call InvocationContext, input GetCloudDocumentInput) (GetCloudDocumentResult, error) {
	f.call, f.cloudGetInput = call, input
	documents := []CloudDocumentMetadata{{ID: "online-1"}, {ID: "online-2"}}
	next := "provider-next"
	if input.ProviderCursor != "" {
		documents = []CloudDocumentMetadata{{ID: "online-3"}}
		next = ""
	}
	return GetCloudDocumentResult{
		Documents:     documents,
		DocumentsPage: &CursorPageInfo{ProviderCursor: next},
	}, nil
}
func (f *fakePorts) SearchCloudDocuments(_ context.Context, call InvocationContext, _ SearchCloudDocumentsInput) (SearchCloudDocumentsResult, error) {
	f.call = call
	return SearchCloudDocumentsResult{}, nil
}

func (f *fakePorts) ListSkills(_ context.Context, call InvocationContext, query SkillListQuery) (SkillListPage, error) {
	f.call, f.skillQuery = call, query
	items := []SkillSummary{{ID: "skill-1", HeadRevisionID: "rev-1"}, {ID: "skill-2", HeadRevisionID: "rev-2"}}
	if query.Offset > 0 {
		items = []SkillSummary{{ID: "skill-3", HeadRevisionID: "rev-3"}}
	}
	return SkillListPage{Items: items, Total: 3}, nil
}

func (f *fakePorts) GetSkillMetadata(_ context.Context, call InvocationContext, _ string) (SkillMetadata, error) {
	f.call = call
	return f.metadata, nil
}

func (f *fakePorts) ReadSkillContent(_ context.Context, call InvocationContext, _, _ string) (SkillContent, error) {
	f.call = call
	return f.content, nil
}

func (f *fakePorts) ListKnowledge(_ context.Context, call InvocationContext, query KnowledgeListQuery) (KnowledgeListPage, error) {
	f.call, f.knowledgeQuery = call, query
	return KnowledgeListPage{Items: []KnowledgeSummary{}, Total: 0}, nil
}

func (f *fakePorts) ListKnowledgeDocuments(_ context.Context, call InvocationContext, query KnowledgeDocumentListQuery) (KnowledgeDocumentListPage, error) {
	f.call, f.documentListQuery = call, query
	items := []KnowledgeDocumentSummary{{ID: "doc-1", KnowledgeID: query.KnowledgeID}, {ID: "doc-2", KnowledgeID: query.KnowledgeID}}
	if query.Offset > 0 {
		items = []KnowledgeDocumentSummary{{ID: "doc-3", KnowledgeID: query.KnowledgeID}}
	}
	return KnowledgeDocumentListPage{Items: items, Total: 3}, nil
}

func (f *fakePorts) GetKnowledgeDocument(_ context.Context, call InvocationContext, _ GetKnowledgeDocumentInput) (GetKnowledgeDocumentResult, error) {
	f.call = call
	return f.document, nil
}

func (f *fakePorts) SearchKnowledge(_ context.Context, call InvocationContext, input SearchKnowledgeInput) (SearchKnowledgeResult, error) {
	f.call, f.searchInput = call, input
	return f.searchResult, nil
}

func TestServiceRequiresEveryPublishedCapability(t *testing.T) {
	ports := &fakePorts{}
	for name, deps := range map[string]Dependencies{
		"skills":     {Knowledge: ports, Documents: ports, Search: ports, Cloud: ports},
		"knowledge":  {Skills: ports, Documents: ports, Search: ports, Cloud: ports},
		"documents":  {Skills: ports, Knowledge: ports, Search: ports, Cloud: ports},
		"search":     {Skills: ports, Knowledge: ports, Documents: ports, Cloud: ports},
		"cloud":      {Skills: ports, Knowledge: ports, Documents: ports, Search: ports},
		"vocabulary": {Skills: ports, Knowledge: ports, Documents: ports, Search: ports, Cloud: ports},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewService(deps); err == nil {
				t.Fatal("NewService error = nil")
			}
		})
	}
}

func TestListSkillsUsesBoundCursorAndPublishedFilter(t *testing.T) {
	ports := &fakePorts{}
	service := mustService(t, ports)
	call := authorizedCall()

	first, err := service.ListSkills(context.Background(), call, ListSkillsInput{
		Keyword: " alpha ", Tags: []string{"z", "a", "a"}, Page: PageRequest{PageSize: 2},
	})
	if err != nil {
		t.Fatalf("ListSkills first page: %v", err)
	}
	if ports.skillQuery.Offset != 0 || strings.Join(ports.skillQuery.Tags, ",") != "a,z" {
		t.Fatalf("normalized query = %#v", ports.skillQuery)
	}
	if first.Page.NextPageToken == "" || len(first.Items) != 2 {
		t.Fatalf("first page = %#v", first)
	}

	second, err := service.ListSkills(context.Background(), call, ListSkillsInput{
		Keyword: "alpha", Tags: []string{"a", "z"}, Page: PageRequest{PageSize: 2, PageToken: first.Page.NextPageToken},
	})
	if err != nil {
		t.Fatalf("ListSkills second page: %v", err)
	}
	if ports.skillQuery.Offset != 2 || second.Page.NextPageToken != "" || len(second.Items) != 1 {
		t.Fatalf("second page query=%#v result=%#v", ports.skillQuery, second)
	}

	_, err = service.ListSkills(context.Background(), call, ListSkillsInput{
		Keyword: "different", Tags: []string{"a", "z"}, Page: PageRequest{PageSize: 2, PageToken: first.Page.NextPageToken},
	})
	assertCode(t, err, InvalidArgument)
}

func TestGetCloudDocumentUsesOpaqueBoundDocumentCursor(t *testing.T) {
	ports := &fakePorts{}
	service := mustService(t, ports)
	call := authorizedCall()

	first, err := service.GetCloudDocument(context.Background(), call, GetCloudDocumentInput{
		SourceID: "source-1", IncludeDocuments: true,
		DocumentsPage: PageRequest{PageSize: 2},
	})
	if err != nil || first.DocumentsPage == nil || first.DocumentsPage.NextPageToken == "" {
		t.Fatalf("first cloud document page=%#v err=%v", first, err)
	}
	second, err := service.GetCloudDocument(context.Background(), call, GetCloudDocumentInput{
		SourceID: "source-1", IncludeDocuments: true,
		DocumentsPage: PageRequest{PageSize: 2, PageToken: first.DocumentsPage.NextPageToken},
	})
	if err != nil || ports.cloudGetInput.ProviderCursor != "provider-next" || len(second.Documents) != 1 || second.DocumentsPage.NextPageToken != "" {
		t.Fatalf("second cloud document page=%#v input=%#v err=%v", second, ports.cloudGetInput, err)
	}
	for _, principal := range []Principal{
		{UserID: "another-user", TenantID: call.Principal.TenantID, Permissions: call.Principal.Permissions},
		{UserID: call.Principal.UserID, TenantID: "another-tenant", Permissions: call.Principal.Permissions},
	} {
		_, err := service.GetCloudDocument(context.Background(), InvocationContext{Principal: principal}, GetCloudDocumentInput{
			SourceID: "source-1", IncludeDocuments: true,
			DocumentsPage: PageRequest{PageSize: 2, PageToken: first.DocumentsPage.NextPageToken},
		})
		assertCode(t, err, InvalidArgument)
	}
	_, err = service.GetCloudDocument(context.Background(), call, GetCloudDocumentInput{
		SourceID: "different", IncludeDocuments: true,
		DocumentsPage: PageRequest{PageSize: 2, PageToken: first.DocumentsPage.NextPageToken},
	})
	assertCode(t, err, InvalidArgument)
	_, err = service.GetCloudDocument(context.Background(), call, GetCloudDocumentInput{
		SourceID: "source-1", IncludeDocuments: true,
		DocumentsPage: PageRequest{PageSize: 2, PageToken: strings.Repeat("x", maxPageTokenBytes+1)},
	})
	assertCode(t, err, InvalidArgument)
}

func TestCloudCursorPageOmitsUnknownTotalAndZeroTimes(t *testing.T) {
	value := struct {
		Source CloudDocumentSource        `json:"source"`
		Search SearchCloudDocumentsResult `json:"search"`
	}{
		Source: CloudDocumentSource{ID: "source-1"},
		Search: SearchCloudDocumentsResult{Page: CursorPageInfo{NextPageToken: "next"}},
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, `"total":0`) || strings.Contains(text, "0001-01-01") {
		t.Fatalf("unknown total or zero time leaked: %s", text)
	}
}

func TestGetSkillReturnsOnlyPublishedMatchingRevisionContent(t *testing.T) {
	ports := &fakePorts{
		metadata: SkillMetadata{Published: true, Summary: SkillSummary{ID: "skill-1", HeadRevisionID: "rev-1"}},
		content:  SkillContent{RevisionID: "rev-1", Text: "published"},
	}
	service := mustService(t, ports)
	result, err := service.GetSkill(context.Background(), authorizedCall(), GetSkillInput{SkillID: "skill-1", IncludeContent: true})
	if err != nil || result.Content == nil || result.Content.Text != "published" {
		t.Fatalf("GetSkill result=%#v err=%v", result, err)
	}

	ports.metadata.Published = false
	_, err = service.GetSkill(context.Background(), authorizedCall(), GetSkillInput{SkillID: "skill-1"})
	assertCode(t, err, NotFound)

	ports.metadata.Published = true
	ports.content = SkillContent{RevisionID: "old", Text: "stale"}
	_, err = service.GetSkill(context.Background(), authorizedCall(), GetSkillInput{SkillID: "skill-1", IncludeContent: true})
	assertCode(t, err, Internal)

	ports.content = SkillContent{RevisionID: "rev-1", Text: strings.Repeat("x", maxSkillContentBytes+1)}
	_, err = service.GetSkill(context.Background(), authorizedCall(), GetSkillInput{SkillID: "skill-1", IncludeContent: true})
	assertCode(t, err, ResultTooLarge)
}

func TestListKnowledgeDocumentsUsesKnowledgeBoundCursor(t *testing.T) {
	ports := &fakePorts{}
	service := mustService(t, ports)
	first, err := service.ListKnowledgeDocuments(context.Background(), authorizedCall(), ListKnowledgeDocumentsInput{
		KnowledgeID: " kb-1 ", Page: PageRequest{PageSize: 2},
	})
	if err != nil || len(first.Items) != 2 || first.Page.NextPageToken == "" || ports.documentListQuery.KnowledgeID != "kb-1" {
		t.Fatalf("first document page=%#v query=%#v err=%v", first, ports.documentListQuery, err)
	}
	second, err := service.ListKnowledgeDocuments(context.Background(), authorizedCall(), ListKnowledgeDocumentsInput{
		KnowledgeID: "kb-1", Page: PageRequest{PageSize: 2, PageToken: first.Page.NextPageToken},
	})
	if err != nil || len(second.Items) != 1 || ports.documentListQuery.Offset != 2 {
		t.Fatalf("second document page=%#v query=%#v err=%v", second, ports.documentListQuery, err)
	}
	_, err = service.ListKnowledgeDocuments(context.Background(), authorizedCall(), ListKnowledgeDocumentsInput{
		KnowledgeID: "kb-2", Page: PageRequest{PageSize: 2, PageToken: first.Page.NextPageToken},
	})
	assertCode(t, err, InvalidArgument)
}

func TestKnowledgeDocumentFiltersBindCursor(t *testing.T) {
	ports := &fakePorts{}
	service := mustService(t, ports)
	input := ListKnowledgeDocumentsInput{KnowledgeID: "kb", Name: " report ", Path: " team/ ", Page: PageRequest{PageSize: 2}}
	first, err := service.ListKnowledgeDocuments(context.Background(), authorizedCall(), input)
	if err != nil || first.Page.NextPageToken == "" || ports.documentListQuery.Name != "report" || ports.documentListQuery.Path != "team/" {
		t.Fatalf("page=%+v query=%+v err=%v", first, ports.documentListQuery, err)
	}
	input.Page.PageToken = first.Page.NextPageToken
	if _, err := service.ListKnowledgeDocuments(context.Background(), authorizedCall(), input); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"name", "path", "user", "tenant"} {
		t.Run(field, func(t *testing.T) {
			changed, call := input, authorizedCall()
			switch field {
			case "name":
				changed.Name = "other"
			case "path":
				changed.Path = "other"
			case "user":
				call.Principal.UserID = "other"
			case "tenant":
				call.Principal.TenantID = "other"
			}
			_, err := service.ListKnowledgeDocuments(context.Background(), call, changed)
			assertCode(t, err, InvalidArgument)
		})
	}
	input.Page.PageToken = ""
	input.Name = strings.Repeat("n", maxFilterBytes+1)
	_, err = service.ListKnowledgeDocuments(context.Background(), authorizedCall(), input)
	assertCode(t, err, InvalidArgument)
	input.Name, input.Path = "", strings.Repeat("p", 1025)
	_, err = service.ListKnowledgeDocuments(context.Background(), authorizedCall(), input)
	assertCode(t, err, InvalidArgument)
}

func TestSearchKnowledgeNormalizesAndBoundsRetrieval(t *testing.T) {
	ports := &fakePorts{searchResult: SearchKnowledgeResult{Hits: []KnowledgeSearchHit{{KnowledgeID: "kb-1", Text: "safe"}}}}
	service := mustService(t, ports)
	result, err := service.SearchKnowledge(context.Background(), authorizedCall(), SearchKnowledgeInput{
		Query: " query ", KnowledgeIDs: []string{"kb-1", " kb-1 ", "kb-2"},
	})
	if err != nil || len(result.Hits) != 1 {
		t.Fatalf("SearchKnowledge result=%#v err=%v", result, err)
	}
	if ports.searchInput.Query != "query" || ports.searchInput.TopK != defaultSearchTopK || strings.Join(ports.searchInput.KnowledgeIDs, ",") != "kb-1,kb-2" {
		t.Fatalf("normalized search input = %#v", ports.searchInput)
	}
	ports.searchResult = SearchKnowledgeResult{Hits: []KnowledgeSearchHit{{KnowledgeID: "kb", Text: strings.Repeat("x", maxSearchHitTextBytes+1)}}}
	_, err = service.SearchKnowledge(context.Background(), authorizedCall(), SearchKnowledgeInput{Query: "q", KnowledgeIDs: []string{"kb"}})
	assertCode(t, err, ResultTooLarge)
	_, err = service.SearchKnowledge(context.Background(), authorizedCall(), SearchKnowledgeInput{Query: "q", KnowledgeIDs: []string{"kb"}, TopK: 51})
	assertCode(t, err, InvalidArgument)
}

func TestSearchKnowledgeExecutionScope(t *testing.T) {
	for _, tc := range []struct {
		name       string
		scope, ids []string
		want       ErrorCode
	}{
		{"unrestricted", nil, []string{"outside"}, ""},
		{"allowed", []string{"inside"}, []string{" inside "}, ""},
		{"denied", []string{"inside"}, []string{"outside"}, PermissionDenied},
		{"mixed", []string{"inside"}, []string{"inside", "outside"}, PermissionDenied},
		{"empty scope", []string{}, []string{"inside"}, PermissionDenied},
		{"missing ids", []string{"inside"}, nil, InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakePorts{}
			s := mustService(t, p)
			c := authorizedCall()
			c.AllowedDatasetIDs = tc.scope
			_, err := s.SearchKnowledge(context.Background(), c, SearchKnowledgeInput{Query: "q", KnowledgeIDs: tc.ids})
			if tc.want != "" {
				assertCode(t, err, tc.want)
				if p.searchInput.Query != "" {
					t.Fatal("denied request reached search adapter")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.call.Principal.UserID != c.Principal.UserID || p.call.Principal.TenantID != c.Principal.TenantID {
				t.Fatal("caller identity lost before ACL checks")
			}
		})
	}
}

func TestSearchKnowledgeParameterBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   SearchKnowledgeInput
		invalid bool
	}{
		{"empty query", SearchKnowledgeInput{KnowledgeIDs: []string{"kb"}}, true},
		{"whitespace query", SearchKnowledgeInput{Query: " \t\n", KnowledgeIDs: []string{"kb"}}, true},
		{"8192 bytes", SearchKnowledgeInput{Query: strings.Repeat("x", 8192), KnowledgeIDs: []string{"kb"}}, false},
		{"8193 bytes", SearchKnowledgeInput{Query: strings.Repeat("x", 8193), KnowledgeIDs: []string{"kb"}}, true},
		{"nil IDs", SearchKnowledgeInput{Query: "q"}, true},
		{"empty IDs", SearchKnowledgeInput{Query: "q", KnowledgeIDs: []string{}}, true},
		{"blank ID", SearchKnowledgeInput{Query: "q", KnowledgeIDs: []string{" "}}, true},
		{"negative topK", SearchKnowledgeInput{Query: "q", KnowledgeIDs: []string{"kb"}, TopK: -1}, true},
		{"minimum topK", SearchKnowledgeInput{Query: "q", KnowledgeIDs: []string{"kb"}, TopK: 1}, false},
		{"maximum topK", SearchKnowledgeInput{Query: "q", KnowledgeIDs: []string{"kb"}, TopK: 50}, false},
		{"excessive topK", SearchKnowledgeInput{Query: "q", KnowledgeIDs: []string{"kb"}, TopK: 51}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ports := &fakePorts{}
			_, err := mustService(t, ports).SearchKnowledge(context.Background(), authorizedCall(), tc.input)
			if tc.invalid {
				assertCode(t, err, InvalidArgument)
				if ports.searchInput.Query != "" {
					t.Fatal("invalid request reached search adapter")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ports.searchInput.Query != tc.input.Query {
				t.Fatal("query changed at valid boundary")
			}
			if tc.input.TopK != 0 && ports.searchInput.TopK != tc.input.TopK {
				t.Fatal("valid top_k changed")
			}
		})
	}
}

func TestGetKnowledgeDocumentExecutionScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scope  []string
		denied bool
	}{
		{"unrestricted", nil, false}, {"allowed", []string{"inside"}, false},
		{"outside", []string{"outside"}, true}, {"empty", []string{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakePorts{}
			c := authorizedCall()
			c.AllowedDatasetIDs = tc.scope
			_, err := mustService(t, p).GetKnowledgeDocument(context.Background(), c, GetKnowledgeDocumentInput{KnowledgeID: " inside ", DocumentID: "doc", IncludeContent: true})
			if tc.denied {
				assertCode(t, err, PermissionDenied)
				if p.call.Principal.UserID != "" {
					t.Fatal("denied read reached adapter")
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSearchKnowledgeDropsHitsOutsideRequestedLibraries(t *testing.T) {
	p := &fakePorts{searchResult: SearchKnowledgeResult{Hits: []KnowledgeSearchHit{{KnowledgeID: "inside", Text: "allowed"}, {KnowledgeID: "outside", Text: "unrequested"}}}}
	c := authorizedCall()
	c.AllowedDatasetIDs = []string{"inside"}
	r, e := mustService(t, p).SearchKnowledge(context.Background(), c, SearchKnowledgeInput{Query: "q", KnowledgeIDs: []string{"inside"}})
	if e != nil || len(r.Hits) != 1 || r.Hits[0].KnowledgeID != "inside" {
		t.Fatalf("result=%+v err=%v", r, e)
	}
}

func TestServiceRejectsUntrustedOrUnderprivilegedCaller(t *testing.T) {
	service := mustService(t, &fakePorts{})
	_, err := service.ListKnowledge(context.Background(), InvocationContext{}, ListKnowledgeInput{})
	assertCode(t, err, Unauthenticated)
	_, err = service.ListKnowledge(context.Background(), InvocationContext{Principal: Principal{UserID: "user"}}, ListKnowledgeInput{})
	assertCode(t, err, PermissionDenied)
}

func mustService(t *testing.T, ports *fakePorts) *Service {
	t.Helper()
	service, err := NewService(Dependencies{Skills: ports, Knowledge: ports, Documents: ports, Search: ports, Cloud: ports, Vocabulary: ports})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func authorizedCall() InvocationContext {
	return InvocationContext{Principal: Principal{UserID: "user-1", TenantID: "tenant-1", Permissions: NewPermissionSet(RequiredPermission)}}
}

func assertCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if got, ok := CodeOf(err); !ok || got != want {
		t.Fatalf("error code = %q, %v; want %q; err=%v", got, ok, want, err)
	}
}

func (r *fakePorts) ReadCloudDocument(_ context.Context, _ InvocationContext, in ReadCloudDocumentInput) (ReadCloudDocumentResult, error) {
	r.contentReadCalls++
	return ReadCloudDocumentResult{SourceID: in.SourceID, Content: "body"}, nil
}

func TestReadCloudDocumentValidatesCallerAndPagingBeforeAdapter(t *testing.T) {
	r := &fakePorts{}
	s := &Service{cloudContent: r}
	call := InvocationContext{Principal: Principal{UserID: "user", Permissions: NewPermissionSet(RequiredPermission)}}
	for _, in := range []ReadCloudDocumentInput{
		{Locator: "googledrive:/doc"}, {SourceID: "connection"},
		{SourceID: "connection", Locator: "googledrive:/doc", Offset: 1},
		{SourceID: "connection", Locator: "googledrive:/doc", Offset: -1},
		{SourceID: "connection", Locator: "googledrive:/doc", Limit: 100001},
	} {
		if _, err := s.ReadCloudDocument(context.Background(), call, in); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	in := ReadCloudDocumentInput{SourceID: "connection", Locator: "googledrive:/doc"}
	if _, err := s.ReadCloudDocument(context.Background(), InvocationContext{}, in); err == nil {
		t.Fatal("anonymous read accepted")
	}
	if r.contentReadCalls != 0 {
		t.Fatal("adapter called before validation")
	}
	if _, err := s.ReadCloudDocument(context.Background(), call, in); err != nil || r.contentReadCalls != 1 {
		t.Fatalf("read=%v calls=%d", err, r.contentReadCalls)
	}
}
