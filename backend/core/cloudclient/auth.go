package cloudclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type AuthTokens struct {
	AccessToken       string  `json:"access_token"`
	RefreshToken      string  `json:"refresh_token"`
	SessionID         string  `json:"session_id"`
	ReauthenticatedAt *string `json:"reauthenticated_at"`
}

func (c *Client) RefreshSession(ctx context.Context, refreshToken string) (AuthTokens, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return AuthTokens{}, errors.New("LazyMind Cloud refresh token is required")
	}
	body, err := json.Marshal(map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return AuthTokens{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.resolve("/v1/auth/refresh"), bytes.NewReader(body))
	if err != nil {
		return AuthTokens{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	var tokens AuthTokens
	if err := c.doJSON(request, http.StatusOK, &tokens, "decode LazyMind Cloud refresh response"); err != nil {
		return AuthTokens{}, err
	}
	if strings.TrimSpace(tokens.AccessToken) == "" || strings.TrimSpace(tokens.RefreshToken) == "" || !isSafeCloudID(tokens.SessionID) {
		return AuthTokens{}, errors.New("LazyMind Cloud returned an incomplete token pair")
	}
	return tokens, nil
}

func (c *Client) LogoutSession(ctx context.Context, accessToken, refreshToken string) error {
	if err := validateBearer(accessToken); err != nil {
		return err
	}
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return errors.New("LazyMind Cloud refresh token is required")
	}
	body, err := json.Marshal(map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.resolve("/v1/auth/logout"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	setCloudHeaders(request, accessToken)
	request.Header.Set("Content-Type", "application/json")
	return c.doJSON(request, http.StatusNoContent, nil, "")
}

func AccessTokenExpiresAt(accessToken string) (time.Time, error) {
	parts := strings.Split(strings.TrimSpace(accessToken), ".")
	if len(parts) != 3 {
		return time.Time{}, errors.New("LazyMind Cloud access token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, errors.New("LazyMind Cloud access token payload is invalid")
	}
	var claims struct {
		ExpiresAt int64 `json:"exp"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&claims); err != nil || claims.ExpiresAt <= 0 {
		return time.Time{}, errors.New("LazyMind Cloud access token has no valid expiry")
	}
	return time.Unix(claims.ExpiresAt, 0).UTC(), nil
}
