package modelconfig

import (
	"context"
	"crypto/sha256"
	"sort"
	"strings"
	"sync"
	"time"

	"lazymind/core/cloudclient"
	"lazymind/core/modelprovider"
)

type CloudRuntimeConfig struct {
	Source      string
	BaseURL     string
	AccessToken string
	Models      map[string]string
}

type CloudTokenSource interface {
	AccessToken(context.Context, time.Duration) (string, error)
}

type CloudProviderClient interface {
	Origin() string
	GetProviderBootstrap(context.Context, string) (cloudclient.ModelProviderBootstrap, error)
}

type RuntimeProvider interface {
	RuntimeConfig(context.Context) (CloudRuntimeConfig, bool, error)
}

type CloudRuntimeProvider struct {
	Session    CloudTokenSource
	Client     CloudProviderClient
	Now        func() time.Time
	Locale     string
	mu         sync.Mutex
	cached     cloudclient.ModelProviderBootstrap
	cacheToken [32]byte
	cacheUntil time.Time
}

func (p *CloudRuntimeProvider) RuntimeConfig(ctx context.Context) (CloudRuntimeConfig, bool, error) {
	if p.Session == nil || p.Client == nil {
		return CloudRuntimeConfig{}, false, nil
	}
	bootstrap, token, err := p.currentBootstrap(ctx)
	if err != nil {
		return CloudRuntimeConfig{}, false, nil
	}
	if !bootstrap.HasTokenPlan || !bootstrap.CloudChatAvailable || !bootstrap.Available {
		return CloudRuntimeConfig{}, false, nil
	}
	return RuntimeConfigFromCloudBootstrap(bootstrap, strings.TrimRight(p.Client.Origin(), "/")+"/v1", token), true, nil
}

func (p *CloudRuntimeProvider) CloudModelReadiness(ctx context.Context, modelType string) (modelprovider.CloudModelReadiness, error) {
	if p.Session == nil || p.Client == nil {
		return modelprovider.CloudModelReadiness{}, nil
	}
	bootstrap, _, err := p.currentBootstrap(ctx)
	if err != nil {
		return modelprovider.CloudModelReadiness{}, nil
	}
	locale := strings.ToLower(strings.TrimSpace(p.Locale))
	if locale != "en" {
		locale = "zh"
	}
	status := modelprovider.CloudModelReadiness{
		Known: true, PlanURL: strings.TrimRight(p.Client.Origin(), "/") + "/" + locale + "/console#token-plan",
	}
	if !bootstrap.HasTokenPlan {
		status.Reason = "cloud_plan_required"
		return status, nil
	}
	config := RuntimeConfigFromCloudBootstrap(bootstrap, strings.TrimRight(p.Client.Origin(), "/")+"/v1", "entitlement-check")
	_, status.Ready = config.Models[strings.ToLower(strings.TrimSpace(modelType))]
	if modelType == "llm" && !bootstrap.CloudChatAvailable {
		status.Ready = false
	}
	if !status.Ready {
		status.Reason = "model_unavailable"
	}
	return status, nil
}

func (p *CloudRuntimeProvider) currentBootstrap(ctx context.Context) (cloudclient.ModelProviderBootstrap, string, error) {
	if p.Session == nil || p.Client == nil {
		return cloudclient.ModelProviderBootstrap{}, "", context.Canceled
	}
	token, err := p.Session.AccessToken(ctx, time.Minute)
	if err != nil {
		return cloudclient.ModelProviderBootstrap{}, "", err
	}
	bootstrap, err := p.bootstrap(ctx, token)
	return bootstrap, token, err
}

func (p *CloudRuntimeProvider) bootstrap(ctx context.Context, token string) (cloudclient.ModelProviderBootstrap, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	tokenHash := sha256.Sum256([]byte(token))
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	if now.Before(p.cacheUntil) && p.cacheToken == tokenHash && len(p.cached.Models) > 0 {
		return p.cached, nil
	}
	bootstrap, err := p.Client.GetProviderBootstrap(ctx, token)
	if err != nil {
		return cloudclient.ModelProviderBootstrap{}, err
	}
	p.cached = bootstrap
	p.cacheToken = tokenHash
	p.cacheUntil = now.Add(5 * time.Minute)
	return bootstrap, nil
}

var runtimeProviderState struct {
	sync.RWMutex
	provider RuntimeProvider
}

func SetRuntimeProvider(provider RuntimeProvider) {
	runtimeProviderState.Lock()
	runtimeProviderState.provider = provider
	runtimeProviderState.Unlock()
}

func fillMissingRolesFromRuntimeProvider(ctx context.Context, rows []SelectedRuntimeModel) []SelectedRuntimeModel {
	runtimeProviderState.RLock()
	provider := runtimeProviderState.provider
	runtimeProviderState.RUnlock()
	if provider == nil {
		return rows
	}
	config, available, err := provider.RuntimeConfig(ctx)
	if err != nil || !available {
		return rows
	}
	return FillMissingRoles(rows, config)
}

func RuntimeConfigFromCloudBootstrap(bootstrap cloudclient.ModelProviderBootstrap, modelBaseURL, accessToken string) CloudRuntimeConfig {
	models := map[string]string{}
	for _, model := range bootstrap.Models {
		if model.Status != "available" && model.Status != "degraded" {
			continue
		}
		for _, capability := range model.Capabilities {
			role := cloudRoleForCapability(capability)
			if role != "" {
				if _, exists := models[role]; !exists {
					models[role] = strings.TrimSpace(model.ModelKey)
				}
			}
		}
	}
	return CloudRuntimeConfig{
		Source: "openai", BaseURL: normalizeCloudModelBaseURL(modelBaseURL),
		AccessToken: strings.TrimSpace(accessToken), Models: models,
	}
}

func normalizeCloudModelBaseURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.TrimRight(value, "/") + "/"
}

func cloudRoleForCapability(capability string) string {
	switch strings.TrimSpace(capability) {
	case "chat":
		return "llm"
	case "vision":
		return "vlm"
	case "embedding":
		return "embed_main"
	case "multimodal_embedding":
		return "embed_image"
	case "rerank":
		return "reranker"
	case "image_generation":
		return "text2image"
	default:
		return ""
	}
}

// FillMissingRoles adds Cloud runtime models only where the user's existing
// personal/shared model selections do not already cover a role.
func FillMissingRoles(existing []SelectedRuntimeModel, cloud CloudRuntimeConfig) []SelectedRuntimeModel {
	result := append([]SelectedRuntimeModel(nil), existing...)
	if strings.TrimSpace(cloud.AccessToken) == "" {
		return result
	}
	covered := make(map[string]struct{}, len(existing))
	for _, row := range existing {
		role := strings.ToLower(strings.TrimSpace(row.ModelType))
		if role != "" {
			covered[role] = struct{}{}
		}
	}
	models := make(map[string]string, len(cloud.Models))
	roles := make([]string, 0, len(cloud.Models))
	for role, model := range cloud.Models {
		role = strings.ToLower(strings.TrimSpace(role))
		if _, exists := models[role]; role != "" && !exists {
			roles = append(roles, role)
		}
		models[role] = strings.TrimSpace(model)
	}
	sort.Strings(roles)
	for _, role := range roles {
		if _, exists := covered[role]; exists {
			continue
		}
		model := models[role]
		if model == "" {
			continue
		}
		result = append(result, SelectedRuntimeModel{
			ModelType: role, ProviderName: strings.TrimSpace(cloud.Source), ModelName: model,
			BaseURL: strings.TrimSpace(cloud.BaseURL), APIKey: strings.TrimSpace(cloud.AccessToken),
		})
		covered[role] = struct{}{}
	}
	return result
}
