package cloudclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type PublicCloudModel struct {
	ModelKey     string   `json:"model_key"`
	DisplayName  string   `json:"display_name"`
	Capabilities []string `json:"capabilities"`
	Status       string   `json:"status"`
}

type ModelProviderBootstrap struct {
	ProviderKey           string             `json:"provider_key"`
	DisplayName           string             `json:"display_name"`
	Available             bool               `json:"available"`
	UnavailableReasonCode *int               `json:"unavailable_reason_code,omitempty"`
	ModelKey              string             `json:"model_key"`
	ConfigVersion         *int64             `json:"config_version,omitempty"`
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.resolve("/v1/provider-bootstrap"), nil)
	if err != nil {
		return ModelProviderBootstrap{}, err
	}
	setCloudHeaders(request, accessToken)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return ModelProviderBootstrap{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ModelProviderBootstrap{}, decodeCloudError(response)
	}
	var bootstrap ModelProviderBootstrap
	if err := decodeStrictJSON(response, &bootstrap); err != nil {
		return ModelProviderBootstrap{}, fmt.Errorf("decode LazyMind Cloud provider bootstrap: %w", err)
	}
	if bootstrap.ProviderKey != "lazymind-cloud" || bootstrap.DisplayName != "LazyMind Cloud" || strings.TrimSpace(bootstrap.ModelKey) == "" || len(bootstrap.Models) > 1000 ||
		bootstrap.CloudChatAvailable && (!bootstrap.HasTokenPlan || !bootstrap.Available || len(bootstrap.Models) == 0) ||
		bootstrap.ReasonCode != "" && bootstrap.ReasonCode != "token_plan_required" && bootstrap.ReasonCode != "model_unavailable" {
		return ModelProviderBootstrap{}, errors.New("LazyMind Cloud returned an invalid provider bootstrap")
	}
	if bootstrap.RefreshedAt != "" {
		if _, err := time.Parse(time.RFC3339, bootstrap.RefreshedAt); err != nil {
			return ModelProviderBootstrap{}, errors.New("LazyMind Cloud returned an invalid provider refresh time")
		}
	}
	return bootstrap, nil
}
