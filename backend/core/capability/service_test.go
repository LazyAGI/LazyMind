package capability

import (
	"context"
	"strings"
	"testing"
)

type fakePorts struct {
	skillQuery        SkillListQuery
	knowledgeQuery    KnowledgeListQuery
	documentListQuery KnowledgeDocumentListQuery
	searchInput       SearchKnowledgeInput
	call              InvocationContext
	metadata          SkillMetadata
	content           SkillContent
	document          GetKnowledgeDocumentResult
	searchResult      SearchKnowledgeResult
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
		"skills":    {Knowledge: ports, Documents: ports, Search: ports},
		"knowledge": {Skills: ports, Documents: ports, Search: ports},
		"documents": {Skills: ports, Knowledge: ports, Search: ports},
		"search":    {Skills: ports, Knowledge: ports, Documents: ports},
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

func TestSearchKnowledgeNormalizesAndBoundsRetrieval(t *testing.T) {
	ports := &fakePorts{searchResult: SearchKnowledgeResult{Hits: []KnowledgeSearchHit{{Text: "safe"}}}}
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
	ports.searchResult = SearchKnowledgeResult{Hits: []KnowledgeSearchHit{{Text: strings.Repeat("x", maxSearchHitTextBytes+1)}}}
	_, err = service.SearchKnowledge(context.Background(), authorizedCall(), SearchKnowledgeInput{Query: "q", KnowledgeIDs: []string{"kb"}})
	assertCode(t, err, ResultTooLarge)
	_, err = service.SearchKnowledge(context.Background(), authorizedCall(), SearchKnowledgeInput{Query: "q", KnowledgeIDs: []string{"kb"}, TopK: 51})
	assertCode(t, err, InvalidArgument)
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
	service, err := NewService(Dependencies{Skills: ports, Knowledge: ports, Documents: ports, Search: ports})
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
