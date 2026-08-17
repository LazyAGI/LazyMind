package mcpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"lazymind/core/capability"
)

const defaultAuthResponseBytes = 64 << 10

const extraTenantID = "tenant_id"

type AuthServiceVerifierConfig struct {
	BaseURL          string
	HTTPClient       *http.Client
	MaxResponseBytes int64
}

func NewAuthServiceVerifier(config AuthServiceVerifierConfig) (auth.TokenVerifier, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		return nil, capability.NewError(capability.Internal, "mcp.auth.new", "auth service base URL is required", false, nil)
	}
	if config.HTTPClient == nil {
		return nil, capability.NewError(capability.Internal, "mcp.auth.new", "auth service HTTP client is required", false, nil)
	}
	maxBytes := config.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = defaultAuthResponseBytes
	}
	endpoint := baseURL + "/auth/validate"
	return func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(nil))
		if err != nil {
			return nil, capability.NewError(capability.Internal, "mcp.auth.verify", "build token validation request failed", false, err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := config.HTTPClient.Do(request)
		if err != nil {
			return nil, capability.NewError(capability.Unavailable, "mcp.auth.verify", "auth service unavailable", true, err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
		if err != nil {
			return nil, capability.NewError(capability.Unavailable, "mcp.auth.verify", "read token validation response failed", true, err)
		}
		if int64(len(body)) > maxBytes {
			return nil, capability.NewError(capability.ResultTooLarge, "mcp.auth.verify", "token validation response is too large", false, nil)
		}
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return nil, capability.NewError(capability.Unauthenticated, "mcp.auth.verify", "bearer token rejected", false, auth.ErrInvalidToken)
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return nil, capability.NewError(capability.Unavailable, "mcp.auth.verify", "auth service rejected validation request", response.StatusCode >= 500, nil)
		}
		type validateClaims struct {
			Subject     string   `json:"sub"`
			TenantID    string   `json:"tenant_id"`
			Permissions []string `json:"permissions"`
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, capability.NewError(capability.Unavailable, "mcp.auth.verify", "decode token validation response failed", true, err)
		}
		claimsPayload := body
		if len(envelope.Data) > 0 && string(envelope.Data) != "null" {
			claimsPayload = envelope.Data
		}
		var payload validateClaims
		if err := json.Unmarshal(claimsPayload, &payload); err != nil {
			return nil, capability.NewError(capability.Unavailable, "mcp.auth.verify", "decode token validation claims failed", true, err)
		}
		payload.Subject = strings.TrimSpace(payload.Subject)
		if payload.Subject == "" {
			return nil, capability.NewError(capability.Unauthenticated, "mcp.auth.verify", "token validation response has no subject", false, auth.ErrInvalidToken)
		}
		permissions := compactStrings(payload.Permissions)
		return &auth.TokenInfo{
			UserID: payload.Subject,
			Scopes: permissions,
			Extra: map[string]any{
				extraTenantID: strings.TrimSpace(payload.TenantID),
			},
		}, nil
	}, nil
}

func compactStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
