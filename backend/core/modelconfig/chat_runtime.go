package modelconfig

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/modelprovider"
)

type AvailableChatModel struct {
	CredentialRevision  int64    `gorm:"column:credential_revision"`
	LogoDataURL         string   `gorm:"column:logo_data_url"`
	Vision              bool     `gorm:"column:vision"`
	ModelSettings       string   `gorm:"column:model_settings"`
	ID                  string   `gorm:"column:model_id"`
	ProviderID          string   `gorm:"column:provider_id"`
	ProviderGroupID     string   `gorm:"column:provider_group_id"`
	OwnerUserID         string   `gorm:"column:owner_user_id"`
	ProviderName        string   `gorm:"column:provider_name"`
	ProviderDisplayName string   `gorm:"column:provider_display_name"`
	ProviderIsCustom    bool     `gorm:"column:provider_is_custom"`
	GroupName           string   `gorm:"column:group_name"`
	ModelName           string   `gorm:"column:model_name"`
	ModelType           string   `gorm:"column:model_type"`
	BaseURL             string   `gorm:"column:base_url"`
	APIKey              string   `gorm:"column:api_key"`
	APIKeyCiphertext    string   `gorm:"column:api_key_ciphertext"`
	MaxInputTokens      *string  `gorm:"column:max_input_tokens"`
	InputTokenLimit     *string  `gorm:"column:input_token_limit"`
	MaxOutputTokens     *string  `gorm:"column:max_output_tokens"`
	GroupVerified       bool     `gorm:"column:group_verified"`
	Source              string   `gorm:"-"`
	Availability        string   `gorm:"-"`
	Lifecycle           string   `gorm:"-"`
	Capabilities        []string `gorm:"-"`
	DefaultForType      bool     `gorm:"-"`
}

func LoadAvailableChatModels(ctx context.Context, db *gorm.DB, userID string) ([]AvailableChatModel, error) {
	if db == nil {
		return nil, nil
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, nil
	}
	rows := make([]AvailableChatModel, 0)
	err := db.WithContext(ctx).
		Table("user_model_provider_group_models m").
		Select(
			"m.id AS model_id, m.user_model_provider_id AS provider_id, "+
				"m.user_model_provider_group_id AS provider_group_id, "+
				"m.create_user_id AS owner_user_id, m.provider_name, m.name AS model_name, "+
				"p.logo_data_url, p.name AS provider_display_name, p.is_custom AS provider_is_custom, "+
				"m.model_type, m.vision, m.model_settings, m.max_input_tokens, m.input_token_limit, m.max_output_tokens, "+
				"g.name AS group_name, g.base_url, g.is_verified AS group_verified, "+
				"g.api_key, g.api_key_ciphertext, g.credential_revision",
		).
		Joins(
			"JOIN user_model_provider_groups g ON "+
				"g.id = m.user_model_provider_group_id AND g.create_user_id = m.create_user_id "+
				"AND g.deleted_at IS NULL",
		).
		Joins(
			"JOIN user_model_providers p ON "+
				"p.id = m.user_model_provider_id AND p.create_user_id = m.create_user_id "+
				"AND p.deleted_at IS NULL",
		).
		Where("m.deleted_at IS NULL AND LOWER(TRIM(m.model_type)) = ?", "llm").
		Where(
			"(m.create_user_id = ? OR EXISTS ("+
				"SELECT 1 FROM user_selected_models shared "+
				"WHERE shared.user_model_provider_group_model_id = m.id "+
				"AND shared.user_id = m.create_user_id "+
				"AND shared.model_type = ? AND shared.share = ?"+
				"))",
			userID, "llm", true,
		).
		Order("m.provider_name ASC, g.name ASC, m.name ASC, m.id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	available := rows[:0]
	for index := range rows {
		if rows[index].OwnerUserID == userID {
			rows[index].Source = "own"
		} else {
			rows[index].Source = "shared"
		}
		if !rows[index].GroupVerified && rows[index].OwnerUserID == userID {
			var selected int64
			if err := db.WithContext(ctx).Table("user_selected_models").
				Where("user_id = ? AND user_model_provider_group_model_id = ? AND model_type = ?", userID, rows[index].ID, "llm").Count(&selected).Error; err != nil || selected == 0 {
				continue
			}
		} else if !rows[index].GroupVerified {
			continue
		}
		rows[index].Availability = "available"
		rows[index].Lifecycle = "active"
		rows[index].Capabilities = []string{"chat"}
		available = append(available, rows[index])
	}
	rows = available
	catalog, _ := modelprovider.ResolveCloudModelCatalog(ctx)
	for _, item := range catalog.Models {
		if item.ModelType != "llm" || item.Lifecycle == "retired" {
			continue
		}
		rows = append(rows, AvailableChatModel{
			ID: item.ModelKey, ProviderID: modelprovider.CloudSystemProviderID,
			ProviderName: modelprovider.CloudSystemProviderName, ModelName: item.DisplayName,
			ModelType: "llm", Source: "cloud", Availability: item.Status,
			Lifecycle:    item.Lifecycle,
			Capabilities: append([]string(nil), item.Capabilities...), DefaultForType: item.DefaultForType,
		})
	}
	if catalog.Known && db.Migrator().HasTable(&orm.UserSelectedCloudModel{}) {
		var selected orm.UserSelectedCloudModel
		err := db.WithContext(ctx).
			Where("user_id = ? AND model_type = ?", userID, "llm").
			Take(&selected).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil && !hasChatModel(rows, selected.PublicModelKey, "cloud") {
			rows = append(rows, AvailableChatModel{
				ID: selected.PublicModelKey, ProviderID: modelprovider.CloudSystemProviderID,
				ProviderName: modelprovider.CloudSystemProviderName, ModelName: selected.DisplayNameSnapshot,
				ModelType: "llm", Source: "cloud", Availability: "unavailable",
				Lifecycle:    "retired",
				Capabilities: []string{"chat"},
			})
		}
	}
	return rows, nil
}

func hasChatModel(models []AvailableChatModel, id, source string) bool {
	for _, model := range models {
		if model.ID == id && model.Source == source {
			return true
		}
	}
	return false
}

// ChatModelCandidate is Host-private execution input. Never persist or expose it.
type ChatModelCandidate struct {
	CandidateID        string         `json:"candidate_id"`
	Source             string         `json:"source"`
	ModelID            string         `json:"model_id"`
	ProviderGroupID    string         `json:"provider_group_id"`
	CredentialRevision int64          `json:"credential_revision"`
	CredentialCount    int            `json:"credential_count"`
	MaxInputTokens     *int64         `json:"max_input_tokens"`
	LLMConfig          map[string]any `json:"llm_config"`
}

type ChatRuntimeConfig struct {
	Config      map[string]any
	Models      []AvailableChatModel
	Candidates  []ChatModelCandidate
	credentials runtimeCredentials
}

type runtimeCredentialKey struct {
	groupID  string
	revision int64
}
type runtimeCredentialResult struct {
	key     string
	baseURL string
	err     error
}
type runtimeCredentials map[runtimeCredentialKey]runtimeCredentialResult

func (cache runtimeCredentials) resolve(groupID string, revision int64, plain, ciphertext string) (string, error) {
	identity := runtimeCredentialKey{groupID, revision}
	if groupID != "" {
		if result, ok := cache[identity]; ok {
			return result.key, result.err
		}
	}
	key, err := modelprovider.ResolveAPIKey(plain, ciphertext)
	if groupID != "" && cache != nil {
		cache[identity] = runtimeCredentialResult{key: key, err: err}
	}
	return key, err
}

// LoadChatRuntimeConfig preserves all non-chat role rules, preparing the chat
// role separately so its unusable credential cannot block authorized alternatives.
func LoadChatRuntimeConfig(ctx context.Context, db *gorm.DB, userID string) (*ChatRuntimeConfig, error) {
	cache := runtimeCredentials{}
	config, err := loadLLMConfigWithOptions(ctx, db, userID, false, true, cache)
	if err != nil {
		return nil, err
	}
	result, err := loadChatModelCandidates(ctx, db, userID, cache)
	if err != nil {
		return nil, err
	}
	result.Config = config
	return result, nil
}

func LoadChatModelCandidates(ctx context.Context, db *gorm.DB, userID string) (*ChatRuntimeConfig, error) {
	return loadChatModelCandidates(ctx, db, userID, runtimeCredentials{})
}

func loadChatModelCandidates(ctx context.Context, db *gorm.DB, userID string, cache runtimeCredentials) (*ChatRuntimeConfig, error) {
	models, err := LoadAvailableChatModels(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	result := &ChatRuntimeConfig{Models: models, credentials: cache}
	cloudID := ""
	if db != nil && db.Migrator().HasTable(&orm.UserSelectedCloudModel{}) {
		var selected orm.UserSelectedCloudModel
		err := db.WithContext(ctx).Where("user_id = ? AND model_type = ?", strings.TrimSpace(userID), "llm").Take(&selected).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			cloudID = selected.PublicModelKey
		}
	}
	for _, model := range models {
		if model.Source == "cloud" && model.ID != cloudID {
			continue
		}
		if model.Lifecycle != "" && model.Lifecycle != "active" {
			continue
		}
		// One malformed candidate does not invalidate other authorized connections.
		result.PrepareCandidate(ctx, model)
	}
	return result, nil
}

func ChatCandidateID(source, modelID string) string { return source + ":" + modelID }

// PrepareCandidate also permits the conversation's explicitly bound current
// Cloud model. Other Cloud catalog entries never become automatic alternatives.
func (result *ChatRuntimeConfig) PrepareCandidate(ctx context.Context, model AvailableChatModel) *ChatModelCandidate {
	id := ChatCandidateID(model.Source, model.ID)
	for i := range result.Candidates {
		if result.Candidates[i].CandidateID == id {
			return &result.Candidates[i]
		}
	}
	if model.Availability != "" && model.Availability != "available" && model.Availability != "degraded" {
		return nil
	}
	row := SelectedRuntimeModel{ModelType: "llm", TechnicalModelType: model.ModelType, Vision: model.Vision,
		ModelSettings: model.ModelSettings, ProviderName: model.ProviderName, ModelName: model.ModelName,
		BaseURL: model.BaseURL, ProviderGroupID: model.ProviderGroupID, RuntimeGroupID: model.ProviderGroupID,
		RuntimeProviderID: model.ProviderID, MaxInputTokens: model.MaxInputTokens,
		InputTokenLimit: model.InputTokenLimit, MaxOutputTokens: model.MaxOutputTokens}
	count := 0
	if model.Source == "cloud" {
		resolved, available, err := ResolveCloudRuntimeModel(ctx, "llm", model.ID)
		if err != nil || !available {
			return nil
		}
		row = resolved
	} else {
		key, err := result.credentials.resolve(model.ProviderGroupID, model.CredentialRevision, model.APIKey, model.APIKeyCiphertext)
		if err != nil {
			return nil
		}
		row.APIKey = key
		// Match the regular runtime path for endpoints that do not require a key.
		row.SkipAuth = strings.TrimSpace(key) == "" && strings.TrimSpace(row.BaseURL) != ""
	}
	for _, line := range strings.Split(row.APIKey, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	config := BuildLLMConfig([]SelectedRuntimeModel{row})
	if config == nil {
		return nil
	}
	role := config["llm"].(map[string]any)
	source, _ := role["source"].(string)
	name, _ := role["model"].(string)
	key, _ := role["api_key"].(string)
	key = strings.TrimSpace(key)
	role["api_key"] = key
	skipAuth, _ := role["skip_auth"].(bool)
	// The current AutoModel is constructed before the recovery wrapper. Do not
	// select a configuration which that constructor or its auth header rejects.
	if strings.TrimSpace(source) == "" || strings.TrimSpace(name) == "" || !skipAuth && (key == "" || strings.ContainsAny(key, "\r\n")) {
		return nil
	}
	// Declaration comes from the stored field, never BuildLLMConfig's 128K default.
	window := ConfiguredChatWindow(model.MaxInputTokens)
	if window == nil {
		delete(config["llm"].(map[string]any), "max_input_tokens")
	}
	result.Candidates = append(result.Candidates, ChatModelCandidate{CandidateID: id, Source: model.Source, ModelID: model.ID,
		ProviderGroupID: model.ProviderGroupID, CredentialRevision: model.CredentialRevision, CredentialCount: count,
		MaxInputTokens: window, LLMConfig: config})
	return &result.Candidates[len(result.Candidates)-1]
}

func ConfiguredChatWindow(value *string) *int64 {
	if value == nil {
		return nil
	}
	raw := strings.ToUpper(strings.TrimSpace(*value))
	multiplier := int64(1)
	if strings.HasSuffix(raw, "K") {
		multiplier = 1024
		raw = strings.TrimSuffix(raw, "K")
	} else if strings.HasSuffix(raw, "M") {
		multiplier = 1024 * 1024
		raw = strings.TrimSuffix(raw, "M")
	}
	count, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || count <= 0 || count > math.MaxInt64/multiplier {
		return nil
	}
	count *= multiplier
	return &count
}
