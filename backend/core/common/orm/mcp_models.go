package orm

import (
	"encoding/json"
	"time"
)

type MCPServer struct {
	ID               string          `gorm:"column:id;type:varchar(64);primaryKey"`
	Name             string          `gorm:"column:name;type:varchar(255);not null"`
	Transport        string          `gorm:"column:transport;type:varchar(32);not null"`
	URL              string          `gorm:"column:url;type:text;not null;default:''"`
	HeadersJSON      json.RawMessage `gorm:"column:headers_json;type:json;not null"`
	AllowedToolsJSON json.RawMessage `gorm:"column:allowed_tools_json;type:json;not null"`
	Enabled          bool            `gorm:"column:enabled;type:boolean;not null;default:false"`
	IsVerified       bool            `gorm:"column:is_verified;type:boolean;not null;default:false"`
	Share            bool            `gorm:"column:share;type:boolean;not null;default:false"`
	Timeout          int             `gorm:"column:timeout;not null;default:5"`
	BaseModel
}

func (MCPServer) TableName() string { return "mcp_servers" }

type MCPServerTool struct {
	ID               string          `gorm:"column:id;type:varchar(64);primaryKey"`
	MCPServerID      string          `gorm:"column:mcp_server_id;type:varchar(64);not null;index:idx_mcp_tools_server,priority:1"`
	ToolName         string          `gorm:"column:tool_name;type:varchar(255);not null"`
	Description      string          `gorm:"column:description;type:text;not null;default:''"`
	InputSchemaJSON  json.RawMessage `gorm:"column:input_schema_json;type:json;not null"`
	LastDiscoveredAt time.Time       `gorm:"column:last_discovered_at;not null"`
	CreatedAt        time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt        time.Time       `gorm:"column:updated_at;not null"`
	DeletedAt        *time.Time      `gorm:"column:deleted_at;index:idx_mcp_tools_server,priority:2"`
}

func (MCPServerTool) TableName() string { return "mcp_server_tools" }
