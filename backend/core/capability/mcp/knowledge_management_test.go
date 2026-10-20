package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lazymind/core/capability"
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
	result, e := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "knowledge.create", Arguments: map[string]any{"name": "A"}})
	if e != nil {
		t.Fatal(e)
	}
	if !result.IsError || writes.calls != 0 {
		t.Fatal("dispatch bypassed runtime guard")
	}
	allowed = true
	params := &mcp.CallToolParams{Name: "knowledge.create", Arguments: map[string]any{"name": "A"}}
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
}

func TestPublicKnowledgeRegistryAndSourceUnion(t *testing.T) {
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
