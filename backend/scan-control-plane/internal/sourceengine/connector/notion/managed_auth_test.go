package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector/feishu"
)

const (
	testNotionPageID     = "11111111111111111111111111111111"
	testNotionDatabaseID = "22222222222222222222222222222222"
	testNotionChildID    = "33333333333333333333333333333333"
	testNotionRowID      = "44444444444444444444444444444444"
)

type recordingNotionTokenResolver struct {
	requests []feishu.TokenRequest
	token    feishu.Token
	err      error
}

func (resolver *recordingNotionTokenResolver) GetToken(_ context.Context, request feishu.TokenRequest) (feishu.Token, error) {
	resolver.requests = append(resolver.requests, request)
	return resolver.token, resolver.err
}

func TestManagedNotionTokenContextByOperation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		requiredCapability string
		invoke             func(*NotionConnector) error
	}{
		{
			name:               "list uses browse",
			requiredCapability: "datasource.browse",
			invoke: func(connectorUnderTest *NotionConnector) error {
				_, err := connectorUnderTest.ListChildren(context.Background(), connector.ListChildrenRequest{
					TargetType:       TargetTypePage,
					TargetRef:        testNotionPageID,
					NodeRef:          testNotionPageID,
					AuthConnectionID: "notion-connection",
					PageSize:         10,
					ProviderOptions: connector.ProviderOptions{
						"user_id": "user-owner", "source_id": "source-real", "binding_id": "binding-real",
					},
				})
				return err
			},
		},
		{
			name:               "search uses browse",
			requiredCapability: "datasource.browse",
			invoke: func(connectorUnderTest *NotionConnector) error {
				_, err := connectorUnderTest.Search(context.Background(), connector.SearchRequest{
					Keyword:          "release notes",
					AuthConnectionID: "notion-connection",
					PageSize:         10,
					ProviderOptions: connector.ProviderOptions{
						"user_id": "user-owner", "source_id": "source-real", "binding_id": "binding-real",
					},
				})
				return err
			},
		},
		{
			name:               "fetch uses read",
			requiredCapability: "datasource.read",
			invoke: func(connectorUnderTest *NotionConnector) error {
				_, err := connectorUnderTest.FetchPage(context.Background(), connector.FetchPageRequest{
					SourceID:          "source-real",
					BindingID:         "binding-real",
					BindingGeneration: 1,
					TargetType:        TargetTypePage,
					TargetRef:         testNotionPageID,
					ScopeType:         connector.ScopeTypeFull,
					PageSize:          10,
					AuthConnectionID:  "notion-connection",
					ProviderOptions:   connector.ProviderOptions{"user_id": "user-owner"},
				})
				return err
			},
		},
		{
			name:               "export uses parse",
			requiredCapability: "datasource.parse",
			invoke: func(connectorUnderTest *NotionConnector) error {
				_, err := connectorUnderTest.ExportObject(context.Background(), connector.ExportObjectRequest{
					SourceID:        "source-real",
					BindingID:       "binding-real",
					ObjectKey:       testNotionPageID,
					SourceVersion:   "revision-1",
					ExportFormat:    connector.ExportFormatMarkdown,
					ProviderOptions: connector.ProviderOptions{"user_id": "user-owner"},
					ProviderMeta: connector.ProviderMeta{
						"auth_connection_id": "notion-connection", "kind": string(ObjectKindPage), "id": testNotionPageID,
					},
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stop := connector.NewError(connector.ErrorCodeTransient, "stop after token request")
			resolver := &recordingNotionTokenResolver{err: stop}
			connectorUnderTest := NewNotionConnector(resolver, nil)

			if err := test.invoke(connectorUnderTest); err != stop {
				t.Fatalf("operation error = %v, want resolver sentinel", err)
			}
			if len(resolver.requests) != 1 {
				t.Fatalf("token requests = %d, want 1", len(resolver.requests))
			}
			assertNotionTokenContext(t, resolver.requests[0], test.requiredCapability)
		})
	}
}

func TestManagedNotionRejectsFeishuConnection(t *testing.T) {
	t.Parallel()

	resolver := &recordingNotionTokenResolver{token: feishu.Token{
		AccessToken: "fake-feishu-access", Provider: "feishu", Status: "ACTIVE",
	}}
	connectorUnderTest := NewNotionConnector(resolver, nil)
	if _, err := connectorUnderTest.loadToken(context.Background(), "feishu-connection", "user-owner"); err == nil {
		t.Fatal("Notion connector accepted a Feishu Provider token")
	}
}

func TestManagedNotionPreBindingBrowseUsesExplicitContextMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		invoke func(*NotionConnector) error
	}{
		{
			name: "list",
			invoke: func(connectorUnderTest *NotionConnector) error {
				_, err := connectorUnderTest.ListChildren(context.Background(), connector.ListChildrenRequest{
					TargetType: TargetTypePage, TargetRef: testNotionPageID, NodeRef: testNotionPageID,
					AuthConnectionID: "notion-connection", PageSize: 10,
					ProviderOptions: connector.ProviderOptions{"user_id": "user-owner", "tenant_id": "tenant-owner"},
				})
				return err
			},
		},
		{
			name: "search",
			invoke: func(connectorUnderTest *NotionConnector) error {
				_, err := connectorUnderTest.Search(context.Background(), connector.SearchRequest{
					Keyword: "release", AuthConnectionID: "notion-connection", PageSize: 10,
					ProviderOptions: connector.ProviderOptions{"user_id": "user-owner", "tenant_id": "tenant-owner"},
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stop := connector.NewError(connector.ErrorCodeTransient, "stop after token request")
			resolver := &recordingNotionTokenResolver{err: stop}
			if err := test.invoke(NewNotionConnector(resolver, nil)); err != stop {
				t.Fatalf("operation error = %v, want resolver sentinel", err)
			}
			if len(resolver.requests) != 1 {
				t.Fatalf("token requests = %d, want 1", len(resolver.requests))
			}
			request := resolver.requests[0]
			if request.UserID != "user-owner" || request.TenantID != "tenant-owner" || request.SourceID != "" || request.BindingID != "" ||
				request.Consumer != "datasource" || request.RequiredCapability != "datasource.browse" {
				t.Fatalf("pre-binding token context = %+v", request)
			}
			if mode := reflectedTokenContextMode(t, request); mode != "pre_binding_browse" {
				t.Fatalf("context mode = %q, want pre_binding_browse", mode)
			}
		})
	}
}

func TestManagedNotionPropagatesNeedsReauth(t *testing.T) {
	t.Parallel()

	wantErr := connector.NewError(ErrorCodeAuthInvalid, "NEEDS_REAUTH")
	resolver := &recordingNotionTokenResolver{err: wantErr}
	connectorUnderTest := NewNotionConnector(resolver, nil)
	_, err := connectorUnderTest.Search(context.Background(), connector.SearchRequest{
		Keyword: "page", AuthConnectionID: "notion-connection", PageSize: 10,
		ProviderOptions: connector.ProviderOptions{"user_id": "user-owner"},
	})
	if err != wantErr {
		t.Fatalf("search error = %v, want NEEDS_REAUTH resolver error", err)
	}
}

func TestNotionClientSearchPageDatabaseBlockAndMarkdown(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer fake-notion-access" {
			t.Fatalf("missing Notion bearer token on %s", request.URL.Path)
		}
		if request.Header.Get("Notion-Version") != notionVersion {
			t.Fatalf("Notion-Version = %q, want %q", request.Header.Get("Notion-Version"), notionVersion)
		}
		switch request.Method + " " + request.URL.Path {
		case http.MethodPost + " /search":
			writeNotionTestJSON(t, response, map[string]any{"results": []any{notionTestPage(testNotionPageID, "Release Notes"), notionTestDatabase(testNotionDatabaseID, "Roadmap")}})
		case http.MethodGet + " /pages/" + testNotionPageID:
			writeNotionTestJSON(t, response, notionTestPage(testNotionPageID, "Release Notes"))
		case http.MethodGet + " /databases/" + testNotionDatabaseID:
			writeNotionTestJSON(t, response, notionTestDatabase(testNotionDatabaseID, "Roadmap"))
		case http.MethodGet + " /blocks/" + testNotionPageID + "/children":
			writeNotionTestJSON(t, response, notionTestBlocks("Page body", testNotionChildID))
		case http.MethodPost + " /databases/" + testNotionDatabaseID + "/query":
			writeNotionTestJSON(t, response, map[string]any{"results": []any{notionTestPage(testNotionRowID, "Roadmap row")}})
		case http.MethodGet + " /pages/" + testNotionRowID:
			writeNotionTestJSON(t, response, notionTestPage(testNotionRowID, "Roadmap row"))
		case http.MethodGet + " /blocks/" + testNotionRowID + "/children":
			writeNotionTestJSON(t, response, notionTestBlocks("Row body", ""))
		default:
			t.Fatalf("unexpected Notion request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	ctx := context.Background()
	search, err := client.Search(ctx, "fake-notion-access", "release", "", 10)
	if err != nil || len(search.Items) != 2 || search.Items[0].Kind != ObjectKindPage || search.Items[1].Kind != ObjectKindDatabase {
		t.Fatalf("search = %+v, err = %v", search, err)
	}
	page, err := client.GetPage(ctx, "fake-notion-access", testNotionPageID)
	if err != nil || page.Name != "Release Notes" {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
	database, err := client.GetDatabase(ctx, "fake-notion-access", testNotionDatabaseID)
	if err != nil || database.Name != "Roadmap" {
		t.Fatalf("database = %+v, err = %v", database, err)
	}
	children, err := client.ListBlockChildren(ctx, "fake-notion-access", testNotionPageID, "", 10)
	if err != nil || len(children.Items) != 1 || children.Items[0].ID != testNotionChildID {
		t.Fatalf("block children = %+v, err = %v", children, err)
	}
	pageMarkdown, err := client.PageToMarkdown(ctx, "fake-notion-access", testNotionPageID)
	if err != nil || !strings.Contains(pageMarkdown, "# Release Notes") || !strings.Contains(pageMarkdown, "Page body") {
		t.Fatalf("page markdown = %q, err = %v", pageMarkdown, err)
	}
	databaseMarkdown, err := client.DatabaseToMarkdown(ctx, "fake-notion-access", testNotionDatabaseID)
	if err != nil || !strings.Contains(databaseMarkdown, "# Roadmap") || !strings.Contains(databaseMarkdown, "Roadmap row") || !strings.Contains(databaseMarkdown, "Row body") {
		t.Fatalf("database markdown = %q, err = %v", databaseMarkdown, err)
	}
}

func assertNotionTokenContext(t *testing.T, request feishu.TokenRequest, requiredCapability string) {
	t.Helper()
	if request.AuthConnectionID != "notion-connection" || request.UserID != "user-owner" ||
		request.SourceID != "source-real" || request.BindingID != "binding-real" ||
		request.Consumer != "datasource" || request.RequiredCapability != requiredCapability {
		t.Fatalf("token context = %+v, want owner/source/binding datasource context with capability %s", request, requiredCapability)
	}
}

func reflectedTokenContextMode(t *testing.T, request feishu.TokenRequest) string {
	t.Helper()
	field := reflect.ValueOf(request).FieldByName("ContextMode")
	if !field.IsValid() || field.Kind() != reflect.String {
		t.Fatal("TokenRequest must declare an explicit ContextMode")
	}
	return field.String()
}

func notionTestPage(id, title string) map[string]any {
	return map[string]any{
		"object": "page", "id": id, "last_edited_time": "2026-09-02T08:00:00.000Z",
		"properties": map[string]any{"Name": map[string]any{"type": "title", "title": []any{map[string]any{"plain_text": title}}}},
	}
}

func notionTestDatabase(id, title string) map[string]any {
	return map[string]any{
		"object": "database", "id": id, "last_edited_time": "2026-09-02T08:00:00.000Z",
		"title": []any{map[string]any{"plain_text": title}},
	}
}

func notionTestBlocks(paragraph, childID string) map[string]any {
	results := []any{map[string]any{
		"id": "55555555555555555555555555555555", "type": "paragraph",
		"paragraph": map[string]any{"rich_text": []any{map[string]any{"plain_text": paragraph}}},
	}}
	if childID != "" {
		results = append(results, map[string]any{
			"id": childID, "type": "child_page", "child_page": map[string]any{"title": "Child page"},
		})
	}
	return map[string]any{"results": results, "has_more": false, "next_cursor": ""}
}

func writeNotionTestJSON(t *testing.T, response http.ResponseWriter, payload any) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(payload); err != nil {
		t.Fatalf("write Notion test response: %v", err)
	}
}

func TestNotionClientClassifiesUnauthorizedWithoutLeakingProviderBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusUnauthorized)
		_, _ = response.Write([]byte(`{"code":"unauthorized","message":"provider-sensitive-detail"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	_, err := client.GetPage(context.Background(), "test-only-access", testNotionPageID)
	if err == nil {
		t.Fatal("Notion 401 was accepted")
	}
	code, ok := connector.ErrorCodeOf(err)
	if !ok || code != ErrorCodeAuthInvalid {
		t.Fatalf("Notion 401 error code = %q/%v, want %s", code, ok, ErrorCodeAuthInvalid)
	}
	if strings.Contains(err.Error(), "provider-sensitive-detail") {
		t.Fatal("Notion 401 error leaked the raw Provider response body")
	}
}
