package modelconfig

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"lazymind/core/common"
)

var errWriterCredentialLoad = errors.New("load cloud document authorization failed")

type writerConnectionList struct {
	Data *struct {
		Items []*struct {
			ConnectionID string `json:"connection_id"`
			Provider     string `json:"provider"`
			OwnerUserID  string `json:"owner_user_id"`
			Status       string `json:"status"`
		} `json:"items"`
	} `json:"data"`
}
type writerConnectionToken struct {
	Data *struct {
		ConnectionID string `json:"connection_id"`
		Provider     string `json:"provider"`
		Status       string `json:"status"`
		AccessToken  string `json:"access_token"`
	} `json:"data"`
}

// LoadWriterProviderToolConfig reads only the selected provider's credentials.
// Unlike the general chat loader, document writes require all listed tokens to
// load successfully. An Auth failure must never become a partial account set.
func LoadWriterProviderToolConfig(ctx context.Context, provider, userID string) (map[string]any, error) {
	provider = strings.TrimSpace(provider)
	userID = strings.TrimSpace(userID)
	if provider == "" || userID == "" {
		return nil, errWriterCredentialLoad
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	headers := map[string]string{}
	if token := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN")); token != "" {
		headers["X-LazyMind-Internal-Token"] = token
	}
	endpoint := fmt.Sprintf("%s/v1/cloud/connections/internal/chat-enabled?provider=%s&owner_user_id=%s", common.AuthServiceBaseURL(), url.QueryEscape(provider), url.QueryEscape(userID))
	var connections writerConnectionList
	if err := common.ApiGet(ctx, endpoint, headers, &connections, cloudToolTokenTimeout); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errWriterCredentialLoad
	}
	if connections.Data == nil || connections.Data.Items == nil {
		return nil, errWriterCredentialLoad
	}
	// Validate the whole list before fetching any secret, including owner and
	// provider identity. The internal-token header alone is not a data match.
	seen := make(map[string]struct{}, len(connections.Data.Items))
	for _, item := range connections.Data.Items {
		if item == nil || strings.TrimSpace(item.ConnectionID) == "" || item.Provider != provider || item.OwnerUserID != userID || item.Status != "ACTIVE" {
			return nil, errWriterCredentialLoad
		}
		if _, duplicate := seen[item.ConnectionID]; duplicate {
			return nil, errWriterCredentialLoad
		}
		seen[item.ConnectionID] = struct{}{}
	}
	tokens := make([]string, 0, len(connections.Data.Items))
	for _, item := range connections.Data.Items {
		endpoint := fmt.Sprintf("%s/v1/cloud/connections/%s/token?user_id=%s", common.AuthServiceBaseURL(), url.PathEscape(item.ConnectionID), url.QueryEscape(userID))
		var response writerConnectionToken
		if err := common.ApiGet(ctx, endpoint, headers, &response, cloudToolTokenTimeout); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errWriterCredentialLoad
		}
		if response.Data == nil || response.Data.ConnectionID != item.ConnectionID || response.Data.Provider != provider || response.Data.Status != "ACTIVE" || strings.TrimSpace(response.Data.AccessToken) == "" {
			return nil, errWriterCredentialLoad
		}
		tokens = append(tokens, strings.TrimSpace(response.Data.AccessToken))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		// Known cloud providers cannot write without an authorized connection.
		// Local/custom providers may legitimately require no cloud credentials.
		if IsCloudToolProvider(provider) {
			return nil, errWriterCredentialLoad
		}
		return nil, nil
	}
	if len(tokens) == 1 {
		return map[string]any{provider: tokens[0]}, nil
	}
	return map[string]any{provider: tokens}, nil
}
