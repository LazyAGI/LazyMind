package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lazymind/core/capability"
	"lazymind/core/learning"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

type managementTestPort struct {
	capability.KnowledgeManager
	calls int
}

func (p *managementTestPort) CreateKnowledge(_ context.Context, c capability.InvocationContext, in capability.CreateKnowledgeRequest) (capability.ResourceOperationResult, error) {
	p.calls++
	return capability.ResourceOperationResult{Action: "knowledge.create", Status: "succeeded", Items: []capability.ResourceOperationItem{}}, nil
}
func TestManagementRegistryAndDispatchGuard(t *testing.T) {
	ports := &mcpFakePorts{}
	writes := &managementTestPort{}
	service, e := capability.NewService(capability.Dependencies{Skills: ports, Knowledge: ports, Documents: ports, Search: ports, Cloud: ports, Vocabulary: ports, Management: writes})
	if e != nil {
		t.Fatal(e)
	}
	allowed := false
	handler, e := NewHandler(service, HandlerConfig{ManagementOnly: true, Verifier: func(context.Context, string, *http.Request) (*auth.TokenInfo, error) {
		return &auth.TokenInfo{UserID: "u", Scopes: []string{"document.write"}}, nil
	}, AuthorizeCall: func(context.Context, *mcp.CallToolRequest) (capability.InvocationContext, error) {
		if !allowed {
			return capability.InvocationContext{}, errors.New("denied")
		}
		return capability.InvocationContext{Principal: capability.Principal{UserID: "u", Permissions: capability.NewPermissionSet("document.write")}}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	progress := make(chan *mcp.ProgressNotificationParams, 4)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) { progress <- req.Params }})
	session, e := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: bearerTransport{base: http.DefaultTransport, token: "token"}}, DisableStandaloneSSE: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	list, e := session.ListTools(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(list.Tools) != 9 {
		t.Fatalf("tools=%d", len(list.Tools))
	}
	for _, tool := range list.Tools {
		assertKnowledgeToolDocumentation(t, tool)
		if !slices.Contains(KnowledgeManagementTools, tool.Name) {
			t.Fatalf("unexpected %s", tool.Name)
		}
		b, _ := json.Marshal(tool.InputSchema)
		for _, forbidden := range []string{`"confirmed"`, `"user_id"`, `"operation_key"`, `"dispatch_token"`} {
			if strings.Contains(string(b), forbidden) {
				t.Fatalf("model can supply %s", forbidden)
			}
		}
	}
	result, e := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "knowledge.create", Arguments: map[string]any{"name": "A", "tags": []string{"test"}}})
	if e != nil {
		t.Fatal(e)
	}
	if !result.IsError || writes.calls != 0 {
		t.Fatal("dispatch bypassed runtime guard")
	}
	allowed = true
	params := &mcp.CallToolParams{Name: "knowledge.create", Arguments: map[string]any{"name": "A", "tags": []string{"test"}}}
	params.SetProgressToken("real-call-17")
	result, e = session.CallTool(context.Background(), params)
	if e != nil || result.IsError || writes.calls != 1 {
		t.Fatalf("%+v %v calls%d", result, e, writes.calls)
	}
	for _, expected := range []float64{0, 1} {
		select {
		case event := <-progress:
			if event.ProgressToken != "real-call-17" || event.Progress != expected {
				t.Fatalf("wrong progress binding: %+v", event)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("progress notification missing")
		}
	}
	removed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "knowledge.options", Arguments: map[string]any{}})
	if err == nil && (removed == nil || !removed.IsError) {
		t.Fatal("removed options tool is still callable")
	}
}

func TestPublicKnowledgeRegistryAndLocalPathContract(t *testing.T) {
	ports := &mcpFakePorts{}
	service, err := capability.NewService(capability.Dependencies{Skills: ports, Knowledge: ports, Documents: ports, Search: ports, Cloud: ports, Vocabulary: ports, Management: &managementTestPort{}})
	if err != nil {
		t.Fatal(err)
	}
	server := newServer(service)
	transportA, transportB := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), transportA, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "contract", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), transportB, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, tool := range tools.Tools {
		if slices.Contains(KnowledgeManagementTools, tool.Name) {
			assertKnowledgeToolDocumentation(t, tool)
		}
		if tool.Name == "knowledge.options" {
			t.Fatal("public registry exposes removed options tool")
		}
		if strings.HasPrefix(tool.Name, "knowledge.") {
			count++
		}
	}
	if count != 11 {
		t.Fatalf("public knowledge tools=%d", count)
	}
	schema, err := knowledgeInputSchema[capability.UploadKnowledgeDocumentInput]()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(map[string]any{"knowledge_base_id": "kb", "files": []any{map[string]any{"path": "/tmp/paper.pdf"}}}); err != nil {
		t.Fatalf("local path rejected: %v", err)
	}
	for _, source := range []map[string]any{
		{"kind": "remote_file", "url": "https://example.com/paper.pdf", "path": "/private/file"},
		{"kind": "work_artifact", "artifact_id": "artifact"},
		{"kind": "uploaded_file", "file_ref": ""},
	} {
		input := map[string]any{"knowledge_base_id": "kb", "files": []any{map[string]any{"source": source}}}
		if err := resolved.Validate(input); err == nil {
			t.Fatalf("ambiguous/unfixed source accepted: %+v", source)
		}
	}
}

// Inspect the actual wire schema, including manually built resource variants
// and nested capability settings, rather than only the Go field annotations.
func assertKnowledgeToolDocumentation(t *testing.T, tool *mcp.Tool) {
	t.Helper()
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if tool.Description == "" || strings.Contains(tool.Description+string(raw), "knowledge.options") {
		t.Fatalf("%s has missing/stale tool documentation", tool.Name)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	var visit func(any, string)
	visit = func(value any, path string) {
		switch node := value.(type) {
		case map[string]any:
			if properties, ok := node["properties"].(map[string]any); ok {
				for name, value := range properties {
					property := value.(map[string]any)
					description, _ := property["description"].(string)
					if strings.TrimSpace(description) == "" {
						t.Errorf("%s%s/%s has no parameter description", tool.Name, path, name)
					}
				}
			}
			for key, child := range node {
				visit(child, path+"/"+key)
			}
		case []any:
			for _, child := range node {
				visit(child, path)
			}
		}
	}
	visit(schema, "")
	if tool.Name == "knowledge.create" || tool.Name == "knowledge.resource.update_metadata" || tool.Name == "knowledge.list" {
		for _, profile := range learning.BuiltinProfiles() {
			if !strings.Contains(string(raw), profile.Key) {
				t.Errorf("%s omits scene %s", tool.Name, profile.Key)
			}
		}
	}
	if tool.Name == "knowledge.create" || tool.Name == "knowledge.resource.update_metadata" {
		for _, definition := range learning.Capabilities() {
			if !strings.Contains(string(raw), definition.Key) {
				t.Errorf("%s omits capability %s", tool.Name, definition.Key)
			}
		}
	}
}

func TestKnowledgeConfigurationDescriptionSemantics(t *testing.T) {
	create, err := knowledgeInputSchema[capability.CreateKnowledgeRequest]()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(create.Required, []string{"name", "tags"}) {
		t.Fatalf("creation required fields changed: %v", create.Required)
	}
	for field, fragments := range map[string][]string{
		"scene":            {"general", "custom", "paper_collection"},
		"processing_level": {"stored", "parsed", "chunked", "indexed", "parsing is disabled", "embedding models"},
		"algo_id":          {"general_algo", "deployment"},
		"capabilities":     {"nonempty", "replaces defaults"},
	} {
		for _, fragment := range fragments {
			if !strings.Contains(create.Properties[field].Description, fragment) {
				t.Errorf("%s description omits %q", field, fragment)
			}
		}
	}
	update, err := knowledgeInputSchema[capability.UpdateMetadataRequest]()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"Empty string is invalid", "Omit or null to keep", "replace (default", "recursive=true", "descendant documents only"} {
		if !strings.Contains(string(raw), fragment) {
			t.Errorf("update description omits %q", fragment)
		}
	}
}

func TestKnowledgeMetadataSchemaMatchesResourceKind(t *testing.T) {
	schema, err := knowledgeInputSchema[capability.UpdateMetadataRequest]()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind    string
		changes map[string]any
		valid   bool
	}{
		{"knowledge_base", map[string]any{"scene": "academic_papers"}, true},
		{"knowledge_base", map[string]any{"capabilities": []any{map[string]any{"key": "general_translation", "version": float64(1), "enabled": true, "display_order": float64(1)}}}, true},
		{"document", map[string]any{"scene": "academic_papers"}, false},
		{"folder", map[string]any{"processing_level": "indexed"}, false},
		{"folder", map[string]any{"tags": []any{"论文"}, "recursive": true}, true},
	} {
		ref := map[string]any{"kind": tc.kind, "knowledge_base_id": "kb"}
		if tc.kind != "knowledge_base" {
			ref[tc.kind+"_id"] = "resource"
		}
		err := resolved.Validate(map[string]any{"items": []any{map[string]any{"target": ref, "changes": tc.changes}}})
		if (err == nil) != tc.valid {
			t.Fatalf("%s %v: %v", tc.kind, tc.changes, err)
		}
	}
}

func TestKnowledgeMetadataDescriptionsStayKindSpecific(t *testing.T) {
	schema, err := knowledgeInputSchema[capability.UpdateMetadataRequest]()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	items := wire["properties"].(map[string]any)["items"].(map[string]any)["items"].(map[string]any)
	for _, value := range items["oneOf"].([]any) {
		properties := value.(map[string]any)["properties"].(map[string]any)
		target := properties["target"].(map[string]any)["properties"].(map[string]any)
		kind := target["kind"].(map[string]any)["enum"].([]any)[0].(string)
		changes := properties["changes"].(map[string]any)["properties"].(map[string]any)
		name := changes["name"].(map[string]any)["description"].(string)
		tags := changes["tags"].(map[string]any)["description"].(string)
		if kind == "knowledge_base" {
			if !strings.Contains(name, "100 characters") || !strings.Contains(tags, "20 characters") {
				t.Fatal("library limits missing from library descriptions")
			}
		} else if !strings.Contains(name, "512 bytes") || strings.Contains(tags, "20 characters") {
			t.Fatalf("%s inherited the library's name/tag rules", kind)
		}
		if strings.Contains(tags, "recursive=true") != (kind == "folder") {
			t.Fatalf("%s has incorrect folder-tag instructions", kind)
		}
	}
}
