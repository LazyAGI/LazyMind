package mcp

import (
	"context"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/settings"
	"strings"
)

// CapabilityConfig is a projection of an existing server, not an executable grant.
// Runtime is populated only by the existing authorization/allowlist path.
type CapabilityConfig struct {
	Service string         `json:"service"`
	Label   string         `json:"label"`
	Status  string         `json:"status"`
	Runtime *RuntimeConfig `json:"runtime,omitempty"`
}

func LoadCapabilities(ctx context.Context, db *gorm.DB, userID string) ([]CapabilityConfig, error) {
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
	var rows []orm.MCPServer
	if err := q.Order("share ASC, updated_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	runtimes, err := LoadRuntimeConfig(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	byID := map[string]RuntimeConfig{}
	for _, runtime := range runtimes {
		byID[runtime.ID] = runtime
	}
	out := make([]CapabilityConfig, 0, len(rows))
	for _, row := range dedupeServers(rows) {
		if effectiveAuthType(row) == "oauth" && (row.CreateUserID != userID || row.Share) {
			continue
		}
		item := CapabilityConfig{Service: "mcp:" + row.ID, Label: row.Name, Status: "needs_configuration"}
		if runtime, ok := byID[row.ID]; ok {
			item.Status, item.Runtime = "ready", &runtime
		} else if effectiveAuthType(row) == "oauth" {
			status, err := oauthOperation(ctx, row, "status", nil)
			if err != nil {
				item.Status = "unavailable"
			} else if status.Status != "authorized" {
				item.Status = "needs_authorization"
			} else if len(parseStringJSON(row.AllowedToolsJSON)) == 0 {
				item.Status = "needs_tool_selection"
			}
		} else if row.IsVerified && len(parseStringJSON(row.AllowedToolsJSON)) == 0 {
			item.Status = "needs_tool_selection"
		}
		out = append(out, item)
	}
	if userID != "" && controls.MCPEnabled {
		exists, err := hasNotionConfiguration(ctx, db, userID)
		if err != nil {
			return nil, err
		}
		if !exists {
			row := builtinNotion(userID)
			out = append(out, CapabilityConfig{Service: "mcp:" + row.ID, Label: row.Name, Status: "needs_authorization"})
		}
	}
	return out, nil
}
