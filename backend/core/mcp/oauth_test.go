package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"lazymind/core/common/orm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func oauthTestAuth(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", s.URL)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-test")
}
func TestOAuthDiscoveryRefreshOnlyExplicit401Once(t *testing.T) {
	for _, status := range []int{401, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			tokens, refreshes, calls := 0, 0, 0
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer second" {
					w.WriteHeader(status)
					return
				}
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["method"] == "tools/list" {
					_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"search"}]}}`))
				} else {
					_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
				}
			}))
			defer remote.Close()
			oauthTestAuth(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-LazyMind-Internal-Token") != "internal-test" {
					t.Error("missing internal authentication")
				}
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["user_id"] != "owner" || body["server_id"] != "server" || body["server_url"] != remote.URL {
					t.Errorf("wrong identity: %v", body)
				}
				if strings.HasSuffix(r.URL.Path, "/status") {
					_, _ = w.Write([]byte(`{"code":200,"data":{"status":"authorized","grant_id":"g","grant_version":4}}`))
					return
				}
				tokens++
				token := "first"
				if body["rejected_token_version"] != nil {
					refreshes++
					if body["rejected_token_version"] != float64(7) {
						t.Error("incorrect rejected version")
					}
					token = "second"
				}
				_, _ = fmt.Fprintf(w, `{"code":200,"data":{"status":"authorized","access_token":%q,"token_version":7}}`, token)
			})
			tools, err := listRemoteTools(context.Background(), orm.MCPServer{ID: "server", AuthType: "oauth", Transport: "http", URL: remote.URL, BaseModel: orm.BaseModel{CreateUserID: "owner"}})
			if status == 401 {
				if err != nil || len(tools) != 1 || tokens != 2 || refreshes != 1 {
					t.Fatalf("tools=%v err=%v tokens=%d refreshes=%d", tools, err, tokens, refreshes)
				}
			} else if err == nil || tokens != 1 || refreshes != 0 || calls != 1 {
				t.Fatalf("unexpected retry: %v tokens=%d calls=%d", err, tokens, calls)
			}
		})
	}
}
func TestOAuthOwnedPrivateRuntimeAndEmptyPermissions(t *testing.T) {
	db := newTestDB(t)
	oauthTestAuth(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"data":{"status":"authorized","grant_id":"grant","grant_version":3}}`))
	})
	server, err := CreateServer(context.Background(), db.DB, CreateServerRequest{Name: "Personal", URL: "https://mcp.example", Transport: "http", AuthType: "oauth", APIKey: "must-not-store"}, "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateServer(context.Background(), db.DB, "other", server.ID, UpdateServerRequest{}); err == nil {
		t.Fatal("other owner can edit")
	}
	var row orm.MCPServer
	db.First(&row, "id = ?", server.ID)
	if apiKeyPreview(row.HeadersJSON) != "" || row.Share {
		t.Fatal("OAuth contains legacy credentials or share")
	}
	db.Model(&row).Updates(map[string]any{"enabled": true, "is_verified": true})
	configs, err := LoadRuntimeConfig(context.Background(), db.DB, "owner")
	if err != nil || len(configs) != 0 {
		t.Fatalf("empty tools callable: %v %v", configs, err)
	}
	db.Model(&row).Update("allowed_tools_json", json.RawMessage(`["search"]`))
	configs, err = LoadRuntimeConfig(context.Background(), db.DB, "owner")
	if err != nil || len(configs) != 1 || configs[0].OAuth.UserID != "owner" || configs[0].OAuth.GrantVersion != 3 || len(configs[0].Headers) != 0 {
		t.Fatalf("runtime=%#v err=%v", configs, err)
	}
	db.Model(&row).Update("share", true)
	configs, err = LoadRuntimeConfig(context.Background(), db.DB, "other")
	if err != nil || len(configs) != 0 {
		t.Fatal("OAuth leaked to shared runtime")
	}
	listed, err := ListServers(context.Background(), db.DB, "other", ListServersRequest{})
	if err != nil || len(listed.MCPServers) != 0 {
		t.Fatal("OAuth leaked in list")
	}
}
func TestOAuthChangesFailClosedWhenDisconnectFails(t *testing.T) {
	db := newTestDB(t)
	oauthTestAuth(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	row, err := CreateServer(context.Background(), db.DB, CreateServerRequest{Name: "Personal", URL: "https://mcp.example", Transport: "http", AuthType: "oauth"}, "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	next := "https://other.example"
	if _, err := UpdateServer(context.Background(), db.DB, "owner", row.ID, UpdateServerRequest{URL: &next}); err == nil {
		t.Fatal("URL changed without disconnect")
	}
	if err := DeleteServer(context.Background(), db.DB, "owner", row.ID); err == nil {
		t.Fatal("deleted without disconnect")
	}
	var stored orm.MCPServer
	db.First(&stored, "id = ?", row.ID)
	if stored.URL != row.URL || stored.DeletedAt != nil {
		t.Fatal("mutation committed despite disconnect failure")
	}
}
func TestOAuthRejectsSSEAndAuthenticatedRedirect(t *testing.T) {
	if _, err := validateAuthType("oauth", "sse"); err == nil {
		t.Fatal("SSE accepted")
	}
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++ }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	_, err := listRemoteToolsWithHeaders(context.Background(), orm.MCPServer{URL: source.URL, Transport: "http", AuthType: "oauth"}, map[string]any{"Authorization": "Bearer secret"})
	if err == nil || targetCalls != 0 {
		t.Fatal("credential redirect followed")
	}
}

func TestUnavailableOAuthDoesNotDropHealthyLegacyRuntime(t *testing.T) {
	db := newTestDB(t)
	oauthTestAuth(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	for _, authType := range []string{"oauth", "api_key"} {
		row := orm.MCPServer{ID: authType, Name: authType, AuthType: authType, Transport: "http", URL: "https://mcp.example", HeadersJSON: []byte(`{}`), AllowedToolsJSON: []byte(`["search"]`), Enabled: true, IsVerified: true, BaseModel: orm.BaseModel{CreateUserID: "owner"}}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	configs, err := LoadRuntimeConfig(context.Background(), db.DB, "owner")
	if err != nil || len(configs) != 1 || configs[0].ID != "api_key" {
		t.Fatalf("healthy MCP dropped: %#v %v", configs, err)
	}
}
func TestLegacyMCPHTTPRedirectPreserved(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls++
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{"tools":[]}}`))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	_, err := listRemoteToolsWithHeaders(context.Background(), orm.MCPServer{URL: source.URL, Transport: "http", AuthType: "api_key"}, map[string]any{})
	if err != nil || targetCalls == 0 {
		t.Fatalf("legacy redirect blocked: %v calls=%d", err, targetCalls)
	}
}

func TestLegacyMCPAuthenticationModeUsesStoredHeaders(t *testing.T) {
	empty, err := headersJSONFromAPIKey("")
	if err != nil {
		t.Fatal(err)
	}
	keyed, err := headersJSONFromAPIKey("legacy-secret")
	if err != nil {
		t.Fatal(err)
	}
	if effectiveAuthType(orm.MCPServer{HeadersJSON: empty}) != "none" {
		t.Fatal("empty legacy headers classified as key")
	}
	if effectiveAuthType(orm.MCPServer{HeadersJSON: keyed}) != "api_key" {
		t.Fatal("encrypted key not recognized")
	}
	db := newTestDB(t)
	created, err := CreateServer(context.Background(), db.DB, CreateServerRequest{Name: "Legacy", Transport: "http", URL: "https://mcp.example"}, "owner", "")
	if err != nil || created.AuthType != "none" {
		t.Fatalf("old unauthenticated client: %#v %v", created, err)
	}
}

func TestLegacyMCPClientCanAddAPIKeyWithoutAuthType(t *testing.T) {
	for _, migrated := range []bool{true, false} {
		t.Run(fmt.Sprintf("migrated_%t", migrated), func(t *testing.T) {
			db := newTestDB(t)
			created, err := CreateServer(context.Background(), db.DB, CreateServerRequest{Name: "Legacy", Transport: "http", URL: "https://mcp.example"}, "owner", "")
			if err != nil {
				t.Fatal(err)
			}
			if migrated {
				if err := db.Model(&orm.MCPServer{}).Where("id = ?", created.ID).Update("auth_type", "").Error; err != nil {
					t.Fatal(err)
				}
			}
			key := "added-key"
			updated, err := UpdateServer(context.Background(), db.DB, "owner", created.ID, UpdateServerRequest{APIKey: &key})
			if err != nil || updated.AuthType != "api_key" {
				t.Fatalf("legacy key update: %#v %v", updated, err)
			}
			var row orm.MCPServer
			if err := db.First(&row, "id = ?", created.ID).Error; err != nil {
				t.Fatal(err)
			}
			headers, err := decodeHeaders(row.HeadersJSON)
			if err != nil || headers["Authorization"] != "Bearer added-key" {
				t.Fatalf("legacy key lost: %v", err)
			}
		})
	}
}

func TestAddingAPIKeyDoesNotOverrideExplicitNoneOrOAuth(t *testing.T) {
	for _, mode := range []string{"none", "oauth"} {
		t.Run(mode, func(t *testing.T) {
			db := newTestDB(t)
			oauthTestAuth(t, func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/status") {
					t.Error("key-only update should not disconnect OAuth")
				}
				_, _ = w.Write([]byte(`{"code":200,"data":{"status":"authorized","grant_id":"grant","grant_version":1}}`))
			})
			created, err := CreateServer(context.Background(), db.DB, CreateServerRequest{Name: "Personal", Transport: "http", URL: "https://mcp.example", AuthType: mode}, "owner", "")
			if err != nil {
				t.Fatal(err)
			}
			key := "must-not-store"
			req := UpdateServerRequest{APIKey: &key}
			if mode == "none" {
				req.AuthType = &mode
			}
			updated, err := UpdateServer(context.Background(), db.DB, "owner", created.ID, req)
			if err != nil || updated.AuthType != mode {
				t.Fatalf("mode overwritten: %#v %v", updated, err)
			}
			var row orm.MCPServer
			if err := db.First(&row, "id = ?", created.ID).Error; err != nil {
				t.Fatal(err)
			}
			headers, err := decodeHeaders(row.HeadersJSON)
			if err != nil || len(headers) != 0 {
				t.Fatalf("key stored for %s", mode)
			}
		})
	}
}
