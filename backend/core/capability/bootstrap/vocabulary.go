package bootstrap

import (
	"context"
	"crypto/subtle"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"lazymind/core/common"
)

// Only trusted services choose the user; permissions still come from the live account.
func vocabularyVerifier(secret string) auth.TokenVerifier {
	return func(ctx context.Context, token string, request *http.Request) (*auth.TokenInfo, error) {
		if secret == "" || subtle.ConstantTimeCompare([]byte(token), []byte(secret)) != 1 {
			return nil, auth.ErrInvalidToken
		}
		identity, err := common.GetCurrentIdentity(ctx, common.UserID(request))
		if err != nil {
			return nil, err
		}
		return &auth.TokenInfo{
			UserID: identity.Subject, Scopes: identity.Permissions,
			Extra: map[string]any{"tenant_id": identity.TenantID},
		}, nil
	}
}
