package chat

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"lazymind/core/common"
)

const (
	_authTokenTimeout    = 5 * time.Second
	_feishuProvider      = "feishu"
	_googleDriveProvider = "googledrive"
	_notionProvider      = "notion"
)

var _cloudToolProviders = []string{_feishuProvider, _googleDriveProvider, _notionProvider}

// _chatEnabledConnectionItem is a minimal projection of the auth-service
// connection list response.
type _chatEnabledConnectionItem struct {
	ConnectionID string `json:"connection_id"`
}

type _chatEnabledConnectionsResponse struct {
	Data struct {
		Items []_chatEnabledConnectionItem `json:"items"`
	} `json:"data"`
}

// _authTokenResponse is a minimal projection of the auth-service token response.
type _authTokenResponse struct {
	Data struct {
		AccessToken string `json:"access_token"`
	} `json:"data"`
}

func authServiceInternalHeaders() map[string]string {
	headers := map[string]string{}
	if tok := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN")); tok != "" {
		headers["X-LazyMind-Internal-Token"] = tok
	}
	return headers
}

// fetchCloudToolConfig returns tool credentials for all chat-enabled cloud
// connections owned by the current user. It intentionally uses auth-service as
// the source of truth, so providers can share the same dynamic-token flow.
func fetchCloudToolConfig(ctx context.Context, userID string) (map[string]any, error) {
	userID = strings.TrimSpace(userID)
	fmt.Printf("[Core] [CLOUD_TOOL_TOKEN] fetchCloudToolConfig called userID=%q\n", userID)
	if userID == "" {
		fmt.Printf("[Core] [CLOUD_TOOL_TOKEN] empty userID, skip\n")
		return nil, nil
	}

	var toolConfig map[string]any
	for _, provider := range _cloudToolProviders {
		tokens, err := fetchCloudProviderTokens(ctx, provider, userID)
		if err != nil {
			return nil, err
		}
		if len(tokens) == 0 {
			continue
		}
		var value any
		if len(tokens) == 1 {
			value = tokens[0]
		} else {
			value = tokens
		}
		toolConfig = mergeToolConfig(toolConfig, map[string]any{provider: value})
	}
	return toolConfig, nil
}

func fetchCloudProviderTokens(ctx context.Context, provider, userID string) ([]string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" || strings.TrimSpace(userID) == "" {
		return nil, nil
	}
	fmt.Printf("[Core] [CLOUD_TOOL_TOKEN] list chat-enabled provider=%q userID=%q\n", provider, userID)

	listURL := fmt.Sprintf(
		"%s/v1/cloud/connections/internal/chat-enabled?provider=%s&owner_user_id=%s",
		common.AuthServiceBaseURL(),
		url.QueryEscape(provider),
		url.QueryEscape(userID),
	)
	var connectionsResp _chatEnabledConnectionsResponse
	err := common.ApiGet(
		ctx,
		listURL,
		authServiceInternalHeaders(),
		&connectionsResp,
		_authTokenTimeout,
	)
	if err != nil {
		return nil, fmt.Errorf("list chat-enabled %s connections: %w", provider, err)
	}
	if len(connectionsResp.Data.Items) == 0 {
		fmt.Printf("[Core] [CLOUD_TOOL_TOKEN] no chat-enabled %s connections for userID=%q\n", provider, userID)
		return nil, nil
	}

	tokens := make([]string, 0, len(connectionsResp.Data.Items))
	for _, item := range connectionsResp.Data.Items {
		connectionID := strings.TrimSpace(item.ConnectionID)
		if connectionID == "" {
			continue
		}
		tokenURL := fmt.Sprintf(
			"%s/v1/cloud/connections/%s/token?user_id=%s",
			common.AuthServiceBaseURL(),
			url.PathEscape(connectionID),
			url.QueryEscape(userID),
		)
		var tokenResp _authTokenResponse
		if err := common.ApiGet(
			ctx,
			tokenURL,
			authServiceInternalHeaders(),
			&tokenResp,
			_authTokenTimeout,
		); err != nil {
			fmt.Printf("[Core] [CLOUD_TOOL_TOKEN] failed to fetch provider=%q token for connectionID=%q: %v\n", provider, connectionID, err)
			continue
		}
		tok := strings.TrimSpace(tokenResp.Data.AccessToken)
		if tok != "" {
			fmt.Printf("[Core] [CLOUD_TOOL_TOKEN] got provider=%q token len=%d for connectionID=%q\n", provider, len(tok), connectionID)
			tokens = append(tokens, tok)
		}
	}
	return tokens, nil
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
