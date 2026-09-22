package providerconnection

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"lazymind/core/cloudclient"
)

const (
	FeishuCLIStatusAppCreationWaitingUser = "APP_CREATION_WAITING_USER"
	FeishuCLIStatusAuthWaitingUser        = "AUTH_WAITING_USER"
	FeishuCLIStatusAuthWaitingAdmin       = "AUTH_WAITING_ADMIN"
	FeishuCLIStatusDeviceCodeExpired      = "AUTH_DEVICE_CODE_EXPIRED"
	FeishuCLIStatusCanceled               = "AUTH_CANCELED"
	maxFeishuCLIProfileRecoveryAttempts   = 2
)

var (
	ErrCLIConnectionNotFound = errors.New("Provider Connection was not found")
	ErrCLIConnectionConflict = errors.New("Provider Connection state conflict")
)

var DefaultFeishuCLIReadScopes = []string{
	"offline_access",
	"drive:drive:readonly",
	"wiki:space:retrieve",
	"wiki:node:read",
	"wiki:node:retrieve",
	"docx:document:readonly",
}

var feishuCLIAuthLoginCommand = [...]string{"auth", "login"}

const (
	feishuCLIAuthNoWaitFlag = "--no-wait"
	feishuCLIDeviceCodeFlag = "--device-code"
)

type FeishuCLIConnectionMirror struct {
	AuthConnectionID    string
	OwnerUserID         string
	DisplayName         string
	ProviderAccountID   string
	ProviderTenantKey   string
	ProviderWorkspaceID string
	ProviderAccountMeta map[string]any
	ProfileReference    string
	CredentialLocation  string
	GrantedScopes       []string
	Status              string
	Capabilities        []cloudclient.ProviderConnectionCapability
}

type FeishuCLIConnectionRegistry interface {
	UpsertCLI(context.Context, FeishuCLIConnectionMirror) error
}

type FeishuCLIBackend interface {
	Start(context.Context, string, string) (cloudclient.ProviderConnectionSession, error)
	Get(context.Context, string, string) (cloudclient.ProviderConnectionSession, error)
	Cancel(context.Context, string, string) error
	Execute(context.Context, string, string, string, string, map[string]string) (FeishuCLIExecutionResult, error)
}

type feishuCLIRuntime interface {
	VerifyVersion(context.Context, string) error
	StartConfigInit(context.Context, string) (*FeishuCLIProcess, error)
	AuthLoginStart(context.Context, string, []string) (FeishuCLIAuthStart, error)
	AuthLoginComplete(context.Context, string, string) (FeishuCLIAuthComplete, error)
	AuthStatus(context.Context, string) (FeishuCLIAuthStatus, error)
	AuthCheck(context.Context, string, []string) (FeishuCLIAuthCheck, error)
	ResolveUserIdentity(context.Context, string) (FeishuCLIUserIdentity, error)
	RunAPI(context.Context, string, string, map[string]any) (FeishuCLIAPIResult, error)
	DownloadFile(context.Context, FeishuCLIProfile, string) ([]byte, error)
}

type FeishuCLIExecutionResult struct {
	Data          json.RawMessage `json:"data,omitempty"`
	ContentBase64 string          `json:"content_base64,omitempty"`
}

type FeishuCLISession struct {
	SessionID             string                                     `json:"session_id"`
	Provider              string                                     `json:"provider"`
	Status                string                                     `json:"status"`
	AuthorizationStartURL string                                     `json:"authorization_start_url,omitempty"`
	ExpiresAt             time.Time                                  `json:"expires_at"`
	AuthConnectionID      string                                     `json:"auth_connection_id,omitempty"`
	DisplayName           string                                     `json:"display_name,omitempty"`
	Capabilities          []cloudclient.ProviderConnectionCapability `json:"capabilities,omitempty"`
	ErrorCode             string                                     `json:"error_code,omitempty"`
	DeviceCode            string                                     `json:"-"`
	OwnerUserID           string                                     `json:"-"`
	Profile               FeishuCLIProfile                           `json:"-"`
	GrantedScopes         []string                                   `json:"-"`
	PollStarted           bool                                       `json:"-"`
	AuthRetryCount        int                                        `json:"-"`
	ProfileRecoveryCount  int                                        `json:"-"`
	AdminRetryCount       int                                        `json:"-"`
	RetryAfter            time.Time                                  `json:"-"`
	process               *FeishuCLIProcess
	cancel                context.CancelFunc
}

type feishuCLISessionState struct {
	SessionID             string                                     `json:"session_id"`
	OwnerUserID           string                                     `json:"owner_user_id"`
	Status                string                                     `json:"status"`
	AuthorizationStartURL string                                     `json:"authorization_start_url,omitempty"`
	ExpiresAt             time.Time                                  `json:"expires_at"`
	AuthConnectionID      string                                     `json:"auth_connection_id"`
	DisplayName           string                                     `json:"display_name,omitempty"`
	GrantedScopes         []string                                   `json:"granted_scopes,omitempty"`
	Capabilities          []cloudclient.ProviderConnectionCapability `json:"capabilities,omitempty"`
	DeviceCode            string                                     `json:"device_code,omitempty"`
	ErrorCode             string                                     `json:"error_code,omitempty"`
	AuthRetryCount        int                                        `json:"auth_retry_count,omitempty"`
	ProfileRecoveryCount  int                                        `json:"profile_recovery_count,omitempty"`
	AdminRetryCount       int                                        `json:"admin_retry_count,omitempty"`
	RetryAfter            time.Time                                  `json:"retry_after,omitempty"`
}

type FeishuCLIDeviceFlowCoordinator struct {
	runner             feishuCLIRuntime
	profiles           *FeishuCLIProfileStore
	registry           FeishuCLIConnectionRegistry
	now                func() time.Time
	scopes             []string
	credentialLocation string

	mu       sync.Mutex
	sessions map[string]*FeishuCLISession
}

func NewFeishuCLIDeviceFlowCoordinator(runner feishuCLIRuntime, profiles *FeishuCLIProfileStore, registry FeishuCLIConnectionRegistry, scopes []string) (*FeishuCLIDeviceFlowCoordinator, error) {
	if runner == nil || profiles == nil || registry == nil {
		return nil, ErrCLIUnavailable
	}
	normalized := normalizeFeishuCLIScopes(scopes)
	if len(normalized) == 0 {
		return nil, ErrCLIOutputInvalid
	}
	return &FeishuCLIDeviceFlowCoordinator{
		runner: runner, profiles: profiles, registry: registry, now: time.Now,
		scopes: normalized, credentialLocation: "local", sessions: map[string]*FeishuCLISession{},
	}, nil
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) UseCredentialLocation(location string) error {
	location = strings.TrimSpace(location)
	if location != "local" && location != "cli_sidecar" {
		return ErrCLIOutputInvalid
	}
	coordinator.mu.Lock()
	coordinator.credentialLocation = location
	coordinator.mu.Unlock()
	return nil
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) Start(ctx context.Context, ownerUserID, reauthorizeConnectionID string) (cloudclient.ProviderConnectionSession, error) {
	ownerUserID = strings.TrimSpace(ownerUserID)
	if !feishuCLIProfileIDPattern.MatchString(ownerUserID) {
		return cloudclient.ProviderConnectionSession{}, ErrCLIProfileOwnerMismatch
	}
	connectionID := strings.TrimSpace(reauthorizeConnectionID)
	reauthorizing := connectionID != ""
	if connectionID == "" {
		connectionID = "conn_" + randomFeishuCLIIdentifier()
	}
	if !feishuCLIProfileIDPattern.MatchString(connectionID) {
		return cloudclient.ProviderConnectionSession{}, ErrCLIProfileNotFound
	}
	var profile FeishuCLIProfile
	var err error
	if reauthorizing {
		profile, err = coordinator.profiles.Load(ctx, ownerUserID, connectionID)
	} else {
		profile, err = coordinator.profiles.Ensure(ctx, ownerUserID, connectionID)
	}
	if err != nil {
		return cloudclient.ProviderConnectionSession{}, err
	}
	if err := coordinator.runner.VerifyVersion(ctx, profile.ConfigDir); err != nil {
		return cloudclient.ProviderConnectionSession{}, err
	}
	if reauthorizing {
		return coordinator.startReauthorization(ctx, ownerUserID, connectionID, profile)
	}
	background, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	process, err := coordinator.runner.StartConfigInit(background, profile.ConfigDir)
	if err != nil {
		cancel()
		return cloudclient.ProviderConnectionSession{}, err
	}
	now := coordinator.now().UTC()
	session := &FeishuCLISession{
		SessionID: "fcli_" + randomFeishuCLIIdentifier(), Provider: "feishu",
		Status: FeishuCLIStatusAppCreationWaitingUser, ExpiresAt: now.Add(10 * time.Minute),
		AuthConnectionID: connectionID, OwnerUserID: ownerUserID, Profile: profile,
		process: process, cancel: cancel,
	}
	coordinator.mu.Lock()
	coordinator.sessions[session.SessionID] = session
	coordinator.mu.Unlock()

	continueAsync := true
	select {
	case verificationURL, ok := <-process.VerificationURL:
		if ok && validFeishuVerificationURL(verificationURL) {
			coordinator.updateSession(session.SessionID, func(current *FeishuCLISession) {
				current.AuthorizationStartURL = verificationURL
			})
		}
	case result := <-process.Done:
		continueAsync = false
		go coordinator.handleAppCreationResult(session.SessionID, result)
	case <-time.After(30 * time.Second):
		coordinator.failSession(session.SessionID, "CLI_TIMEOUT")
		process.Cancel()
	case <-ctx.Done():
		coordinator.failSession(session.SessionID, FeishuCLIStatusCanceled)
		process.Cancel()
		return cloudclient.ProviderConnectionSession{}, ctx.Err()
	}
	if err := coordinator.persist(session.SessionID); err != nil {
		process.Cancel()
		return cloudclient.ProviderConnectionSession{}, err
	}
	if continueAsync {
		go coordinator.continueAfterAppCreation(session.SessionID, process)
	}
	return coordinator.Get(context.Background(), ownerUserID, session.SessionID)
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) startReauthorization(
	ctx context.Context,
	ownerUserID string,
	connectionID string,
	profile FeishuCLIProfile,
) (cloudclient.ProviderConnectionSession, error) {
	startContext, cancel := context.WithTimeout(ctx, time.Minute)
	start, err := coordinator.runner.AuthLoginStart(startContext, profile.ConfigDir, coordinator.scopes)
	cancel()
	if err != nil {
		return cloudclient.ProviderConnectionSession{}, err
	}
	now := coordinator.now().UTC()
	session := &FeishuCLISession{
		SessionID: "fcli_" + randomFeishuCLIIdentifier(), Provider: "feishu",
		Status: FeishuCLIStatusAuthWaitingUser, AuthorizationStartURL: start.VerificationURL,
		ExpiresAt:        now.Add(time.Duration(start.ExpiresIn) * time.Second),
		AuthConnectionID: connectionID, OwnerUserID: ownerUserID, Profile: profile,
		DeviceCode: start.DeviceCode,
	}
	coordinator.mu.Lock()
	coordinator.sessions[session.SessionID] = session
	coordinator.mu.Unlock()
	if err := coordinator.persist(session.SessionID); err != nil {
		return cloudclient.ProviderConnectionSession{}, err
	}
	return coordinator.Get(context.Background(), ownerUserID, session.SessionID)
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) Get(ctx context.Context, ownerUserID, sessionID string) (cloudclient.ProviderConnectionSession, error) {
	if err := ctx.Err(); err != nil {
		return cloudclient.ProviderConnectionSession{}, err
	}
	coordinator.mu.Lock()
	session, found := coordinator.sessions[strings.TrimSpace(sessionID)]
	coordinator.mu.Unlock()
	if !found {
		restored, err := coordinator.restoreSession(ctx, strings.TrimSpace(ownerUserID), strings.TrimSpace(sessionID))
		if err != nil {
			return cloudclient.ProviderConnectionSession{}, ErrCLIConnectionNotFound
		}
		session = restored
		found = true
	}
	coordinator.mu.Lock()
	if !found || session.OwnerUserID != strings.TrimSpace(ownerUserID) {
		coordinator.mu.Unlock()
		return cloudclient.ProviderConnectionSession{}, ErrCLIConnectionNotFound
	}
	now := coordinator.now()
	shouldRecoverAuth := false
	if (session.Status == FeishuCLIStatusDeviceCodeExpired || session.Status == "AUTH_TOKEN_EXPIRED" || session.Status == "CLI_UNAVAILABLE") &&
		session.DeviceCode == "" && session.ProfileRecoveryCount < maxFeishuCLIProfileRecoveryAttempts && !session.PollStarted {
		session.Status = FeishuCLIStatusAuthWaitingUser
		session.ErrorCode = ""
		session.AuthorizationStartURL = ""
		session.PollStarted = true
		session.ProfileRecoveryCount++
		session.ExpiresAt = now.UTC().Add(time.Minute)
		shouldRecoverAuth = true
	}
	if session.Status == FeishuCLIStatusDeviceCodeExpired && session.DeviceCode != "" && !session.PollStarted {
		session.Status = FeishuCLIStatusAuthWaitingUser
		session.ErrorCode = ""
		session.ExpiresAt = now.UTC().Add(time.Minute)
	}
	if session.Status != FeishuCLIStatusAuthWaitingAdmin && now.After(session.ExpiresAt) && !terminalFeishuCLISessionStatus(session.Status) {
		session.Status = FeishuCLIStatusDeviceCodeExpired
		session.ErrorCode = FeishuCLIStatusDeviceCodeExpired
		session.AuthorizationStartURL = ""
		if session.cancel != nil {
			session.cancel()
		}
	}
	shouldPoll := session.Status == FeishuCLIStatusAuthWaitingUser && session.DeviceCode != "" && !session.PollStarted
	if shouldPoll {
		session.PollStarted = true
	}
	shouldRetryAdmin := session.Status == FeishuCLIStatusAuthWaitingAdmin && !session.PollStarted && session.AdminRetryCount < 3 && !session.RetryAfter.After(coordinator.now())
	if shouldRetryAdmin {
		session.PollStarted = true
		session.AdminRetryCount++
	}
	public := publicFeishuCLISession(*session)
	coordinator.mu.Unlock()
	if shouldPoll {
		go coordinator.completeAuthorization(sessionID)
	}
	if shouldRetryAdmin {
		go coordinator.resumeAfterAppCreation(sessionID, session.Profile)
	}
	if shouldRecoverAuth {
		go coordinator.recoverOrRestartAuthorization(sessionID, session.Profile)
	} else {
		_ = coordinator.persist(sessionID)
	}
	return public, nil
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) restoreSession(ctx context.Context, ownerUserID, sessionID string) (*FeishuCLISession, error) {
	var state feishuCLISessionState
	profile, err := coordinator.profiles.FindState(ctx, ownerUserID, sessionID, &state)
	if err != nil || state.OwnerUserID != ownerUserID || state.SessionID != sessionID || state.AuthConnectionID != profile.ConnectionID {
		return nil, ErrCLIConnectionNotFound
	}
	session := &FeishuCLISession{
		SessionID: state.SessionID, Provider: "feishu", Status: state.Status,
		AuthorizationStartURL: state.AuthorizationStartURL, ExpiresAt: state.ExpiresAt,
		AuthConnectionID: state.AuthConnectionID, DisplayName: state.DisplayName,
		GrantedScopes: append([]string(nil), state.GrantedScopes...),
		Capabilities:  append([]cloudclient.ProviderConnectionCapability(nil), state.Capabilities...),
		ErrorCode:     state.ErrorCode,
		DeviceCode:    state.DeviceCode, OwnerUserID: state.OwnerUserID, Profile: profile,
		AuthRetryCount: state.AuthRetryCount, ProfileRecoveryCount: state.ProfileRecoveryCount,
		AdminRetryCount: state.AdminRetryCount, RetryAfter: state.RetryAfter,
	}
	if session.Status == "COMPLETED" && (session.DisplayName == "" || len(session.Capabilities) == 0) {
		status, statusErr := coordinator.runner.AuthStatus(ctx, profile.ConfigDir)
		checked, checkErr := coordinator.runner.AuthCheck(ctx, profile.ConfigDir, coordinator.scopes)
		if statusErr == nil && checkErr == nil {
			session.DisplayName = status.Identities.User.UserName
			session.GrantedScopes = append([]string(nil), checked.Granted...)
			session.Capabilities = feishuCLICapabilities()
		}
	}
	coordinator.mu.Lock()
	if existing := coordinator.sessions[sessionID]; existing != nil {
		coordinator.mu.Unlock()
		return existing, nil
	}
	coordinator.sessions[sessionID] = session
	coordinator.mu.Unlock()
	if session.Status == FeishuCLIStatusAppCreationWaitingUser {
		if _, statErr := os.Stat(filepath.Join(profile.ConfigDir, "config.json")); statErr != nil {
			coordinator.failSession(sessionID, "CLI_UNAVAILABLE")
		} else {
			go coordinator.resumeAfterAppCreation(sessionID, profile)
		}
	}
	return session, nil
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) resumeAfterAppCreation(sessionID string, profile FeishuCLIProfile) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	start, err := coordinator.runner.AuthLoginStart(ctx, profile.ConfigDir, coordinator.scopes)
	if err != nil {
		coordinator.failSession(sessionID, classifyFeishuCLIAuthError(err))
		return
	}
	coordinator.updateSession(sessionID, func(current *FeishuCLISession) {
		current.Status = FeishuCLIStatusAuthWaitingUser
		current.AuthorizationStartURL = start.VerificationURL
		current.DeviceCode = start.DeviceCode
		current.ExpiresAt = coordinator.now().UTC().Add(time.Duration(start.ExpiresIn) * time.Second)
		current.PollStarted = false
		current.ProfileRecoveryCount = 0
		current.ErrorCode = ""
		current.RetryAfter = time.Time{}
	})
	_ = coordinator.persist(sessionID)
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) recoverOrRestartAuthorization(sessionID string, profile FeishuCLIProfile) {
	if coordinator.recoverAuthenticatedProfile(sessionID, profile) {
		return
	}
	coordinator.mu.Lock()
	session := coordinator.sessions[sessionID]
	if session == nil || session.Status != FeishuCLIStatusAuthWaitingUser {
		coordinator.mu.Unlock()
		return
	}
	if session.AuthRetryCount >= 1 {
		session.AuthRetryCount = 2
		coordinator.mu.Unlock()
		coordinator.failSession(sessionID, "AUTH_TOKEN_EXPIRED")
		return
	}
	session.AuthRetryCount++
	coordinator.mu.Unlock()
	coordinator.resumeAfterAppCreation(sessionID, profile)
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) recoverAuthenticatedProfile(sessionID string, profile FeishuCLIProfile) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	status, err := coordinator.runner.AuthStatus(ctx, profile.ConfigDir)
	if err != nil || status.Identity != "user" || !status.Verified || !status.Identities.User.Available || strings.TrimSpace(status.Identities.User.OpenID) == "" {
		return false
	}
	checked, err := coordinator.runner.AuthCheck(ctx, profile.ConfigDir, coordinator.scopes)
	if err != nil {
		return false
	}
	coordinator.mu.Lock()
	session := coordinator.sessions[sessionID]
	if session == nil || session.Status != FeishuCLIStatusAuthWaitingUser {
		coordinator.mu.Unlock()
		return false
	}
	ownerUserID := session.OwnerUserID
	connectionID := session.AuthConnectionID
	coordinator.mu.Unlock()
	return coordinator.finalizeAuthenticatedProfile(
		ctx, sessionID, profile, ownerUserID, connectionID,
		status.Identities.User.UserName, status.Identities.User.OpenID, checked.Granted,
	) == ""
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) Cancel(ctx context.Context, ownerUserID, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	coordinator.mu.Lock()
	session, found := coordinator.sessions[strings.TrimSpace(sessionID)]
	if !found || session.OwnerUserID != strings.TrimSpace(ownerUserID) {
		coordinator.mu.Unlock()
		return ErrCLIConnectionNotFound
	}
	if terminalFeishuCLISessionStatus(session.Status) {
		coordinator.mu.Unlock()
		return ErrCLIConnectionConflict
	}
	session.Status = FeishuCLIStatusCanceled
	session.ErrorCode = FeishuCLIStatusCanceled
	session.AuthorizationStartURL = ""
	if session.cancel != nil {
		session.cancel()
	}
	coordinator.mu.Unlock()
	return coordinator.persist(sessionID)
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) Execute(ctx context.Context, ownerUserID, connectionID, profileReference, operation string, params map[string]string) (FeishuCLIExecutionResult, error) {
	profile, err := coordinator.profiles.Load(ctx, strings.TrimSpace(ownerUserID), strings.TrimSpace(connectionID))
	if err != nil {
		return FeishuCLIExecutionResult{}, err
	}
	if strings.TrimSpace(profileReference) != profile.Reference {
		return FeishuCLIExecutionResult{}, ErrCLIProfileOwnerMismatch
	}
	identity, err := coordinator.profiles.Identity(ctx, profile)
	if err != nil || identity.LocalUserID != ownerUserID || identity.ConnectionID != connectionID {
		return FeishuCLIExecutionResult{}, ErrCLIProfileOwnerMismatch
	}
	api := func(path string, query map[string]any) (FeishuCLIExecutionResult, error) {
		result, runErr := coordinator.runner.RunAPI(ctx, profile.ConfigDir, path, query)
		if runErr != nil {
			return FeishuCLIExecutionResult{}, runErr
		}
		return FeishuCLIExecutionResult{Data: result.Data}, nil
	}
	pageSize := boundedCLIInteger(params["page_size"], 1, 200)
	switch operation {
	case "drive_list":
		query := map[string]any{"page_size": pageSize}
		if folderToken := strings.TrimSpace(params["folder_token"]); folderToken != "" && folderToken != "root" {
			if !validFeishuCLIObjectToken(folderToken) {
				return FeishuCLIExecutionResult{}, ErrCLIOutputInvalid
			}
			query["folder_token"] = folderToken
		}
		if cursor := strings.TrimSpace(params["cursor"]); cursor != "" {
			if !validFeishuCLICursor(cursor) {
				return FeishuCLIExecutionResult{}, ErrCLIOutputInvalid
			}
			query["page_token"] = cursor
		}
		return api("/open-apis/drive/v1/files", query)
	case "wiki_spaces":
		query := map[string]any{"page_size": minCLIInteger(pageSize, 50)}
		if cursor := strings.TrimSpace(params["cursor"]); cursor != "" {
			if !validFeishuCLICursor(cursor) {
				return FeishuCLIExecutionResult{}, ErrCLIOutputInvalid
			}
			query["page_token"] = cursor
		}
		return api("/open-apis/wiki/v2/spaces", query)
	case "wiki_node":
		nodeToken := strings.TrimSpace(params["node_token"])
		if !validFeishuCLIObjectToken(nodeToken) {
			return FeishuCLIExecutionResult{}, ErrCLIOutputInvalid
		}
		return api("/open-apis/wiki/v2/spaces/get_node", map[string]any{"token": nodeToken, "obj_type": "wiki"})
	case "wiki_children":
		spaceID := strings.TrimSpace(params["space_id"])
		if !validFeishuCLIObjectToken(spaceID) {
			return FeishuCLIExecutionResult{}, ErrCLIOutputInvalid
		}
		query := map[string]any{"page_size": minCLIInteger(pageSize, 50)}
		if nodeToken := strings.TrimSpace(params["node_token"]); nodeToken != "" {
			if !validFeishuCLIObjectToken(nodeToken) {
				return FeishuCLIExecutionResult{}, ErrCLIOutputInvalid
			}
			query["parent_node_token"] = nodeToken
		}
		if cursor := strings.TrimSpace(params["cursor"]); cursor != "" {
			if !validFeishuCLICursor(cursor) {
				return FeishuCLIExecutionResult{}, ErrCLIOutputInvalid
			}
			query["page_token"] = cursor
		}
		return api("/open-apis/wiki/v2/spaces/"+spaceID+"/nodes", query)
	case "docx_raw":
		documentToken := strings.TrimSpace(params["document_token"])
		if !validFeishuCLIObjectToken(documentToken) {
			return FeishuCLIExecutionResult{}, ErrCLIOutputInvalid
		}
		return api("/open-apis/docx/v1/documents/"+documentToken+"/raw_content", nil)
	case "doc_raw":
		documentToken := strings.TrimSpace(params["document_token"])
		if !validFeishuCLIObjectToken(documentToken) {
			return FeishuCLIExecutionResult{}, ErrCLIOutputInvalid
		}
		return api("/open-apis/doc/v2/"+documentToken+"/raw_content", nil)
	case "file_download":
		fileToken := strings.TrimSpace(params["file_token"])
		content, runErr := coordinator.runner.DownloadFile(ctx, profile, fileToken)
		if runErr != nil {
			return FeishuCLIExecutionResult{}, runErr
		}
		return FeishuCLIExecutionResult{ContentBase64: EncodeFeishuCLIDownload(content)}, nil
	default:
		return FeishuCLIExecutionResult{}, ErrCLIOutputInvalid
	}
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) continueAfterAppCreation(sessionID string, process *FeishuCLIProcess) {
	result, ok := <-process.Done
	if !ok {
		coordinator.failSession(sessionID, "CLI_UNAVAILABLE")
		return
	}
	coordinator.handleAppCreationResult(sessionID, result)
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) handleAppCreationResult(sessionID string, result FeishuCLIProcessResult) {
	if result.Err != nil {
		coordinator.failSession(sessionID, safeFeishuCLIErrorCode(result.Err))
		return
	}
	var completed struct {
		AppID     string `json:"appId"`
		AppSecret string `json:"appSecret"`
		Brand     string `json:"brand"`
	}
	if err := decodeFeishuCLIEnvelope(result.Stdout, &completed); err != nil || (completed.Brand != "feishu" && completed.Brand != "lark") || !strings.HasPrefix(completed.AppID, "cli_") || completed.AppSecret != "****" {
		coordinator.failSession(sessionID, "CLI_OUTPUT_INVALID")
		return
	}
	coordinator.mu.Lock()
	session, found := coordinator.sessions[sessionID]
	if !found || terminalFeishuCLISessionStatus(session.Status) {
		coordinator.mu.Unlock()
		return
	}
	profile := session.Profile
	coordinator.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	start, err := coordinator.runner.AuthLoginStart(ctx, profile.ConfigDir, coordinator.scopes)
	cancel()
	if err != nil {
		coordinator.failSession(sessionID, classifyFeishuCLIAuthError(err))
		return
	}
	coordinator.updateSession(sessionID, func(current *FeishuCLISession) {
		current.Status = FeishuCLIStatusAuthWaitingUser
		current.AuthorizationStartURL = start.VerificationURL
		current.DeviceCode = start.DeviceCode
		current.ExpiresAt = coordinator.now().UTC().Add(time.Duration(start.ExpiresIn) * time.Second)
		current.PollStarted = false
		current.ErrorCode = ""
		current.RetryAfter = time.Time{}
	})
	_ = coordinator.persist(sessionID)
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) completeAuthorization(sessionID string) {
	coordinator.mu.Lock()
	session, found := coordinator.sessions[sessionID]
	if !found || session.Status != FeishuCLIStatusAuthWaitingUser || session.DeviceCode == "" {
		coordinator.mu.Unlock()
		return
	}
	deviceCode := session.DeviceCode
	profile := session.Profile
	ownerUserID := session.OwnerUserID
	connectionID := session.AuthConnectionID
	expiresAt := session.ExpiresAt
	coordinator.mu.Unlock()

	ctx, cancel := context.WithDeadline(context.Background(), expiresAt)
	defer cancel()
	completed, err := coordinator.runner.AuthLoginComplete(ctx, profile.ConfigDir, deviceCode)
	deviceCode = ""
	if err != nil {
		coordinator.failSession(sessionID, classifyFeishuCLIAuthError(err))
		return
	}
	status, err := coordinator.runner.AuthStatus(ctx, profile.ConfigDir)
	if err != nil || status.Identity != "user" || !status.Verified || !status.Identities.User.Available || status.Identities.User.OpenID != completed.UserOpenID {
		coordinator.failSession(sessionID, "AUTH_TOKEN_EXPIRED")
		return
	}
	if code := coordinator.finalizeAuthenticatedProfile(
		ctx, sessionID, profile, ownerUserID, connectionID,
		completed.UserName, completed.UserOpenID, completed.Granted,
	); code != "" {
		coordinator.failSession(sessionID, code)
	}
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) finalizeAuthenticatedProfile(
	ctx context.Context,
	sessionID string,
	profile FeishuCLIProfile,
	ownerUserID string,
	connectionID string,
	displayName string,
	expectedOpenID string,
	grantedScopes []string,
) string {
	identity, err := coordinator.runner.ResolveUserIdentity(ctx, profile.ConfigDir)
	if err != nil || identity.OpenID != expectedOpenID {
		return "PROFILE_TENANT_MISMATCH"
	}
	if err := coordinator.profiles.BindIdentity(ctx, profile, identity.TenantKey, identity.OpenID, coordinator.now()); err != nil {
		return safeFeishuCLIErrorCode(err)
	}
	capabilities := feishuCLICapabilities()
	if err := coordinator.registry.UpsertCLI(ctx, FeishuCLIConnectionMirror{
		AuthConnectionID: connectionID, OwnerUserID: ownerUserID, DisplayName: displayName,
		ProviderAccountID: identity.OpenID, ProviderTenantKey: identity.TenantKey, ProviderWorkspaceID: identity.TenantKey,
		ProviderAccountMeta: map[string]any{"open_id": identity.OpenID, "union_id": identity.UnionID, "user_id": identity.UserID},
		ProfileReference:    profile.Reference, CredentialLocation: coordinator.credentialLocation,
		GrantedScopes: append([]string(nil), grantedScopes...), Status: "ACTIVE", Capabilities: capabilities,
	}); err != nil {
		return "CLI_UNAVAILABLE"
	}
	coordinator.mu.Lock()
	current := coordinator.sessions[sessionID]
	if current == nil || terminalFeishuCLISessionStatus(current.Status) {
		coordinator.mu.Unlock()
		return "CLI_UNAVAILABLE"
	}
	current.Status = "COMPLETED"
	current.AuthorizationStartURL = ""
	current.DeviceCode = ""
	current.DisplayName = displayName
	current.GrantedScopes = append([]string(nil), grantedScopes...)
	current.Capabilities = capabilities
	current.ErrorCode = ""
	coordinator.mu.Unlock()
	if err := coordinator.persist(sessionID); err != nil {
		return "CLI_UNAVAILABLE"
	}
	return ""
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) persist(sessionID string) error {
	coordinator.mu.Lock()
	session, found := coordinator.sessions[sessionID]
	if !found {
		coordinator.mu.Unlock()
		return ErrCLIConnectionNotFound
	}
	state := feishuCLISessionState{
		SessionID: session.SessionID, OwnerUserID: session.OwnerUserID, Status: session.Status,
		AuthorizationStartURL: session.AuthorizationStartURL, ExpiresAt: session.ExpiresAt,
		AuthConnectionID: session.AuthConnectionID, DisplayName: session.DisplayName,
		GrantedScopes: append([]string(nil), session.GrantedScopes...),
		Capabilities:  append([]cloudclient.ProviderConnectionCapability(nil), session.Capabilities...),
		DeviceCode:    session.DeviceCode, ErrorCode: session.ErrorCode,
		AuthRetryCount: session.AuthRetryCount, ProfileRecoveryCount: session.ProfileRecoveryCount,
		AdminRetryCount: session.AdminRetryCount, RetryAfter: session.RetryAfter,
	}
	profile := session.Profile
	coordinator.mu.Unlock()
	return coordinator.profiles.WriteState(context.Background(), profile, sessionID, state)
}

func feishuCLICapabilities() []cloudclient.ProviderConnectionCapability {
	return []cloudclient.ProviderConnectionCapability{
		{Capability: "datasource.browse", Status: "AVAILABLE", ContractVersion: "feishu-cli/v1"},
		{Capability: "datasource.read", Status: "AVAILABLE", ContractVersion: "feishu-cli/v1"},
		{Capability: "datasource.download", Status: "AVAILABLE", ContractVersion: "feishu-cli/v1"},
		{Capability: "datasource.export", Status: "AVAILABLE", ContractVersion: "feishu-cli/v1"},
		{Capability: "datasource.parse", Status: "AVAILABLE", ContractVersion: "feishu-cli/v1"},
	}
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) updateSession(sessionID string, update func(*FeishuCLISession)) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if session := coordinator.sessions[sessionID]; session != nil && !terminalFeishuCLISessionStatus(session.Status) {
		update(session)
	}
}

func (coordinator *FeishuCLIDeviceFlowCoordinator) failSession(sessionID, code string) {
	coordinator.updateSession(sessionID, func(session *FeishuCLISession) {
		session.Status = code
		session.ErrorCode = code
		session.AuthorizationStartURL = ""
		session.DeviceCode = ""
		session.PollStarted = false
		if code == FeishuCLIStatusAuthWaitingAdmin {
			session.ExpiresAt = coordinator.now().UTC().Add(24 * time.Hour)
			session.RetryAfter = coordinator.now().UTC().Add(30 * time.Second)
		}
	})
	_ = coordinator.persist(sessionID)
}

func publicFeishuCLISession(session FeishuCLISession) cloudclient.ProviderConnectionSession {
	return cloudclient.ProviderConnectionSession{
		SessionID: session.SessionID, Provider: "feishu", Status: session.Status,
		AuthorizationStartURL: session.AuthorizationStartURL, ExpiresAt: session.ExpiresAt,
		AuthConnectionID: session.AuthConnectionID, DisplayName: session.DisplayName,
		Capabilities: append([]cloudclient.ProviderConnectionCapability(nil), session.Capabilities...),
		ErrorCode:    session.ErrorCode,
	}
}

func terminalFeishuCLISessionStatus(status string) bool {
	switch status {
	case "COMPLETED", FeishuCLIStatusCanceled, FeishuCLIStatusDeviceCodeExpired,
		"APP_CREATION_DENIED", "APP_CREATION_FORBIDDEN", "AUTH_SCOPE_MISSING",
		"AUTH_TOKEN_EXPIRED", "AUTH_REFRESH_FAILED", "PROFILE_NOT_FOUND",
		"PROFILE_OWNER_MISMATCH", "PROFILE_TENANT_MISMATCH", "CLI_OUTPUT_INVALID",
		"CLI_TIMEOUT", "CLI_UNAVAILABLE", "CLI_NOT_INSTALLED", "CLI_VERSION_UNSUPPORTED",
		"CLI_INTEGRITY_MISMATCH":
		return true
	default:
		return false
	}
}

func classifyFeishuCLIAuthError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrCLITimeout) {
		return FeishuCLIStatusDeviceCodeExpired
	}
	return safeFeishuCLIErrorCode(err)
}

func normalizeFeishuCLIScopes(scopes []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if !validFeishuCLIScope(scope) {
			return nil
		}
		if _, exists := seen[scope]; exists {
			continue
		}
		seen[scope] = struct{}{}
		normalized = append(normalized, scope)
	}
	sort.Strings(normalized)
	return normalized
}

func randomFeishuCLIIdentifier() string {
	payload := make([]byte, 16)
	if _, err := rand.Read(payload); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(payload)
}

func boundedCLIInteger(raw string, minimum, maximum int) int {
	value := 0
	for _, character := range strings.TrimSpace(raw) {
		if character < '0' || character > '9' {
			return minimum
		}
		value = value*10 + int(character-'0')
		if value > maximum {
			return maximum
		}
	}
	if value < minimum {
		return minimum
	}
	return value
}

func minCLIInteger(value, maximum int) int {
	if value > maximum {
		return maximum
	}
	return value
}
