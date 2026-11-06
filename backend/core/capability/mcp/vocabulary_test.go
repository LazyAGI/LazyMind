package mcpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lazymind/core/capability"
)

func TestVocabularyEndpointOnlyPublishesQueriesAndPreservesCaller(t *testing.T) {
	ports := &mcpFakePorts{}
	service, err := capability.NewService(capability.Dependencies{
		Skills: ports, Knowledge: ports, Documents: ports, Search: ports, Cloud: ports, Vocabulary: ports,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(service, HandlerConfig{VocabularyOnly: true,
		Verifier: func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
			return &auth.TokenInfo{UserID: token, Scopes: []string{capability.RequiredPermission}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, user := range []string{"alice", "bob"} {
		t.Run(user, func(t *testing.T) {
			client := mcp.NewClient(&mcp.Implementation{Name: "vocabulary-test", Version: "1"}, nil)
			session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
				Endpoint: server.URL, DisableStandaloneSSE: true,
				HTTPClient: &http.Client{Transport: bearerTransport{base: http.DefaultTransport, token: user}},
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			listed, err := session.ListTools(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			names := make([]string, 0, len(listed.Tools))
			for _, tool := range listed.Tools {
				names = append(names, tool.Name)
				if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
					t.Fatalf("not a read-only query: %s", tool.Name)
				}
			}
			slices.Sort(names)
			if !slices.Equal(names, []string{"vocabulary.word.list", "vocabulary.wordbook.list"}) {
				t.Fatalf("unexpected tools: %v", names)
			}
			for _, name := range ChatVocabularyTools() {
				result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
				if err != nil || result.IsError || ports.call.Principal.UserID != user {
					t.Fatalf("query=%s user=%s invocation=%+v result=%+v error=%v", name, user, ports.call, result, err)
				}
			}
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "knowledge.create", Arguments: map[string]any{"name": "forbidden"}})
			if err == nil && !result.IsError {
				t.Fatal("vocabulary endpoint dispatched a knowledge mutation")
			}
		})
	}
}
