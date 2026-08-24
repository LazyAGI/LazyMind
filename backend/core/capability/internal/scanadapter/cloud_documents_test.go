package scanadapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/core/capability"
)

func cloudCall() capability.InvocationContext {
	return capability.InvocationContext{Principal: capability.Principal{
		UserID: "user", TenantID: "tenant",
		Permissions: capability.NewPermissionSet(capability.RequiredPermission),
	}}
}

func TestCloudReaderUsesAuthorizedAccountAndOnlineConnector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-ID") != "user" || r.Header.Get("X-Tenant-ID") != "tenant" {
			t.Fatalf("identity headers missing: %#v", r.Header)
		}
		switch r.URL.Path {
		case "/api/authservice/v1/cloud/connections/internal/chat-enabled":
			if r.Header.Get("X-LazyMind-Internal-Token") != "internal" || r.URL.Query().Get("owner_user_id") != "user" || r.URL.Query().Get("provider") != "feishu" {
				t.Fatalf("invalid auth request: %s %#v", r.URL.String(), r.Header)
			}
			_, _ = io.WriteString(w, `{"data":{"items":[{"connection_id":"connection-1","provider":"feishu","display_name":"Feishu account","status":"ACTIVE"}]}}`)
		case "/api/scan/binding-targets/tree/children":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["auth_connection_id"] != "connection-1" || body["include_files"] != true || body["cursor"] != "provider-next" {
				t.Fatalf("invalid online list body: %#v", body)
			}
			_, _ = io.WriteString(w, `{"items":[{"key":"doc-1","node_ref":"wiki:space:node","display_name":"Handbook","target_type":"wiki_node","target_ref":"wiki:space:node","object_key":"doc-1","is_document":true,"is_container":true,"provider_meta":{"file_type":"docx"}}],"next_cursor":"next","has_more":true}`)
		case "/api/scan/binding-targets/tree/search":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["auth_connection_id"] != "connection-1" || body["direct"] != true || body["keyword"] != "handbook" {
				t.Fatalf("invalid online search body: %#v", body)
			}
			_, _ = io.WriteString(w, `{"items":[{"key":"doc-1","node_ref":"wiki:space:node","display_name":"Handbook","is_document":true,"is_container":true}],"has_more":false}`)
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
	}))
	defer server.Close()

	reader, err := NewCloudDocumentReader(server.URL, server.URL+"/api/authservice", "internal", 0)
	if err != nil {
		t.Fatal(err)
	}
	list, err := reader.ListCloudDocuments(context.Background(), cloudCall(), capability.CloudDocumentListQuery{Limit: 20})
	if err != nil || len(list.Items) != 1 || list.Items[0].ID != "connection-1" || list.Items[0].Provider != "feishu" {
		t.Fatalf("list=%#v err=%v", list, err)
	}
	got, err := reader.GetCloudDocument(context.Background(), cloudCall(), capability.GetCloudDocumentInput{
		SourceID: "connection-1", IncludeDocuments: true, ProviderCursor: "provider-next",
		DocumentsPage: capability.PageRequest{PageSize: 20},
	})
	if err != nil || len(got.Documents) != 1 || got.Documents[0].NodeRef == "" || got.DocumentsPage.ProviderCursor != "next" {
		t.Fatalf("get=%#v err=%v", got, err)
	}
	search, err := reader.SearchCloudDocuments(context.Background(), cloudCall(), capability.SearchCloudDocumentsInput{
		SourceID: "connection-1", Query: "handbook", Page: capability.PageRequest{PageSize: 20},
	})
	if err != nil || len(search.Hits) != 1 || search.Page.ProviderCursor != "" {
		t.Fatalf("search=%#v err=%v", search, err)
	}
}

func TestCloudReaderRejectsConnectionOutsideChatEnabledOwnerScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"items":[]}}`)
	}))
	defer server.Close()
	reader, err := NewCloudDocumentReader(server.URL, server.URL, "internal", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reader.GetCloudDocument(context.Background(), cloudCall(), capability.GetCloudDocumentInput{SourceID: "other"})
	if code, ok := capability.CodeOf(err); !ok || code != capability.NotFound {
		t.Fatalf("error=%v", err)
	}
}
