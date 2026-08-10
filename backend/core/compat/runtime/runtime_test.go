package runtime

import (
	"context"
	"reflect"
	"testing"

	"lazymind/core/compat/contract"
	"lazymind/core/compat/knowledge"
	"lazymind/core/compat/skill"
)

type stubSkillPort struct{}

func (stubSkillPort) List(context.Context, contract.CallContext, skill.ListInput) (skill.ListResult, error) {
	return skill.ListResult{}, nil
}

func (stubSkillPort) GetMetadata(context.Context, contract.CallContext, string) (skill.Summary, error) {
	return skill.Summary{}, nil
}

func (stubSkillPort) ReadContent(context.Context, contract.CallContext, string, string) (skill.Content, error) {
	return skill.Content{}, nil
}

type stubKnowledgeCatalogPort struct{}

func (stubKnowledgeCatalogPort) List(context.Context, contract.CallContext, knowledge.ListInput) (knowledge.ListResult, error) {
	return knowledge.ListResult{}, nil
}

func (stubKnowledgeCatalogPort) Get(context.Context, contract.CallContext, knowledge.GetInput) (knowledge.GetResult, error) {
	return knowledge.GetResult{}, nil
}

type stubKnowledgeDocumentPort struct{}

func (stubKnowledgeDocumentPort) GetDocument(context.Context, contract.CallContext, knowledge.GetDocumentInput) (knowledge.GetDocumentResult, error) {
	return knowledge.GetDocumentResult{Document: knowledge.DocumentDetail{ID: "doc-1", KnowledgeID: "ds-1"}}, nil
}

func (stubKnowledgeDocumentPort) GetDocumentMetadata(context.Context, contract.CallContext, knowledge.GetDocumentMetadataInput) (knowledge.DocumentDetail, error) {
	return knowledge.DocumentDetail{ID: "doc-1", KnowledgeID: "ds-1"}, nil
}

func (stubKnowledgeDocumentPort) ReadDocumentContent(context.Context, contract.CallContext, knowledge.ReadDocumentContentInput) (knowledge.DocumentContent, error) {
	return knowledge.DocumentContent{}, nil
}

func (stubKnowledgeDocumentPort) ListDocumentChunks(context.Context, contract.CallContext, knowledge.ListDocumentChunksInput) (knowledge.ListDocumentChunksResult, error) {
	return knowledge.ListDocumentChunksResult{}, nil
}

type stubKnowledgeSearchPort struct{}

func (stubKnowledgeSearchPort) Search(context.Context, contract.CallContext, knowledge.SearchInput) (knowledge.SearchResult, error) {
	return knowledge.SearchResult{Hits: []knowledge.SearchHit{{KnowledgeID: "ds-1", DocumentID: "doc-1", Text: "hit"}}}, nil
}

func TestNewCreatesSkillFacadeWhenPortProvided(t *testing.T) {
	rt, err := New(Dependencies{SkillPort: stubSkillPort{}})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if rt.Skill == nil {
		t.Fatalf("Skill facade is nil")
	}
}

func TestNewCreatesKnowledgeFacadeWhenCatalogProvided(t *testing.T) {
	rt, err := New(Dependencies{KnowledgeCatalog: stubKnowledgeCatalogPort{}})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if rt.Knowledge == nil {
		t.Fatalf("Knowledge facade is nil")
	}
	if rt.Skill != nil {
		t.Fatalf("Skill facade = %#v, want nil", rt.Skill)
	}
}

func TestNewCreatesKnowledgeFacadeWhenDocumentProvided(t *testing.T) {
	rt, err := New(Dependencies{KnowledgeDocument: stubKnowledgeDocumentPort{}})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if rt.Knowledge == nil {
		t.Fatalf("Knowledge facade is nil")
	}
	got, err := rt.Knowledge.GetDocument(context.Background(), contract.CallContext{UserID: "user"}, knowledge.GetDocumentInput{
		KnowledgeID: "ds-1",
		DocumentID:  "doc-1",
	})
	if err != nil {
		t.Fatalf("GetDocument returned error: %v", err)
	}
	if got.Document.ID != "doc-1" {
		t.Fatalf("GetDocument = %#v, want doc-1", got)
	}
}

func TestRuntimeCatalogOnlyGetDocumentUnsupported(t *testing.T) {
	rt, err := New(Dependencies{KnowledgeCatalog: stubKnowledgeCatalogPort{}})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if rt.Knowledge == nil {
		t.Fatalf("Knowledge facade is nil")
	}
	_, err = rt.Knowledge.GetDocument(context.Background(), contract.CallContext{UserID: "user"}, knowledge.GetDocumentInput{
		KnowledgeID: "ds-1",
		DocumentID:  "doc-1",
	})
	if code, ok := contract.CodeOf(err); !ok || code != contract.Unsupported {
		t.Fatalf("code = %v, %v; want UNSUPPORTED", code, ok)
	}
}

func TestNewCreatesKnowledgeFacadeWhenSearchProvided(t *testing.T) {
	rt, err := New(Dependencies{KnowledgeSearch: stubKnowledgeSearchPort{}})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if rt.Knowledge == nil {
		t.Fatalf("Knowledge facade is nil")
	}
	got, err := rt.Knowledge.Search(context.Background(), contract.CallContext{UserID: "user"}, knowledge.SearchInput{
		Query:        "q",
		KnowledgeIDs: []string{"ds-1"},
	})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(got.Hits) != 1 || got.Hits[0].DocumentID != "doc-1" {
		t.Fatalf("Search = %#v", got)
	}
}

func TestRuntimeCatalogOnlySearchUnsupported(t *testing.T) {
	rt, err := New(Dependencies{KnowledgeCatalog: stubKnowledgeCatalogPort{}})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = rt.Knowledge.Search(context.Background(), contract.CallContext{UserID: "user"}, knowledge.SearchInput{
		Query:        "q",
		KnowledgeIDs: []string{"ds-1"},
	})
	if code, ok := contract.CodeOf(err); !ok || code != contract.Unsupported {
		t.Fatalf("code = %v, %v; want UNSUPPORTED", code, ok)
	}
}

func TestNewAllowsNilSkillPort(t *testing.T) {
	rt, err := New(Dependencies{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if rt == nil {
		t.Fatalf("Runtime is nil")
	}
	if rt.Skill != nil {
		t.Fatalf("Skill facade = %#v, want nil", rt.Skill)
	}
	if rt.Knowledge != nil {
		t.Fatalf("Knowledge facade = %#v, want nil", rt.Knowledge)
	}
}

func TestRuntimeDoesNotContainRequestState(t *testing.T) {
	typ := reflect.TypeOf(Runtime{})
	for _, name := range []string{"UserID", "UserName", "RequestID", "PageRequest"} {
		if _, ok := typ.FieldByName(name); ok {
			t.Fatalf("Runtime contains request field %s", name)
		}
	}
	if typ.NumField() != 2 {
		t.Fatalf("Runtime field count = %d, want 2", typ.NumField())
	}
}

func TestNewKeepsSkillWiringWithKnowledgeDocument(t *testing.T) {
	rt, err := New(Dependencies{SkillPort: stubSkillPort{}, KnowledgeDocument: stubKnowledgeDocumentPort{}})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if rt.Skill == nil || rt.Knowledge == nil {
		t.Fatalf("Skill=%#v Knowledge=%#v, want both wired", rt.Skill, rt.Knowledge)
	}
}

func TestNewKeepsSkillWiringWithKnowledgeSearch(t *testing.T) {
	rt, err := New(Dependencies{SkillPort: stubSkillPort{}, KnowledgeSearch: stubKnowledgeSearchPort{}})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if rt.Skill == nil || rt.Knowledge == nil {
		t.Fatalf("Skill=%#v Knowledge=%#v, want both wired", rt.Skill, rt.Knowledge)
	}
}

func TestNewWiresAllKnowledgePorts(t *testing.T) {
	rt, err := New(Dependencies{
		KnowledgeCatalog:  stubKnowledgeCatalogPort{},
		KnowledgeDocument: stubKnowledgeDocumentPort{},
		KnowledgeSearch:   stubKnowledgeSearchPort{},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if rt.Knowledge == nil {
		t.Fatalf("Knowledge facade is nil")
	}
	if _, err := rt.Knowledge.List(context.Background(), contract.CallContext{UserID: "user"}, knowledge.ListInput{}); err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if _, err := rt.Knowledge.GetDocument(context.Background(), contract.CallContext{UserID: "user"}, knowledge.GetDocumentInput{KnowledgeID: "ds-1", DocumentID: "doc-1"}); err != nil {
		t.Fatalf("GetDocument returned error: %v", err)
	}
	if _, err := rt.Knowledge.Search(context.Background(), contract.CallContext{UserID: "user"}, knowledge.SearchInput{Query: "q", KnowledgeIDs: []string{"ds-1"}}); err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
}
