package modelconfig

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/modelprovider"
)

const cloudToolTokenTimeout = 5 * time.Second

var cloudToolProviders = []string{"feishu", "googledrive", "notion"}

type cloudConnectionList struct {
	Data struct {
		Items []struct {
			ConnectionID string `json:"connection_id"`
		} `json:"items"`
	} `json:"data"`
}

type cloudTokenResponse struct {
	Data struct {
		AccessToken string `json:"access_token"`
	} `json:"data"`
}

// LoadCloudToolConfig loads current user-scoped cloud credentials at execution time.
func LoadCloudToolConfig(ctx context.Context, userID string) (map[string]any, error) {
	toolConfig := map[string]any{}
	for _, provider := range cloudToolProviders {
		tokens, err := LoadCloudProviderTokens(ctx, provider, userID)
		if err != nil {
			return nil, err
		}
		if len(tokens) == 1 {
			toolConfig[provider] = tokens[0]
		} else if len(tokens) > 1 {
			toolConfig[provider] = tokens
		}
	}
	return toolConfig, nil
}

func LoadCloudProviderTokens(ctx context.Context, provider, userID string) ([]string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	userID = strings.TrimSpace(userID)
	if provider == "" || userID == "" {
		return nil, nil
	}
	headers := map[string]string{}
	if token := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN")); token != "" {
		headers["X-LazyMind-Internal-Token"] = token
	}
	listURL := fmt.Sprintf("%s/v1/cloud/connections/internal/chat-enabled?provider=%s&owner_user_id=%s",
		common.AuthServiceBaseURL(), url.QueryEscape(provider), url.QueryEscape(userID))
	var connections cloudConnectionList
	if err := common.ApiGet(ctx, listURL, headers, &connections, cloudToolTokenTimeout); err != nil {
		return nil, fmt.Errorf("list chat-enabled %s connections: %w", provider, err)
	}
	tokens := make([]string, 0, len(connections.Data.Items))
	for _, item := range connections.Data.Items {
		connectionID := strings.TrimSpace(item.ConnectionID)
		if connectionID == "" {
			continue
		}
		tokenURL := fmt.Sprintf("%s/v1/cloud/connections/%s/token?user_id=%s",
			common.AuthServiceBaseURL(), url.PathEscape(connectionID), url.QueryEscape(userID))
		var response cloudTokenResponse
		if err := common.ApiGet(ctx, tokenURL, headers, &response, cloudToolTokenTimeout); err != nil {
			continue
		}
		if token := strings.TrimSpace(response.Data.AccessToken); token != "" {
			tokens = append(tokens, token)
		}
	}
	return tokens, nil
}

type SelectedRuntimeModel struct {
	ModelType          string
	TechnicalModelType string
	IsDefault          bool
	ProviderName       string
	ModelName          string
	BaseURL            string
	APIKey             string
	APIKeyCiphertext   string
	MaxInputTokens     *string
}

// LoadMaxInputTokens returns the configured context window for a runtime model role.
// It follows the same own-selection then shared-selection precedence as LoadLLMConfig.
func LoadMaxInputTokens(ctx context.Context, db *gorm.DB, userID, modelType string) (*string, error) {
	var row struct {
		SelectionID    string  `gorm:"column:selection_id"`
		MaxInputTokens *string `gorm:"column:max_input_tokens"`
	}
	err := db.WithContext(ctx).
		Table("user_selected_models usm").
		Select("usm.id AS selection_id, m.max_input_tokens").
		Joins("JOIN user_model_provider_group_models m ON m.id = usm.user_model_provider_group_model_id AND m.create_user_id = usm.user_id AND m.deleted_at IS NULL").
		Where("usm.user_id = ? AND usm.model_type = ?", strings.TrimSpace(userID), modelType).
		Limit(1).Scan(&row).Error
	if err != nil || row.SelectionID != "" {
		return row.MaxInputTokens, err
	}
	row = struct {
		SelectionID    string  `gorm:"column:selection_id"`
		MaxInputTokens *string `gorm:"column:max_input_tokens"`
	}{}
	err = db.WithContext(ctx).
		Table("user_selected_models usm").
		Select("usm.id AS selection_id, m.max_input_tokens").
		Joins("JOIN user_model_provider_group_models m ON m.id = usm.user_model_provider_group_model_id AND m.deleted_at IS NULL").
		Where("usm.share = ? AND usm.model_type = ?", true, modelType).
		Limit(1).Scan(&row).Error
	return row.MaxInputTokens, err
}

func LoadLLMConfig(ctx context.Context, db *gorm.DB, userID string) (map[string]any, error) {
	// Step 1: load the user's own selections.
	var ownRows []SelectedRuntimeModel
	err := db.WithContext(ctx).
		Table("user_selected_models usm").
		Select(
			"usm.model_type, "+
				"m.model_type AS technical_model_type, "+
				"m.is_default, "+
				"m.provider_name, "+
				"m.name AS model_name, "+
				"g.base_url, "+
				"g.api_key, g.api_key_ciphertext, "+
				"m.max_input_tokens",
		).
		Joins(
			"JOIN user_model_provider_group_models m ON "+
				"m.id = usm.user_model_provider_group_model_id AND "+
				"m.create_user_id = usm.user_id AND "+
				"m.deleted_at IS NULL",
		).
		Joins(
			"JOIN user_model_provider_groups g ON "+
				"g.id = m.user_model_provider_group_id AND "+
				"g.create_user_id = usm.user_id AND "+
				"g.deleted_at IS NULL AND g.is_verified = ?",
			true,
		).
		Where("usm.user_id = ?", strings.TrimSpace(userID)).
		Scan(&ownRows).Error
	if err != nil {
		return nil, err
	}
	ownRows = eligibleRuntimeModels(ownRows)
	if err := decryptRuntimeModels(ownRows); err != nil {
		return nil, err
	}

	// Collect which model_types the user already has.
	coveredTypes := make(map[string]struct{}, len(ownRows))
	for _, row := range ownRows {
		coveredTypes[strings.ToLower(strings.TrimSpace(row.ModelType))] = struct{}{}
	}

	// Step 2: for model_types not covered by the user, fall back to share=true rows.
	var sharedRows []SelectedRuntimeModel
	err = db.WithContext(ctx).
		Table("user_selected_models usm").
		Select(
			"usm.model_type, "+
				"m.model_type AS technical_model_type, "+
				"m.is_default, "+
				"m.provider_name, "+
				"m.name AS model_name, "+
				"g.base_url, "+
				"g.api_key, g.api_key_ciphertext, "+
				"m.max_input_tokens",
		).
		Joins(
			"JOIN user_model_provider_group_models m ON "+
				"m.id = usm.user_model_provider_group_model_id AND "+
				"m.deleted_at IS NULL",
		).
		Joins(
			"JOIN user_model_provider_groups g ON "+
				"g.id = m.user_model_provider_group_id AND "+
				"g.deleted_at IS NULL AND g.is_verified = ?",
			true,
		).
		Where("usm.share = ?", true).
		Scan(&sharedRows).Error
	if err != nil {
		return nil, err
	}
	sharedRows = eligibleRuntimeModels(sharedRows)
	if err := decryptRuntimeModels(sharedRows); err != nil {
		return nil, err
	}

	// Merge: own rows take priority; shared rows fill in missing types.
	rows := make([]SelectedRuntimeModel, 0, len(ownRows)+len(sharedRows))
	rows = append(rows, ownRows...)
	for _, row := range sharedRows {
		normalized := strings.ToLower(strings.TrimSpace(row.ModelType))
		if _, covered := coveredTypes[normalized]; !covered {
			rows = append(rows, row)
			coveredTypes[normalized] = struct{}{}
		}
	}

	return BuildLLMConfig(rows), nil
}

func LoadOCRConfig(ctx context.Context, db *gorm.DB, userID string) (map[string]any, error) {
	row, err := loadSelectedProviderConfig(ctx, db, strings.TrimSpace(userID), "ocr", false)
	if err != nil {
		return nil, err
	}
	if row == nil {
		row, err = loadSelectedProviderConfig(ctx, db, "", "ocr", true)
		if err != nil {
			return nil, err
		}
	}
	if row == nil {
		return nil, nil
	}
	ocrType := normalizeOCRType(row.ProviderName)
	if ocrType == "" {
		return nil, nil
	}
	config := map[string]any{
		"ocr_type": ocrType,
		"ocr_url":  row.BaseURL,
	}
	if authValue := normalizeOCRAuthValue(row.APIKey); authValue != nil {
		config["ocr_auth"] = map[string]any{ocrType: authValue}
	}
	return config, nil
}

// LoadSearchToolConfig returns the selected web-search credential in the
// dynamic tool-auth shape consumed by the algorithm service. Workflow attempts
// use this alongside LoadLLMConfig because their durable/public Attempt context
// intentionally does not persist Host-private credentials.
func LoadSearchToolConfig(ctx context.Context, db *gorm.DB, userID string) (map[string]any, error) {
	row, err := loadSelectedProviderConfig(ctx, db, strings.TrimSpace(userID), "search", false)
	if err != nil {
		return nil, err
	}
	if row == nil {
		row, err = loadSelectedProviderConfig(ctx, db, "", "search", true)
		if err != nil {
			return nil, err
		}
	}
	if row == nil {
		return nil, nil
	}
	toolName := normalizeSearchToolName(row.ProviderName)
	if toolName == "" {
		return nil, nil
	}
	keys := splitOCRAuthKeys(row.APIKey)
	if len(keys) == 0 {
		return nil, nil
	}
	var value any = keys[0]
	if len(keys) > 1 {
		value = keys
	}
	return map[string]any{toolName: value}, nil
}

// LoadAcademicSearchToolConfig returns the selected academic-search
// credential in the dynamic tool-auth shape consumed by the algorithm
// service. Sciverse is configured as a datasource rather than a generic web
// search provider, so it must be resolved independently from
// LoadSearchToolConfig.
func LoadAcademicSearchToolConfig(ctx context.Context, db *gorm.DB, userID string) (map[string]any, error) {
	userID = strings.TrimSpace(userID)
	row, err := loadSelectedProviderConfig(ctx, db, userID, "datasource", false)
	if err != nil {
		return nil, err
	}
	if row == nil {
		row, err = loadConfiguredSciverseDatasource(ctx, db, userID)
		if err != nil {
			return nil, err
		}
	}
	if row == nil {
		row, err = loadSelectedProviderConfig(ctx, db, "", "datasource", true)
		if err != nil {
			return nil, err
		}
	}
	if row == nil || normalizeSearchToolName(row.ProviderName) != "sciverse" {
		return nil, nil
	}
	value := normalizeOCRAuthValue(row.APIKey)
	if value == nil {
		return nil, nil
	}
	return map[string]any{"sciverse": value}, nil
}

// LoadToolConfigForCapabilities loads Host-private credentials only for tools
// declared by the current Workflow node. The declaration is the SubAgent tool
// allowlist; a local-only step therefore receives no search or cloud secrets.
func LoadToolConfigForCapabilities(
	ctx context.Context,
	db *gorm.DB,
	userID string,
	capabilities []string,
) (map[string]any, error) {
	allowed := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		name := strings.ToLower(strings.TrimSpace(capability))
		if name != "" {
			allowed[name] = struct{}{}
		}
	}
	merge := func(target map[string]any, source map[string]any) map[string]any {
		if target == nil && len(source) > 0 {
			target = map[string]any{}
		}
		for key, value := range source {
			target[key] = value
		}
		return target
	}
	has := func(name string) bool {
		_, ok := allowed[name]
		return ok
	}

	var config map[string]any
	if has("web_search") {
		entry, err := LoadSearchToolConfig(ctx, db, userID)
		if err != nil {
			return nil, err
		}
		config = merge(config, entry)
	}
	if has("academic_search") {
		entry, err := LoadAcademicSearchToolConfig(ctx, db, userID)
		if err != nil {
			return nil, err
		}
		config = merge(config, entry)
	}
	for _, provider := range cloudToolProviders {
		if !has("cloud_files") && !has(provider) {
			continue
		}
		tokens, err := LoadCloudProviderTokens(ctx, provider, userID)
		if err != nil {
			return nil, err
		}
		if len(tokens) == 1 {
			config = merge(config, map[string]any{provider: tokens[0]})
		} else if len(tokens) > 1 {
			config = merge(config, map[string]any{provider: tokens})
		}
	}
	return config, nil
}

func loadConfiguredSciverseDatasource(
	ctx context.Context,
	db *gorm.DB,
	userID string,
) (*selectedProviderConfig, error) {
	if db == nil || userID == "" {
		return nil, nil
	}
	var row selectedProviderConfig
	err := db.WithContext(ctx).Table("user_model_provider_groups g").
		Select("p.name AS provider_name, g.base_url, g.api_key, g.api_key_ciphertext").
		Joins(
			"JOIN user_model_providers p ON "+
				"p.id = g.user_model_provider_id AND "+
				"p.create_user_id = g.create_user_id AND "+
				"p.deleted_at IS NULL",
		).
		Where(
			"g.create_user_id = ? AND g.deleted_at IS NULL AND g.is_verified = ? "+
				"AND (TRIM(g.api_key) <> '' OR TRIM(g.api_key_ciphertext) <> '') "+
				"AND p.category = ? AND p.name IN ?",
			userID,
			true,
			"datasource",
			[]string{"Sciverse", "Sciverse Search"},
		).
		Order("g.updated_at DESC").
		Limit(1).
		Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ProviderName == "" && row.BaseURL == "" {
		return nil, nil
	}
	row.APIKey, err = modelprovider.ResolveAPIKey(row.APIKey, row.APIKeyCiphertext)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func normalizeSearchToolName(providerName string) string {
	normalized := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return -1
	}, providerName)
	switch normalized {
	case "google", "googlesearch", "googlecustomsearch":
		return "google"
	case "bocha", "bochasearch":
		return "bocha"
	case "bing", "bingsearch":
		return "bing"
	case "tavily":
		return "tavily"
	case "sciverse", "sciversesearch":
		return "sciverse"
	default:
		return ""
	}
}

func normalizeOCRAuthValue(raw string) any {
	keys := splitOCRAuthKeys(raw)
	if len(keys) == 0 {
		return nil
	}
	if len(keys) == 1 {
		return keys[0]
	}
	return keys
}

func splitOCRAuthKeys(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

type selectedProviderConfig struct {
	ProviderName     string
	BaseURL          string
	APIKey           string
	APIKeyCiphertext string
}

func loadSelectedProviderConfig(
	ctx context.Context,
	db *gorm.DB,
	userID string,
	category string,
	sharedOnly bool,
) (*selectedProviderConfig, error) {
	var row selectedProviderConfig
	q := db.WithContext(ctx).Table("user_selected_providers usp").
		Select(
			"p.name AS provider_name, "+
				"g.base_url, "+
				"g.api_key, g.api_key_ciphertext",
		).
		Joins("JOIN user_model_provider_groups g ON g.id = usp.user_model_provider_group_id AND g.deleted_at IS NULL").
		Joins("JOIN user_model_providers p ON p.id = g.user_model_provider_id AND p.deleted_at IS NULL").
		Where("usp.category = ?", category)
	if sharedOnly {
		q = q.Where("usp.share = ?", true)
	} else {
		q = q.Where("usp.user_id = ?", userID)
	}
	err := q.Order("usp.updated_at DESC").Limit(1).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ProviderName == "" && row.BaseURL == "" {
		return nil, nil
	}
	row.APIKey, err = modelprovider.ResolveAPIKey(row.APIKey, row.APIKeyCiphertext)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func normalizeOCRType(providerName string) string {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(providerName), " ", "")) {
	case "mineru":
		return "mineru"
	case "paddleocr", "paddle":
		return "paddleocr"
	default:
		return ""
	}
}

// LoadAdminEmbedConfig queries the first system-wide default embedding model
// (is_default=true, model_type=embed_main) across all users, and returns it as
// an embed_main config map. This is the admin-configured embedding model shared
// by all users for document parsing and knowledge-base search.
// Returns nil when no default embedding model is configured.
func LoadAdminEmbedConfig(ctx context.Context, db *gorm.DB) (map[string]any, error) {
	var row SelectedRuntimeModel
	err := db.WithContext(ctx).
		Table("user_model_provider_group_models m").
		Select("m.provider_name, m.name AS model_name, g.base_url, g.api_key, g.api_key_ciphertext").
		Joins(
			"JOIN user_model_provider_groups g ON "+
				"g.id = m.user_model_provider_group_id AND "+
				"g.deleted_at IS NULL",
		).
		Where("m.model_type IN ? AND m.is_default = ? AND m.deleted_at IS NULL", []string{"embed", "cross_modal_embed"}, true).
		Order("m.created_at ASC").
		Limit(1).
		Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ProviderName == "" && row.ModelName == "" {
		return nil, nil
	}
	row.APIKey, err = modelprovider.ResolveAPIKey(row.APIKey, row.APIKeyCiphertext)
	if err != nil {
		return nil, err
	}
	cfg := map[string]any{
		"source":   modelprovider.LazyLLMSource(row.ProviderName),
		"model":    row.ModelName,
		"base_url": modelprovider.LazyLLMBaseURL(row.ProviderName, row.BaseURL),
		"api_key":  row.APIKey,
	}
	return cfg, nil
}

func decryptRuntimeModels(rows []SelectedRuntimeModel) error {
	for i := range rows {
		apiKey, err := modelprovider.ResolveAPIKey(rows[i].APIKey, rows[i].APIKeyCiphertext)
		if err != nil {
			return err
		}
		rows[i].APIKey = apiKey
	}
	return nil
}

func eligibleRuntimeModels(rows []SelectedRuntimeModel) []SelectedRuntimeModel {
	eligible := rows[:0]
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.ModelType), modelprovider.EvoModelKey) {
			if _, ok := openCodeDescriptor(row); !ok {
				continue
			}
		}
		eligible = append(eligible, row)
	}
	return eligible
}

func BuildLLMConfig(rows []SelectedRuntimeModel) map[string]any {
	out := map[string]any{}
	for _, row := range rows {
		role := strings.ToLower(strings.TrimSpace(row.ModelType))
		cfg := map[string]any{
			"source":   modelprovider.LazyLLMSource(row.ProviderName),
			"model":    row.ModelName,
			"base_url": modelprovider.LazyLLMBaseURL(row.ProviderName, row.BaseURL),
			"api_key":  row.APIKey,
		}
		if row.MaxInputTokens != nil {
			cfg["max_input_tokens"] = *row.MaxInputTokens
		}
		if role == modelprovider.EvoModelKey {
			descriptor, ok := openCodeDescriptor(row)
			if !ok {
				continue
			}
			cfg["opencode"] = descriptor
		}
		out[role] = cfg
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func openCodeDescriptor(row SelectedRuntimeModel) (modelprovider.OpenCodeModelDescriptor, bool) {
	return modelprovider.ResolveOpenCodeModel(
		row.ProviderName, row.ModelName, row.BaseURL, row.TechnicalModelType, row.IsDefault,
	)
}

func SummarizeLLMConfigForLog(config map[string]any) string {
	if len(config) == 0 {
		return "roles=[]"
	}
	roles := make([]string, 0, len(config))
	for role := range config {
		roles = append(roles, role)
	}
	sort.Strings(roles)

	parts := make([]string, 0, len(roles)+1)
	parts = append(parts, "roles=["+strings.Join(roles, ",")+"]")
	for _, role := range roles {
		roleConfig, _ := config[role].(map[string]any)
		if roleConfig == nil {
			parts = append(parts, fmt.Sprintf("%s(type=%T)", role, config[role]))
			continue
		}
		parts = append(parts, fmt.Sprintf(
			"%s(source=%s, model=%s, base_url=%s, api_key=%s)",
			role,
			stringValue(roleConfig["source"]),
			stringValue(roleConfig["model"]),
			stringValue(roleConfig["base_url"]),
			APIKeyState(roleConfig["api_key"]),
		))
	}
	return strings.Join(parts, " ")
}

func stringValue(value any) string {
	s, _ := value.(string)
	return s
}

func APIKeyState(value any) string {
	if strings.TrimSpace(stringValue(value)) == "" {
		return "empty"
	}
	return "set"
}
