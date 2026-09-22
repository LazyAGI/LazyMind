package providerconnection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"lazymind/core/cloudclient"
	"lazymind/core/cloudsession"
	"lazymind/core/common"
)

var (
	ErrCloudUnavailable    = errors.New("cloud_unavailable")
	ErrCloudReauthRequired = errors.New("cloud_reauth_required")
	ErrManagedTokenBridge  = errors.New("managed Provider token requires Core Bridge")
)

type ConnectionMeta struct {
	AuthConnectionID          string         `json:"connection_id"`
	TenantID                  string         `json:"tenant_id"`
	OwnerUserID               string         `json:"owner_user_id"`
	Provider                  string         `json:"provider"`
	AuthMode                  string         `json:"auth_mode"`
	ConnectionMethod          string         `json:"connection_method"`
	CredentialLocation        string         `json:"credential_location"`
	ProfileRef                string         `json:"profile_ref"`
	CloudConnectionID         string         `json:"cloud_connection_id"`
	CloudOwnerUserID          string         `json:"cloud_owner_user_id"`
	AppID                     string         `json:"app_id"`
	ProviderAccountID         string         `json:"provider_account_id"`
	DisplayName               string         `json:"display_name"`
	ProviderTenantKey         string         `json:"provider_tenant_key"`
	ProviderWorkspaceID       string         `json:"provider_workspace_id"`
	CapabilityContractVersion string         `json:"capability_contract_version"`
	ProviderAccountMeta       map[string]any `json:"provider_account_meta"`
	ProviderOptions           map[string]any `json:"provider_options"`
	Scope                     string         `json:"scope"`
	LastUsedAt                *string        `json:"last_used_at"`
	Status                    string         `json:"status"`
	LastError                 string         `json:"last_error"`
	CreatedAt                 string         `json:"created_at"`
	UpdatedAt                 string         `json:"updated_at"`
}

type ManagedMirror struct {
	AuthConnectionID          string
	LocalOwnerUserID          string
	CloudOwnerUserID          string
	Provider                  string
	DisplayName               string
	ProviderTenantKey         string
	ProviderWorkspaceID       string
	ProviderAccountMeta       map[string]any
	Status                    string
	CapabilityContractVersion string
	Capabilities              []cloudclient.ProviderConnectionCapability
}

type LocalConnectionRegistry interface {
	Connection(context.Context, string, string) (ConnectionMeta, error)
	LegacyAccessToken(context.Context, string, string) (string, error)
	UpsertManaged(context.Context, ManagedMirror) error
}

type SourceBindingAuthorizer interface {
	Authorize(context.Context, string, string, string, string, string) error
}

type preBindingBrowseAuthorizer interface {
	AuthorizePreBindingBrowse(context.Context, string, string, string) error
}

const (
	ContextModeSourceBinding    = "source_binding"
	ContextModePreBindingBrowse = "pre_binding_browse"
)

type ResolveRequest struct {
	AuthConnectionID   string `json:"auth_connection_id"`
	UserID             string `json:"user_id"`
	TenantID           string `json:"tenant_id"`
	SourceID           string `json:"source_id"`
	BindingID          string `json:"binding_id"`
	ContextMode        string `json:"context_mode"`
	Consumer           string `json:"consumer"`
	RequiredCapability string `json:"required_capability"`
}

type ResolvedToken struct {
	AuthConnectionID string    `json:"auth_connection_id"`
	Provider         string    `json:"provider"`
	AccessToken      string    `json:"access_token"`
	TokenType        string    `json:"token_type"`
	SubjectType      string    `json:"subject_type"`
	ExpiresAt        time.Time `json:"expires_at"`
	Status           string    `json:"status"`
	TokenVersion     int64     `json:"token_version"`
}

type AccessTokenFailureReport struct {
	AuthConnectionID   string `json:"auth_connection_id"`
	UserID             string `json:"user_id"`
	TenantID           string `json:"tenant_id"`
	SourceID           string `json:"source_id"`
	BindingID          string `json:"binding_id"`
	ContextMode        string `json:"context_mode"`
	Consumer           string `json:"consumer"`
	RequiredCapability string `json:"required_capability"`
	TokenVersion       int64  `json:"token_version"`
	ErrorClass         string `json:"error_class"`
	RequestID          string `json:"request_id"`
}

type cachedAccessToken struct {
	resolved  ResolvedToken
	expiresAt time.Time
}

type feishuCLIHandle struct {
	OwnerUserID  string
	ConnectionID string
	ProfileRef   string
	ExpiresAt    time.Time
}

type Service struct {
	Cloud            *cloudclient.Client
	Session          *cloudsession.Service
	Registry         LocalConnectionRegistry
	Authorizer       SourceBindingAuthorizer
	ClientInstanceID string
	Now              func() time.Time
	FeishuCLI        FeishuCLIBackend

	cacheMu     sync.Mutex
	cached      map[string]cachedAccessToken
	cliHandleMu sync.Mutex
	cliHandles  map[string]feishuCLIHandle
}

type ProviderConnectionBridge = Service

func NewService(cloud *cloudclient.Client, session *cloudsession.Service, registry LocalConnectionRegistry, authorizer SourceBindingAuthorizer, clientInstanceID string) (*Service, error) {
	clientInstanceID = strings.TrimSpace(clientInstanceID)
	if cloud == nil || session == nil || registry == nil || authorizer == nil || len(clientInstanceID) < 16 {
		return nil, errors.New("invalid Provider Connection Bridge configuration")
	}
	return &Service{Cloud: cloud, Session: session, Registry: registry, Authorizer: authorizer, ClientInstanceID: clientInstanceID, Now: time.Now, cached: map[string]cachedAccessToken{}, cliHandles: map[string]feishuCLIHandle{}}, nil
}

func NewLocalService(registry LocalConnectionRegistry, authorizer SourceBindingAuthorizer, clientInstanceID string) (*Service, error) {
	clientInstanceID = strings.TrimSpace(clientInstanceID)
	if registry == nil || authorizer == nil || len(clientInstanceID) < 16 {
		return nil, errors.New("invalid local Provider Connection configuration")
	}
	return &Service{Registry: registry, Authorizer: authorizer, ClientInstanceID: clientInstanceID, Now: time.Now, cached: map[string]cachedAccessToken{}, cliHandles: map[string]feishuCLIHandle{}}, nil
}

func (service *Service) CreateSession(ctx context.Context, provider string) (cloudclient.ProviderConnectionSession, error) {
	return service.CreateSessionForUser(ctx, "", provider)
}

func (service *Service) CreateSessionForUser(ctx context.Context, userID, provider string) (cloudclient.ProviderConnectionSession, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "feishu" {
		if service.FeishuCLI == nil {
			return cloudclient.ProviderConnectionSession{}, ErrCLINotInstalled
		}
		return service.FeishuCLI.Start(ctx, strings.TrimSpace(userID), "")
	}
	accessToken, err := service.cloudAccessToken(ctx)
	if err != nil {
		return cloudclient.ProviderConnectionSession{}, err
	}
	return service.Cloud.CreateProviderConnectionSession(ctx, accessToken, strings.ToLower(strings.TrimSpace(provider)), service.ClientInstanceID)
}

func (service *Service) GetSession(ctx context.Context, userID, sessionID string) (cloudclient.ProviderConnectionSession, error) {
	if strings.HasPrefix(strings.TrimSpace(sessionID), "fcli_") {
		if service.FeishuCLI == nil {
			return cloudclient.ProviderConnectionSession{}, ErrCLINotInstalled
		}
		return service.FeishuCLI.Get(ctx, strings.TrimSpace(userID), strings.TrimSpace(sessionID))
	}
	accessToken, err := service.cloudAccessToken(ctx)
	if err != nil {
		return cloudclient.ProviderConnectionSession{}, err
	}
	session, err := service.Cloud.GetProviderConnectionSession(ctx, accessToken, service.ClientInstanceID, sessionID)
	if err != nil {
		return cloudclient.ProviderConnectionSession{}, err
	}
	if session.Status == "COMPLETED" && session.AuthConnectionID != "" {
		connection, getErr := service.Cloud.GetProviderConnection(ctx, accessToken, session.AuthConnectionID)
		if getErr != nil {
			return cloudclient.ProviderConnectionSession{}, getErr
		}
		account, accountErr := service.Cloud.GetCurrentAccount(ctx, accessToken)
		if accountErr != nil {
			return cloudclient.ProviderConnectionSession{}, accountErr
		}
		if err := service.Registry.UpsertManaged(ctx, ManagedMirror{
			AuthConnectionID: connection.AuthConnectionID, LocalOwnerUserID: userID, CloudOwnerUserID: account.ID, Provider: connection.Provider,
			DisplayName: connection.DisplayName, ProviderTenantKey: connection.ProviderTenantKey,
			ProviderWorkspaceID: connection.ProviderWorkspaceID, Status: connection.Status,
			ProviderAccountMeta:       connection.ProviderAccountMeta,
			CapabilityContractVersion: "provider-capabilities/v1", Capabilities: connection.Capabilities,
		}); err != nil {
			return cloudclient.ProviderConnectionSession{}, err
		}
		service.cacheMu.Lock()
		delete(service.cached, session.AuthConnectionID)
		service.cacheMu.Unlock()
	}
	return session, nil
}

func (service *Service) CancelSession(ctx context.Context, sessionID string) error {
	return service.CancelSessionForUser(ctx, "", sessionID)
}

func (service *Service) CancelSessionForUser(ctx context.Context, userID, sessionID string) error {
	if strings.HasPrefix(strings.TrimSpace(sessionID), "fcli_") {
		if service.FeishuCLI == nil {
			return ErrCLINotInstalled
		}
		return service.FeishuCLI.Cancel(ctx, strings.TrimSpace(userID), strings.TrimSpace(sessionID))
	}
	accessToken, err := service.cloudAccessToken(ctx)
	if err != nil {
		return err
	}
	return service.Cloud.CancelProviderConnectionSession(ctx, accessToken, service.ClientInstanceID, sessionID)
}

func (service *Service) ListConnections(ctx context.Context, userID string) (cloudclient.ProviderConnectionPage, error) {
	userID = strings.TrimSpace(userID)
	accessToken, err := service.cloudAccessToken(ctx)
	if err != nil {
		return cloudclient.ProviderConnectionPage{}, err
	}
	page, err := service.Cloud.ListProviderConnections(ctx, accessToken)
	if err != nil || len(page.Items) == 0 {
		return page, err
	}
	account, err := service.Cloud.GetCurrentAccount(ctx, accessToken)
	if err != nil {
		return cloudclient.ProviderConnectionPage{}, err
	}
	for _, connection := range page.Items {
		if err := service.Registry.UpsertManaged(ctx, ManagedMirror{
			AuthConnectionID: connection.AuthConnectionID, LocalOwnerUserID: userID, CloudOwnerUserID: account.ID, Provider: connection.Provider,
			DisplayName: connection.DisplayName, ProviderTenantKey: connection.ProviderTenantKey,
			ProviderWorkspaceID: connection.ProviderWorkspaceID, Status: connection.Status,
			ProviderAccountMeta:       connection.ProviderAccountMeta,
			CapabilityContractVersion: "provider-capabilities/v1", Capabilities: connection.Capabilities,
		}); err != nil {
			return cloudclient.ProviderConnectionPage{}, err
		}
		service.cacheMu.Lock()
		delete(service.cached, connection.AuthConnectionID)
		service.cacheMu.Unlock()
	}
	return page, nil
}

func (service *Service) Reauthorize(ctx context.Context, connectionID string) (cloudclient.ProviderConnectionSession, error) {
	return service.ReauthorizeForUser(ctx, "", connectionID)
}

func (service *Service) ReauthorizeForUser(ctx context.Context, userID, connectionID string) (cloudclient.ProviderConnectionSession, error) {
	meta, metaErr := service.Registry.Connection(ctx, strings.TrimSpace(userID), strings.TrimSpace(connectionID))
	if metaErr == nil && meta.ConnectionMethod == "cli_personal_app" {
		if service.FeishuCLI == nil {
			return cloudclient.ProviderConnectionSession{}, ErrCLINotInstalled
		}
		return service.FeishuCLI.Start(ctx, strings.TrimSpace(userID), strings.TrimSpace(connectionID))
	}
	if metaErr == nil && meta.Provider == "feishu" && meta.ConnectionMethod == "managed_oauth" {
		if service.FeishuCLI == nil {
			return cloudclient.ProviderConnectionSession{}, ErrCLINotInstalled
		}
		return service.FeishuCLI.Start(ctx, strings.TrimSpace(userID), "")
	}
	accessToken, err := service.cloudAccessToken(ctx)
	if err != nil {
		return cloudclient.ProviderConnectionSession{}, err
	}
	return service.Cloud.ReauthorizeProviderConnection(ctx, accessToken, service.ClientInstanceID, connectionID)
}

func (service *Service) Revoke(ctx context.Context, connectionID string) error {
	accessToken, err := service.cloudAccessToken(ctx)
	if err != nil {
		return err
	}
	service.cacheMu.Lock()
	delete(service.cached, connectionID)
	service.cacheMu.Unlock()
	return service.Cloud.RevokeProviderConnection(ctx, accessToken, connectionID)
}

func (service *Service) ResolveAccessToken(ctx context.Context, request ResolveRequest) (ResolvedToken, error) {
	if !validResolveRequest(request) {
		return ResolvedToken{}, errors.New("invalid Provider Token Bridge request")
	}
	meta, err := service.Registry.Connection(ctx, request.UserID, request.AuthConnectionID)
	if err != nil || meta.OwnerUserID != request.UserID {
		return ResolvedToken{}, errors.New("Provider Connection was not found")
	}
	switch meta.ConnectionMethod {
	case "legacy_byo":
		token, err := service.Registry.LegacyAccessToken(ctx, request.UserID, request.AuthConnectionID)
		if err != nil {
			return ResolvedToken{}, err
		}
		return ResolvedToken{AuthConnectionID: request.AuthConnectionID, Provider: meta.Provider, AccessToken: token, TokenType: "Bearer", Status: meta.Status}, nil
	case "managed_oauth":
		return service.resolveManagedAccessToken(ctx, meta, request)
	case "cli_personal_app":
		if meta.Provider != "feishu" || (meta.CredentialLocation != "local" && meta.CredentialLocation != "cli_sidecar") || strings.TrimSpace(meta.ProfileRef) == "" || service.FeishuCLI == nil {
			return ResolvedToken{}, ErrCLIUnavailable
		}
		if err := service.authorizeManagedRequest(ctx, request); err != nil {
			return ResolvedToken{}, err
		}
		now := service.now().UTC()
		handle := "lmc_fcli_" + randomFeishuCLIIdentifier()
		expiresAt := now.Add(5 * time.Minute)
		service.cliHandleMu.Lock()
		service.cliHandles[handle] = feishuCLIHandle{
			OwnerUserID: request.UserID, ConnectionID: meta.AuthConnectionID,
			ProfileRef: meta.ProfileRef, ExpiresAt: expiresAt,
		}
		service.cliHandleMu.Unlock()
		return ResolvedToken{
			AuthConnectionID: meta.AuthConnectionID, Provider: "feishu", AccessToken: handle,
			TokenType: "CLIProfile", SubjectType: "user", ExpiresAt: expiresAt, Status: meta.Status, TokenVersion: 1,
		}, nil
	default:
		return ResolvedToken{}, errors.New("Provider Connection method is unsupported")
	}
}

func (service *Service) ExecuteFeishuCLI(ctx context.Context, handle, operation string, params map[string]string) (FeishuCLIExecutionResult, error) {
	if service == nil || service.FeishuCLI == nil || !strings.HasPrefix(handle, "lmc_fcli_") {
		return FeishuCLIExecutionResult{}, ErrCLIUnavailable
	}
	service.cliHandleMu.Lock()
	reference, found := service.cliHandles[handle]
	if found && !reference.ExpiresAt.After(service.now()) {
		delete(service.cliHandles, handle)
		found = false
	}
	service.cliHandleMu.Unlock()
	if !found {
		return FeishuCLIExecutionResult{}, ErrCLIUnavailable
	}
	meta, err := service.Registry.Connection(ctx, reference.OwnerUserID, reference.ConnectionID)
	if err != nil || meta.OwnerUserID != reference.OwnerUserID || meta.ConnectionMethod != "cli_personal_app" ||
		meta.Provider != "feishu" || meta.ProfileRef != reference.ProfileRef || meta.Status != "ACTIVE" {
		return FeishuCLIExecutionResult{}, ErrCLIProfileOwnerMismatch
	}
	return service.FeishuCLI.Execute(ctx, reference.OwnerUserID, reference.ConnectionID, reference.ProfileRef, operation, params)
}

func (service *Service) ReportAccessTokenFailure(ctx context.Context, report AccessTokenFailureReport) error {
	request := ResolveRequest{
		AuthConnectionID: report.AuthConnectionID, UserID: report.UserID, TenantID: report.TenantID,
		SourceID: report.SourceID, BindingID: report.BindingID, ContextMode: report.ContextMode,
		Consumer: report.Consumer, RequiredCapability: report.RequiredCapability,
	}
	if !validResolveRequest(request) || report.TokenVersion <= 0 || report.ErrorClass != "invalid_token" || strings.TrimSpace(report.RequestID) == "" {
		return errors.New("invalid Provider Token failure report")
	}
	meta, err := service.Registry.Connection(ctx, report.UserID, report.AuthConnectionID)
	if err != nil || meta.OwnerUserID != report.UserID || meta.ConnectionMethod != "managed_oauth" {
		return errors.New("Provider Connection was not found")
	}
	if err := service.authorizeManagedRequest(ctx, request); err != nil {
		return err
	}
	service.cacheMu.Lock()
	delete(service.cached, report.AuthConnectionID)
	service.cacheMu.Unlock()
	cloudToken, err := service.cloudAccessToken(ctx)
	if err != nil {
		return ErrCloudUnavailable
	}
	return service.Cloud.ReportProviderAccessTokenFailure(ctx, cloudToken, service.ClientInstanceID, meta.CloudConnectionID, cloudclient.ProviderAccessTokenFailureRequest{
		TokenVersion: report.TokenVersion, ErrorClass: report.ErrorClass,
		SourceID: report.SourceID, BindingID: report.BindingID, ContextMode: report.ContextMode,
		Consumer: report.Consumer, RequiredCapability: report.RequiredCapability,
		RequestID: report.RequestID,
	})
}

func (service *Service) resolveManagedAccessToken(ctx context.Context, meta ConnectionMeta, request ResolveRequest) (ResolvedToken, error) {
	if err := service.authorizeManagedRequest(ctx, request); err != nil {
		return ResolvedToken{}, err
	}
	now := service.now()
	service.cacheMu.Lock()
	item, found := service.cached[meta.AuthConnectionID]
	service.cacheMu.Unlock()
	if found && item.expiresAt.After(now.Add(15*time.Second)) {
		return item.resolved, nil
	}
	cloudToken, err := service.cloudAccessToken(ctx)
	if err != nil {
		if found && item.expiresAt.After(now) {
			return item.resolved, nil
		}
		return ResolvedToken{}, ErrCloudUnavailable
	}
	contextMode := request.ContextMode
	if contextMode == "" {
		contextMode = ContextModeSourceBinding
	}
	lease, err := service.Cloud.LeaseProviderAccessToken(ctx, cloudToken, service.ClientInstanceID, meta.CloudConnectionID, cloudclient.ProviderAccessTokenLeaseRequest{
		SourceID: request.SourceID, BindingID: request.BindingID, Consumer: request.Consumer,
		ContextMode: contextMode, RequiredCapability: request.RequiredCapability, RequestID: fmt.Sprintf("core-%d", now.UnixNano()),
	})
	if err != nil {
		return ResolvedToken{}, providerLeaseError{class: classifyProviderLeaseError(err), cause: err}
	}
	resolved := ResolvedToken{
		AuthConnectionID: meta.AuthConnectionID, Provider: lease.Provider, AccessToken: lease.AccessToken,
		TokenType: lease.TokenType, SubjectType: lease.SubjectType, ExpiresAt: lease.ExpiresAt, Status: lease.Status, TokenVersion: lease.TokenVersion,
	}
	service.cacheMu.Lock()
	service.cached[meta.AuthConnectionID] = cachedAccessToken{resolved: resolved, expiresAt: lease.ExpiresAt}
	service.cacheMu.Unlock()
	return resolved, nil
}

func (service *Service) authorizeManagedRequest(ctx context.Context, request ResolveRequest) error {
	switch request.ContextMode {
	case ContextModePreBindingBrowse:
		authorizer, ok := service.Authorizer.(preBindingBrowseAuthorizer)
		if !ok || authorizer.AuthorizePreBindingBrowse(
			ctx, request.UserID, request.TenantID, request.AuthConnectionID,
		) != nil {
			return errors.New("Provider Connection was not found")
		}
	case ContextModeSourceBinding:
		if service.Authorizer == nil || service.Authorizer.Authorize(
			ctx, request.UserID, request.TenantID, request.SourceID, request.BindingID, request.AuthConnectionID,
		) != nil {
			return errors.New("Provider Connection was not found")
		}
	}
	return nil
}

type providerLeaseError struct {
	class string
	cause error
}

func (err providerLeaseError) Error() string { return "Provider access token lease failed" }
func (err providerLeaseError) Unwrap() error { return err.cause }
func (err providerLeaseError) Class() string { return err.class }

func classifyProviderLeaseError(err error) string {
	var cloudErr *cloudclient.CloudError
	if errors.As(err, &cloudErr) {
		return fmt.Sprintf("cloud_status_%d_code_%d", cloudErr.HTTPStatus, cloudErr.Code)
	}
	switch err.Error() {
	case "invalid Provider access token lease request":
		return "invalid_request"
	case "LazyMind Cloud returned an invalid Provider access token lease":
		return "invalid_response"
	default:
		return "transport_or_unknown"
	}
}

func (service *Service) cloudAccessToken(ctx context.Context) (string, error) {
	if service == nil || service.Session == nil || service.Cloud == nil {
		return "", ErrCloudUnavailable
	}
	token, err := service.Session.AccessToken(ctx, time.Minute)
	if err != nil {
		status := service.Session.Status(ctx)
		if status.State == cloudsession.StateReauthRequired || status.State == cloudsession.StateSignedOut {
			return "", ErrCloudReauthRequired
		}
		return "", ErrCloudUnavailable
	}
	return token, nil
}

func (service *Service) now() time.Time {
	if service.Now == nil {
		return time.Now()
	}
	return service.Now()
}

func validConsumer(value string) bool {
	return value == "chat" || value == "datasource" || value == "scheduler"
}

func validResolveRequest(request ResolveRequest) bool {
	if strings.TrimSpace(request.AuthConnectionID) == "" || strings.TrimSpace(request.UserID) == "" ||
		!validConsumer(request.Consumer) || strings.TrimSpace(request.RequiredCapability) == "" {
		return false
	}
	if request.Consumer == "chat" {
		return strings.TrimSpace(request.SourceID) != "" && strings.TrimSpace(request.BindingID) != "" &&
			(request.ContextMode == "" || request.ContextMode == ContextModeSourceBinding)
	}
	switch request.ContextMode {
	case ContextModeSourceBinding:
		return strings.TrimSpace(request.SourceID) != "" && strings.TrimSpace(request.BindingID) != ""
	case ContextModePreBindingBrowse:
		return strings.TrimSpace(request.SourceID) == "" && strings.TrimSpace(request.BindingID) == "" &&
			request.Consumer == "datasource" && request.RequiredCapability == "datasource.browse"
	default:
		return false
	}
}

type HTTPRegistry struct {
	BaseURL       string
	InternalToken string
	HTTPClient    *http.Client
}

func (registry HTTPRegistry) Connection(ctx context.Context, userID, connectionID string) (ConnectionMeta, error) {
	var response struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    ConnectionMeta `json:"data"`
	}
	path := fmt.Sprintf("%s/v1/cloud/connections/internal/%s?user_id=%s", strings.TrimRight(registry.BaseURL, "/"), url.PathEscape(connectionID), url.QueryEscape(userID))
	if err := registry.get(ctx, path, &response); err != nil {
		return ConnectionMeta{}, err
	}
	if response.Code != http.StatusOK {
		return ConnectionMeta{}, errors.New("local Connection Registry rejected the request")
	}
	return response.Data, nil
}

func (registry HTTPRegistry) LegacyAccessToken(ctx context.Context, userID, connectionID string) (string, error) {
	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	path := fmt.Sprintf("%s/v1/cloud/connections/%s/token?user_id=%s", strings.TrimRight(registry.BaseURL, "/"), url.PathEscape(connectionID), url.QueryEscape(userID))
	if err := registry.get(ctx, path, &response); err != nil {
		return "", err
	}
	if response.Code != http.StatusOK {
		return "", errors.New("local Connection Registry rejected the request")
	}
	if strings.TrimSpace(response.Data.AccessToken) == "" {
		return "", ErrManagedTokenBridge
	}
	return response.Data.AccessToken, nil
}

func (registry HTTPRegistry) UpsertManaged(ctx context.Context, mirror ManagedMirror) error {
	return common.ApiPost(ctx, strings.TrimRight(registry.BaseURL, "/")+"/v1/cloud/connections/internal/managed:upsert",
		map[string]any{
			"auth_connection_id": mirror.AuthConnectionID, "owner_user_id": mirror.LocalOwnerUserID,
			"cloud_owner_user_id": mirror.CloudOwnerUserID,
			"provider":            mirror.Provider, "display_name": mirror.DisplayName, "provider_tenant_key": mirror.ProviderTenantKey,
			"provider_workspace_id": mirror.ProviderWorkspaceID, "status": mirror.Status,
			"provider_account_meta":       mirror.ProviderAccountMeta,
			"capability_contract_version": mirror.CapabilityContractVersion, "capabilities": mirror.Capabilities,
		}, map[string]string{"X-LazyMind-Internal-Token": registry.InternalToken}, nil, 10*time.Second)
}

func (registry HTTPRegistry) UpsertCLI(ctx context.Context, mirror FeishuCLIConnectionMirror) error {
	return common.ApiPost(ctx, strings.TrimRight(registry.BaseURL, "/")+"/v1/cloud/connections/internal/feishu-cli:upsert",
		map[string]any{
			"auth_connection_id":          mirror.AuthConnectionID,
			"owner_user_id":               mirror.OwnerUserID,
			"display_name":                mirror.DisplayName,
			"provider_account_id":         mirror.ProviderAccountID,
			"provider_tenant_key":         mirror.ProviderTenantKey,
			"provider_workspace_id":       mirror.ProviderWorkspaceID,
			"provider_account_meta":       mirror.ProviderAccountMeta,
			"profile_ref":                 mirror.ProfileReference,
			"granted_scopes":              mirror.GrantedScopes,
			"credential_location":         firstNonEmptyString(mirror.CredentialLocation, "local"),
			"status":                      mirror.Status,
			"capability_contract_version": "feishu-cli/v1",
			"capabilities":                mirror.Capabilities,
		}, map[string]string{"X-LazyMind-Internal-Token": registry.InternalToken}, nil, 10*time.Second)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (registry HTTPRegistry) get(ctx context.Context, endpoint string, output any) error {
	client := registry.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-LazyMind-Internal-Token", registry.InternalToken)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("local Connection Registry returned status %d", response.StatusCode)
	}
	// Auth responses include metadata beyond the fields needed by each caller.
	return json.NewDecoder(response.Body).Decode(output)
}
