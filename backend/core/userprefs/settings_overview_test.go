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
