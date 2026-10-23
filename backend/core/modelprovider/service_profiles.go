package modelprovider

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

type providerServiceProfile struct {
	brand, service, adapter string
}

var errServiceSyncConflict = errors.New("provider configuration changed during service synchronization")

// Catalog names are stable service identities, independent of editable protocol
// adapters. Do not infer a built-in service from a custom supplier's name.
var providerServiceProfiles = map[string]providerServiceProfile{
	"Doubao":             {"Doubao", "standard", "Doubao"},
	"DoubaoCodingPlan":   {"Doubao", "coding", "OpenAI"},
	"DoubaoAgentPlan":    {"Doubao", "agent", "OpenAI"},
	"SenseNova":          {"SenseNova", "standard", "SenseNova"},
	"SenseNovaTokenPlan": {"SenseNova", "token", "OpenAI"},
	"Qwen":               {"Qwen", "standard", "Qwen"},
	"QwenCodingPlan":     {"Qwen", "coding", "OpenAI"},
	"QwenTokenPlan":      {"Qwen", "token", "OpenAI"},
	"QwenTokenPlanTeam":  {"Qwen", "token_team", "OpenAI"},
	"GLM":                {"GLM", "standard", "GLM"},
	"GLMCodingPlan":      {"GLM", "coding", "OpenAI"},
	"Kimi":               {"Kimi", "standard", "Kimi"},
	"KimiCode":           {"Kimi", "code", "Claude"},
	"Minimax":            {"Minimax", "standard", "Minimax"},
	"MiniMaxMPlan":       {"Minimax", "m_plan", "OpenAI"},
}

// ProviderServiceIdentity returns optional UI grouping metadata. The runtime
// protocol and API host cannot change an explicitly selected service identity.
func ProviderServiceIdentity(provider orm.UserModelProvider) (brandKey, serviceKey string) {
	if !provider.IsCustom {
		if profile, ok := providerServiceProfiles[strings.TrimSpace(provider.Name)]; ok {
			return profile.brand, profile.service
		}
	}
	return "", ""
}

func serviceSupportsRequestProtocol(provider orm.UserModelProvider, protocol string) bool {
	// Token Plan documents Authorization: Bearer for Messages, whereas the
	// current Claude adapter sends only x-api-key. Do not advertise that route.
	return protocol == "openai" || protocol == "anthropic" && (provider.IsCustom || provider.Name != "SenseNovaTokenPlan")
}

// CatalogCredentialKey keeps built-in service credential namespaces separate,
// including when two services use the same host or selected protocol adapter.
func CatalogCredentialKey(provider orm.UserModelProvider) string {
	if !provider.IsCustom {
		return strings.TrimSpace(provider.Name)
	}
	if name := strings.TrimSpace(RuntimeProviderName(provider)); name != "" {
		return name
	}
	return strings.TrimSpace(provider.DefaultModelProviderID)
}

// ResolveServiceCatalogKey classifies only unambiguous legacy official roots.
// Token Plan personal/team and MiniMax services share roots, so never guess.
func ResolveServiceCatalogKey(catalogKey, baseURL string) string {
	catalogKey = strings.TrimSpace(catalogKey)
	profile, ok := providerServiceProfiles[catalogKey]
	if !ok || profile.service != "standard" {
		return catalogKey
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return catalogKey
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	switch catalogKey {
	case "Doubao":
		if !strings.EqualFold(u.Host, "ark.cn-beijing.volces.com") {
			break
		}
		switch path {
		case "/api/coding/v3", "/api/coding":
			return "DoubaoCodingPlan"
		case "/api/plan/v3", "/api/plan":
			return "DoubaoAgentPlan"
		}
	case "SenseNova":
		if strings.EqualFold(u.Host, "token.sensenova.cn") && (path == "" || path == "/v1" || path == "/v1/chat/completions") {
			return "SenseNovaTokenPlan"
		}
	case "Qwen":
		if strings.EqualFold(u.Host, "coding.dashscope.aliyuncs.com") && (path == "/v1" || path == "/apps/anthropic") {
			return "QwenCodingPlan"
		}
	case "GLM":
		if strings.EqualFold(u.Host, "open.bigmodel.cn") && path == "/api/coding/paas/v4" {
			return "GLMCodingPlan"
		}
	case "Kimi":
		if strings.EqualFold(u.Host, "api.kimi.com") && (path == "/coding" || path == "/coding/v1") {
			return "KimiCode"
		}
	}
	return catalogKey
}

// ServiceProtocolForBaseURL returns a compatible adapter only for a documented
// official service root. A proxy or ambiguous root returns an empty string.
func ServiceProtocolForBaseURL(catalogKey, baseURL string) string {
	profile, ok := providerServiceProfiles[strings.TrimSpace(catalogKey)]
	if !ok {
		return ""
	}
	provider := orm.UserModelProvider{Name: strings.TrimSpace(catalogKey)}
	if profile.brand == "SenseNova" && ResolveServiceCatalogKey("SenseNova", baseURL) == "SenseNovaTokenPlan" {
		return "OpenAI"
	}
	known, ok := supplierProtocolDefaults(provider, baseURL)
	if !ok {
		return ""
	}
	if normalizeBaseURLForCompare(baseURL) == normalizeBaseURLForCompare(known["anthropic"]) && serviceSupportsRequestProtocol(provider, "anthropic") {
		return "Claude"
	}
	if normalizeBaseURLForCompare(baseURL) == normalizeBaseURLForCompare(known["openai"]) {
		return "OpenAI"
	}
	return ""
}

// rebindLegacyServiceProviders runs inside catalog synchronization's transaction.
// It changes only the template link/name; all credentials, endpoints, groups,
// model UUIDs and user overrides remain attached to the existing provider ID.
func rebindLegacyServiceProviders(tx *gorm.DB, existing []orm.UserModelProvider, defs []orm.DefaultModelProvider, now time.Time) error {
	byID := make(map[string]orm.DefaultModelProvider, len(defs))
	byName := make(map[string]orm.DefaultModelProvider, len(defs))
	occupied := make(map[string]bool, len(existing))
	for _, def := range defs {
		byID[def.ID], byName[def.Name] = def, def
	}
	for _, row := range existing {
		occupied[row.DefaultModelProviderID] = true
	}
	for i := range existing {
		row := &existing[i]
		standard, ok := byID[row.DefaultModelProviderID]
		if !ok || row.IsCustom || row.Name != standard.Name || row.Category != defaultProviderCategory {
			continue
		}
		resolved := ResolveServiceCatalogKey(standard.Name, row.BaseURL)
		if resolved == standard.Name {
			continue
		}
		target, ok := byName[resolved]
		if !ok || occupied[target.ID] {
			continue
		}
		oldProtocol := row.ProtocolName
		updates := map[string]any{"default_model_provider_id": target.ID, "name": target.Name, "updated_at": now}
		// An explicit protocol wins. A former native adapter is a catalog default,
		// unless request configuration records an explicit protocol selection.
		if strings.TrimSpace(row.RequestConfig) == "" && (strings.TrimSpace(row.ProtocolName) == "" || row.ProtocolName == standard.Name) {
			adapter := providerServiceProfiles[target.Name].adapter
			if official := ServiceProtocolForBaseURL(target.Name, row.BaseURL); official != "" {
				adapter = official
			}
			updates["protocol_name"] = adapter
			row.ProtocolName = adapter
		}
		result := tx.Model(&orm.UserModelProvider{}).
			Where("id = ? AND create_user_id = ? AND is_custom = ? AND default_model_provider_id = ? AND name = ? AND deleted_at IS NULL", row.ID, row.CreateUserID, false, standard.ID, standard.Name).
			Where("COALESCE(base_url, '') = ? AND COALESCE(protocol_name, '') = ? AND COALESCE(request_config, '') = ?", row.BaseURL, oldProtocol, row.RequestConfig).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			// Abort before using the stale in-memory identity to create templates
			// or update model adapters. The caller retries with a fresh snapshot.
			return errServiceSyncConflict
		}
		occupied[target.ID] = true
		delete(occupied, standard.ID)
		row.DefaultModelProviderID, row.Name = target.ID, target.Name
		if tx.Migrator().HasTable(&orm.UserModelProviderGroupModel{}) {
			if err := tx.Model(&orm.UserModelProviderGroupModel{}).Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, row.CreateUserID).Update("provider_name", RuntimeProviderName(*row)).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
