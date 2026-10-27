package capability

import (
	"context"
	"encoding/json"
	"testing"
)

func TestManagementDocumentQueryPreservesRootAndScope(t *testing.T) {
	ports := &fakePorts{}
	svc := mustService(t, ports)
	var input ListKnowledgeDocumentsInput
	if err := json.Unmarshal([]byte(`{"knowledge_ids":["kb"],"parent_id":"","recursive":false,"resource_type":"all","match":"exact","name":"Paper"}`), &input); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ListKnowledgeDocuments(context.Background(), authorizedCall(), input)
	if err != nil {
		t.Fatal(err)
	}
	q := ports.documentListQuery
	if q.ParentID == nil || *q.ParentID != "" || q.Recursive || q.ResourceType != "all" || q.Match != "exact" || len(q.KnowledgeIDs) != 1 {
		t.Fatalf("lost query semantics: %+v", q)
	}
}
func TestManagementDocumentQueryRejectsAmbiguousScopes(t *testing.T) {
	svc := mustService(t, &fakePorts{})
	for _, raw := range []string{`{"knowledge_id":"a","knowledge_ids":["b"]}`, `{"knowledge_ids":[]}`, `{"knowledge_ids":["a","b"],"parent_id":""}`, `{"knowledge_id":"a","resource_type":"unknown"}`, `{"knowledge_id":"a","match":"wildcard"}`} {
		var input ListKnowledgeDocumentsInput
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			t.Fatal(err)
		}
		_, err := svc.ListKnowledgeDocuments(context.Background(), authorizedCall(), input)
		if code, _ := CodeOf(err); code != InvalidArgument {
			t.Fatalf("input %s: %v", raw, err)
		}
	}
}
