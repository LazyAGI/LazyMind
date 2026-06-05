package chat

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"lazymind/core/common"
)

const (
	_authTokenTimeout = 5 * time.Second
	_feishuProvider   = "feishu"
)

// _chatEnabledConnectionItem is a minimal projection of the auth-service connection list response.
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

// fetchFeishuTokens returns all feishu OAuth access tokens for connections that
// have chat_enabled=true for the given userID. Returns nil when none are found.
func fetchFeishuTokens(ctx context.Context, userID string) ([]string, error) {
	fmt.Printf("[Core] [FEISHU_TOKEN] fetchFeishuTokens called userID=%q\n", userID)
	if strings.TrimSpace(userID) == "" {
		fmt.Printf("[Core] [FEISHU_TOKEN] empty userID, skip\n")
		return nil, nil
	}

	// 1. Query auth-service for all feishu connections with chat_enabled=true for this user.
	listURL := fmt.Sprintf(
		"%s/v1/cloud/connections/internal/chat-enabled?provider=%s&owner_user_id=%s",
		common.AuthServiceBaseURL(),
		url.QueryEscape(_feishuProvider),
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
		return nil, fmt.Errorf("list chat-enabled feishu connections: %w", err)
	}
	if len(connectionsResp.Data.Items) == 0 {
		fmt.Printf("[Core] [FEISHU_TOKEN] no chat-enabled feishu connections for userID=%q\n", userID)
		return nil, nil
	}
	fmt.Printf("[Core] [FEISHU_TOKEN] found %d chat-enabled feishu connection(s) for userID=%q\n", len(connectionsResp.Data.Items), userID)

	// 2. Fetch access token for each connection.
	tokens := make([]string, 0, len(connectionsResp.Data.Items))
	for _, item := range connectionsResp.Data.Items {
		connectionID := strings.TrimSpace(item.ConnectionID)
		if connectionID == "" {
			continue
		}
		tokenURL := fmt.Sprintf(
			"%s/v1/cloud/connections/%s/token",
			common.AuthServiceBaseURL(),
			url.PathEscape(connectionID),
		)
		var tokenResp _authTokenResponse
		if err := common.ApiGet(
			ctx,
			tokenURL,
			authServiceInternalHeaders(),
			&tokenResp,
			_authTokenTimeout,
		); err != nil {
			fmt.Printf("[Core] [FEISHU_TOKEN] failed to fetch token for connectionID=%q: %v\n", connectionID, err)
			continue
		}
		tok := strings.TrimSpace(tokenResp.Data.AccessToken)
		if tok != "" {
			fmt.Printf("[Core] [FEISHU_TOKEN] got token len=%d for connectionID=%q\n", len(tok), connectionID)
			tokens = append(tokens, tok)
		}
	}
	return tokens, nil
}
