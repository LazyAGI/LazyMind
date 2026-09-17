package cloudclient

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

type PublicCloudModel struct {
	ModelKey       string   `json:"model_key"`
	DisplayName    string   `json:"display_name"`
	ModelType      string   `json:"model_type,omitempty"`
	Capabilities   []string `json:"capabilities"`
	Status         string   `json:"status"`
	Lifecycle      string   `json:"lifecycle,omitempty"`
	DefaultForType bool     `json:"default_for_type,omitempty"`
}

type ModelProviderBootstrap struct {
	ProviderKey           string             `json:"provider_key"`
	DisplayName           string             `json:"display_name"`
	Available             bool               `json:"available"`
	UnavailableReasonCode *int               `json:"unavailable_reason_code,omitempty"`
	ModelKey              string             `json:"model_key"`
	ConfigVersion         *int64             `json:"config_version,omitempty"`
	CatalogRevision       string             `json:"catalog_revision,omitempty"`
	RefreshedAt           string             `json:"refreshed_at,omitempty"`
	HasTokenPlan          bool               `json:"has_token_plan"`
	CloudChatAvailable    bool               `json:"cloud_chat_available"`
	ReasonCode            string             `json:"reason_code,omitempty"`
	Models                []PublicCloudModel `json:"models"`
}

func (c *Client) GetProviderBootstrap(ctx context.Context, accessToken string) (ModelProviderBootstrap, error) {
	if err := validateBearer(accessToken); err != nil {
		return ModelProviderBootstrap{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.resolve("/v1/provider-bootstrap")+"?catalog_version=2", nil)
	if err != nil {
		return ModelProviderBootstrap{}, err
	}
	setCloudHeaders(request, accessToken)
	var bootstrap ModelProviderBootstrap
	if err := c.doJSON(request, http.StatusOK, &bootstrap, "decode LazyMind Cloud provider bootstrap"); err != nil {
		return ModelProviderBootstrap{}, err
	}
	if bootstrap.ProviderKey != "lazymind-cloud" || bootstrap.DisplayName != "LazyMind Cloud" || strings.TrimSpace(bootstrap.ModelKey) == "" || len(bootstrap.Models) > 1000 ||
		bootstrap.CloudChatAvailable && (!bootstrap.HasTokenPlan || !bootstrap.Available || len(bootstrap.Models) == 0) ||
		bootstrap.ReasonCode != "" && bootstrap.ReasonCode != "token_plan_required" && bootstrap.ReasonCode != "model_unavailable" {
		return ModelProviderBootstrap{}, errors.New("LazyMind Cloud returned an invalid provider bootstrap")
	}
	if len(bootstrap.CatalogRevision) > 128 {
		return ModelProviderBootstrap{}, errors.New("LazyMind Cloud returned an invalid catalog revision")
	}
	for _, model := range bootstrap.Models {
		if !validPublicCloudModel(model) {
			return ModelProviderBootstrap{}, errors.New("LazyMind Cloud returned an invalid public model")
		}
	}
	if bootstrap.RefreshedAt != "" {
		if _, err := time.Parse(time.RFC3339, bootstrap.RefreshedAt); err != nil {
			return ModelProviderBootstrap{}, errors.New("LazyMind Cloud returned an invalid provider refresh time")
		}
	}
	return bootstrap, nil
}

func validPublicCloudModel(model PublicCloudModel) bool {
	if !validPublicModelKey(model.ModelKey) || strings.TrimSpace(model.DisplayName) == "" || len(model.DisplayName) > 128 ||
		(model.Status != "available" && model.Status != "degraded" && model.Status != "unavailable") ||
		len(model.Capabilities) < 1 || len(model.Capabilities) > 3 {
		return false
	}
	if model.ModelType != "" && !validCloudModelType(model.ModelType) {
		return false
	}
	if model.Lifecycle != "" && model.Lifecycle != "active" && model.Lifecycle != "deprecated" && model.Lifecycle != "retired" {
		return false
	}
	if model.DefaultForType && model.ModelType == "" {
		return false
	}
	seenCapabilities := make(map[string]struct{}, len(model.Capabilities))
	for _, capability := range model.Capabilities {
		if !validCloudCapability(capability) {
			return false
		}
		if _, exists := seenCapabilities[capability]; exists {
			return false
		}
		seenCapabilities[capability] = struct{}{}
	}
	return true
}

func validCloudModelType(value string) bool {
	switch value {
	case "llm", "evo_llm", "vlm", "embed_main", "embed_image", "reranker", "text2image", "image_editing", "text2video", "stt", "tts":
		return true
	default:
		return false
	}
}

func validCloudCapability(value string) bool {
	switch value {
	case "chat", "stream", "tool_calls", "vision", "embedding", "multimodal_embedding", "rerank", "image_generation", "image_editing", "video_generation", "speech_to_text", "text_to_speech":
		return true
	default:
		return false
	}
}

func validPublicModelKey(value string) bool {
	if len(value) < 1 || len(value) > 96 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	separator := false
	for _, char := range value[1:] {
		if char == '.' || char == '_' || char == '-' {
			if separator {
				return false
			}
			separator = true
			continue
		}
		if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
			return false
		}
		separator = false
	}
	return !separator
}
