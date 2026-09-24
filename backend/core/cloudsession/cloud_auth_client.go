package cloudsession

import (
	"context"
	"errors"

	"lazymind/core/cloudclient"
)

type CloudAuthClient struct {
	Client *cloudclient.Client
}

func (c CloudAuthClient) Refresh(ctx context.Context, refreshToken RefreshToken) (TokenPair, error) {
	if c.Client == nil {
		return TokenPair{}, errors.New("LazyMind Cloud auth client is unavailable")
	}
	tokens, err := c.Client.RefreshSession(ctx, string(refreshToken))
	if err != nil {
		return TokenPair{}, err
	}
	expiresAt, err := cloudclient.AccessTokenExpiresAt(tokens.AccessToken)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, AccessExpiresAt: expiresAt}, nil
}

func (c CloudAuthClient) Logout(ctx context.Context, accessToken string, refreshToken RefreshToken) error {
	if c.Client == nil {
		return errors.New("LazyMind Cloud auth client is unavailable")
	}
	return c.Client.LogoutSession(ctx, accessToken, string(refreshToken))
}

var _ AuthClient = CloudAuthClient{}
var _ LogoutClient = CloudAuthClient{}
