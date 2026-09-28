package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"lazymind/core/settings"
)

const notionMCPURL = "https://mcp.notion.com/mcp"

// Only the definition is shared. Both the server key and OAuth grant are personal.
func builtinNotion(userID string) orm.MCPServer {
	userID = strings.TrimSpace(userID)
	hash := sha256.Sum256([]byte(userID))
	return orm.MCPServer{
		ID: "msp_notion_" + hex.EncodeToString(hash[:16]), Name: "Notion", URL: notionMCPURL,
		Transport: transportHTTP, AuthType: "oauth", DiscoveryEnabled: true, Timeout: 30,
		HeadersJSON: json.RawMessage(`{}`),
		// Fixed read-only policy, intersected with the actual discovered tools.
		// https://developers.notion.com/guides/mcp/mcp-supported-tools
		AllowedToolsJSON: json.RawMessage(`["notion-search","notion-ai-search","notion-fetch","notion-get-tool-access","notion-query-data-sources","notion-get-users","notion-get-teams","notion-get-comments"]`),
		BaseModel:        orm.BaseModel{CreateUserID: userID},
	}
}

func isBuiltinNotion(row orm.MCPServer) bool {
	return row.CreateUserID != "" && row.ID == builtinNotion(row.CreateUserID).ID &&
		row.URL == notionMCPURL && row.AuthType == "oauth" && row.Transport == transportHTTP && !row.Share
}

func hasNotionConfiguration(ctx context.Context, db *gorm.DB, userID string) (bool, error) {
	var count int64
	// Preserve disabled configurations and explicit removal of the builtin itself.
	// A deleted legacy server is no longer configured and must not hide the builtin.
	err := db.WithContext(ctx).Model(&orm.MCPServer{}).
		Where("create_user_id = ? AND (id = ? OR (url = ? AND deleted_at IS NULL))", userID, builtinNotion(userID).ID, notionMCPURL).Count(&count).Error
	return count > 0, err
}

func authorizeServer(ctx context.Context, db *gorm.DB, userID, id string) (*orm.MCPServer, error) {
	userID = strings.TrimSpace(userID)
	if db == nil || userID == "" {
		return nil, errForbidden
	}
	builtin := builtinNotion(userID)
	if id != builtin.ID {
		return getOwnedServer(ctx, db, userID, id)
	}
	controls, err := settings.LoadFeatureControls(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	if !controls.MCPEnabled {
		return nil, errForbidden
	}
	exists, err := hasNotionConfiguration(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	if !exists {
		now := time.Now()
		builtin.CreatedAt, builtin.UpdatedAt = now, now
		// Concurrent clicks create one personal record, never a shared connection.
		if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Select("*").Create(&builtin).Error; err != nil {
			return nil, err
		}
	}
	return getOwnedServer(ctx, db, userID, id)
}
