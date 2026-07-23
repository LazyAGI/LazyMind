package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/access"
)

type authServiceAdminVerifier struct {
	baseURL       *url.URL
	internalToken string
	httpClient    *http.Client
}

func newAuthServiceAdminVerifier(baseURL, internalToken string, client *http.Client) (*authServiceAdminVerifier, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("base url must include scheme and host")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &authServiceAdminVerifier{
		baseURL:       parsed,
		internalToken: strings.TrimSpace(internalToken),
		httpClient:    client,
	}, nil
}

func (v *authServiceAdminVerifier) IsAdmin(ctx context.Context, actor access.Actor) (bool, error) {
	if v == nil || v.baseURL == nil || v.httpClient == nil {
		return false, fmt.Errorf("auth service admin verifier is not configured")
	}
	authorization := strings.TrimSpace(actor.Authorization)
	if authorization != "" {
		return v.isAdminWithAuthorization(ctx, authorization)
	}
	if !internalTokenMatches(v.internalToken, actor.InternalToken) {
		return false, nil
	}
	return v.isAdminWithInternalToken(ctx, actor.UserID)
}

func (v *authServiceAdminVerifier) isAdminWithAuthorization(ctx context.Context, authorization string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authServiceEndpoint(v.baseURL, "/api/authservice/auth/validate"), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", authorization)
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return false, fmt.Errorf("auth service validate failed: %s", resp.Status)
	}
	role, err := decodeAuthServiceRole(resp.Body)
	if err != nil {
		return false, err
	}
	return authServiceRoleIsAdmin(role), nil
}

func (v *authServiceAdminVerifier) isAdminWithInternalToken(ctx context.Context, userID string) (bool, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false, nil
	}
	endpoint := authServiceEndpoint(v.baseURL, "/api/authservice/user/"+url.PathEscape(userID)+"/role/internal")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-LazyMind-Internal-Token", v.internalToken)
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return false, fmt.Errorf("auth service validate failed: internal role lookup returned %s", resp.Status)
	}
	var payload struct {
		UserID   string `json:"user_id"`
		Role     string `json:"role"`
		Disabled bool   `json:"disabled"`
		Data     struct {
			UserID   string `json:"user_id"`
			Role     string `json:"role"`
			Disabled bool   `json:"disabled"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false, err
	}
	responseUserID := strings.TrimSpace(payload.UserID)
	responseRole := payload.Role
	responseDisabled := payload.Disabled
	if responseUserID == "" {
		responseUserID = strings.TrimSpace(payload.Data.UserID)
		responseRole = payload.Data.Role
		responseDisabled = payload.Data.Disabled
	}
	if responseDisabled || responseUserID != userID {
		return false, nil
	}
	return authServiceRoleIsAdmin(responseRole), nil
}

func internalTokenMatches(expected, actual string) bool {
	expected = strings.TrimSpace(expected)
	actual = strings.TrimSpace(actual)
	if expected == "" || actual == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

func decodeAuthServiceRole(r io.Reader) (string, error) {
	var payload struct {
		Role string `json:"role"`
		Data struct {
			Role string `json:"role"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&payload); err != nil {
		return "", err
	}
	if role := strings.TrimSpace(payload.Role); role != "" {
		return role, nil
	}
	return strings.TrimSpace(payload.Data.Role), nil
}

func authServiceRoleIsAdmin(role string) bool {
	normalized := strings.ToLower(strings.TrimSpace(role))
	switch normalized {
	case "system-admin", "system_admin", "admin":
		return true
	default:
		return strings.HasSuffix(normalized, ".admin")
	}
}

func authServiceEndpoint(base *url.URL, endpointPath string) string {
	u := *base
	u.Path = path.Join(base.Path, endpointPath)
	u.RawQuery = ""
	return u.String()
}
