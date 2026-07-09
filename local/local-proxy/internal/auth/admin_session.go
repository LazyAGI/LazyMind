package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const tokenRefreshSkew = 30 * time.Second

type AdminSession struct {
	Token            string `json:"token"`
	RefreshToken     string `json:"refreshToken,omitempty"`
	Username         string `json:"username"`
	UserID           string `json:"userId,omitempty"`
	Role             string `json:"role,omitempty"`
	Email            string `json:"email,omitempty"`
	DisplayName      string `json:"displayName,omitempty"`
	TenantID         string `json:"tenantId,omitempty"`
	Dynamic          bool   `json:"dynamic,omitempty"`
	ChatUnlikeSwitch bool   `json:"chatUnlikeSwitch,omitempty"`
	Timestamp        int64  `json:"timestamp"`

	expiresAt time.Time
}

type AdminSessionManager struct {
	AuthServiceURL string
	Username       string
	Password       string
	Client         *http.Client

	mu      sync.Mutex
	session *AdminSession
}

func NewAdminSessionManager(authServiceURL string, client *http.Client) *AdminSessionManager {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &AdminSessionManager{
		AuthServiceURL: strings.TrimRight(strings.TrimSpace(authServiceURL), "/"),
		Username:       envText("LAZYMIND_BOOTSTRAP_ADMIN_USERNAME", "admin"),
		Password:       envText("LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD", "admin"),
		Client:         client,
	}
}

func (m *AdminSessionManager) Ensure(ctx context.Context, force bool) (*AdminSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !force && m.session != nil && sessionStillValid(m.session) {
		return cloneAdminSession(m.session), nil
	}

	if !force && m.session != nil && strings.TrimSpace(m.session.RefreshToken) != "" {
		if session, err := m.refresh(ctx, m.session.RefreshToken); err == nil {
			m.session = session
			return cloneAdminSession(session), nil
		}
	}

	session, err := m.login(ctx)
	if err != nil {
		return nil, err
	}
	m.session = session
	return cloneAdminSession(session), nil
}

type loginResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresAt string `json:"refresh_expires_at"`
	TokenType        string `json:"token_type"`
	Role             string `json:"role"`
	ExpiresIn        int    `json:"expires_in"`
	TenantID         string `json:"tenant_id"`
}

type meResponse struct {
	UserID           string `json:"user_id"`
	Username         string `json:"username"`
	DisplayName      string `json:"display_name"`
	Email            string `json:"email"`
	Role             string `json:"role"`
	TenantID         string `json:"tenant_id"`
	Dynamic          bool   `json:"dynamic"`
	ChatUnlikeSwitch bool   `json:"chat_unlike_switch"`
}

func (m *AdminSessionManager) login(ctx context.Context) (*AdminSession, error) {
	var login loginResponse
	if err := m.postJSON(ctx, "/api/authservice/auth/login", map[string]string{
		"username": m.Username,
		"password": m.Password,
	}, &login); err != nil {
		return nil, err
	}
	return m.sessionFromLogin(ctx, login)
}

func (m *AdminSessionManager) refresh(ctx context.Context, refreshToken string) (*AdminSession, error) {
	var login loginResponse
	if err := m.postJSON(ctx, "/api/authservice/auth/refresh", map[string]string{
		"refresh_token": refreshToken,
	}, &login); err != nil {
		return nil, err
	}
	return m.sessionFromLogin(ctx, login)
}

func (m *AdminSessionManager) sessionFromLogin(ctx context.Context, login loginResponse) (*AdminSession, error) {
	if strings.TrimSpace(login.AccessToken) == "" {
		return nil, fmt.Errorf("admin login did not return access token")
	}

	var me meResponse
	if err := m.getJSON(ctx, "/api/authservice/auth/me", login.AccessToken, &me); err != nil {
		return nil, err
	}

	username := strings.TrimSpace(me.Username)
	if username == "" {
		username = m.Username
	}
	role := strings.TrimSpace(me.Role)
	if role == "" {
		role = login.Role
	}
	tenantID := strings.TrimSpace(me.TenantID)
	if tenantID == "" {
		tenantID = login.TenantID
	}

	return &AdminSession{
		Token:            login.AccessToken,
		RefreshToken:     login.RefreshToken,
		Username:         username,
		UserID:           me.UserID,
		Role:             role,
		Email:            me.Email,
		DisplayName:      me.DisplayName,
		TenantID:         tenantID,
		Dynamic:          me.Dynamic,
		ChatUnlikeSwitch: me.ChatUnlikeSwitch,
		Timestamp:        time.Now().UnixMilli(),
		expiresAt:        tokenExpiresAt(login.AccessToken, login.ExpiresIn),
	}, nil
}

func (m *AdminSessionManager) postJSON(ctx context.Context, path string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.AuthServiceURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return m.doJSON(req, out)
}

func (m *AdminSessionManager) getJSON(ctx context.Context, path string, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.AuthServiceURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return m.doJSON(req, out)
}

func (m *AdminSessionManager) doJSON(req *http.Request, out any) error {
	resp, err := m.Client.Do(req)
	if err != nil {
		return fmt.Errorf("auth-service request failed: %w", err)
	}
	defer resp.Body.Close()
	rawBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return readErr
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var payload map[string]any
		_ = json.Unmarshal(rawBody, &payload)
		if detail, ok := payload["detail"].(string); ok && detail != "" {
			return fmt.Errorf("auth-service request failed (%d): %s", resp.StatusCode, detail)
		}
		if message, ok := payload["message"].(string); ok && message != "" {
			return fmt.Errorf("auth-service request failed (%d): %s", resp.StatusCode, message)
		}
		return fmt.Errorf("auth-service request failed with status %d", resp.StatusCode)
	}

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &envelope); err != nil {
		return err
	}
	raw := envelope.Data
	if len(raw) == 0 || string(raw) == "null" {
		raw = rawBody
	}
	return json.Unmarshal(raw, out)
}

func sessionStillValid(session *AdminSession) bool {
	if session == nil || strings.TrimSpace(session.Token) == "" {
		return false
	}
	if session.expiresAt.IsZero() {
		return true
	}
	return time.Until(session.expiresAt) > tokenRefreshSkew
}

func cloneAdminSession(session *AdminSession) *AdminSession {
	if session == nil {
		return nil
	}
	clone := *session
	return &clone
}

func tokenExpiresAt(token string, expiresIn int) time.Time {
	if exp := jwtExpiresAt(token); !exp.IsZero() {
		return exp
	}
	if expiresIn > 0 {
		return time.Now().Add(time.Duration(expiresIn) * time.Second)
	}
	return time.Time{}
}

func jwtExpiresAt(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var payload struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Exp <= 0 {
		return time.Time{}
	}
	return time.Unix(payload.Exp, 0)
}

func envText(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
