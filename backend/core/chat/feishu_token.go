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
	_feishuSourceType   = "feishu"
	_sourceStatusActive = "active"
)

// _scanSourceItem is a minimal projection of the scan-control-plane Source model.
type _scanSourceItem struct {
	ID           string `json:"id"`
	SourceType   string `json:"source_type"`
	Status       string `json:"status"`
	CloudBinding *struct {
		AuthConnectionID string `json:"auth_connection_id"`
	} `json:"cloud_binding,omitempty"`
}

type _scanSourcesResponse struct {
	Items []_scanSourceItem `json:"items"`
}

// _authTokenResponse is a minimal projection of the auth-service token response.
type _authTokenResponse struct {
	AccessToken string `json:"access_token"`
}

func scanControlPlaneEndpoint() string {
	if u := strings.TrimSpace(os.Getenv("LAZYMIND_SCAN_CONTROL_PLANE_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://scan-control-plane:18080"
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
	if strings.TrimSpace(userID) == "" {
		return "", nil
	}

	// 1. List the user's sources from scan-control-plane.
	scanURL := fmt.Sprintf("%s/api/scan/sources", scanControlPlaneEndpoint())
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

	// 2. Find the first active feishu source that has a cloud binding.
	connectionID := ""
	for _, src := range sourcesResp.Items {
		if !strings.EqualFold(src.SourceType, _feishuSourceType) {
			continue
		}
		if !strings.EqualFold(src.Status, _sourceStatusActive) {
			continue
		}
		if src.CloudBinding == nil || strings.TrimSpace(src.CloudBinding.AuthConnectionID) == "" {
			continue
		}
		connectionID = src.CloudBinding.AuthConnectionID
		break
	}

	if connectionID == "" {
		return "", nil
	}

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

	return strings.TrimSpace(tokenResp.AccessToken), nil
}
