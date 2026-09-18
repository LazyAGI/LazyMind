package externalcapability

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/capability"
	"lazymind/core/common/orm"
)

func TestRegistrySearchUsesVerifiedOwnConfigAndFreshGrant(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	for _, row := range []any{
		&orm.UserModelProvider{ID: "search-provider", Name: "Tavily", Category: "search", BaseModel: orm.BaseModel{CreateUserID: "user-1"}},
		&orm.UserModelProviderGroup{ID: "search-group", UserModelProviderID: "search-provider", APIKey: "test-search-key", IsVerified: true, BaseModel: orm.BaseModel{CreateUserID: "user-1"}},
		&orm.UserSelectedProvider{UserID: "user-1", Category: "search", UserModelProviderGroupID: "search-group", CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-LazyMind-Internal-Token") != "test-internal" {
			t.Error("missing internal auth")
		}
		var input struct {
			ToolConfig map[string]any `json:"tool_config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		available := input.ToolConfig["tavily"] == "test-search-key"
		if r.URL.Path == "/api/chat/tools/external-catalog" {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
				"name": "web_search", "available": available, "input_schema": map[string]any{"type": "object"},
			}}})
			return
		}
		if !available {
			t.Error("search credential was not injected")
		}
		calls++
		_, _ = w.Write([]byte(`{"result":{"results":[{"title":"test","url":"https://example.com"}]}}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "test-internal")
	s := New(db, server.Client())
	ctx := context.Background()
	call := capability.InvocationContext{Principal: capability.Principal{UserID: "user-1"}, ExternalAgent: "codex"}
	input := capability.InvokeExternalToolInput{ToolID: "builtin:web_search", Arguments: map[string]any{"query": "test"}}
	listed, err := s.ListExternalTools(ctx, call)
	if err != nil || len(listed.Items) != 1 {
		t.Fatalf("default tools listing=%#v err=%v", listed, err)
	}
	if _, err := s.InvokeExternalTool(ctx, call, input); err != nil {
		t.Fatalf("available tools must be open by default: %v", err)
	}
	grant := GrantUpdate{Agent: "codex", CapabilityType: CapabilityTool, CapabilityID: input.ToolID, Enabled: true}
	if err := s.SetGrant(ctx, "user-1", grant); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InvokeExternalTool(ctx, call, input); err != nil {
		t.Fatal(err)
	}
	call.ExternalAgent = "cursor"
	if err := s.SetGrant(ctx, "user-1", GrantUpdate{Agent: "cursor", CapabilityType: CapabilityTool, CapabilityID: input.ToolID, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InvokeExternalTool(ctx, call, input); err == nil {
		t.Fatal("explicitly disabled Agent must be denied")
	}
	// A fresh service must retain the opt-out, including in discovery and settings.
	fresh := New(db, server.Client())
	listed, err = fresh.ListExternalTools(ctx, call)
	if err != nil || len(listed.Items) != 0 {
		t.Fatalf("disabled tools listing=%#v err=%v", listed, err)
	}
	inventory, err := fresh.Inventory(ctx, "user-1", "cursor")
	if err != nil || len(inventory.Capabilities) != 1 || inventory.Capabilities[0].Authorized {
		t.Fatalf("disabled tool inventory=%#v err=%v", inventory, err)
	}
	call.ExternalAgent = "codex"
	listed, err = fresh.ListExternalTools(ctx, call)
	if err != nil || len(listed.Items) != 1 {
		t.Fatalf("another Agent's opt-out affected Codex: %#v err=%v", listed, err)
	}
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "search-group").Update("is_verified", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.InvokeExternalTool(ctx, call, input); err == nil {
		t.Fatal("unverified provider must be denied")
	}
	grant.Enabled = false
	if err := s.SetGrant(ctx, "user-1", grant); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InvokeExternalTool(ctx, call, input); err == nil {
		t.Fatal("revoked grant must be denied")
	}
	if calls != 2 {
		t.Fatalf("unexpected executions: %d", calls)
	}
}
