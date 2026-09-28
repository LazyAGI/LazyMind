package providerconnection

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type registryRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn registryRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestHTTPRegistryLegacyAccessTokenAcceptsAuthServiceTokenResponse(t *testing.T) {
	client := &http.Client{Transport: registryRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
			"code": 200,
			"message": "success",
			"data": {
				"connection_id": "connection-1",
				"provider": "feishu",
				"auth_mode": "oauth_user",
				"access_token": "access-token",
				"token_type": "Bearer",
				"expires_at": "2026-09-21T10:08:49Z",
				"status": "ACTIVE"
			}
		}`)),
			Header:  make(http.Header),
			Request: request,
		}, nil
	})}

	registry := HTTPRegistry{BaseURL: "http://registry.test", InternalToken: "internal-token", HTTPClient: client}
	token, err := registry.LegacyAccessToken(context.Background(), "user-1", "connection-1")
	if err != nil {
		t.Fatalf("LegacyAccessToken() error = %v", err)
	}
	if token != "access-token" {
		t.Fatalf("LegacyAccessToken() = %q, want access-token", token)
	}
}
