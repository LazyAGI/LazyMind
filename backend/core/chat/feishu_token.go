package chat

import (
	"context"
	"net/http"
	"os"
	"strings"

	"lazymind/core/modelconfig"
)

const _feishuProvider = "feishu"

var _cloudToolProviders = []string{"feishu", "googledrive", "notion"}

func authServiceInternalHeaders() map[string]string {
	headers := map[string]string{}
	if token := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN")); token != "" {
		headers["X-LazyMind-Internal-Token"] = token
	}
	return headers
}

// fetchCloudToolConfig returns tool credentials for all chat-enabled cloud
// connections owned by the current user. It intentionally uses auth-service as
// the source of truth, so providers can share the same dynamic-token flow.
func fetchCloudToolConfig(ctx context.Context, userID string) (map[string]any, error) {
	return modelconfig.LoadCloudToolConfig(ctx, userID)
}

func fetchCloudProviderTokens(ctx context.Context, provider, userID string) ([]string, error) {
	return modelconfig.LoadCloudProviderTokens(ctx, provider, userID)
}

// fetchFeishuTokens keeps the old helper available for focused tests and callers.
func fetchFeishuTokens(ctx context.Context, userID string) ([]string, error) {
	return fetchCloudProviderTokens(ctx, _feishuProvider, userID)
}

// fetchFeishuToken keeps the older single-token helper available for focused
// tests and callers.
func fetchFeishuToken(ctx context.Context, _ *http.Request, userID string) (string, error) {
	tokens, err := fetchFeishuTokens(ctx, userID)
	if err != nil || len(tokens) == 0 {
		return "", err
	}
	return tokens[0], nil
}
