package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/mcp"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
)

const chatToolsPath = "/api/chat/tools"

type chatToolGroup map[string]any

type chatToolsResponse struct {
	ToolGroups []chatToolGroup `json:"tool_groups"`
	Page       int             `json:"page"`
	PageSize   int             `json:"page_size"`
	Total      int             `json:"total"`
}

type toolListQuery struct {
	Keyword string
}

func ListTools(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	if strings.TrimSpace(userID) == "" {
		userID = "0"
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	toolsResp, err := fetchChatTools(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "chat service unavailable", err), http.StatusBadGateway)
		return
	}
	disabled, err := listDisabledToolNames(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "query disabled tools failed", http.StatusInternalServerError)
		return
	}
	markDisabledTools(toolsResp.ToolGroups, disabled)
	applyToolListQuery(toolsResp, parseToolListQuery(r))
	common.ReplyOK(w, toolsResp)
}

func DisableTool(w http.ResponseWriter, r *http.Request) {
	setToolDisabled(w, r, true)
}

func EnableTool(w http.ResponseWriter, r *http.Request) {
	setToolDisabled(w, r, false)
}

func setToolDisabled(w http.ResponseWriter, r *http.Request, disabled bool) {
	userID := store.UserID(r)
	if strings.TrimSpace(userID) == "" {
		userID = "0"
	}
	userName := store.UserName(r)
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	toolName := strings.TrimSpace(mux.Vars(r)["tool_name"])
	if toolName == "" {
		common.ReplyErr(w, "tool_name required", http.StatusBadRequest)
		return
	}

	if disabled {
		toolsResp, err := fetchChatTools(r.Context(), db, userID)
		if err != nil {
			common.ReplyErr(w, fmt.Sprintf("%s: %v", "chat service unavailable", err), http.StatusBadGateway)
			return
		}
		group, ok := findToolGroup(toolsResp.ToolGroups, toolName)
		if !ok {
			common.ReplyErr(w, "tool not found", http.StatusNotFound)
			return
		}
		canDisable, _ := group["can_disable"].(bool)
		if !canDisable {
			common.ReplyErr(w, "tool cannot be disabled", http.StatusBadRequest)
			return
		}
		if err := disableToolForUser(r.Context(), db, userID, userName, toolName); err != nil {
			common.ReplyErr(w, "disable tool failed", http.StatusInternalServerError)
			return
		}
	} else if err := enableToolForUser(r.Context(), db, userID, toolName); err != nil {
		common.ReplyErr(w, "enable tool failed", http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, map[string]any{
		"name":     toolName,
		"disabled": disabled,
	})
}

func fetchChatTools(ctx context.Context, db *gorm.DB, userID string) (*chatToolsResponse, error) {
	reqBody := map[string]any{}
	if err := applyChatRuntimeConfigs(ctx, db, userID, reqBody); err != nil {
		return nil, err
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	respBytes, statusCode, err := common.HTTPPost(ctx, common.JoinURL(chatServiceURL(), chatToolsPath), "application/json", bodyBytes)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream %s returned %d", chatToolsPath, statusCode)
	}
	var out chatToolsResponse
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return nil, err
	}
	if out.ToolGroups == nil {
		out.ToolGroups = []chatToolGroup{}
	}
	return &out, nil
}

func applyChatRuntimeConfigs(ctx context.Context, db *gorm.DB, userID string, body map[string]any) error {
	if db == nil {
		return nil
	}
	llmConfig, err := modelconfig.LoadLLMConfig(ctx, db, userID)
	if err != nil {
		return err
	}
	if len(llmConfig) > 0 {
		body["llm_config"] = llmConfig
	}
	toolConfig, err := loadChatToolConfig(ctx, db, userID)
	if err != nil {
		return err
	}
	if len(toolConfig) > 0 {
		body["tool_config"] = toolConfig
	}
	agentConfig := loadUserAgentConfig(ctx, db, userID, body)
	if len(agentConfig) > 0 {
		// Merge into existing agentic_config body key, or set it.
		if existing, ok := body["agentic_config"].(map[string]any); ok {
			for k, v := range agentConfig {
				existing[k] = v
			}
		} else {
			body["agentic_config"] = agentConfig
		}
	}
	return nil
}

// loadUserAgentConfig reads per-user defaults from user_chat_settings and applies
// conversation-level overrides from the Conversation row when conversation_id is
// present in body. The result is a partial agentic_config dict ready to merge.
// It never returns an error; on DB failure it returns an empty map.
func loadUserAgentConfig(ctx context.Context, db *gorm.DB, userID string, body map[string]any) map[string]any {
	out := map[string]any{}

	// Load user-level defaults.
	var settings orm.UserChatSettings
	if err := db.WithContext(ctx).Where("user_id = ?", userID).First(&settings).Error; err == nil {
		out["enable_plugin"] = settings.EnablePlugin
		out["plugin_mode"] = settings.PluginMode
		out["enable_subagent"] = settings.EnableSubagent
	}

	// Apply conversation-level overrides when present.
	convID, _ := body["conversation_id"].(string)
	if convID != "" {
		var conv orm.Conversation
		if err := db.WithContext(ctx).Where("id = ?", convID).First(&conv).Error; err == nil {
			if conv.EnablePlugin != nil {
				out["enable_plugin"] = *conv.EnablePlugin
			}
			if conv.PluginMode != nil {
				out["plugin_mode"] = *conv.PluginMode
			}
			if conv.EnableSubagent != nil {
				out["enable_subagent"] = *conv.EnableSubagent
			}
		}
	}
	return out
}

func applyMCPRuntimeConfig(ctx context.Context, db *gorm.DB, userID string, body map[string]any) {
	mcpConfig, err := mcp.LoadRuntimeConfig(ctx, db, userID)
	if err != nil {
		fmt.Printf("[Core] [MCP_CONFIG] failed to load for user %s: %v\n", userID, err)
	} else if len(mcpConfig) > 0 {
		body["mcp_config"] = mcpConfig
	}
}

func loadChatToolConfig(ctx context.Context, db *gorm.DB, userID string) (map[string]any, error) {
	var toolConfig map[string]any
	if cloudToolConfig, err := fetchCloudToolConfig(ctx, userID); err != nil {
		fmt.Printf("[Core] [CLOUD_TOOL_TOKEN] failed to fetch cloud tool tokens for user %s: %v\n", userID, err)
	} else if len(cloudToolConfig) > 0 {
		toolConfig = mergeToolConfig(toolConfig, cloudToolConfig)
	}
	if searchConfig, err := searchToolConfigEntry(ctx, db, userID); err != nil {
		fmt.Printf("[Core] [SEARCH_TOOL_CONFIG] failed to load search tool config for user %s: %v\n", userID, err)
	} else if len(searchConfig) > 0 {
		toolConfig = mergeToolConfig(toolConfig, searchConfig)
	}
	return toolConfig, nil
}

func findToolGroup(groups []chatToolGroup, toolName string) (chatToolGroup, bool) {
	for _, group := range groups {
		name, _ := group["name"].(string)
		if name == toolName {
			return group, true
		}
	}
	return nil, false
}

func markDisabledTools(groups []chatToolGroup, disabled []string) {
	disabledSet := make(map[string]struct{}, len(disabled))
	for _, name := range disabled {
		disabledSet[name] = struct{}{}
	}
	for _, group := range groups {
		name, _ := group["name"].(string)
		_, isDisabled := disabledSet[name]
		group["disabled"] = isDisabled
	}
}

func parseToolListQuery(r *http.Request) toolListQuery {
	q := r.URL.Query()
	return toolListQuery{
		Keyword: strings.TrimSpace(q.Get("keyword")),
	}
}

func applyToolListQuery(resp *chatToolsResponse, query toolListQuery) {
	if resp == nil {
		return
	}
	filtered := filterToolGroups(resp.ToolGroups, query.Keyword)
	total := len(filtered)
	sortToolGroupsByDisabled(filtered)
	resp.ToolGroups = filtered
	resp.Page = 1
	resp.PageSize = total
	resp.Total = total
}

func filterToolGroups(groups []chatToolGroup, keyword string) []chatToolGroup {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return groups
	}
	filtered := make([]chatToolGroup, 0, len(groups))
	for _, group := range groups {
		if toolGroupMatchesKeyword(group, keyword) {
			filtered = append(filtered, group)
		}
	}
	return filtered
}

func toolGroupMatchesKeyword(group chatToolGroup, keyword string) bool {
	for _, field := range []string{"name", "label", "description"} {
		value, _ := group[field].(string)
		if strings.Contains(strings.ToLower(value), keyword) {
			return true
		}
	}
	return false
}

func sortToolGroupsByDisabled(groups []chatToolGroup) {
	sort.SliceStable(groups, func(i, j int) bool {
		return !toolGroupDisabled(groups[i]) && toolGroupDisabled(groups[j])
	})
}

func toolGroupDisabled(group chatToolGroup) bool {
	disabled, _ := group["disabled"].(bool)
	return disabled
}

func listDisabledToolNames(ctx context.Context, db *gorm.DB, userID string) ([]string, error) {
	userID = strings.TrimSpace(userID)
	if db == nil || userID == "" {
		return nil, nil
	}
	var rows []orm.UserDisabledTool
	if err := db.WithContext(ctx).
		Where("create_user_id = ? AND deleted_at IS NULL", userID).
		Order("tool_name ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		name := strings.TrimSpace(row.ToolName)
		if name != "" {
			out = append(out, name)
		}
	}
	return out, nil
}

func disableToolForUser(ctx context.Context, db *gorm.DB, userID, userName, toolName string) error {
	userID = strings.TrimSpace(userID)
	toolName = strings.TrimSpace(toolName)
	if db == nil || userID == "" || toolName == "" {
		return nil
	}
	now := time.Now()
	row := orm.UserDisabledTool{
		ToolName:       toolName,
		CreateUserID:   userID,
		CreateUserName: strings.TrimSpace(userName),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "create_user_id"},
			{Name: "tool_name"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"create_user_name": row.CreateUserName,
			"updated_at":       now,
			"deleted_at":       nil,
		}),
	}).Create(&row).Error
}

func enableToolForUser(ctx context.Context, db *gorm.DB, userID, toolName string) error {
	userID = strings.TrimSpace(userID)
	toolName = strings.TrimSpace(toolName)
	if db == nil || userID == "" || toolName == "" {
		return nil
	}
	err := db.WithContext(ctx).
		Where("create_user_id = ? AND tool_name = ?", userID, toolName).
		Delete(&orm.UserDisabledTool{}).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

func mergeDisabledToolNames(left, right []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(left)+len(right))
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	for _, name := range left {
		add(name)
	}
	for _, name := range right {
		add(name)
	}
	return out
}
