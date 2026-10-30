package modelprovider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"lazymind/core/credentialvault"
)

// Ark has no model-independent health contract. Its MissingParameter response
// is used only to recognize credentials; it does not verify inference or quota.
func doArkCredentialCheck(ctx context.Context, providerName, baseURL, apiKey string, advanced credentialvault.ProviderRequestAdvanced) (*modelCheckResponse, error) {
	failure := func(reason string) (*modelCheckResponse, error) {
		return &modelCheckResponse{Success: false, Message: "provider credential check failed", Reason: reason}, nil
	}
	endpoint, err := ProviderModelsListURL(providerName, baseURL)
	if err != nil {
		return failure("invalid_request")
	}
	endpoint = strings.TrimSuffix(endpoint, "models") + "chat/completions"
	if LazyLLMSource(providerName) == "claude" {
		endpoint = strings.TrimSuffix(endpoint, "chat/completions") + "messages"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("{}"))
	if err != nil {
		return failure("invalid_request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if LazyLLMSource(providerName) == "claude" {
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	applyProviderRequestAdvanced(req, advanced)
	resp, err := remoteModelsHTTPClient.Do(req)
	if err != nil {
		return failure(FailureCodeForRemoteModels(0, nil, err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return failure(FailureCodeForRemoteModels(0, nil, err))
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &payload)
	if resp.StatusCode == http.StatusBadRequest && payload.Error.Code == "MissingParameter" {
		return &modelCheckResponse{Success: true, Message: "API key recognized; inference and quota not verified", Reason: "credentials_recognized"}, nil
	}
	// Account/subscription failures must not be reported as an invalid key.
	if resp.StatusCode == http.StatusUnauthorized {
		switch payload.Error.Code {
		case "AuthenticationError":
			return failure("authentication_failed")
		case "InvalidAccountStatus":
			return failure("permission_denied")
		default:
			return failure("upstream_error")
		}
	}
	reason := FailureCodeForRemoteModels(resp.StatusCode, body, nil)
	if reason == "model_list_unavailable" {
		reason = "model_not_found"
	}
	return failure(reason)
}
