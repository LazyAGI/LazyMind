package mcpadapter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lazymind/core/capability"
)

type mcpFakePorts struct{ call capability.InvocationContext }

func (f *mcpFakePorts) ListCloudDocuments(_ context.Context, call capability.InvocationContext, _ capability.CloudDocumentListQuery) (capability.CloudDocumentListPage, error) {
	f.call = call
	return capability.CloudDocumentListPage{}, nil
}
func (f *mcpFakePorts) GetCloudDocument(_ context.Context, call capability.InvocationContext, _ capability.GetCloudDocumentInput) (capability.GetCloudDocumentResult, error) {
	f.call = call
	return capability.GetCloudDocumentResult{}, nil
}
func (f *mcpFakePorts) SearchCloudDocuments(_ context.Context, call capability.InvocationContext, _ capability.SearchCloudDocumentsInput) (capability.SearchCloudDocumentsResult, error) {
	f.call = call
	return capability.SearchCloudDocumentsResult{}, nil
}

func (f *mcpFakePorts) ListSkills(context.Context, capability.InvocationContext, capability.SkillListQuery) (capability.SkillListPage, error) {
	return capability.SkillListPage{Items: []capability.SkillSummary{}, Total: 0}, nil
}
func (f *mcpFakePorts) GetSkillMetadata(context.Context, capability.InvocationContext, string) (capability.SkillMetadata, error) {
	return capability.SkillMetadata{Published: true, Summary: capability.SkillSummary{ID: "skill", HeadRevisionID: "rev"}}, nil
}
func (f *mcpFakePorts) ReadSkillContent(context.Context, capability.InvocationContext, string, string) (capability.SkillContent, error) {
	return capability.SkillContent{RevisionID: "rev", Text: "content"}, nil
}
func (f *mcpFakePorts) ListKnowledge(context.Context, capability.InvocationContext, capability.KnowledgeListQuery) (capability.KnowledgeListPage, error) {
	return capability.KnowledgeListPage{Items: []capability.KnowledgeSummary{}, Total: 0}, nil
}
func (f *mcpFakePorts) ListKnowledgeDocuments(context.Context, capability.InvocationContext, capability.KnowledgeDocumentListQuery) (capability.KnowledgeDocumentListPage, error) {
	return capability.KnowledgeDocumentListPage{Items: []capability.KnowledgeDocumentSummary{}, Total: 0}, nil
}
func (f *mcpFakePorts) GetKnowledgeDocument(context.Context, capability.InvocationContext, capability.GetKnowledgeDocumentInput) (capability.GetKnowledgeDocumentResult, error) {
	return capability.GetKnowledgeDocumentResult{Document: capability.KnowledgeDocumentDetail{KnowledgeDocumentSummary: capability.KnowledgeDocumentSummary{ID: "doc", KnowledgeID: "kb"}}}, nil
}
func (f *mcpFakePorts) SearchKnowledge(_ context.Context, call capability.InvocationContext, _ capability.SearchKnowledgeInput) (capability.SearchKnowledgeResult, error) {
	f.call = call
	return capability.SearchKnowledgeResult{Hits: []capability.KnowledgeSearchHit{{KnowledgeID: "kb", DocumentID: "doc", Text: "source"}}}, nil
}

func TestStreamableHTTPPublishesAuthenticatedReadOnlyTools(t *testing.T) {
	ports := &mcpFakePorts{}
	service, err := capability.NewService(capability.Dependencies{Skills: ports, Knowledge: ports, Documents: ports, Search: ports, Cloud: ports})
	if err != nil {
		t.Fatal(err)
	}
	verifier := func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		if token != "valid-token" {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{
			UserID: "verified-user", Scopes: []string{capability.RequiredPermission},
			Extra: map[string]any{extraTenantID: "verified-tenant"},
		}, nil
	}
	handler, err := NewHandler(service, HandlerConfig{Verifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	httpClient := &http.Client{Transport: bearerTransport{base: http.DefaultTransport, token: "valid-token"}}
	client := mcp.NewClient(&mcp.Implementation{Name: "integration-client", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: server.URL, HTTPClient: httpClient, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Fatalf("tool %q is not marked non-destructive", tool.Name)
		}
		if !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint {
			t.Fatalf("tool %q annotations = %#v, want read-only and idempotent", tool.Name, tool.Annotations)
		}
	}
	sort.Strings(names)
	if got, want := strings.Join(names, ","), "cloud_document.get,cloud_document.list,cloud_document.search,knowledge.document.get,knowledge.document.list,knowledge.list,knowledge.search,skill.get,skill.list"; got != want {
		t.Fatalf("tool names = %q, want %q", got, want)
	}

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "knowledge.search", Arguments: map[string]any{"query": "q", "knowledge_ids": []string{"kb"}},
	})
	if err != nil || result.IsError {
		t.Fatalf("CallTool result=%#v err=%v", result, err)
	}
	if ports.call.Principal.UserID != "verified-user" || ports.call.Principal.TenantID != "verified-tenant" {
		t.Fatalf("verified invocation = %#v", ports.call)
	}

	request, _ := http.NewRequest(http.MethodPost, server.URL, strings.NewReader("{}"))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("unauthenticated status=%d body=%s", response.StatusCode, body)
	}
}

func TestAuthServiceVerifierUsesBearerAndValidatedClaims(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/authservice/auth/validate" || r.Header.Get("Authorization") != "Bearer user-token" {
			t.Fatalf("validation request path=%s authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"success","data":{"sub":"user-9","role":"user","tenant_id":"tenant-9","permissions":["qa.read","qa.read"]}}`))
	}))
	defer authServer.Close()
	verifier, err := NewAuthServiceVerifier(AuthServiceVerifierConfig{BaseURL: authServer.URL + "/api/authservice", HTTPClient: authServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	info, err := verifier(context.Background(), "user-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.UserID != "user-9" || strings.Join(info.Scopes, ",") != "qa.read" || info.Extra[extraTenantID] != "tenant-9" {
		t.Fatalf("token info = %#v", info)
	}
}

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	clone.Header.Set("X-User-Id", "spoofed-user")
	clone.Header.Set("X-Tenant-Id", "spoofed-tenant")
	return t.base.RoundTrip(clone)
}
