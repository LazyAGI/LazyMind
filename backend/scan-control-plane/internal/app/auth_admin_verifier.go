package app

import (
	"context"
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
	baseURL    *url.URL
	httpClient *http.Client
}

func newAuthServiceAdminVerifier(baseURL string, client *http.Client) (*authServiceAdminVerifier, error) {
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
	return &authServiceAdminVerifier{baseURL: parsed, httpClient: client}, nil
}

func (v *authServiceAdminVerifier) IsAdmin(ctx context.Context, actor access.Actor) (bool, error) {
	if v == nil || v.baseURL == nil || v.httpClient == nil {
		return false, fmt.Errorf("auth service admin verifier is not configured")
	}
	authorization := strings.TrimSpace(actor.Authorization)
	if authorization == "" {
		return false, nil
	}
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
