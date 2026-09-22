package modelconfig

import (
	"context"
	"crypto/sha256"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"lazymind/core/cloudclient"
	"lazymind/core/modelprovider"
)

const (
	cloudBootstrapTimeout    = 5 * time.Second
	cloudBootstrapFailureTTL = 15 * time.Second
)

var (
	errCloudBootstrapRefreshInFlight = errors.New("Cloud model bootstrap refresh is already in flight")
	errCloudBootstrapBackoff         = errors.New("Cloud model bootstrap is in backoff")
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
	Session      CloudTokenSource
	Client       CloudProviderClient
	Now          func() time.Time
	Locale       string
	mu           sync.Mutex
	cached       cloudclient.ModelProviderBootstrap
	cacheToken   [32]byte
	cacheUntil   time.Time
	failureToken [32]byte
	failureUntil time.Time
	refreshing   bool
}

func (p *CloudRuntimeProvider) RuntimeConfig(ctx context.Context) (CloudRuntimeConfig, bool, error) {
	if p.Session == nil || p.Client == nil {
		return CloudRuntimeConfig{}, false, nil
	}
	bootstrap, token, err := p.currentBootstrap(ctx)
	if err != nil {
		return CloudRuntimeConfig{}, false, nil
	}
	if !bootstrap.HasTokenPlan || !bootstrap.Available {
		return CloudRuntimeConfig{}, false, nil
	}
	return RuntimeConfigFromCloudBootstrap(bootstrap, strings.TrimRight(p.Client.Origin(), "/")+"/v1", token), true, nil
}

func (p *CloudRuntimeProvider) CloudModelCatalog(ctx context.Context) (modelprovider.CloudModelCatalog, error) {
	if p.Session == nil || p.Client == nil {
		return modelprovider.CloudModelCatalog{}, nil
	}
	bootstrap, _, err := p.currentBootstrap(ctx)
	if err != nil {
		return modelprovider.CloudModelCatalog{}, nil
	}
	locale := strings.ToLower(strings.TrimSpace(p.Locale))
	if locale != "en" {
		locale = "zh"
	}
	catalog := modelprovider.CloudModelCatalog{
		Known: true, Available: bootstrap.Available, HasTokenPlan: bootstrap.HasTokenPlan,
		Reason: bootstrap.ReasonCode, ProviderID: modelprovider.CloudSystemProviderID,
		ProviderName: modelprovider.CloudSystemProviderName, CatalogRevision: bootstrap.CatalogRevision,
		PlanURL: strings.TrimRight(p.Client.Origin(), "/") + "/" + locale + "/console#token-plan",
		Models:  make([]modelprovider.CloudCatalogModel, 0, len(bootstrap.Models)),
	}
	for _, item := range bootstrap.Models {
		modelType := cloudModelType(item)
		if modelType == "" {
			continue
		}
		lifecycle := item.Lifecycle
		if lifecycle == "" {
			lifecycle = "active"
		}
		catalog.Models = append(catalog.Models, modelprovider.CloudCatalogModel{
			ModelKey: item.ModelKey, DisplayName: item.DisplayName, ModelType: modelType,
			Capabilities: append([]string(nil), item.Capabilities...), Status: item.Status,
			Lifecycle: lifecycle, DefaultForType: item.DefaultForType,
		})
	}
	return catalog, nil
}

func (p *CloudRuntimeProvider) ResolveRuntimeModel(ctx context.Context, modelType, modelKey string) (SelectedRuntimeModel, bool, error) {
	if p.Session == nil || p.Client == nil {
		return SelectedRuntimeModel{}, false, nil
	}
	bootstrap, token, err := p.currentBootstrap(ctx)
	if err != nil {
		return SelectedRuntimeModel{}, false, err
	}
	if !bootstrap.HasTokenPlan || !bootstrap.Available {
		return SelectedRuntimeModel{}, false, nil
	}
	for _, item := range bootstrap.Models {
		if item.ModelKey != modelKey || cloudModelType(item) != modelType ||
			(item.Status != "available" && item.Status != "degraded") || item.Lifecycle == "retired" {
			continue
		}
		return SelectedRuntimeModel{
			ModelType: modelType, ProviderName: "openai", ModelName: item.ModelKey,
			BaseURL: normalizeCloudModelBaseURL(strings.TrimRight(p.Client.Origin(), "/") + "/v1"),
			APIKey:  token,
		}, true, nil
	}
	return SelectedRuntimeModel{}, false, nil
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
	if availability, ok := p.Session.(interface{ CloudBusinessAvailable() bool }); ok && !availability.CloudBusinessAvailable() {
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
	tokenHash := sha256.Sum256([]byte(token))
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	if now.Before(p.cacheUntil) && p.cacheToken == tokenHash {
		cached := p.cached
		p.mu.Unlock()
		return cached, nil
	}
	if now.Before(p.failureUntil) && p.failureToken == tokenHash {
		p.mu.Unlock()
		return cloudclient.ModelProviderBootstrap{}, errCloudBootstrapBackoff
	}
	if p.refreshing {
		p.mu.Unlock()
		return cloudclient.ModelProviderBootstrap{}, errCloudBootstrapRefreshInFlight
	}
	p.refreshing = true
	p.mu.Unlock()

	requestCtx, cancel := context.WithTimeout(ctx, cloudBootstrapTimeout)
	defer cancel()
	bootstrap, err := p.Client.GetProviderBootstrap(requestCtx, token)

	p.mu.Lock()
	p.refreshing = false
	defer p.mu.Unlock()
	if err != nil {
		p.failureToken = tokenHash
		p.failureUntil = now.Add(cloudBootstrapFailureTTL)
		return cloudclient.ModelProviderBootstrap{}, err
	}
	p.cached = bootstrap
	p.cacheToken = tokenHash
	p.cacheUntil = now.Add(time.Minute)
	p.failureToken = [32]byte{}
	p.failureUntil = time.Time{}
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

type exactRuntimeModelProvider interface {
	ResolveRuntimeModel(context.Context, string, string) (SelectedRuntimeModel, bool, error)
}

func ResolveCloudRuntimeModel(ctx context.Context, modelType, modelKey string) (SelectedRuntimeModel, bool, error) {
	runtimeProviderState.RLock()
	provider := runtimeProviderState.provider
	runtimeProviderState.RUnlock()
	exact, ok := provider.(exactRuntimeModelProvider)
	if !ok {
		return SelectedRuntimeModel{}, false, nil
	}
	return exact.ResolveRuntimeModel(ctx, strings.ToLower(strings.TrimSpace(modelType)), strings.TrimSpace(modelKey))
}

func RuntimeConfigFromCloudBootstrap(bootstrap cloudclient.ModelProviderBootstrap, modelBaseURL, accessToken string) CloudRuntimeConfig {
	models := map[string]string{}
	versionedCatalog := strings.TrimSpace(bootstrap.CatalogRevision) != ""
	for _, model := range bootstrap.Models {
		if model.Status != "available" && model.Status != "degraded" {
			continue
		}
		if versionedCatalog && (model.Lifecycle != "" && model.Lifecycle != "active" || !model.DefaultForType) {
			continue
		}
		role := cloudModelType(model)
		if role == "" {
			continue
		}
		if _, exists := models[role]; !exists || model.DefaultForType {
			models[role] = strings.TrimSpace(model.ModelKey)
		}
	}
	return CloudRuntimeConfig{
		Source: "openai", BaseURL: normalizeCloudModelBaseURL(modelBaseURL),
		AccessToken: strings.TrimSpace(accessToken), Models: models,
	}
}

func cloudModelType(model cloudclient.PublicCloudModel) string {
	modelType := strings.ToLower(strings.TrimSpace(model.ModelType))
	if modelType != "" {
		return modelType
	}
	if modelType = legacyCloudModelTypes[strings.TrimSpace(model.ModelKey)]; modelType != "" {
		return modelType
	}
	for _, capability := range model.Capabilities {
		if modelType = cloudRoleForCapability(capability); modelType != "" {
			return modelType
		}
	}
	return ""
}

var legacyCloudModelTypes = map[string]string{
	"lazymind-text-default":                 "llm",
	"lazymind-evolution-default":            "evo_llm",
	"lazymind-vision-default":               "vlm",
	"lazymind-embedding-default":            "embed_main",
	"lazymind-multimodal-embedding-default": "embed_image",
	"lazymind-rerank-default":               "reranker",
	"lazymind-image-default":                "text2image",
	"lazymind-image-edit-default":           "image_editing",
	"lazymind-video-default":                "text2video",
	"lazymind-stt-default":                  "stt",
	"lazymind-tts-default":                  "tts",
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
	case "image_editing":
		return "image_editing"
	case "video_generation":
		return "text2video"
	case "speech_to_text":
		return "stt"
	case "text_to_speech":
		return "tts"
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
