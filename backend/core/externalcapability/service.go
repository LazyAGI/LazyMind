package externalcapability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/capability"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/mcp"
	"lazymind/core/modelconfig"
	"lazymind/core/modelprovider"
)

const (
	CapabilityModel   = "model"
	CapabilityTool    = "tool"
	maxProxyBytes     = 2 << 20
	modelTimeout      = 2 * time.Minute
	builtinToolPrefix = "builtin:"
)

var supportedAgents = map[string]struct{}{
	"codex": {}, "cursor": {}, "workbuddy": {}, "raccoon": {},
	"traework": {}, "deepseek-harness": {},
}

type Service struct {
	db         *gorm.DB
	httpClient *http.Client
}

func New(db *gorm.DB, httpClient *http.Client) *Service {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: modelTimeout}
	}
	return &Service{db: db, httpClient: httpClient}
}

type CapabilityItem struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Source      string          `json:"source"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	Available   bool            `json:"available"`
	Reason      string          `json:"reason,omitempty"`
	Authorized  bool            `json:"authorized"`
}

type Inventory struct {
	Agent        string           `json:"agent"`
	Capabilities []CapabilityItem `json:"capabilities"`
}

type InvocationCapabilitySummary struct {
	Agent          string `json:"agent" gorm:"column:agent"`
	CapabilityType string `json:"capability_type" gorm:"column:capability_type"`
	CapabilityID   string `json:"capability_id" gorm:"column:capability_id"`
	CapabilityName string `json:"capability_name" gorm:"column:capability_name"`
	CallCount      int64  `json:"call_count" gorm:"column:call_count"`
	Succeeded      int64  `json:"succeeded" gorm:"column:succeeded"`
	Failed         int64  `json:"failed" gorm:"column:failed"`
}

type InvocationSummary struct {
	Total        int64                         `json:"total"`
	Succeeded    int64                         `json:"succeeded"`
	Failed       int64                         `json:"failed"`
	Running      int64                         `json:"running"`
	ModelCalls   int64                         `json:"model_calls"`
	ToolCalls    int64                         `json:"tool_calls"`
	Capabilities []InvocationCapabilitySummary `json:"capabilities"`
}

type InvocationPage struct {
	Invocations []orm.ExternalCapabilityInvocation `json:"invocations"`
	Total       int64                              `json:"total"`
	Summary     InvocationSummary                  `json:"summary"`
}

type GrantUpdate struct {
	Agent          string `json:"agent"`
	CapabilityType string `json:"capability_type"`
	CapabilityID   string `json:"capability_id"`
	Enabled        bool   `json:"enabled"`
}

type modelRow struct {
	ID                       string `gorm:"column:id"`
	Name                     string `gorm:"column:name"`
	ModelType                string `gorm:"column:model_type"`
	ProviderName             string `gorm:"column:provider_name"`
	GroupName                string `gorm:"column:group_name"`
	GroupID                  string `gorm:"column:group_id"`
	BaseURL                  string `gorm:"column:base_url"`
	APIKey                   string `gorm:"column:api_key"`
	APIKeyCiphertext         string `gorm:"column:api_key_ciphertext"`
	IsVerified               bool   `gorm:"column:is_verified"`
	ProviderDeletedAtPresent bool   `gorm:"column:provider_deleted"`
	GroupDeletedAtPresent    bool   `gorm:"column:group_deleted"`
}

type toolRow struct {
	ID                string          `gorm:"column:id"`
	Name              string          `gorm:"column:tool_name"`
	Description       string          `gorm:"column:description"`
	InputSchema       json.RawMessage `gorm:"column:input_schema_json"`
	ServerID          string          `gorm:"column:server_id"`
	ServerName        string          `gorm:"column:server_name"`
	AllowedToolsRaw   json.RawMessage `gorm:"column:allowed_tools_json"`
	Enabled           bool            `gorm:"column:enabled"`
	IsVerified        bool            `gorm:"column:is_verified"`
	Builtin           bool            `gorm:"-"`
	UnavailableReason string          `gorm:"-"`
}

func NormalizeAgent(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if _, ok := supportedAgents[value]; !ok {
		return "", fmt.Errorf("unsupported external Agent")
	}
	return value, nil
}

func (s *Service) Inventory(ctx context.Context, userID, agent string) (Inventory, error) {
	if s == nil || s.db == nil {
		return Inventory{}, errors.New("store not initialized")
	}
	agent, err := NormalizeAgent(agent)
	if err != nil {
		return Inventory{}, err
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return Inventory{}, errors.New("missing user id")
	}
	grants, err := s.grants(ctx, userID, agent)
	if err != nil {
		return Inventory{}, err
	}
	models, err := s.models(ctx, userID)
	if err != nil {
		return Inventory{}, err
	}
	tools, err := s.allTools(ctx, userID)
	if err != nil {
		return Inventory{}, err
	}
	items := make([]CapabilityItem, 0, len(models)+len(tools))
	for _, row := range models {
		available, reason := modelAvailability(row)
		authorized, explicit := grants[grantKey(CapabilityModel, row.ID)]
		if !explicit {
			authorized = available
		}
		items = append(items, CapabilityItem{
			ID: row.ID, Type: CapabilityModel, Name: row.Name,
			Source:    strings.Trim(strings.Join([]string{row.ProviderName, row.GroupName}, " · "), " ·"),
			Available: available, Reason: reason, Authorized: authorized,
		})
	}
	for _, row := range tools {
		available, reason := toolAvailability(row)
		authorized, explicit := grants[grantKey(CapabilityTool, row.ID)]
		if !explicit {
			authorized = available
		}
		items = append(items, CapabilityItem{
			ID: row.ID, Type: CapabilityTool, Name: row.Name, Source: row.ServerName,
			Description: row.Description, InputSchema: row.InputSchema,
			Available: available, Reason: reason, Authorized: authorized,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Type != items[j].Type {
			return items[i].Type < items[j].Type
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return Inventory{Agent: agent, Capabilities: items}, nil
}

func (s *Service) SetGrant(ctx context.Context, userID string, update GrantUpdate) error {
	if s == nil || s.db == nil {
		return errors.New("store not initialized")
	}
	userID = strings.TrimSpace(userID)
	agent, err := NormalizeAgent(update.Agent)
	if err != nil {
		return err
	}
	kind, capabilityID := strings.ToLower(strings.TrimSpace(update.CapabilityType)), strings.TrimSpace(update.CapabilityID)
	if userID == "" || capabilityID == "" || (kind != CapabilityModel && kind != CapabilityTool) {
		return errors.New("invalid capability grant")
	}
	if update.Enabled {
		available, err := s.isAvailable(ctx, userID, kind, capabilityID)
		if err != nil {
			return err
		}
		if !available {
			return errors.New("capability is unavailable; verify and enable its connection first")
		}
	}
	now := time.Now().UTC()
	row := orm.ExternalCapabilityGrant{
		ID: "ecg_" + common.GenerateID(), OwnerUserID: userID, Agent: agent,
		CapabilityType: kind, CapabilityID: capabilityID, Enabled: update.Enabled,
		CreatedAt: now, UpdatedAt: now,
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "owner_user_id"}, {Name: "agent"}, {Name: "capability_type"}, {Name: "capability_id"}},
		DoUpdates: clause.Assignments(map[string]any{"enabled": update.Enabled, "updated_at": now}),
	}).Create(&row).Error
}

func (s *Service) InvocationHistory(ctx context.Context, userID, agent string, limit int) (InvocationPage, error) {
	if s == nil || s.db == nil {
		return InvocationPage{}, errors.New("store not initialized")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return InvocationPage{}, errors.New("missing user id")
	}
	agent = strings.TrimSpace(agent)
	if agent != "" {
		var err error
		agent, err = NormalizeAgent(agent)
		if err != nil {
			return InvocationPage{}, err
		}
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	filtered := func() *gorm.DB {
		query := s.db.WithContext(ctx).Model(&orm.ExternalCapabilityInvocation{}).
			Where("owner_user_id = ?", userID)
		if agent != "" {
			query = query.Where("agent = ?", agent)
		}
		return query
	}

	var total int64
	if err := filtered().Count(&total).Error; err != nil {
		return InvocationPage{}, err
	}
	aggregates := []InvocationCapabilitySummary{}
	if err := filtered().
		Select("agent, capability_type, capability_id, " +
			"COALESCE(NULLIF(MAX(CASE WHEN capability_name <> capability_id THEN capability_name ELSE '' END), ''), capability_id) AS capability_name, " +
			"COUNT(*) AS call_count, " +
			"SUM(CASE WHEN status = 'succeeded' THEN 1 ELSE 0 END) AS succeeded, " +
			"SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) AS failed").
		Group("agent, capability_type, capability_id").
		Order("call_count DESC, capability_name ASC").
		Scan(&aggregates).Error; err != nil {
		return InvocationPage{}, err
	}
	summary := InvocationSummary{Total: total, Capabilities: aggregates}
	for _, item := range aggregates {
		summary.Succeeded += item.Succeeded
		summary.Failed += item.Failed
		if item.CapabilityType == CapabilityModel {
			summary.ModelCalls += item.CallCount
		} else if item.CapabilityType == CapabilityTool {
			summary.ToolCalls += item.CallCount
		}
	}
	summary.Running = summary.Total - summary.Succeeded - summary.Failed

	rows := []orm.ExternalCapabilityInvocation{}
	if err := filtered().Order("started_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return InvocationPage{}, err
	}
	for i := range rows {
		if rows[i].CapabilityType == CapabilityTool && rows[i].CapabilityID == "builtin:image_generator" {
			rows[i].ResultJSON = orm.InvocationResultJSON(rewriteImageResult(rows[i].ResultJSON, true))
		}
	}
	return InvocationPage{Invocations: rows, Total: total, Summary: summary}, nil
}

func (s *Service) ListExternalModels(ctx context.Context, call capability.InvocationContext) (capability.ListExternalModelsResult, error) {
	agent, err := NormalizeAgent(call.ExternalAgent)
	if err != nil {
		return capability.ListExternalModelsResult{}, capability.NewError(capability.PermissionDenied, "model.list", "external Agent is not supported", false, nil)
	}
	rows, err := s.models(ctx, call.Principal.UserID)
	if err != nil {
		return capability.ListExternalModelsResult{}, capability.NewError(capability.Internal, "model.list", "cannot load models", false, err)
	}
	grants, err := s.grants(ctx, call.Principal.UserID, agent)
	if err != nil {
		return capability.ListExternalModelsResult{}, capability.NewError(capability.Internal, "model.list", "cannot load authorizations", false, err)
	}
	items := []capability.ExternalModelSummary{}
	for _, row := range rows {
		available, _ := modelAvailability(row)
		if !available || !capabilityGranted(grants, CapabilityModel, row.ID) {
			continue
		}
		items = append(items, capability.ExternalModelSummary{
			ID: row.ID, Name: row.Name, ProviderName: row.ProviderName, GroupName: row.GroupName, ModelType: row.ModelType,
		})
	}
	return capability.ListExternalModelsResult{Items: items}, nil
}

func (s *Service) ListExternalTools(ctx context.Context, call capability.InvocationContext) (capability.ListExternalToolsResult, error) {
	agent, err := NormalizeAgent(call.ExternalAgent)
	if err != nil {
		return capability.ListExternalToolsResult{}, capability.NewError(capability.PermissionDenied, "tool.list", "external Agent is not supported", false, nil)
	}
	rows, err := s.allTools(ctx, call.Principal.UserID)
	if err != nil {
		return capability.ListExternalToolsResult{}, capability.NewError(capability.Internal, "tool.list", "cannot load tools", false, err)
	}
	grants, err := s.grants(ctx, call.Principal.UserID, agent)
	if err != nil {
		return capability.ListExternalToolsResult{}, capability.NewError(capability.Internal, "tool.list", "cannot load authorizations", false, err)
	}
	items := []capability.ExternalToolSummary{}
	for _, row := range rows {
		available, _ := toolAvailability(row)
		if !available || !capabilityGranted(grants, CapabilityTool, row.ID) {
			continue
		}
		schema := map[string]any{}
		_ = json.Unmarshal(row.InputSchema, &schema)
		items = append(items, capability.ExternalToolSummary{ID: row.ID, Name: row.Name, ServerName: row.ServerName, Description: row.Description, InputSchema: schema})
	}
	return capability.ListExternalToolsResult{Items: items}, nil
}

func (s *Service) InvokeExternalModel(ctx context.Context, call capability.InvocationContext, input capability.InvokeExternalModelInput) (result capability.InvokeExternalModelResult, finalErr error) {
	const op = "model.chat"
	audit := s.startAudit(ctx, call, CapabilityModel, input.ModelID)
	defer func() {
		preview := any(nil)
		if finalErr == nil {
			preview = map[string]any{
				"id": result.ID, "model": result.Model, "content": result.Content,
				"finish_reason": result.FinishReason,
			}
		}
		s.finishAudit(call, audit, result.Model, result.Usage, preview, finalErr)
	}()
	agent, err := NormalizeAgent(call.ExternalAgent)
	if err != nil || !s.hasGrant(ctx, call.Principal.UserID, agent, CapabilityModel, input.ModelID) {
		return result, capability.NewError(capability.PermissionDenied, op, "model is not authorized for this Agent; enable it in Settings → Assistants", false, nil)
	}
	row, err := s.model(ctx, call.Principal.UserID, input.ModelID)
	if err != nil {
		return result, capability.NewError(capability.Unavailable, op, "model is unavailable; verify the model connection in Settings", false, err)
	}
	if available, reason := modelAvailability(row); !available {
		return result, capability.NewError(capability.Unavailable, op, reason+"; verify the model connection in Settings", false, nil)
	}
	apiKey, err := modelprovider.ResolveAPIKey(row.APIKey, row.APIKeyCiphertext)
	if err != nil {
		return result, capability.NewError(capability.Unavailable, op, "model credentials cannot be loaded; save the connection again", false, err)
	}
	payload := map[string]any{"model": row.Name, "messages": input.Messages, "stream": false}
	if input.Temperature != nil {
		payload["temperature"] = *input.Temperature
	}
	if input.MaxTokens > 0 {
		payload["max_tokens"] = input.MaxTokens
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatCompletionsURL(row.BaseURL), bytes.NewReader(body))
	if err != nil {
		return result, capability.NewError(capability.Internal, op, "cannot prepare model request", false, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return result, capability.NewError(capability.Unavailable, op, "model connection failed; verify it in Settings and try again", true, err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxProxyBytes+1))
	if readErr != nil || len(raw) > maxProxyBytes {
		return result, capability.NewError(capability.ResultTooLarge, op, "model response could not be read or exceeded 2 MiB", false, readErr)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return result, capability.NewError(
			capability.Unavailable,
			op,
			fmt.Sprintf("model request failed (%d); verify the model connection in Settings and try again", resp.StatusCode),
			resp.StatusCode >= 500,
			nil,
		)
	}
	var response struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage capability.ExternalModelUsage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || len(response.Choices) == 0 {
		return result, capability.NewError(capability.Unavailable, op, "model returned an invalid chat completion", false, err)
	}
	result = capability.InvokeExternalModelResult{
		ID: response.ID, Model: row.Name, Content: response.Choices[0].Message.Content,
		FinishReason: response.Choices[0].FinishReason, Usage: response.Usage,
	}
	return result, nil
}

func (s *Service) InvokeExternalTool(ctx context.Context, call capability.InvocationContext, input capability.InvokeExternalToolInput) (result capability.InvokeExternalToolResult, finalErr error) {
	const op = "tool.call"
	audit := s.startAudit(ctx, call, CapabilityTool, input.ToolID)
	toolName := ""
	var auditResult any
	defer func() {
		encoded, _ := json.Marshal(result.Result)
		preview := any(nil)
		if finalErr == nil {
			preview = auditResult
		}
		s.finishAudit(call, audit, toolName, map[string]any{"result_bytes": len(encoded)}, preview, finalErr)
	}()
	agent, err := NormalizeAgent(call.ExternalAgent)
	if err != nil || !s.hasGrant(ctx, call.Principal.UserID, agent, CapabilityTool, input.ToolID) {
		return result, capability.NewError(capability.PermissionDenied, op, "tool is not authorized for this Agent; enable it in Settings → Assistants", false, nil)
	}
	row, err := s.tool(ctx, call.Principal.UserID, input.ToolID)
	if err != nil {
		return result, capability.NewError(capability.Unavailable, op, "tool is unavailable; refresh capabilities and check its configuration in Settings", false, err)
	}
	if available, reason := toolAvailability(row); !available {
		return result, capability.NewError(capability.Unavailable, op, reason+"; check the tool connection and authorization in Settings", false, nil)
	}
	toolName = row.Name
	var raw json.RawMessage
	if row.Builtin {
		raw, err = s.callBuiltinTool(ctx, call.Principal.UserID, row.Name, input.Arguments)
	} else {
		raw, toolName, err = mcp.CallAuthorizedTool(ctx, s.db, call.Principal.UserID, input.ToolID, input.Arguments)
	}
	if err != nil {
		return result, capability.NewError(capability.Unavailable, op, "tool execution failed; check the tool connection and authorization in Settings", false, err)
	}
	if len(raw) > maxProxyBytes {
		return result, capability.NewError(capability.ResultTooLarge, op, "tool result exceeds 2 MiB", false, nil)
	}
	if err := json.Unmarshal(raw, &result.Result); err != nil {
		return result, capability.NewError(capability.Unavailable, op, "tool returned an invalid JSON result; check the tool connection in Settings", false, err)
	}
	// Audit the original local references, not the external presentation URLs.
	auditResult = result.Result
	if row.Builtin && row.Name == "image_generator" {
		var external any
		if err := json.Unmarshal(externalImageResult(raw), &external); err == nil {
			result.Result = external
		}
	}
	return result, nil
}

func (s *Service) models(ctx context.Context, userID string) ([]modelRow, error) {
	var rows []modelRow
	err := s.db.WithContext(ctx).Table("user_model_provider_group_models AS m").
		Select("m.id, m.name, m.model_type, m.provider_name, g.name AS group_name, g.id AS group_id, g.base_url, g.api_key, g.api_key_ciphertext, g.is_verified, "+
			"CASE WHEN p.deleted_at IS NULL THEN false ELSE true END AS provider_deleted, CASE WHEN g.deleted_at IS NULL THEN false ELSE true END AS group_deleted").
		Joins("JOIN user_model_provider_groups AS g ON g.id = m.user_model_provider_group_id").
		Joins("JOIN user_model_providers AS p ON p.id = m.user_model_provider_id").
		Where("m.create_user_id = ? AND m.deleted_at IS NULL", strings.TrimSpace(userID)).
		Order("m.provider_name, g.name, m.name").Scan(&rows).Error
	return rows, err
}

func (s *Service) model(ctx context.Context, userID, id string) (modelRow, error) {
	rows, err := s.models(ctx, userID)
	if err != nil {
		return modelRow{}, err
	}
	for _, row := range rows {
		if row.ID == id {
			return row, nil
		}
	}
	return modelRow{}, gorm.ErrRecordNotFound
}

func (s *Service) tools(ctx context.Context, userID string) ([]toolRow, error) {
	var rows []toolRow
	err := s.db.WithContext(ctx).Table("mcp_server_tools AS t").
		Select("t.id, t.tool_name, t.description, t.input_schema_json, s.id AS server_id, s.name AS server_name, s.allowed_tools_json, s.enabled, s.is_verified").
		Joins("JOIN mcp_servers AS s ON s.id = t.mcp_server_id").
		Where("s.create_user_id = ? AND s.deleted_at IS NULL AND t.deleted_at IS NULL", strings.TrimSpace(userID)).
		Order("s.name, t.tool_name").Scan(&rows).Error
	for i := range rows {
		rows[i].Builtin = false
	}
	return rows, err
}

func (s *Service) allTools(ctx context.Context, userID string) ([]toolRow, error) {
	rows, err := s.tools(ctx, userID)
	if err != nil {
		return nil, err
	}
	builtin, err := s.builtinTools(ctx, userID)
	if err != nil {
		return nil, err
	}
	return append(rows, builtin...), nil
}

func (s *Service) builtinTools(ctx context.Context, userID string) ([]toolRow, error) {
	raw, err := s.requestBuiltinTools(ctx, userID, "/api/chat/tools/external-catalog", "", nil)
	if err != nil {
		return nil, err
	}
	var catalog struct {
		Items []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
			Available   bool            `json:"available"`
			Reason      string          `json:"reason"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return nil, errors.New("invalid built-in tool catalog")
	}
	rows := make([]toolRow, 0, len(catalog.Items))
	for _, item := range catalog.Items {
		rows = append(rows, toolRow{
			ID: builtinToolPrefix + item.Name, Name: item.Name,
			Description: item.Description, InputSchema: item.InputSchema,
			ServerName: "LazyMind", Enabled: item.Available, IsVerified: item.Available,
			Builtin: true, UnavailableReason: item.Reason,
		})
	}
	return rows, nil
}

func (s *Service) tool(ctx context.Context, userID, id string) (toolRow, error) {
	rows, err := s.allTools(ctx, userID)
	if err != nil {
		return toolRow{}, err
	}
	for _, row := range rows {
		if row.ID == id {
			return row, nil
		}
	}
	return toolRow{}, gorm.ErrRecordNotFound
}

func (s *Service) callBuiltinTool(ctx context.Context, userID, name string, arguments map[string]any) (json.RawMessage, error) {
	raw, err := s.requestBuiltinTools(ctx, userID, "/api/chat/tools/execute", name, arguments)
	if err != nil {
		return nil, err
	}
	var response struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || len(response.Result) == 0 {
		return nil, errors.New("built-in tool returned an invalid result")
	}
	return response.Result, nil
}

func (s *Service) requestBuiltinTools(ctx context.Context, userID, endpoint, name string, arguments map[string]any) (json.RawMessage, error) {
	llmConfig, err := modelconfig.LoadLLMConfig(ctx, s.db, userID)
	if err != nil {
		return nil, fmt.Errorf("load built-in tool model: %w", err)
	}
	toolConfig := map[string]any{}
	// External calls must not inherit another user's shared or unverified provider.
	for category, capabilityName := range map[string]string{"search": "web_search", "datasource": "academic_search"} {
		var count int64
		err := s.db.WithContext(ctx).Table("user_selected_providers AS selected").
			Joins("JOIN user_model_provider_groups AS g ON g.id = selected.user_model_provider_group_id AND g.deleted_at IS NULL AND g.is_verified = ?", true).
			Joins("JOIN user_model_providers AS p ON p.id = g.user_model_provider_id AND p.deleted_at IS NULL").
			Where("selected.user_id = ? AND selected.category = ? AND g.create_user_id = ? AND p.create_user_id = ?", userID, category, userID, userID).Count(&count).Error
		if err != nil {
			return nil, errors.New("cannot check tool configuration")
		}
		if count == 0 || (name != "" && name != capabilityName) {
			continue
		}
		config, err := modelconfig.LoadToolConfigForCapabilities(ctx, s.db, userID, []string{capabilityName})
		if err != nil {
			return nil, errors.New("cannot load tool configuration")
		}
		for key, value := range config {
			toolConfig[key] = value
		}
	}
	var disabled []string
	if err := s.db.WithContext(ctx).Table("user_disabled_tools").Where("create_user_id = ? AND deleted_at IS NULL", userID).Pluck("tool_name", &disabled).Error; err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"tool_name": name, "arguments": arguments, "llm_config": llmConfig, "tool_config": toolConfig, "disabled_tools": disabled,
	})
	if err != nil {
		return nil, errors.New("encode built-in tool request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		common.JoinURL(common.ChatServiceEndpoint(), endpoint), bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("prepare built-in tool request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-LazyMind-Internal-Token", os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, errors.New("built-in tool service is unreachable")
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxProxyBytes+1))
	if readErr != nil || len(raw) > maxProxyBytes {
		return nil, errors.New("built-in tool result could not be read or exceeded 2 MiB")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("built-in tool request failed (%d)", resp.StatusCode)
	}
	return raw, nil
}

func modelAvailability(row modelRow) (bool, string) {
	if row.ProviderDeletedAtPresent || row.GroupDeletedAtPresent {
		return false, "model connection was deleted"
	}
	if !row.IsVerified {
		return false, "model connection is not verified"
	}
	modelType := strings.ToLower(strings.TrimSpace(row.ModelType))
	if modelType != "llm" && modelType != "vlm" {
		return false, "model type is not available through the chat interface"
	}
	if strings.TrimSpace(row.BaseURL) == "" {
		return false, "model connection is not configured"
	}
	return true, ""
}

func toolAvailability(row toolRow) (bool, string) {
	if row.Builtin && row.UnavailableReason != "" {
		return false, row.UnavailableReason
	}
	if !row.IsVerified {
		return false, "tool connection is not verified"
	}
	if !row.Enabled {
		if row.Builtin {
			return false, "tool is disabled"
		}
		return false, "tool connection is disabled"
	}
	if row.Builtin {
		return true, ""
	}
	allowed := []string{}
	_ = json.Unmarshal(row.AllowedToolsRaw, &allowed)
	for _, value := range allowed {
		if strings.TrimSpace(value) == row.ID || strings.TrimSpace(value) == row.Name {
			return true, ""
		}
	}
	return false, "tool is not enabled on its connection"
}

func (s *Service) isAvailable(ctx context.Context, userID, kind, id string) (bool, error) {
	if kind == CapabilityModel {
		row, err := s.model(ctx, userID, id)
		if err != nil {
			return false, nil
		}
		available, _ := modelAvailability(row)
		return available, nil
	}
	rows, err := s.allTools(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.ID == id {
			available, _ := toolAvailability(row)
			return available, nil
		}
	}
	return false, nil
}

func (s *Service) grants(ctx context.Context, userID, agent string) (map[string]bool, error) {
	var rows []orm.ExternalCapabilityGrant
	err := s.db.WithContext(ctx).Where("owner_user_id = ? AND agent = ?", strings.TrimSpace(userID), agent).Find(&rows).Error
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		out[grantKey(row.CapabilityType, row.CapabilityID)] = row.Enabled
	}
	return out, err
}

func (s *Service) hasGrant(ctx context.Context, userID, agent, kind, id string) bool {
	if agent == "" || strings.TrimSpace(userID) == "" {
		return false
	}
	grants, err := s.grants(ctx, userID, agent)
	return err == nil && capabilityGranted(grants, kind, id)
}

// Models and tools default to allowed; explicit opt-outs always win.
// Availability and ownership are checked separately on every actual call.
func capabilityGranted(grants map[string]bool, kind, id string) bool {
	if enabled, exists := grants[grantKey(kind, id)]; exists {
		return enabled
	}
	return kind == CapabilityTool || kind == CapabilityModel
}

func grantKey(kind, id string) string { return kind + ":" + id }

func chatCompletionsURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(baseURL, "/chat/completions") {
		return baseURL
	}
	return baseURL + "/chat/completions"
}

func (s *Service) startAudit(ctx context.Context, call capability.InvocationContext, kind, id string) *orm.ExternalCapabilityInvocation {
	now := time.Now().UTC()
	row := &orm.ExternalCapabilityInvocation{
		ID: "eci_" + common.GenerateID(), OwnerUserID: call.Principal.UserID,
		Agent: strings.ToLower(strings.TrimSpace(call.ExternalAgent)), InvocationID: call.InvocationID,
		CapabilityType: kind, CapabilityID: id, CapabilityName: id, Status: "running",
		UsageJSON: json.RawMessage(`{}`), ResultJSON: orm.InvocationResultJSON(`{}`),
		StartedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if s == nil || s.db == nil || s.db.WithContext(context.WithoutCancel(ctx)).Create(row).Error != nil {
		return nil
	}
	return row
}

func (s *Service) finishAudit(call capability.InvocationContext, row *orm.ExternalCapabilityInvocation, name string, usage, result any, callErr error) {
	if row == nil || s == nil || s.db == nil {
		return
	}
	now := time.Now().UTC()
	usageJSON, _ := json.Marshal(usage)
	if row.CapabilityType == CapabilityTool && row.CapabilityID == "builtin:image_generator" {
		// Strip temporary signatures before bounding/truncating the audit preview.
		if encoded, err := json.Marshal(result); err == nil {
			_ = json.Unmarshal(rewriteImageResult(encoded, false), &result)
		}
	}
	preview := auditResultPreview(result)
	updates := map[string]any{
		"status": "succeeded", "usage_json": usageJSON, "result_json": preview,
		"finished_at": &now, "updated_at": now,
	}
	if strings.TrimSpace(name) != "" {
		updates["capability_name"] = name
	}
	if callErr != nil {
		code := capability.Internal
		if value, ok := capability.CodeOf(callErr); ok {
			code = value
		}
		updates["status"] = "failed"
		updates["error_code"] = string(code)
		updates["error_message"] = auditErrorMessage(code)
	}
	_ = s.db.Model(&orm.ExternalCapabilityInvocation{}).Where("id = ?", row.ID).Updates(updates).Error
}

func auditErrorMessage(code capability.ErrorCode) string {
	switch code {
	case capability.PermissionDenied:
		return "external Agent is not authorized for the requested capability"
	case capability.Unavailable:
		return "capability connection is unavailable"
	case capability.InvalidArgument:
		return "capability request is invalid"
	default:
		return "capability invocation failed"
	}
}
