package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"net/http"
	"os"
	"strings"
	"time"
)

var errOAuthRequired = errors.New("MCP authorization required; reconnect this service in settings")

type OAuthReference struct {
	UserID       string `json:"user_id"`
	ServerID     string `json:"server_id"`
	ServerURL    string `json:"server_url"`
	GrantID      string `json:"grant_id"`
	GrantVersion int64  `json:"grant_version"`
}
type oauthResult struct {
	Status           string `json:"status"`
	AuthorizationURL string `json:"authorization_url,omitempty"`
	GrantID          string `json:"grant_id,omitempty"`
	GrantVersion     int64  `json:"grant_version,omitempty"`
	AccessToken      string `json:"access_token,omitempty"`
	TokenVersion     int64  `json:"token_version,omitempty"`
}

func effectiveAuthType(row orm.MCPServer) string {
	if row.AuthType == "" {
		headers, err := decodeHeaders(row.HeadersJSON)
		if err == nil && len(headers) == 0 {
			return "none"
		}
		return "api_key"
	}
	return row.AuthType
}
func validateAuthType(value, transport string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = "api_key"
	}
	if value != "none" && value != "api_key" && value != "oauth" {
		return "", fmt.Errorf("%w: invalid auth_type", errBadRequest)
	}
	if value == "oauth" && transport != transportHTTP {
		return "", fmt.Errorf("%w: OAuth requires Streamable HTTP", errBadRequest)
	}
	return value, nil
}

// Provider payloads and tokens never appear in errors or public responses.
func oauthOperation(ctx context.Context, row orm.MCPServer, operation string, extra map[string]any) (*oauthResult, error) {
	if strings.TrimSpace(row.CreateUserID) == "" || row.ID == "" || row.URL == "" {
		return nil, errForbidden
	}
	token := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	if token == "" {
		return nil, errors.New("MCP authorization service unavailable")
	}
	body := map[string]any{"user_id": row.CreateUserID, "server_id": row.ID, "server_url": row.URL}
	for key, value := range extra {
		body[key] = value
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, common.AuthServiceBaseURL()+"/v1/mcp-oauth/"+operation, bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("MCP authorization service unavailable")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-LazyMind-Internal-Token", token)
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("MCP authorization service unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errOAuthRequired
	}
	if resp.StatusCode == http.StatusBadRequest {
		return nil, fmt.Errorf("%w: invalid or expired MCP authorization; reconnect this service", errBadRequest)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("MCP authorization service unavailable")
	}
	var envelope struct {
		Code int         `json:"code"`
		Data oauthResult `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil || envelope.Code != 200 {
		return nil, errors.New("MCP authorization service unavailable")
	}
	return &envelope.Data, nil
}
func fillOAuthStatus(ctx context.Context, row orm.MCPServer, resp *ServerResponse) {
	if effectiveAuthType(row) != "oauth" {
		return
	}
	resp.APIKeyPreview = ""
	resp.OAuthStatus = "unavailable"
	if result, err := oauthOperation(ctx, row, "status", nil); err == nil {
		resp.OAuthStatus = result.Status
	} else if errors.Is(err, errOAuthRequired) {
		resp.OAuthStatus = "needs_authorization"
	}
}
func OAuthAuthorize(w http.ResponseWriter, r *http.Request)  { handleOAuth(w, r, "authorize") }
func OAuthCallback(w http.ResponseWriter, r *http.Request)   { handleOAuth(w, r, "callback") }
func OAuthDisconnect(w http.ResponseWriter, r *http.Request) { handleOAuth(w, r, "disconnect") }
func handleOAuth(w http.ResponseWriter, r *http.Request, operation string) {
	row, err := getOwnedServer(r.Context(), store.DB(), store.UserID(r), common.PathVar(r, "id"))
	if err != nil {
		replyError(w, err, "MCP authorization failed")
		return
	}
	if effectiveAuthType(*row) != "oauth" || row.Share || row.Transport != transportHTTP {
		replyError(w, errBadRequest, "MCP authorization failed")
		return
	}
	extra := map[string]any{}
	if operation == "callback" {
		var body OAuthCallbackRequest
		if json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&body) != nil || strings.TrimSpace(body.Code) == "" || strings.TrimSpace(body.State) == "" {
			replyError(w, errBadRequest, "MCP authorization failed")
			return
		}
		extra["code"] = body.Code
		extra["state"] = body.State
	}
	result, err := oauthOperation(r.Context(), *row, operation, extra)
	if err != nil {
		replyError(w, err, "MCP authorization service unavailable")
		return
	}
	if operation == "disconnect" || operation == "authorize" {
		if err := store.DB().WithContext(r.Context()).Model(&orm.MCPServer{}).Where("id = ? AND create_user_id = ?", row.ID, row.CreateUserID).Updates(map[string]any{"enabled": false, "is_verified": false, "updated_at": time.Now()}).Error; err != nil {
			replyError(w, err, "MCP authorization update failed")
			return
		}
	}
	common.ReplyOK(w, OAuthResponse{Status: result.Status, AuthorizationURL: result.AuthorizationURL})
}

// A retry stays bound to the original grant. Concurrent reconnect must not
// silently switch an in-flight operation to a new authorization.
func oauthHeaders(ctx context.Context, row orm.MCPServer, grant *oauthResult) (map[string]any, *oauthResult, error) {
	extra := map[string]any{}
	if grant == nil {
		var err error
		grant, err = oauthOperation(ctx, row, "status", nil)
		if err != nil {
			return nil, nil, err
		}
		if grant.Status != "authorized" || grant.GrantID == "" {
			return nil, nil, errOAuthRequired
		}
	} else {
		extra["rejected_token_version"] = grant.TokenVersion
	}
	extra["grant_id"] = grant.GrantID
	extra["grant_version"] = grant.GrantVersion
	token, err := oauthOperation(ctx, row, "token", extra)
	if err != nil {
		return nil, nil, err
	}
	if token.AccessToken == "" {
		return nil, nil, errOAuthRequired
	}
	grant.TokenVersion = token.TokenVersion
	return map[string]any{"Authorization": "Bearer " + token.AccessToken}, grant, nil
}
