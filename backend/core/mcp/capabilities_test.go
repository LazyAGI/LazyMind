package mcp

import (
	"encoding/json"
	"fmt"
	"lazymind/core/common/orm"
	"net/http"
	"testing"
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
