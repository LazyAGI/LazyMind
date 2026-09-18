package app

import (
	"context"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/access"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector/feishu"
)

type authConnectionVerifier struct {
	client feishu.AuthConnectionClient
}

type authConnectionHTTPVerifier interface {
	Verify(ctx context.Context, authConnectionID, userID, tenantID string) error
}

type authConnectionStatusVerifier interface {
	BatchStatus(ctx context.Context, req feishu.ConnectionStatusRequest) (map[string]feishu.ConnectionStatus, error)
}

func newAuthConnectionVerifier(client feishu.AuthConnectionClient) *authConnectionVerifier {
	return &authConnectionVerifier{client: client}
}

func (v *authConnectionVerifier) VerifyAuthConnection(ctx context.Context, actor access.Actor, authConnectionID string) error {
	if v == nil || v.client == nil {
		return access.NewError(access.ErrCodeForbidden, "auth connection verifier is not configured")
	}
	if verifier, ok := v.client.(authConnectionStatusVerifier); ok {
		statuses, err := verifier.BatchStatus(ctx, feishu.ConnectionStatusRequest{
			ConnectionIDs: []string{authConnectionID}, UserID: actor.UserID, TenantID: actor.TenantID,
		})
		if err != nil {
			return err
		}
		status, found := statuses[strings.TrimSpace(authConnectionID)]
		if !found || status.OwnerUserID != "" && status.OwnerUserID != actor.UserID {
			return access.NewError(access.ErrCodeForbidden, "access denied")
		}
		switch strings.ToUpper(strings.TrimSpace(status.Status)) {
		case "ACTIVE", "PARTIAL", "RETRYING":
			return nil
		default:
			return access.NewError(access.ErrCodeForbidden, "access denied")
		}
	}
	if verifier, ok := v.client.(authConnectionHTTPVerifier); ok {
		return verifier.Verify(ctx, authConnectionID, actor.UserID, actor.TenantID)
	}
	token, err := v.client.GetToken(ctx, feishu.TokenRequest{
		AuthConnectionID: authConnectionID,
		UserID:           actor.UserID,
	})
	if err != nil {
		return err
	}
	if token.AccessToken == "" {
		return connector.NewError(feishu.ErrorCodeAuthInvalid, "access token is empty")
	}
	return nil
}
