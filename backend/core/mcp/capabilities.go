package mcp

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/settings"
)

// CapabilityConfig is a projection of an existing server, not an executable grant.
// Runtime and cached schemas are populated only for currently authorized tools.
type CapabilityConfig struct {
	Service           string         `json:"service"`
	Label             string         `json:"label"`
	Status            string         `json:"status"`
	Runtime           *RuntimeConfig `json:"runtime,omitempty"`
	Tools             []ToolResponse `json:"tools,omitempty"`
	ToolsDiscoveredAt *time.Time     `json:"tools_discovered_at,omitempty"`
	ToolsComplete     bool           `json:"tools_complete"`
}

func LoadCapabilities(ctx context.Context, db *gorm.DB, userID string) ([]CapabilityConfig, error) {
	return loadCapabilities(ctx, db, userID, "", true)
}

// LoadCapability resolves only the requested service, without loading cached schemas.
// A missing or inaccessible service returns nil; it never probes another service.
func LoadCapability(ctx context.Context, db *gorm.DB, userID, serviceID string) (*CapabilityConfig, error) {
	serviceID = strings.TrimPrefix(strings.TrimSpace(serviceID), "mcp:")
	if serviceID == "" {
		return nil, nil
	}
	items, err := loadCapabilities(ctx, db, userID, serviceID, false)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

func loadCapabilities(ctx context.Context, db *gorm.DB, userID, serviceID string, includeSchemas bool) ([]CapabilityConfig, error) {
	if db == nil {
		return nil, nil
	}
	userID = strings.TrimSpace(userID)
	controls, err := settings.LoadFeatureControls(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	q := db.WithContext(ctx).Where("(enabled = ? OR discovery_enabled = ?) AND deleted_at IS NULL AND transport IN ?", true, true, []string{transportSSE, transportHTTP})
	if userID == "" || !controls.MCPEnabled {
		q = q.Where("share = ? AND enabled = ? AND create_user_id <> ?", true, true, userID)
	} else {
		q = q.Where("(create_user_id = ? OR (share = ? AND enabled = ?))", userID, true, true)
	}
	if serviceID != "" {
		q = q.Where("id = ?", serviceID)
	}
	var rows []orm.MCPServer
	if err := q.Order("share ASC, updated_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]CapabilityConfig, 0, len(rows))
	for _, row := range dedupeServers(rows) {
		if effectiveAuthType(row) == "oauth" && (userID == "" || row.CreateUserID != userID || row.Share) {
			continue
		}
		item, err := resolveCapability(ctx, db, userID, row)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if userID != "" && controls.MCPEnabled && (serviceID == "" || serviceID == builtinNotion(userID).ID) {
		exists, err := hasNotionConfiguration(ctx, db, userID)
		if err != nil {
			return nil, err
		}
		if !exists {
			row := builtinNotion(userID)
			out = append(out, CapabilityConfig{Service: "mcp:" + row.ID, Label: row.Name, Status: "needs_authorization"})
		}
	}
	if includeSchemas {
		if err := loadCapabilitySchemas(ctx, db, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func resolveCapability(ctx context.Context, db *gorm.DB, userID string, row orm.MCPServer) (CapabilityConfig, error) {
	item := CapabilityConfig{Service: "mcp:" + row.ID, Label: row.Name, Status: "needs_configuration"}
	allowed, err := canonicalizeAllowedToolNames(ctx, db, row.ID, parseStringJSON(row.AllowedToolsJSON))
	if err != nil {
		return item, err
	}
	var oauthRef *OAuthReference
	if effectiveAuthType(row) == "oauth" {
		status, err := oauthOperation(ctx, row, "status", nil)
		if err != nil {
			item.Status = "unavailable"
			return item, nil
		}
		if status.Status != "authorized" {
			item.Status = "needs_authorization"
			return item, nil
		}
		oauthRef = &OAuthReference{UserID: userID, ServerID: row.ID, ServerURL: row.URL, GrantID: status.GrantID, GrantVersion: status.GrantVersion}
		if len(allowed) == 0 {
			item.Status = "needs_tool_selection"
			return item, nil
		}
	} else if row.IsVerified && len(allowed) == 0 {
		item.Status = "needs_tool_selection"
	}
	if !row.Enabled || !row.IsVerified || len(allowed) == 0 {
		return item, nil
	}
	headers, err := decodeHeaders(row.HeadersJSON)
	if err != nil {
		item.Status = "unavailable"
		return item, nil
	}
	if oauthRef != nil {
		headers = nil
	}
	item.Status = "ready"
	item.Runtime = &RuntimeConfig{OAuth: oauthRef, ID: row.ID, Name: row.Name, Transport: row.Transport, URL: row.URL, Headers: headers, AllowedTools: allowed, Timeout: normalizedTimeout(row.Timeout)}
	return item, nil
}

func loadCapabilitySchemas(ctx context.Context, db *gorm.DB, items []CapabilityConfig) error {
	byID := map[string]*CapabilityConfig{}
	ids := []string{}
	for i := range items {
		if items[i].Runtime != nil {
			id := items[i].Runtime.ID
			ids = append(ids, id)
			byID[id] = &items[i]
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var rows []orm.MCPServerTool
	if err := db.WithContext(ctx).Where("mcp_server_id IN ? AND deleted_at IS NULL", ids).Order("tool_name ASC").Find(&rows).Error; err != nil {
		return err
	}
	allowed := map[string]map[string]bool{}
	for id, item := range byID {
		allowed[id] = map[string]bool{}
		for _, name := range item.Runtime.AllowedTools {
			allowed[id][name] = true
		}
	}
	for _, row := range rows {
		if !allowed[row.MCPServerID][row.ToolName] {
			continue
		}
		item := byID[row.MCPServerID]
		item.Tools = append(item.Tools, toolResponse(row))
		// The oldest included discovery timestamp conservatively describes freshness.
		if !row.LastDiscoveredAt.IsZero() && (item.ToolsDiscoveredAt == nil || row.LastDiscoveredAt.Before(*item.ToolsDiscoveredAt)) {
			discoveredAt := row.LastDiscoveredAt
			item.ToolsDiscoveredAt = &discoveredAt
		}
		delete(allowed[row.MCPServerID], row.ToolName)
	}
	for id, item := range byID {
		item.ToolsComplete = len(allowed[id]) == 0
	}
	return nil
}
