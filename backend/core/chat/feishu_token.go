package chat

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"lazymind/core/common"
)

const (
	_scanSourcesTimeout = 5 * time.Second
	_authTokenTimeout   = 5 * time.Second
	_feishuProvider     = "feishu"
	_cloudBindingActive = "active"
)

// _scanSourceItem is a minimal projection of the scan-control-plane Source model.
type _scanSourceItem struct {
	ID           string `json:"id"`
	SourceType   string `json:"source_type"`
	Status       string `json:"status"`
	CloudBinding *struct {
		AuthConnectionID string `json:"auth_connection_id"`
		Provider         string `json:"provider"`
		Status           string `json:"status"`
	} `json:"cloud_binding,omitempty"`
}

type _scanSourcesResponse struct {
	Items []_scanSourceItem `json:"items"`
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

// fetchFeishuToken looks up the first active feishu source for userID,
// retrieves its OAuth access token from auth-service, and returns it.
// Returns ("", nil) when the user has no active feishu source.
func fetchFeishuToken(ctx context.Context, r *http.Request, userID string) (string, error) {
	fmt.Printf("[Core] [FEISHU_TOKEN] fetchFeishuToken called userID=%q\n", userID)
	if strings.TrimSpace(userID) == "" {
		fmt.Printf("[Core] [FEISHU_TOKEN] empty userID, skip\n")
		return "", nil
	}

	// 1. List the user's sources from scan-control-plane.
	scanURL := fmt.Sprintf("%s/api/scan/sources", common.ScanControlPlaneEndpoint())
	var sourcesResp _scanSourcesResponse
	err := common.ApiGet(
		ctx,
		scanURL,
		map[string]string{"X-User-Id": userID},
		&sourcesResp,
		_scanSourcesTimeout,
	)
	if err != nil {
		return "", fmt.Errorf("list scan sources: %w", err)
	}

	// 2. Find the first feishu cloud binding with an active status and a connection ID.
	// The source_type may be "cloud_sync" with provider info inside cloud_binding,
	// so we check cloud_binding.provider == "feishu" and cloud_binding.status == "ACTIVE".
	connectionID := ""
	for _, src := range sourcesResp.Items {
		cb := src.CloudBinding
		if cb == nil || strings.TrimSpace(cb.AuthConnectionID) == "" {
			continue
		}
		if !strings.EqualFold(cb.Provider, _feishuProvider) {
			continue
		}
		if !strings.EqualFold(cb.Status, _cloudBindingActive) {
			continue
		}
		connectionID = cb.AuthConnectionID
		break
	}

	if connectionID == "" {
		fmt.Printf("[Core] [FEISHU_TOKEN] no active feishu binding found for userID=%q (total sources=%d)\n", userID, len(sourcesResp.Items))
		return "", nil
	}
	fmt.Printf("[Core] [FEISHU_TOKEN] found connectionID=%q for userID=%q\n", connectionID, userID)

	// 3. Fetch the access token from auth-service using the internal token.
	tokenURL := fmt.Sprintf(
		"%s/v1/cloud/connections/%s/token",
		common.AuthServiceBaseURL(),
		connectionID,
	)
	var tokenResp _authTokenResponse
	err = common.ApiGet(
		ctx,
		tokenURL,
		authServiceInternalHeaders(),
		&tokenResp,
		_authTokenTimeout,
	)
	if err != nil {
		return "", fmt.Errorf("fetch feishu token for connection %s: %w", connectionID, err)
	}

	tok := strings.TrimSpace(tokenResp.Data.AccessToken)
	fmt.Printf("[Core] [FEISHU_TOKEN] got token len=%d for connectionID=%q\n", len(tok), connectionID)
	return tok, nil
}
