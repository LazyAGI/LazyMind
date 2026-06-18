package mcp

import (
	"encoding/json"
	"time"
)

const (
	transportSSE  = "sse"
	transportHTTP = "http"

	defaultTimeoutSeconds = 5
)

type ServerResponse struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Transport     string         `json:"transport"`
	URL           string         `json:"url"`
	APIKeyPreview string         `json:"api_key_preview,omitempty"`
	AllowedTools  []string       `json:"allowed_tools"`
	Enabled       bool           `json:"enabled"`
	IsVerified    bool           `json:"is_verified"`
	Share         bool           `json:"share"`
	Timeout       int            `json:"timeout"`
	ToolCount     int64          `json:"tool_count,omitempty"`
	Tools         []ToolResponse `json:"tools,omitempty"`
	CreateTime    time.Time      `json:"create_time"`
	UpdateTime    time.Time      `json:"update_time"`
}

type ToolResponse struct {
	ID          string          `json:"id"`
	ToolName    string          `json:"tool_name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ListServersResponse struct {
	MCPServers []ServerResponse `json:"mcp_servers"`
}

type CreateServerRequest struct {
	Name         string   `json:"name"`
	Transport    string   `json:"transport"`
	URL          string   `json:"url"`
	APIKey       string   `json:"api_key"`
	AllowedTools []string `json:"allowed_tools"`
	Enabled      *bool    `json:"enabled"`
	Timeout      int      `json:"timeout"`
}

type UpdateServerRequest struct {
	Name         *string  `json:"name"`
	URL          *string  `json:"url"`
	APIKey       *string  `json:"api_key"`
	AllowedTools []string `json:"allowed_tools"`
	Enabled      *bool    `json:"enabled"`
	Timeout      *int     `json:"timeout"`
}

type UpdateToolsRequest struct {
	AllowedTools []string `json:"allowed_tools"`
}

type CheckResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	ToolCount int    `json:"tool_count"`
}

type DiscoverResponse struct {
	Success bool           `json:"success"`
	Tools   []ToolResponse `json:"tools"`
}

type RuntimeConfig struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Transport    string         `json:"transport"`
	URL          string         `json:"url"`
	Headers      map[string]any `json:"headers,omitempty"`
	AllowedTools []string       `json:"allowed_tools"`
	Timeout      int            `json:"timeout"`
}

type discoveredTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}
