package userprefs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestSettingsOverviewMCPCountsAndIssuesUseIndependentStates(t *testing.T) {
	db := newUIPreferencesTestDB(t)
	now := time.Now().UTC()
	servers := []orm.MCPServer{
		newOverviewMCPServer("disabled-verified", false, true, []string{"disabled-tool"}, now),
		newOverviewMCPServer("enabled-unverified", true, false, []string{"unverified-tool"}, now),
		newOverviewMCPServer("enabled-unauthorized", true, true, nil, now),
		newOverviewMCPServer("enabled-runnable", true, true, []string{"search"}, now),
	}
	if err := db.Create(&servers).Error; err != nil {
		t.Fatalf("seed mcp servers: %v", err)
	}

	overview, err := buildSettingsOverview(httptest.NewRequest(http.MethodGet, "/settings/overview", nil), db.DB, "u1")
	if err != nil {
		t.Fatalf("build settings overview: %v", err)
	}
	mcpSection := sectionByID(overview.Sections, "mcp")
	if got, want := mcpSection.Counts.Total, int64(4); got != want {
		t.Fatalf("total MCP servers = %d, want %d", got, want)
	}
	if got, want := mcpSection.Counts.Enabled, int64(3); got != want {
		t.Fatalf("enabled MCP servers = %d, want %d", got, want)
	}
	if got, want := mcpSection.Counts.Verified, int64(3); got != want {
		t.Fatalf("verified MCP servers = %d, want %d", got, want)
	}
	if got, want := mcpSection.Counts.Runnable, int64(1); got != want {
		t.Fatalf("runnable MCP servers = %d, want %d", got, want)
	}

	mcpIssues := overviewMCPIssues(overview.Issues)
	if got, want := len(mcpIssues), 2; got != want {
		t.Fatalf("MCP issues = %#v, want %d distinct issues", mcpIssues, want)
	}
	if got := mcpIssues["mcp-needs-verification"]; got != "存在已启用但尚未通过验证的 MCP 服务" {
		t.Fatalf("verification issue = %q", got)
	}
	if got := mcpIssues["mcp-needs-authorization"]; got != "存在已启用且已验证但尚未授权工具的 MCP 服务" {
		t.Fatalf("authorization issue = %q", got)
	}
}

func TestSettingsOverviewMCPIssuesIgnoreDisabledServers(t *testing.T) {
	db := newUIPreferencesTestDB(t)
	now := time.Now().UTC()
	servers := []orm.MCPServer{
		newOverviewMCPServer("disabled-unverified", false, false, nil, now),
		newOverviewMCPServer("disabled-verified", false, true, nil, now),
		newOverviewMCPServer("enabled-runnable", true, true, []string{"search"}, now),
	}
	if err := db.Create(&servers).Error; err != nil {
		t.Fatalf("seed mcp servers: %v", err)
	}

	overview, err := buildSettingsOverview(httptest.NewRequest(http.MethodGet, "/settings/overview", nil), db.DB, "u1")
	if err != nil {
		t.Fatalf("build settings overview: %v", err)
	}
	if issues := overviewMCPIssues(overview.Issues); len(issues) != 0 {
		t.Fatalf("disabled MCP servers should not create readiness issues: %#v", issues)
	}
	mcpSection := sectionByID(overview.Sections, "mcp")
	if mcpSection.Counts.Total != 3 || mcpSection.Counts.Enabled != 1 || mcpSection.Counts.Verified != 2 || mcpSection.Counts.Runnable != 1 {
		t.Fatalf("unexpected MCP counts: %#v", mcpSection.Counts)
	}
}

func newOverviewMCPServer(id string, enabled, verified bool, allowedTools []string, now time.Time) orm.MCPServer {
	allowedToolsJSON, _ := json.Marshal(allowedTools)
	return orm.MCPServer{
		ID:               id,
		Name:             id,
		Transport:        "http",
		URL:              "https://mcp.example.com/" + id,
		HeadersJSON:      json.RawMessage(`{}`),
		AllowedToolsJSON: allowedToolsJSON,
		Enabled:          enabled,
		IsVerified:       verified,
		BaseModel: orm.BaseModel{
			CreateUserID:   "u1",
			CreateUserName: "User 1",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
}

func overviewMCPIssues(issues []settingsOverviewIssue) map[string]string {
	out := map[string]string{}
	for _, issue := range issues {
		if issue.Section == "mcp" {
			out[issue.ID] = issue.Message
		}
	}
	return out
}

func TestSettingsOverviewCountsConfiguredProviders(t *testing.T) {
	for _, tc := range []struct {
		name          string
		verified      bool
		key           string
		want          int64
		selectedModel bool
		customURL     bool
		deleted       bool
	}{
		{"configured services", true, "test-key", 3, true, false, false},
		{"unverified services with a saved model key", false, "test-key", 1, true, false, false},
		{"verified keyless model provider", true, "", 1, true, false, false},
		{"keyless custom endpoint", true, "", 3, true, true, false},
		{"configured model provider without a selected model", true, "test-key", 3, false, false, false},
		{"no configured entries", false, "", 0, false, false, false},
		{"deleted groups", true, "test-key", 0, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newUIPreferencesTestDB(t)
			now := time.Now().UTC()
			if tc.selectedModel {
				// Selecting the same provider for multiple roles must not inflate the inventory.
				for _, role := range []string{"llm", "evo_llm"} {
					if err := db.Create(&orm.UserSelectedModel{UserID: "u1", ModelKey: role, UserModelProviderGroupModelID: "selected-model", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, service := range []struct{ id, name, category, user string }{
				{"tavily", "Tavily", "search", "u1"},
				{"mineru", "MinerU", "ocr", "u1"},
				{"model", "OpenAI", "model", "u1"},
				{"other-user", "Tavily", "search", "u2"},
				{"unavailable-translation", "Unavailable Translation", "translation", "u1"},
			} {
				base := orm.BaseModel{CreateUserID: service.user, CreatedAt: now, UpdatedAt: now}
				if err := db.Create(&orm.UserModelProvider{ID: service.id, DefaultModelProviderID: service.id, Name: service.name, Category: service.category, BaseModel: base}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&orm.DefaultModelProvider{ID: service.id, Name: service.id, Category: service.category, BaseURL: "https://service.example/api"}).Error; err != nil {
					t.Fatal(err)
				}
				groupURL := "https://service.example/api"
				if tc.customURL {
					groupURL = "http://localhost:8000/api"
				}
				if tc.deleted {
					base.DeletedAt = &now
				}
				// Multiple verified groups still represent one configured service.
				for _, suffix := range []string{"-one", "-two"} {
					if err := db.Create(&orm.UserModelProviderGroup{ID: service.id + suffix, UserModelProviderID: service.id, Name: service.name, BaseURL: groupURL, APIKey: tc.key, IsVerified: tc.verified, BaseModel: base}).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			overview, err := buildSettingsOverview(httptest.NewRequest(http.MethodGet, "/settings/overview", nil), db.DB, "u1")
			if err != nil {
				t.Fatal(err)
			}
			models := sectionByID(overview.Sections, "models")
			if got := models.Counts.Configured; got != tc.want {
				t.Fatalf("configured providers = %d, want %d", got, tc.want)
			}
			wantModels := int64(0)
			if !tc.deleted && (tc.verified || tc.key != "") {
				wantModels = 1
			}
			if models.Counts.ModelProviders != wantModels || models.Counts.ExternalServices != tc.want-wantModels {
				t.Fatalf("inventory breakdown = %#v, want model providers %d and external services %d", models.Counts, wantModels, tc.want-wantModels)
			}
			if models.EffectiveEnabled == nil || *models.EffectiveEnabled != tc.selectedModel {
				t.Fatalf("service configuration must not imply model readiness: %#v", models)
			}
			missingModelIssue := false
			for _, issue := range overview.Issues {
				if issue.ID == "model-not-configured" {
					missingModelIssue = true
				}
			}
			if missingModelIssue == tc.selectedModel {
				t.Fatalf("missing-model issue = %v, selected model = %v", missingModelIssue, tc.selectedModel)
			}
		})
	}
}

func TestSettingsOverviewModelInventoryUsesWorkspaceConfigurationAndBrands(t *testing.T) {
	db := newUIPreferencesTestDB(t)
	now := time.Now().UTC()
	for _, item := range []struct {
		id, name, key                         string
		custom, verified, model, defaultModel bool
	}{
		{"qwen", "Qwen", "saved", false, false, false, false},
		{"qwen-coding", "QwenCodingPlan", "", false, true, false, false},
		{"custom-qwen", "Qwen", "saved", true, false, false, false},
		{"claude", "Claude", "", false, false, true, false},
		{"unverified-custom-defaults", "Custom", "", true, false, true, true},
	} {
		base := orm.BaseModel{CreateUserID: "u1", CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&orm.UserModelProvider{ID: item.id, Name: item.name, Category: "model", IsCustom: item.custom, BaseModel: base}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.UserModelProviderGroup{ID: item.id, UserModelProviderID: item.id, APIKey: item.key, IsVerified: item.verified, BaseModel: base}).Error; err != nil {
			t.Fatal(err)
		}
		if item.model {
			if err := db.Create(&orm.UserModelProviderGroupModel{ID: item.id, UserModelProviderID: item.id, UserModelProviderGroupID: item.id, Name: "saved-model", ModelType: "llm", IsDefault: item.defaultModel, BaseModel: base}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	overview, err := buildSettingsOverview(httptest.NewRequest(http.MethodGet, "/settings/overview", nil), db.DB, "u1")
	if err != nil {
		t.Fatal(err)
	}
	counts := sectionByID(overview.Sections, "models").Counts
	if counts.ModelProviders != 3 || counts.ExternalServices != 0 {
		t.Fatalf("unexpected inventory breakdown: %#v", counts)
	}
	if got := counts.Configured; got != 3 {
		t.Fatalf("configured navigation nodes = %d, want 3 (Qwen brand, custom Qwen, model-only Claude)", got)
	}
}
