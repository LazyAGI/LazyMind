package cloudclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ProviderConnectionCapability struct {
	Capability      string `json:"capability"`
	Status          string `json:"status"`
	ContractVersion string `json:"contract_version"`
}

type ProviderConnectionSession struct {
	SessionID             string                         `json:"session_id"`
	Provider              string                         `json:"provider"`
	Status                string                         `json:"status"`
	AuthorizationStartURL string                         `json:"authorization_start_url,omitempty"`
	ExpiresAt             time.Time                      `json:"expires_at"`
	AuthConnectionID      string                         `json:"auth_connection_id,omitempty"`
	DisplayName           string                         `json:"display_name,omitempty"`
	Capabilities          []ProviderConnectionCapability `json:"capabilities,omitempty"`
	ErrorCode             string                         `json:"error_code,omitempty"`
}

type ProviderConnection struct {
	AuthConnectionID    string                         `json:"auth_connection_id"`
	Provider            string                         `json:"provider"`
	Status              string                         `json:"status"`
	DisplayName         string                         `json:"display_name"`
	ProviderTenantKey   string                         `json:"provider_tenant_key,omitempty"`
	ProviderWorkspaceID string                         `json:"provider_workspace_id,omitempty"`
	ProviderAccountMeta map[string]any                 `json:"provider_account_meta,omitempty"`
	ConnectionMethod    string                         `json:"connection_method"`
	CredentialLocation  string                         `json:"credential_location"`
	Capabilities        []ProviderConnectionCapability `json:"capabilities"`
}

type ProviderConnectionPage struct {
	Items []ProviderConnection `json:"items"`
}

type ProviderAccessTokenLeaseRequest struct {
	SourceID           string `json:"source_id"`
	BindingID          string `json:"binding_id"`
	ContextMode        string `json:"context_mode,omitempty"`
	Consumer           string `json:"consumer"`
	RequiredCapability string `json:"required_capability"`
	RequestID          string `json:"request_id"`
}

type ProviderAccessTokenLease struct {
	AuthConnectionID string    `json:"auth_connection_id"`
	Provider         string    `json:"provider"`
	AccessToken      string    `json:"access_token"`
	TokenType        string    `json:"token_type"`
	SubjectType      string    `json:"subject_type"`
	ExpiresAt        time.Time `json:"expires_at"`
	Status           string    `json:"status"`
	TokenVersion     int64     `json:"token_version"`
}

type ProviderAccessTokenFailureRequest struct {
	TokenVersion       int64  `json:"token_version"`
	ErrorClass         string `json:"error_class"`
	SourceID           string `json:"source_id"`
	BindingID          string `json:"binding_id"`
	ContextMode        string `json:"context_mode,omitempty"`
	Consumer           string `json:"consumer"`
	RequiredCapability string `json:"required_capability"`
	RequestID          string `json:"request_id"`
}

func (c *Client) CreateProviderConnectionSession(ctx context.Context, accessToken, provider, clientInstanceID string) (ProviderConnectionSession, error) {
	body := map[string]string{"provider": provider, "purpose": "connect", "client_instance_id": clientInstanceID}
	var session ProviderConnectionSession
	err := c.providerConnectionJSON(ctx, http.MethodPost, "/v1/provider-connections/sessions", accessToken, clientInstanceID, body, http.StatusCreated, &session)
	return session, err
}

func (c *Client) GetProviderConnectionSession(ctx context.Context, accessToken, clientInstanceID, sessionID string) (ProviderConnectionSession, error) {
	if !isSafeCloudID(sessionID) {
		return ProviderConnectionSession{}, errors.New("invalid Provider Connection session id")
	}
	var session ProviderConnectionSession
	err := c.providerConnectionJSON(ctx, http.MethodGet, "/v1/provider-connections/sessions/"+url.PathEscape(sessionID), accessToken, clientInstanceID, nil, http.StatusOK, &session)
	return session, err
}

func (c *Client) CancelProviderConnectionSession(ctx context.Context, accessToken, clientInstanceID, sessionID string) error {
	if !isSafeCloudID(sessionID) {
		return errors.New("invalid Provider Connection session id")
	}
	return c.providerConnectionJSON(ctx, http.MethodDelete, "/v1/provider-connections/sessions/"+url.PathEscape(sessionID), accessToken, clientInstanceID, nil, http.StatusNoContent, nil)
}

func (c *Client) ListProviderConnections(ctx context.Context, accessToken string) (ProviderConnectionPage, error) {
	var page ProviderConnectionPage
	err := c.providerConnectionJSON(ctx, http.MethodGet, "/v1/provider-connections", accessToken, "", nil, http.StatusOK, &page)
	if page.Items == nil {
		page.Items = []ProviderConnection{}
	}
	return page, err
}

func (c *Client) GetProviderConnection(ctx context.Context, accessToken, connectionID string) (ProviderConnection, error) {
	if !isSafeCloudID(connectionID) {
		return ProviderConnection{}, errors.New("invalid auth_connection_id")
	}
	var connection ProviderConnection
	err := c.providerConnectionJSON(ctx, http.MethodGet, "/v1/provider-connections/"+url.PathEscape(connectionID), accessToken, "", nil, http.StatusOK, &connection)
	return connection, err
}

func (c *Client) ReauthorizeProviderConnection(ctx context.Context, accessToken, clientInstanceID, connectionID string) (ProviderConnectionSession, error) {
	if !isSafeCloudID(connectionID) {
		return ProviderConnectionSession{}, errors.New("invalid auth_connection_id")
	}
	var session ProviderConnectionSession
	err := c.providerConnectionJSON(ctx, http.MethodPost, "/v1/provider-connections/"+url.PathEscape(connectionID)+":reauthorize",
		accessToken, clientInstanceID, map[string]string{"client_instance_id": clientInstanceID}, http.StatusCreated, &session)
	return session, err
}

func (c *Client) RevokeProviderConnection(ctx context.Context, accessToken, connectionID string) error {
	if !isSafeCloudID(connectionID) {
		return errors.New("invalid auth_connection_id")
	}
	return c.providerConnectionJSON(ctx, http.MethodDelete, "/v1/provider-connections/"+url.PathEscape(connectionID), accessToken, "", nil, http.StatusNoContent, nil)
}

func (c *Client) LeaseProviderAccessToken(ctx context.Context, accessToken, clientInstanceID, connectionID string, leaseRequest ProviderAccessTokenLeaseRequest) (ProviderAccessTokenLease, error) {
	if !isSafeCloudID(connectionID) || strings.TrimSpace(clientInstanceID) == "" || !validProviderAccessTokenLeaseRequest(leaseRequest) {
		return ProviderAccessTokenLease{}, errors.New("invalid Provider access token lease request")
	}
	var lease ProviderAccessTokenLease
	err := c.providerConnectionJSON(ctx, http.MethodPost, "/v1/provider-connections/"+url.PathEscape(connectionID)+"/access-token:lease",
		accessToken, clientInstanceID, leaseRequest, http.StatusOK, &lease)
	if err == nil && (lease.AuthConnectionID != connectionID || lease.AccessToken == "" || !lease.ExpiresAt.After(time.Now()) ||
		(lease.Provider == "feishu" && lease.SubjectType != "user")) {
		return ProviderAccessTokenLease{}, errors.New("LazyMind Cloud returned an invalid Provider access token lease")
	}
	return lease, err
}

func (c *Client) ReportProviderAccessTokenFailure(ctx context.Context, accessToken, clientInstanceID, connectionID string, report ProviderAccessTokenFailureRequest) error {
	if !isSafeCloudID(connectionID) || strings.TrimSpace(clientInstanceID) == "" || report.TokenVersion <= 0 ||
		report.ErrorClass != "invalid_token" || !validProviderAccessTokenLeaseRequest(ProviderAccessTokenLeaseRequest{
		SourceID: report.SourceID, BindingID: report.BindingID, ContextMode: report.ContextMode,
		Consumer: report.Consumer, RequiredCapability: report.RequiredCapability, RequestID: report.RequestID,
	}) {
		return errors.New("invalid Provider access token failure report")
	}
	return c.providerConnectionJSON(ctx, http.MethodPost, "/v1/provider-connections/"+url.PathEscape(connectionID)+"/access-token:report",
		accessToken, clientInstanceID, report, http.StatusNoContent, nil)
}

func validProviderAccessTokenLeaseRequest(request ProviderAccessTokenLeaseRequest) bool {
	if strings.TrimSpace(request.Consumer) == "" || strings.TrimSpace(request.RequiredCapability) == "" || strings.TrimSpace(request.RequestID) == "" {
		return false
	}
	mode := strings.TrimSpace(request.ContextMode)
	if mode == "" {
		mode = "source_binding"
	}
	switch mode {
	case "source_binding":
		return strings.TrimSpace(request.SourceID) != "" && strings.TrimSpace(request.BindingID) != ""
	case "pre_binding_browse":
		return strings.TrimSpace(request.SourceID) == "" && strings.TrimSpace(request.BindingID) == "" &&
			request.Consumer == "datasource" && request.RequiredCapability == "datasource.browse"
	default:
		return false
	}
}

func (c *Client) providerConnectionJSON(ctx context.Context, method, path, accessToken, clientInstanceID string, body any, expectedStatus int, output any) error {
	if err := validateBearer(accessToken); err != nil {
		return err
	}
	var requestBody bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&requestBody).Encode(body); err != nil {
			return err
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, c.resolve(path), &requestBody)
	if err != nil {
		return err
	}
	setCloudHeaders(request, accessToken)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if clientInstanceID != "" {
		request.Header.Set("X-LazyMind-Client-Instance", clientInstanceID)
	}
	if err := c.doJSON(request, expectedStatus, output, "decode LazyMind Cloud Provider Connection response"); err != nil {
		return err
	}
	return validateProviderConnectionResponse(output)
}

func validateProviderConnectionResponse(output any) error {
	switch value := output.(type) {
	case *ProviderConnectionSession:
		if !isSafeCloudID(value.SessionID) || value.Provider == "" || value.Status == "" || value.ExpiresAt.IsZero() {
			return errors.New("LazyMind Cloud returned an invalid Provider Connection session")
		}
	case *ProviderConnection:
		if !isSafeCloudID(value.AuthConnectionID) || value.Provider == "" || value.Status == "" || value.ConnectionMethod == "" {
			return errors.New("LazyMind Cloud returned an invalid Provider Connection")
		}
	case *ProviderConnectionPage:
		if len(value.Items) > 100 {
			return errors.New("LazyMind Cloud returned too many Provider Connections")
		}
	}
	return nil
}
