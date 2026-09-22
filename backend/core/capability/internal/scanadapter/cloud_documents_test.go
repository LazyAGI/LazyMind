package scanadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"lazymind/core/capability"
)

func TestConnectionGuidanceTargetsOwnedAccount(t *testing.T) {
	t.Setenv("LAZYMIND_PUBLIC_BASE_URL", "https://lazy.example/team/api/core/")
	for _, provider := range []string{"feishu", "notion", "googledrive"} {
		err := connectionError(capability.PermissionDenied, "cloud_document.read", "AUTH_REQUIRED", "reauthorize", provider, "reauthorize", "account&other=1")
		link, parseErr := url.Parse(err.Action.URL)
		if parseErr != nil || link.Host != "lazy.example" || link.Path != "/team/cloud-documents" ||
			link.Query().Get("connection_id") != "account&other=1" || link.Query().Get("provider") != provider || link.Query().Get("action") != "reauthorize" {
			t.Fatalf("invalid guidance: %s, %v", err.Action.URL, parseErr)
		}
	}
	t.Setenv("LAZYMIND_PUBLIC_BASE_URL", "javascript:alert(1)")
	err := connectionError(capability.NotFound, "cloud_document.read", "CONNECTION_UNAVAILABLE", "connect", "", "connect", "")
	if err.Action.URL != "/cloud-documents" {
		t.Fatalf("unsafe fallback: %s", err.Action.URL)
	}
}

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
			if r.Header.Get("X-LazyMind-Internal-Token") != "internal" || r.URL.Query().Get("owner_user_id") != "user" || r.URL.Query().Get("provider") != "" {
				t.Fatalf("invalid auth request: %s %#v", r.URL.String(), r.Header)
			}
			_, _ = io.WriteString(w, `{"data":{"items":[{"connection_id":"connection-1","owner_user_id":"user","provider_options":{"chat_enabled":true},"provider":"feishu","display_name":"Feishu account","status":"ACTIVE"}]}}`)
		case "/api/authservice/v1/cloud/connections/internal/connection-1":
			if r.Header.Get("X-LazyMind-Internal-Token") != "internal" || r.URL.Query().Get("user_id") != "user" {
				t.Fatal("missing scoped internal detail request")
			}
			_, _ = io.WriteString(w, `{"data":{"connection_id":"connection-1","owner_user_id":"user","provider_options":{"chat_enabled":true},"provider":"feishu","display_name":"Feishu account","status":"ACTIVE"}}`)
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
		w.WriteHeader(http.StatusNotFound)
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

func TestGoogleDiscoveryPreservesEmptyPageCursorScopeAndReadLocator(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cloud/connections/internal/connection":
			_, _ = io.WriteString(w, `{"data":{"connection_id":"connection","provider":"googledrive","owner_user_id":"user","status":"ACTIVE","provider_options":{"chat_enabled":true}}}`)
		case "/v1/cloud/connections/connection/token":
			_, _ = io.WriteString(w, `{"data":{"connection_id":"connection","provider":"googledrive","status":"ACTIVE","access_token":"secret"}}`)
		case "/internal/documents:browse":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["folder_id"] != "folder-1" || body["page_size"] != float64(20) {
				t.Errorf("scope=%v", body)
			}
			if body["page_token"] == "" {
				_, _ = io.WriteString(w, `{"source_id":"connection","provider":"googledrive","items":[],"next_page_token":"continue"}`)
			} else {
				if body["page_token"] != "continue" {
					t.Error("cursor was changed")
				}
				_, _ = io.WriteString(w, `{"source_id":"connection","provider":"googledrive","items":[{"document_id":"doc","title":"Plan","type":"file","read_locator":"googledrive:/doc","parent_ids":["folder-1"],"mime_type":"text/plain"}]}`)
			}
		case "/internal/documents:search":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["query"] != "plan" || body["query_mode"] != "full_text" || body["drive_id"] != "drive-1" {
				t.Errorf("query=%v", body)
			}
			_, _ = io.WriteString(w, `{"source_id":"connection","provider":"googledrive","items":[],"incomplete":true,"warnings":["Google indexing coverage"],"next_page_token":"next"}`)
		default:
			t.Errorf("must not call Scan for Google: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	reader, _ := NewCloudDocumentReader(server.URL, server.URL, "internal", 0)
	_ = reader.SetRuntimeEndpoint(server.URL)
	in := capability.GetCloudDocumentInput{SourceID: "connection", IncludeDocuments: true, NodeRef: "folder-1", TargetType: "folder", DocumentsPage: capability.PageRequest{PageSize: 20}}
	first, err := reader.GetCloudDocument(context.Background(), cloudCall(), in)
	if err != nil || len(first.Documents) != 0 || first.DocumentsPage.ProviderCursor != "continue" {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	in.ProviderCursor = first.DocumentsPage.ProviderCursor
	second, err := reader.GetCloudDocument(context.Background(), cloudCall(), in)
	if err != nil || len(second.Documents) != 1 || second.Documents[0].ReadLocator != "googledrive:/doc" || second.Documents[0].ParentKey != "folder-1" {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	search, err := reader.SearchCloudDocuments(context.Background(), cloudCall(), capability.SearchCloudDocumentsInput{SourceID: "connection", Query: "plan", QueryMode: "full_text", TargetType: "drive", TargetRef: "drive-1", Page: capability.PageRequest{PageSize: 20}})
	if err != nil || !search.Incomplete || len(search.Warnings) != 1 || search.Page.ProviderCursor != "next" {
		t.Fatalf("search=%#v err=%v", search, err)
	}
}

func TestScanDocumentLocations(t *testing.T) {
	for _, tc := range []struct {
		provider string
		meta     map[string]any
		locator  string
	}{
		{"feishu", map[string]any{"kind": "wiki_node", "token": "node"}, "feishu:/~node/node"},
		{"feishu", map[string]any{"kind": "drive_file", "token": "doc", "file_type": "docx"}, "feishu:/~docx/doc"},
		{"feishu", map[string]any{"kind": "drive_file", "file_type": "shortcut", "shortcut_target_type": "docx", "shortcut_target_token": "target"}, "feishu:/~docx/target"},
		{"notion", map[string]any{"kind": "page", "id": "page", "url": "https://notion.so/page"}, "notion:/~page/page"},
		{"notion", map[string]any{"kind": "database", "id": "database"}, ""},
	} {
		item := treeNode{Key: "key", IsDocument: true, ProviderMeta: tc.meta}
		account := cloudAccount{ConnectionID: "connection", Provider: tc.provider}
		metadata, hit := documentMetadata(account, item), searchHit(account, item)
		if metadata.ReadLocator != tc.locator || hit.ReadLocator != tc.locator || hit.SourceID != "connection" {
			t.Fatalf("metadata=%#v hit=%#v", metadata, hit)
		}
		if strings.HasPrefix(tc.locator, "notion") && metadata.SourceURL == "" {
			t.Fatal("lost source URL")
		}
	}
}

func TestNotionDiscoveryRoutesToScanAndRejectsUnsupportedSearchScope(t *testing.T) {
	scanCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/connections/internal/") {
			_, _ = io.WriteString(w, `{"data":{"connection_id":"connection","provider":"notion","owner_user_id":"user","status":"ACTIVE","provider_options":{"chat_enabled":true}}}`)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !strings.HasPrefix(r.URL.Path, "/api/scan/binding-targets/tree/") || body["connector_type"] != "notion" || body["auth_connection_id"] != "connection" {
			t.Error("wrong Scan request")
		}
		scanCalls++
		_, _ = io.WriteString(w, `{"items":[{"key":"page","is_document":true,"provider_meta":{"id":"page","kind":"page","url":"https://notion.so/page"}}]}`)
	}))
	defer server.Close()
	reader, _ := NewCloudDocumentReader(server.URL, server.URL, "internal", 0)
	_, err := reader.GetCloudDocument(context.Background(), cloudCall(), capability.GetCloudDocumentInput{SourceID: "connection", IncludeDocuments: true, DocumentsPage: capability.PageRequest{PageSize: 20}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.SearchCloudDocuments(context.Background(), cloudCall(), capability.SearchCloudDocumentsInput{SourceID: "connection", Query: "plan", Page: capability.PageRequest{PageSize: 20}})
	if err != nil || len(result.Hits) != 1 || result.Hits[0].ReadLocator == "" {
		t.Fatalf("search=%#v err=%v", result, err)
	}
	_, err = reader.SearchCloudDocuments(context.Background(), cloudCall(), capability.SearchCloudDocumentsInput{SourceID: "connection", Query: "plan", NodeRef: "parent"})
	if code, _ := capability.CodeOf(err); code != capability.Unsupported || scanCalls != 2 {
		t.Fatalf("scope=%v calls=%d", err, scanCalls)
	}
}

func TestCloudReadUsesOnlySelectedConnectionAndRecoversExpiredStatus(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-LazyMind-Internal-Token") != "internal" {
			t.Error("missing internal auth")
		}
		user := r.Header.Get("X-User-ID")
		switch {
		case strings.Contains(r.URL.Path, "/internal/connection-"):
			id := strings.TrimPrefix(r.URL.Path, "/v1/cloud/connections/internal/")
			if r.URL.Query().Get("user_id") != user {
				t.Error("detail owner not scoped")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"connection_id": id, "owner_user_id": user, "provider": "googledrive", "status": "EXPIRED", "provider_options": map[string]bool{"chat_enabled": true}}})
		case strings.HasSuffix(r.URL.Path, "/token"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/cloud/connections/"), "/token")
			if r.URL.Query().Get("user_id") != user || r.URL.Query().Get("tenant_id") != "tenant" {
				t.Error("token owner not scoped")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"connection_id": id, "provider": "googledrive", "status": "ACTIVE", "access_token": user + ":" + id}})
		case r.URL.Path == "/internal/documents:read":
			var body struct {
				UserID, SourceID string
				ToolConfig       map[string]string
			}
			var payload map[string]json.RawMessage
			_ = json.NewDecoder(r.Body).Decode(&payload)
			_ = json.Unmarshal(payload["user_id"], &body.UserID)
			_ = json.Unmarshal(payload["source_id"], &body.SourceID)
			_ = json.Unmarshal(payload["tool_config"], &body.ToolConfig)
			if len(body.ToolConfig) != 1 || body.ToolConfig["googledrive"] != body.UserID+":"+body.SourceID {
				t.Error("credential crossed connection boundary")
			}
			mu.Lock()
			seen[body.UserID+body.SourceID]++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(capability.ReadCloudDocumentResult{SourceID: body.SourceID, Provider: "googledrive", DocumentID: "doc", Content: "public text", ContentFormat: "markdown", Version: "v1"})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	reader, _ := NewCloudDocumentReader(server.URL, server.URL, "internal", 0)
	if err := reader.SetRuntimeEndpoint(server.URL); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, user := range []string{"user-a", "user-b"} {
		for _, source := range []string{"connection-1", "connection-2"} {
			wg.Add(1)
			go func(user, source string) {
				defer wg.Done()
				call := cloudCall()
				call.Principal.UserID = user
				result, err := reader.ReadCloudDocument(context.Background(), call, capability.ReadCloudDocumentInput{SourceID: source, Locator: "googledrive:/doc", Limit: 10})
				if err != nil || result.Content != "public text" {
					t.Errorf("read=%#v err=%v", result, err)
				}
			}(user, source)
		}
	}
	wg.Wait()
	if len(seen) != 4 {
		t.Fatalf("read calls=%v", seen)
	}
}

func TestCloudConnectionRejectsOwnerTenantDisabledAndWrongToken(t *testing.T) {
	for _, scenario := range []string{"owner", "tenant", "disabled", "provider", "token"} {
		t.Run(scenario, func(t *testing.T) {
			tokenCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/token") {
					tokenCalls++
					_, _ = io.WriteString(w, `{"data":{"connection_id":"other","provider":"googledrive","status":"ACTIVE","access_token":"secret"}}`)
					return
				}
				if !strings.Contains(r.URL.Path, "/connections/internal/") {
					t.Error("runtime must not be called")
					w.WriteHeader(500)
					return
				}
				owner, tenant, provider, enabled := "user", "tenant", "googledrive", true
				switch scenario {
				case "owner":
					owner = "other"
				case "tenant":
					tenant = "other"
				case "disabled":
					enabled = false
				case "provider":
					provider = "github"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"connection_id": "connection", "owner_user_id": owner, "tenant_id": tenant, "provider": provider, "status": "ACTIVE", "provider_options": map[string]bool{"chat_enabled": enabled}}})
			}))
			defer server.Close()
			reader, _ := NewCloudDocumentReader(server.URL, server.URL, "internal", 0)
			_ = reader.SetRuntimeEndpoint(server.URL)
			_, err := reader.ReadCloudDocument(context.Background(), cloudCall(), capability.ReadCloudDocumentInput{SourceID: "connection", Locator: "googledrive:/doc", Limit: 10})
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe error: %v", err)
			}
			if scenario != "token" && tokenCalls != 0 {
				t.Fatal("secret fetched before connection check")
			}
		})
	}
}

func TestCloudRuntimeErrorsAreSanitized(t *testing.T) {
	for _, tc := range []struct {
		status    int
		reason    string
		code      capability.ErrorCode
		retryable bool
	}{
		{403, "ACCESS_DENIED", capability.PermissionDenied, false},
		{409, "VERSION_CHANGED", capability.Conflict, false},
		{413, "RESOURCE_LIMIT_EXCEEDED", capability.ResultTooLarge, false},
		{422, "UNSUPPORTED", capability.Unsupported, false},
		{429, "RATE_LIMITED", capability.Unavailable, true},
		{504, "TIMEOUT", capability.DeadlineExceeded, true},
	} {
		body, _ := json.Marshal(map[string]any{"detail": map[string]string{"code": tc.reason, "message": "private-token"}})
		err := cloudHTTPError("cloud_document.read", tc.status, body)
		if err.Code != tc.code || err.Reason != tc.reason || err.Retryable != tc.retryable || strings.Contains(err.Error(), "private") {
			t.Fatalf("error=%#v", err)
		}
	}
}

func TestCloudTransportCancelsAndRejectsRedirects(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/secret", http.StatusTemporaryRedirect)
			return
		}
		if r.URL.Path == "/secret" {
			t.Error("followed credential redirect")
			return
		}
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	reader, _ := NewCloudDocumentReader(server.URL, server.URL, "internal", 0)
	var result any
	if err := reader.request(context.Background(), cloudCall(), "read", reader.authBase, "/redirect", nil, true, &result); err == nil {
		t.Fatal("redirect accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reader.request(ctx, cloudCall(), "read", reader.authBase, "/slow", nil, true, &result) }()
	<-started
	cancel()
	var detail *capability.Error
	if err := <-done; !errors.As(err, &detail) || detail.Code != capability.DeadlineExceeded {
		t.Fatalf("cancel=%v", err)
	}
}
