package mcp

import (
	"encoding/json"
	"fmt"
	"lazymind/core/common/orm"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOAuthDiscoveryTransitionsWithoutGrantingUnknownMembers(t *testing.T) {
	db := newTestDB(t)
	status := "needs_authorization"
	oauthTestAuth(t, func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request["user_id"] != "owner" {
			t.Error("OAuth identity mismatch")
		}
		_, _ = fmt.Fprintf(w, `{"code":200,"data":{"status":%q,"grant_id":"grant","grant_version":3}}`, status)
	})
	server, err := CreateServer(t.Context(), db.DB, CreateServerRequest{Name: "Personal", Transport: "http", URL: "https://example.com/mcp", AuthType: "oauth"}, "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	assertState := func(want string, executable bool) {
		catalog, err := LoadCapabilities(t.Context(), db.DB, "owner")
		if err != nil || len(catalog) != 2 || catalog[0].Status != want || (catalog[0].Runtime != nil) != executable {
			t.Fatalf("catalog: %+v %v", catalog, err)
		}
	}
	assertState("needs_authorization", false)
	status = "authorized"
	assertState("needs_tool_selection", false)
	if err := db.Model(&orm.MCPServer{}).Where("id = ?", server.ID).Updates(map[string]any{"enabled": true, "is_verified": true, "allowed_tools_json": json.RawMessage(`["search"]`)}).Error; err != nil {
		t.Fatal(err)
	}
	assertState("ready", true)
	status = "needs_authorization"
	assertState("needs_authorization", false)
}

func TestUnconfiguredDiscoveryDoesNotGrantExecution(t *testing.T) {
	db := newTestDB(t)
	server, err := CreateServer(t.Context(), db.DB, CreateServerRequest{Name: "Documents", Transport: "http", URL: "https://example.com/mcp"}, "owner", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadCapabilities(t.Context(), db.DB, "owner")
	if err != nil || len(catalog) != 2 || catalog[0].Runtime != nil || catalog[0].Status != "needs_configuration" {
		t.Fatalf("catalog: %+v %v", catalog, err)
	}
	runtime, err := LoadRuntimeConfig(t.Context(), db.DB, "owner")
	if err != nil || len(runtime) != 0 {
		t.Fatalf("unconfigured server became executable: %+v %v", runtime, err)
	}
	foreign, err := LoadCapabilities(t.Context(), db.DB, "other")
	if err != nil || len(foreign) != 1 || foreign[0].Label != "Notion" || foreign[0].Runtime != nil {
		t.Fatal("private service leaked", err)
	}
	disabled := false
	if _, err := UpdateServer(t.Context(), db.DB, "owner", server.ID, UpdateServerRequest{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	catalog, err = LoadCapabilities(t.Context(), db.DB, "owner")
	if err != nil || len(catalog) != 1 || catalog[0].Label != "Notion" {
		t.Fatal("explicitly disabled service is discoverable", err)
	}
	// Configuration records are the source of the projection; empty allowlists never grant tools.
	if err := db.Model(&orm.MCPServer{}).Where("id = ?", server.ID).Updates(map[string]any{"enabled": true, "is_verified": true}).Error; err != nil {
		t.Fatal(err)
	}
	catalog, err = LoadCapabilities(t.Context(), db.DB, "owner")
	if err != nil || len(catalog) != 2 || catalog[0].Status != "needs_tool_selection" || catalog[0].Runtime != nil {
		t.Fatalf("empty allowlist: %+v %v", catalog, err)
	}
}

func TestCapabilitiesResolveOAuthOnceAndScopeSchemaSnapshot(t *testing.T) {
	db := newTestDB(t)
	calls := 0
	authorized := false
	oauthTestAuth(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		status := "needs_authorization"
		if authorized {
			status = "authorized"
		}
		_, _ = fmt.Fprintf(w, `{"code":200,"data":{"status":%q,"grant_id":"grant","grant_version":3}}`, status)
	})
	row := orm.MCPServer{ID: "personal", Name: "Personal", Transport: "http", URL: "https://example.com", AuthType: "oauth", HeadersJSON: []byte(`{}`), Enabled: true, IsVerified: true, AllowedToolsJSON: json.RawMessage(`["search"]`), BaseModel: orm.BaseModel{CreateUserID: "owner"}}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := replaceDiscoveredTools(t.Context(), db.DB, row.ID, []discoveredTool{{Name: "search", Description: "Find pages", InputSchema: json.RawMessage(`{"type":"object"}`)}, {Name: "delete"}}); err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadCapabilities(t.Context(), db.DB, "owner")
	if err != nil || calls != 1 {
		t.Fatalf("one status probe required: calls=%d err=%v", calls, err)
	}
	encoded, _ := json.Marshal(catalog[0])
	if strings.Contains(string(encoded), "Find pages") {
		t.Fatal("unauthorized schemas leaked")
	}
	authorized = true
	calls = 0
	catalog, err = LoadCapabilities(t.Context(), db.DB, "owner")
	if err != nil || calls != 1 {
		t.Fatalf("ready status: calls=%d err=%v", calls, err)
	}
	encoded, _ = json.Marshal(catalog[0])
	var snapshot map[string]any
	_ = json.Unmarshal(encoded, &snapshot)
	schemas, ok := snapshot["tools"].([]any)
	if !ok || len(schemas) != 1 || snapshot["tools_complete"] != true || snapshot["tools_discovered_at"] == nil || schemas[0].(map[string]any)["tool_name"] != "search" {
		t.Fatalf("scoped snapshot missing: %s", encoded)
	}
	foreign, err := LoadCapabilities(t.Context(), db.DB, "other")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(foreign)
	if strings.Contains(string(encoded), "Find pages") {
		t.Fatal("foreign schemas leaked")
	}
}

func TestCapabilitySchemaAllowlistChangesAndAuthLoss(t *testing.T) {
	db := newTestDB(t)
	status := "authorized"
	oauthTestAuth(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"code":200,"data":{"status":%q,"grant_id":"grant","grant_version":3}}`, status)
	})
	row := orm.MCPServer{ID: "s", Name: "Documents", Transport: "http", URL: "https://example.com", AuthType: "oauth", Enabled: true, IsVerified: true, HeadersJSON: []byte(`{}`), AllowedToolsJSON: []byte(`["search","read"]`), BaseModel: orm.BaseModel{CreateUserID: "owner"}}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := replaceDiscoveredTools(t.Context(), db.DB, row.ID, []discoveredTool{{Name: "search"}, {Name: "delete"}}); err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadCapabilities(t.Context(), db.DB, "owner")
	if err != nil || len(catalog[0].Tools) != 1 || catalog[0].ToolsComplete {
		t.Fatalf("partial cache must stay incomplete: %+v %v", catalog, err)
	}
	// Checks return authorization but do not load the schema snapshot.
	item, err := LoadCapability(t.Context(), db.DB, "owner", "mcp:s")
	if err != nil || item == nil || item.Runtime == nil || len(item.Tools) != 0 || item.ToolsDiscoveredAt != nil {
		t.Fatalf("target check: %+v %v", item, err)
	}
	if err := db.Model(&row).Update("allowed_tools_json", []byte(`["read"]`)).Error; err != nil {
		t.Fatal(err)
	}
	catalog, err = LoadCapabilities(t.Context(), db.DB, "owner")
	if err != nil || len(catalog[0].Tools) != 0 || catalog[0].ToolsComplete || catalog[0].ToolsDiscoveredAt != nil {
		t.Fatalf("unknown tools must not reuse removed schemas: %+v %v", catalog, err)
	}
	status = "needs_authorization"
	catalog, err = LoadCapabilities(t.Context(), db.DB, "owner")
	if err != nil || catalog[0].Status != status || catalog[0].Runtime != nil || len(catalog[0].Tools) != 0 {
		t.Fatalf("auth loss: %+v %v", catalog, err)
	}
	for _, user := range []string{"other", ""} {
		item, err := LoadCapability(t.Context(), db.DB, user, "s")
		if err != nil || item != nil {
			t.Fatalf("foreign target returned: %+v %v", item, err)
		}
	}
	if err := db.Model(&row).Updates(map[string]any{"enabled": false, "discovery_enabled": false}).Error; err != nil {
		t.Fatal(err)
	}
	item, err = LoadCapability(t.Context(), db.DB, "owner", "s")
	if err != nil || item != nil {
		t.Fatalf("disabled target returned: %+v %v", item, err)
	}
}

func TestLoadCapabilityBuiltinAndFeatureControl(t *testing.T) {
	db := newTestDB(t)
	builtin := builtinNotion("owner")
	item, err := LoadCapability(t.Context(), db.DB, "owner", builtin.ID)
	if err != nil || item == nil || item.Status != "needs_authorization" {
		t.Fatalf("builtin missing: %+v %v", item, err)
	}
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{"user_id": "owner", "mcp_enabled": false, "created_at": time.Now(), "updated_at": time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	item, err = LoadCapability(t.Context(), db.DB, "owner", builtin.ID)
	if err != nil || item != nil {
		t.Fatalf("disabled builtin returned: %+v %v", item, err)
	}
}

func TestCapabilityMalformedHeadersAreIsolatedAndDatabaseErrorsRemainFatal(t *testing.T) {
	db := newTestDB(t)
	for _, id := range []string{"broken", "healthy"} {
		headers := json.RawMessage(`{}`)
		if id == "broken" {
			headers = json.RawMessage(`[]`)
		}
		row := orm.MCPServer{ID: id, Name: id, Transport: "http", URL: "https://example.com/" + id, AuthType: "api_key", Enabled: true, IsVerified: true, HeadersJSON: headers, AllowedToolsJSON: json.RawMessage(`["search"]`), BaseModel: orm.BaseModel{CreateUserID: "owner"}}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := LoadCapabilities(t.Context(), db.DB, "owner")
	if err != nil {
		t.Fatalf("one malformed service blocked catalog: %v", err)
	}
	byID := map[string]CapabilityConfig{}
	for _, item := range catalog {
		byID[item.Service] = item
	}
	broken, healthy := byID["mcp:broken"], byID["mcp:healthy"]
	if broken.Status != "unavailable" || broken.Runtime != nil || len(broken.Tools) != 0 || healthy.Status != "ready" || healthy.Runtime == nil {
		t.Fatalf("malformed service isolation: %+v", catalog)
	}
	runtime, err := LoadRuntimeConfig(t.Context(), db.DB, "owner")
	if err != nil || len(runtime) != 1 || runtime[0].ID != "healthy" {
		t.Fatalf("healthy runtime lost: %+v %v", runtime, err)
	}
	item, err := LoadCapability(t.Context(), db.DB, "owner", "broken")
	if err != nil || item == nil || item.Status != "unavailable" {
		t.Fatalf("malformed target: %+v %v", item, err)
	}
	if err := db.Migrator().DropTable(&orm.MCPServerTool{}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCapabilities(t.Context(), db.DB, "owner"); err == nil {
		t.Fatal("catalog swallowed database failure")
	}
	if _, err := LoadCapability(t.Context(), db.DB, "owner", "broken"); err == nil {
		t.Fatal("target swallowed database failure")
	}
}
