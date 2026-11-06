package workflowtool

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/executor"
	"lazymind/core/workflow/graphengine"
)

func knowledgeFixture(t *testing.T, scope *executor.KnowledgeScope) *Service {
	t.Helper()
	service := academicToolFixture(t)
	uploadRoot := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", uploadRoot)
	previous := http.DefaultTransport
	http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "source-access/by-dataset") {
			return toolResponse(`{"items":[]}`), nil
		}
		return &http.Response{StatusCode: 404, Header: make(http.Header), Body: toolResponse(`{"message":"unavailable"}`).Body}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	var revision orm.WorkflowRevision
	if err := service.DB.First(&revision, "id = ?", "pinned-revision").Error; err != nil {
		t.Fatal(err)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	node := graph.Nodes["write"]
	node.ExternalExecution.Tools = []graphengine.ExternalTool{{Name: "knowledge_list_documents", Source: "package"}, {Name: "knowledge_read_document", Source: "package"}}
	graph.Nodes["write"] = node
	if err := service.DB.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(knowledgeBinding)))
	for _, row := range []any{
		&orm.WorkflowBlob{Hash: hash, Content: []byte(knowledgeBinding), Size: int64(len(knowledgeBinding))},
		&orm.WorkflowRevisionEntry{RevisionID: "pinned-revision", Path: knowledgeBindingPath, EntryType: "file", BlobHash: &hash},
		&orm.Dataset{ID: "selected", KbID: "selected", DisplayName: "Selected", ProcessingLevel: "stored", Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.Dataset{ID: "unselected", KbID: "unselected", DisplayName: "Unselected", ProcessingLevel: "stored", Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: "owner"}},
	} {
		if err := service.DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ id, dataset, name, content, mime string }{{"a", "selected", "Alpha report", "Actual selected evidence", "text/plain"}, {"b", "selected", "Beta report", "Second evidence", "text/plain"}, {"binary", "selected", "Scanned source", "%PDF-1.4", "application/pdf"}, {"foreign", "unselected", "Foreign report", "Must not read", "text/plain"}} {
		path := filepath.Join(uploadRoot, item.id+map[bool]string{true: ".pdf", false: ".txt"}[item.mime == "application/pdf"])
		if err := os.WriteFile(path, []byte(item.content), 0600); err != nil {
			t.Fatal(err)
		}
		ext, _ := json.Marshal(map[string]any{"stored_path": path, "original_filename": filepath.Base(path), "content_type": item.mime})
		row := orm.Document{ID: item.id, DatasetID: item.dataset, DisplayName: item.name, ResourceVersion: 1, Ext: ext, BaseModel: orm.BaseModel{CreateUserID: "owner"}}
		if err := service.DB.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	setKnowledgeScope(t, service, scope)
	return service
}

func setKnowledgeScope(t *testing.T, s *Service, scope *executor.KnowledgeScope) {
	t.Helper()
	payload := map[string]any{"operation": "execute", "metadata": map[string]string{"workflow_control_inputs_frozen": "true"}, "knowledge_scope": scope}
	if err := s.DB.Model(&orm.WorkflowOutbox{}).Where("attempt_id = ?", "attempt").Update("payload_json", academicJSON(payload)).Error; err != nil {
		t.Fatal(err)
	}
}

func knowledgeCall(t *testing.T, s *Service, id, name string, args map[string]any) Result {
	t.Helper()
	result, err := s.Call(t.Context(), "owner", "run", "attempt", CallInput{ExecutionHandle: "lease", ToolName: name, CommandID: id, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func knowledgeValue(t *testing.T, result Result) map[string]any {
	t.Helper()
	if result.Error != nil {
		t.Fatalf("tool error: %+v", result.Error)
	}
	raw, _ := json.Marshal(result.Result)
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestKnowledgeSelectedCatalogListAndReadWithoutModels(t *testing.T) {
	s := knowledgeFixture(t, &executor.KnowledgeScope{DatasetIDs: []string{"selected"}})
	catalog, err := s.List(t.Context(), "owner", "run", "attempt", "lease")
	if err != nil || len(catalog) != 2 || !catalog[0].Available || !catalog[1].Available {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	first := knowledgeValue(t, knowledgeCall(t, s, "list-1", "knowledge_list_documents", map[string]any{"page_size": 1, "keyword": "report"}))
	docs := first["documents"].([]any)
	if len(docs) != 1 || docs[0].(map[string]any)["document_id"] != "a" || first["next_page_token"] == "" {
		t.Fatalf("first page: %+v", first)
	}
	second := knowledgeValue(t, knowledgeCall(t, s, "list-2", "knowledge_list_documents", map[string]any{"page_size": 1, "keyword": "report", "page_token": first["next_page_token"]}))
	if second["documents"].([]any)[0].(map[string]any)["document_id"] != "b" || second["next_page_token"] != "" {
		t.Fatalf("second page: %+v", second)
	}
	read := knowledgeValue(t, knowledgeCall(t, s, "read", "knowledge_read_document", map[string]any{"document_id": "a"}))
	if read["content"].(map[string]any)["text"] != "Actual selected evidence" || read["resource_version"] != float64(1) {
		t.Fatalf("read: %+v", read)
	}
	raw, _ := json.Marshal(read)
	if strings.Contains(string(raw), "stored_path") || strings.Contains(string(raw), os.TempDir()) {
		t.Fatal("server path leaked")
	}
	denied := knowledgeCall(t, s, "foreign", "knowledge_read_document", map[string]any{"document_id": "foreign"})
	if denied.Error == nil || denied.Error.Code != "KNOWLEDGE_SCOPE_DENIED" {
		t.Fatalf("foreign read: %+v", denied)
	}
}

func TestKnowledgeNoSelectionNeverFallsBackToConversation(t *testing.T) {
	s := knowledgeFixture(t, nil)
	if err := s.DB.Model(&orm.Conversation{}).Where("id = ?", "chat").Update("search_config", json.RawMessage(`{"dataset_ids":["unselected"]}`)).Error; err != nil {
		t.Fatal(err)
	}
	value := knowledgeValue(t, knowledgeCall(t, s, "empty", "knowledge_list_documents", nil))
	if value["status"] != "no_selected_knowledge" || len(value["documents"].([]any)) != 0 {
		t.Fatalf("live selection leaked: %+v", value)
	}
	for i, args := range []map[string]any{{"dataset_ids": []string{"unselected"}}, {"page_size": 0}, {"page_size": 1.5}, {"page_size": 101}} {
		result := knowledgeCall(t, s, fmt.Sprintf("bad-%d", i), "knowledge_list_documents", args)
		if result.Error == nil || result.Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("invalid schema accepted: %+v", result)
		}
	}
}

func TestKnowledgeSingleDocumentDoesNotGrantItsEntireDataset(t *testing.T) {
	s := knowledgeFixture(t, &executor.KnowledgeScope{Documents: []executor.KnowledgeDocumentRef{{DatasetID: "selected", DocumentID: "a", ResourceVersion: 1}}})
	listed := knowledgeValue(t, knowledgeCall(t, s, "list", "knowledge_list_documents", nil))["documents"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["document_id"] != "a" {
		t.Fatalf("expanded document scope: %+v", listed)
	}
	result := knowledgeCall(t, s, "other", "knowledge_read_document", map[string]any{"document_id": "b"})
	if result.Error == nil || result.Error.Code != "KNOWLEDGE_SCOPE_DENIED" {
		t.Fatal("read sibling document")
	}
	if err := s.DB.Model(&orm.Document{}).Where("id = ?", "a").Update("resource_version", 2).Error; err != nil {
		t.Fatal(err)
	}
	result = knowledgeCall(t, s, "version", "knowledge_read_document", map[string]any{"document_id": "a"})
	if result.Error == nil || result.Error.Code != "SOURCE_VERSION_CHANGED" {
		t.Fatalf("changed source accepted: %+v", result)
	}
	if err := s.DB.Model(&orm.Document{}).Where("id = ?", "a").Update("dataset_id", "unselected").Error; err != nil {
		t.Fatal(err)
	}
	result = knowledgeCall(t, s, "move", "knowledge_read_document", map[string]any{"document_id": "a"})
	if result.Error == nil || result.Error.Code != "KNOWLEDGE_SCOPE_DENIED" {
		t.Fatal("moved source gained access")
	}
}

func TestKnowledgeDoesNotStartParsingAndRechecksACL(t *testing.T) {
	s := knowledgeFixture(t, &executor.KnowledgeScope{DatasetIDs: []string{"selected"}})
	var before, after int64
	if err := s.DB.Model(&orm.AsyncJob{}).Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	result := knowledgeCall(t, s, "binary", "knowledge_read_document", map[string]any{"document_id": "binary"})
	if result.Error == nil || result.Error.Code != "DOCUMENT_CONTENT_UNAVAILABLE" {
		t.Fatalf("missing text claimed readable: %+v", result)
	}
	if err := s.DB.Model(&orm.AsyncJob{}).Count(&after).Error; err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("read enqueued parsing/model work")
	}
	if err := s.DB.Model(&orm.Dataset{}).Where("id = ?", "selected").Update("create_user_id", "other-owner").Error; err != nil {
		t.Fatal(err)
	}
	result = knowledgeCall(t, s, "acl", "knowledge_read_document", map[string]any{"document_id": "a"})
	if result.Error == nil {
		t.Fatal("revoked dataset ACL ignored")
	}
}

func TestKnowledgePinnedBindingAndPaginationScope(t *testing.T) {
	s := knowledgeFixture(t, &executor.KnowledgeScope{DatasetIDs: []string{"selected"}})
	first := knowledgeValue(t, knowledgeCall(t, s, "list", "knowledge_list_documents", map[string]any{"page_size": 1}))
	setKnowledgeScope(t, s, &executor.KnowledgeScope{DatasetIDs: []string{"unselected"}})
	result := knowledgeCall(t, s, "cursor", "knowledge_list_documents", map[string]any{"page_token": first["next_page_token"]})
	if result.Error == nil || result.Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("cross scope page token accepted: %+v", result)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(knowledgeBinding)))
	if err := s.DB.Model(&orm.WorkflowBlob{}).Where("hash = ?", hash).Update("content", []byte("modified code")).Error; err != nil {
		t.Fatal(err)
	}
	result = knowledgeCall(t, s, "binding", "knowledge_list_documents", nil)
	if result.Error == nil || result.Error.Code != "TOOL_NOT_ALLOWED" {
		t.Fatalf("modified adapter accepted: %+v", result)
	}
}
