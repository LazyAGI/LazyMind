package mcp

import (
	"encoding/json"
	"github.com/gorilla/mux"
	"io"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBuiltinNotionDiscoverableWithoutConfiguration(t *testing.T) {
	db := newTestDB(t)
	var previous string
	for _, user := range []string{"alice", "bob"} {
		catalog, err := LoadCapabilities(t.Context(), db.DB, user)
		if err != nil || len(catalog) != 1 {
			t.Fatalf("empty account must discover Notion: %+v %v", catalog, err)
		}
		item := catalog[0]
		if item.Label != "Notion" || item.Status != "needs_authorization" || item.Runtime != nil || !strings.HasPrefix(item.Service, "mcp:") || item.Service == previous {
			t.Fatalf("unsafe builtin: %+v", item)
		}
		if len("get_mcp_"+strings.TrimPrefix(item.Service, "mcp:")+"_methods") > 64 {
			t.Fatal("builtin gateway exceeds provider function-name limit")
		}
		previous = item.Service
	}
	var count int64
	db.Model(&orm.MCPServer{}).Count(&count)
	if count != 0 {
		t.Fatal("discovery must not create personal configurations")
	}
	catalog, err := LoadCapabilities(t.Context(), db.DB, "")
	if err != nil || len(catalog) != 0 {
		t.Fatalf("anonymous discovery: %+v %v", catalog, err)
	}
}

func TestBuiltinNotionHonorsExistingDisabledConfiguration(t *testing.T) {
	db := newTestDB(t)
	existing, err := CreateServer(t.Context(), db.DB, CreateServerRequest{Name: "My Notion", URL: "https://mcp.notion.com/mcp", Transport: "http", AuthType: "oauth"}, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := UpdateServer(t.Context(), db.DB, "alice", existing.ID, UpdateServerRequest{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadCapabilities(t.Context(), db.DB, "alice")
	if err != nil || len(catalog) != 0 {
		t.Fatalf("builtin bypassed explicit disable: %+v %v", catalog, err)
	}
}

func TestBuiltinNotionCanRequestAuthorizationAfterBeingDisabled(t *testing.T) {
	db := newTestDB(t)
	oauthTestAuth(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"data":{"status":"disconnected"}}`))
	})
	row, err := authorizeServer(t.Context(), db.DB, "alice", builtinNotion("alice").ID)
	if err != nil {
		t.Fatal(err)
	}
	disabled, requested := false, true
	if _, err := UpdateServer(t.Context(), db.DB, "alice", row.ID, UpdateServerRequest{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	response, err := UpdateServer(t.Context(), db.DB, "alice", row.ID, UpdateServerRequest{Enabled: &requested})
	if err != nil {
		t.Fatalf("requesting authorization for disabled Notion: %v", err)
	}
	if response.Enabled || !response.DiscoveryEnabled {
		t.Fatalf("unverified Notion state: %+v", response)
	}
	catalog, err := LoadCapabilities(t.Context(), db.DB, "alice")
	if err != nil || len(catalog) != 1 || catalog[0].Status != "needs_authorization" {
		t.Fatalf("Notion authorization entry missing: %+v %v", catalog, err)
	}
	runtime, err := LoadRuntimeConfig(t.Context(), db.DB, "alice")
	if err != nil || len(runtime) != 0 {
		t.Fatalf("unverified Notion became executable: %+v %v", runtime, err)
	}
	if _, err := UpdateServer(t.Context(), db.DB, "alice", row.ID, UpdateServerRequest{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	catalog, err = LoadCapabilities(t.Context(), db.DB, "alice")
	if err != nil || len(catalog) != 0 {
		t.Fatalf("explicit disable still exposed Notion: %+v %v", catalog, err)
	}
}

func TestBuiltinNotionCannotRequestAuthorizationWhileChangingConnection(t *testing.T) {
	db := newTestDB(t)
	oauthTestAuth(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"data":{"status":"disconnected"}}`))
	})
	row, err := authorizeServer(t.Context(), db.DB, "alice", builtinNotion("alice").ID)
	if err != nil {
		t.Fatal(err)
	}
	requested := true
	otherURL := "https://example.com/mcp"
	if _, err := UpdateServer(t.Context(), db.DB, "alice", row.ID, UpdateServerRequest{
		Enabled: &requested,
		URL:     &otherURL,
	}); err == nil {
		t.Fatal("changing the builtin Notion connection while requesting authorization should fail")
	}
}

func TestBuiltinNotionAuthorizationIsPersonalAndIdempotent(t *testing.T) {
	db := newTestDB(t)
	for _, user := range []string{"alice", "bob", "alice"} {
		row, err := authorizeServer(t.Context(), db.DB, user, builtinNotion(user).ID)
		if err != nil || row.CreateUserID != user || row.Share || row.Enabled {
			t.Fatalf("personal authorization: %+v %v", row, err)
		}
	}
	var count int64
	db.Model(&orm.MCPServer{}).Count(&count)
	if count != 2 {
		t.Fatalf("duplicate records: %d", count)
	}
	if _, err := authorizeServer(t.Context(), db.DB, "bob", builtinNotion("alice").ID); err == nil {
		t.Fatal("cross-user authorization accepted")
	}
	if err := db.Create(&orm.UserUIPreferences{UserID: "blocked", MCPEnabled: false}).Error; err != nil {
		t.Fatal(err)
	}
	// Use an explicit update because GORM fills model defaults on Create.
	db.Model(&orm.UserUIPreferences{}).Where("user_id = ?", "blocked").Update("mcp_enabled", false)
	if _, err := authorizeServer(t.Context(), db.DB, "blocked", builtinNotion("blocked").ID); err == nil {
		t.Fatal("disabled MCP authorized")
	}
	catalog, err := LoadCapabilities(t.Context(), db.DB, "blocked")
	if err != nil || len(catalog) != 0 {
		t.Fatalf("disabled MCP exposed: %+v %v", catalog, err)
	}
}

type notionTestTransport func(*http.Request) (*http.Response, error)

func (f notionTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBuiltinNotionDiscoveryEnablesOnlyKnownReadTools(t *testing.T) {
	db := newTestDB(t)
	row, err := authorizeServer(t.Context(), db.DB, "alice", builtinNotion("alice").ID)
	if err != nil {
		t.Fatal(err)
	}
	oauthTestAuth(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["user_id"] != "alice" || body["server_id"] != row.ID || body["server_url"] != "https://mcp.notion.com/mcp" {
			t.Errorf("wrong grant identity: %v", body)
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"status":"authorized","grant_id":"g","grant_version":1,"access_token":"test-only","token_version":1}}`))
	})
	emptyDiscovery := false
	transport := http.DefaultTransport
	http.DefaultTransport = notionTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "mcp.notion.com" {
			return transport.RoundTrip(r)
		}
		if r.Header.Get("Authorization") != "Bearer test-only" {
			t.Error("missing OAuth token")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		result := `{"jsonrpc":"2.0","id":1,"result":{}}`
		if body["method"] == "tools/list" {
			result = `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"notion-search"},{"name":"notion-fetch"},{"name":"notion-update-page"},{"name":"unknown-new-tool"}]}}`
		}
		if body["method"] == "tools/list" && emptyDiscovery {
			result = `{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(result))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	discovery, err := DiscoverServer(t.Context(), db.DB, "alice", row.ID)
	if err != nil || !discovery.Success {
		t.Fatalf("discovery failed: %+v %v", discovery, err)
	}
	runtime, err := LoadRuntimeConfig(t.Context(), db.DB, "alice")
	if err != nil || len(runtime) != 1 {
		t.Fatalf("authorized builtin not ready: %+v %v", runtime, err)
	}
	if got := strings.Join(runtime[0].AllowedTools, ","); got != "notion-fetch,notion-search" {
		t.Fatalf("unexpected permissions: %s", got)
	}

	emptyDiscovery = true
	discovery, err = DiscoverServer(t.Context(), db.DB, "alice", row.ID)
	if err != nil || discovery.Success {
		t.Fatalf("empty discovery reported ready: %+v %v", discovery, err)
	}
	emptyDiscovery = false
	discovery, err = DiscoverServer(t.Context(), db.DB, "alice", row.ID)
	if err != nil || !discovery.Success {
		t.Fatalf("retry lost the configured read permissions: %+v %v", discovery, err)
	}

	disabled := false
	if _, err := UpdateServer(t.Context(), db.DB, "alice", row.ID, UpdateServerRequest{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverServer(t.Context(), db.DB, "alice", row.ID); err != nil {
		t.Fatal(err)
	}
	runtime, err = LoadRuntimeConfig(t.Context(), db.DB, "alice")
	if err != nil || len(runtime) != 0 {
		t.Fatal("viewing discovered tools re-enabled an explicitly disabled connection")
	}
	// A later discovery must not re-grant tools explicitly removed by the user.
	if _, err := UpdateServerTools(t.Context(), db.DB, "alice", row.ID, UpdateToolsRequest{AllowedTools: []string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverServer(t.Context(), db.DB, "alice", row.ID); err != nil {
		t.Fatal(err)
	}
	runtime, err = LoadRuntimeConfig(t.Context(), db.DB, "alice")
	if err != nil || len(runtime) != 0 {
		t.Fatal("empty permissions were replaced with defaults")
	}
}

func TestBuiltinNotionAuthorizeHandlerCreatesOnlyCurrentUsersConnection(t *testing.T) {
	db := newTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	calls := 0
	oauthTestAuth(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["user_id"] != "alice" || body["server_url"] != "https://mcp.notion.com/mcp" {
			t.Errorf("wrong identity: %v", body)
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"status":"pending","authorization_url":"https://notion.example/authorize"}}`))
	})
	catalog, err := LoadCapabilities(t.Context(), db.DB, "alice")
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(catalog[0].Service, "mcp:")
	for _, user := range []string{"alice", "alice", "bob"} {
		request := httptest.NewRequest("POST", "/mcp_servers/"+id+"/oauth/authorize", nil)
		request.Header.Set("X-User-Id", user)
		request = mux.SetURLVars(request, map[string]string{"id": id})
		response := httptest.NewRecorder()
		OAuthAuthorize(response, request)
		if user == "alice" && (response.Code != 200 || !strings.Contains(response.Body.String(), "https://notion.example/authorize")) {
			t.Fatalf("authorization failed: %s", response.Body.String())
		}
		if user == "bob" && response.Code == 200 {
			t.Fatal("another user authorized alice's server")
		}
	}
	var count int64
	db.Model(&orm.MCPServer{}).Count(&count)
	if calls != 2 || count != 1 {
		t.Fatalf("calls=%d records=%d", calls, count)
	}
}

func TestBuiltinNotionAvailableAfterLegacyConfigurationDeleted(t *testing.T) {
	db := newTestDB(t)
	legacy, err := CreateServer(t.Context(), db.DB, CreateServerRequest{Name: "Legacy Notion", URL: notionMCPURL, Transport: "http", AuthType: "oauth"}, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.MCPServer{}).Where("id = ?", legacy.ID).Update("deleted_at", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadCapabilities(t.Context(), db.DB, "alice")
	if err != nil || len(catalog) != 1 || catalog[0].Status != "needs_authorization" || catalog[0].Service == "mcp:"+legacy.ID {
		t.Fatalf("deleted legacy record hid builtin: %+v %v", catalog, err)
	}
}
