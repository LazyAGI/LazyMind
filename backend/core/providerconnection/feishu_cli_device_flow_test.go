package providerconnection

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"lazymind/core/cloudclient"
)

type fakeFeishuCLIRuntime struct {
	verificationURL chan string
	done            chan FeishuCLIProcessResult
	authAvailable   bool
	configInitCalls int
}

func newFakeFeishuCLIRuntime() *fakeFeishuCLIRuntime {
	return &fakeFeishuCLIRuntime{verificationURL: make(chan string, 1), done: make(chan FeishuCLIProcessResult, 1), authAvailable: true}
}

func (*fakeFeishuCLIRuntime) VerifyVersion(context.Context, string) error { return nil }
func (runtime *fakeFeishuCLIRuntime) StartConfigInit(context.Context, string) (*FeishuCLIProcess, error) {
	runtime.configInitCalls++
	return &FeishuCLIProcess{VerificationURL: runtime.verificationURL, Done: runtime.done, cancel: func() {}}, nil
}
func (*fakeFeishuCLIRuntime) AuthLoginStart(context.Context, string, []string) (FeishuCLIAuthStart, error) {
	return FeishuCLIAuthStart{
		VerificationURL: "https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=fixture",
		DeviceCode:      "fixture-device-code", ExpiresIn: 240,
	}, nil
}
func (runtime *fakeFeishuCLIRuntime) AuthLoginComplete(context.Context, string, string) (FeishuCLIAuthComplete, error) {
	runtime.authAvailable = true
	return FeishuCLIAuthComplete{
		Event: "authorization_complete", UserOpenID: "ou_fixture", UserName: "Fixture User",
		Granted: append([]string(nil), DefaultFeishuCLIReadScopes...),
	}, nil
}
func (runtime *fakeFeishuCLIRuntime) AuthStatus(context.Context, string) (FeishuCLIAuthStatus, error) {
	var status FeishuCLIAuthStatus
	if runtime.authAvailable {
		status.Identity = "user"
		status.Verified = true
		status.Identities.User.Available = true
	}
	status.Identities.User.OpenID = "ou_fixture"
	status.Identities.User.UserName = "Fixture User"
	return status, nil
}
func (*fakeFeishuCLIRuntime) AuthCheck(_ context.Context, _ string, scopes []string) (FeishuCLIAuthCheck, error) {
	return FeishuCLIAuthCheck{OK: true, Granted: append([]string(nil), scopes...)}, nil
}
func (*fakeFeishuCLIRuntime) ResolveUserIdentity(context.Context, string) (FeishuCLIUserIdentity, error) {
	return FeishuCLIUserIdentity{Name: "Fixture User", OpenID: "ou_fixture", TenantKey: "tenant_fixture"}, nil
}
func (*fakeFeishuCLIRuntime) RunAPI(context.Context, string, string, map[string]any) (FeishuCLIAPIResult, error) {
	return FeishuCLIAPIResult{Data: json.RawMessage(`{}`)}, nil
}
func (*fakeFeishuCLIRuntime) DownloadFile(context.Context, FeishuCLIProfile, string) ([]byte, error) {
	return []byte("fixture"), nil
}

type fakeFeishuCLIConnectionRegistry struct {
	mu      sync.Mutex
	mirrors []FeishuCLIConnectionMirror
}

func (registry *fakeFeishuCLIConnectionRegistry) UpsertCLI(_ context.Context, mirror FeishuCLIConnectionMirror) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.mirrors = append(registry.mirrors, mirror)
	return nil
}

func TestFeishuCLIDeviceFlowSeparatesAppCreationAndUserAuthorization(t *testing.T) {
	runtime := newFakeFeishuCLIRuntime()
	profiles, err := NewFeishuCLIProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry := &fakeFeishuCLIConnectionRegistry{}
	coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, registry, DefaultFeishuCLIReadScopes)
	if err != nil {
		t.Fatal(err)
	}
	runtime.verificationURL <- "https://open.feishu.cn/page/cli?user_code=fixture"
	session, err := coordinator.Start(context.Background(), "user-fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != FeishuCLIStatusAppCreationWaitingUser || session.AuthorizationStartURL == "" {
		t.Fatalf("app creation session = %#v", session)
	}
	runtime.done <- FeishuCLIProcessResult{Stdout: []byte(`{"appId":"cli_fixture","appSecret":"****","brand":"feishu"}`)}

	waitForSessionStatus(t, coordinator, "user-fixture", session.SessionID, FeishuCLIStatusAuthWaitingUser)
	completed := waitForSessionStatus(t, coordinator, "user-fixture", session.SessionID, "COMPLETED")
	if completed.AuthConnectionID == "" || completed.AuthorizationStartURL != "" {
		t.Fatalf("completed session = %#v", completed)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if len(registry.mirrors) != 1 || registry.mirrors[0].ProviderTenantKey != "tenant_fixture" || registry.mirrors[0].CredentialLocation != "local" {
		t.Fatalf("CLI mirror = %#v", registry.mirrors)
	}
}

func TestFeishuCLIDeviceFlowReauthorizationReusesExistingAppProfile(t *testing.T) {
	runtime := newFakeFeishuCLIRuntime()
	profiles, err := NewFeishuCLIProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.Ensure(context.Background(), "user-fixture", "conn_fixture")
	if err != nil {
		t.Fatal(err)
	}
	registry := &fakeFeishuCLIConnectionRegistry{}
	coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, registry, DefaultFeishuCLIReadScopes)
	if err != nil {
		t.Fatal(err)
	}

	session, err := coordinator.Start(context.Background(), profile.LocalUserID, profile.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.AuthConnectionID != profile.ConnectionID || session.AuthorizationStartURL == "" {
		t.Fatalf("reauthorization session = %#v", session)
	}
	if runtime.configInitCalls != 0 {
		t.Fatalf("reauthorization created another CLI App: config init calls = %d", runtime.configInitCalls)
	}
	completed := waitForSessionStatus(t, coordinator, profile.LocalUserID, session.SessionID, "COMPLETED")
	if completed.AuthConnectionID != profile.ConnectionID {
		t.Fatalf("reauthorization connection = %q, want %q", completed.AuthConnectionID, profile.ConnectionID)
	}
}

func TestFeishuCLIDeviceFlowFinalizesAnAuthorizedExpiredSessionOnce(t *testing.T) {
	runtime := newFakeFeishuCLIRuntime()
	profiles, err := NewFeishuCLIProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.Ensure(context.Background(), "user-fixture", "conn_fixture")
	if err != nil {
		t.Fatal(err)
	}
	registry := &fakeFeishuCLIConnectionRegistry{}
	coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, registry, DefaultFeishuCLIReadScopes)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	coordinator.now = func() time.Time { return now }
	coordinator.sessions["fcli_expired"] = &FeishuCLISession{
		SessionID: "fcli_expired", Provider: "feishu", Status: FeishuCLIStatusDeviceCodeExpired,
		ExpiresAt: now.Add(-time.Minute), AuthConnectionID: profile.ConnectionID,
		DeviceCode: "authorized-device-code", OwnerUserID: profile.LocalUserID, Profile: profile,
		ErrorCode: FeishuCLIStatusDeviceCodeExpired,
	}

	session, err := coordinator.Get(context.Background(), profile.LocalUserID, "fcli_expired")
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != FeishuCLIStatusAuthWaitingUser {
		t.Fatalf("expired authorized session status = %q", session.Status)
	}
	completed := waitForSessionStatus(t, coordinator, profile.LocalUserID, "fcli_expired", "COMPLETED")
	if completed.AuthConnectionID != profile.ConnectionID {
		t.Fatalf("completed connection = %q", completed.AuthConnectionID)
	}
}

func TestFeishuCLIDeviceFlowRetriesMissingTokenOnceWithoutCreatingAnotherApp(t *testing.T) {
	runtime := newFakeFeishuCLIRuntime()
	runtime.authAvailable = false
	profiles, err := NewFeishuCLIProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.Ensure(context.Background(), "user-fixture", "conn_fixture")
	if err != nil {
		t.Fatal(err)
	}
	registry := &fakeFeishuCLIConnectionRegistry{}
	coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, registry, DefaultFeishuCLIReadScopes)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.sessions["fcli_retry"] = &FeishuCLISession{
		SessionID: "fcli_retry", Provider: "feishu", Status: "AUTH_TOKEN_EXPIRED",
		ExpiresAt: time.Now().Add(-time.Minute), AuthConnectionID: profile.ConnectionID,
		OwnerUserID: profile.LocalUserID, Profile: profile, ErrorCode: "AUTH_TOKEN_EXPIRED",
	}

	session, err := coordinator.Get(context.Background(), profile.LocalUserID, "fcli_retry")
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != FeishuCLIStatusAuthWaitingUser {
		t.Fatalf("retry session status = %q", session.Status)
	}
	completed := waitForSessionStatus(t, coordinator, profile.LocalUserID, "fcli_retry", "COMPLETED")
	if completed.AuthConnectionID != profile.ConnectionID {
		t.Fatalf("completed connection = %q", completed.AuthConnectionID)
	}
	coordinator.mu.Lock()
	retries := coordinator.sessions["fcli_retry"].AuthRetryCount
	coordinator.mu.Unlock()
	if retries != 1 {
		t.Fatalf("auth retries = %d, want 1", retries)
	}
}

func TestFeishuCLIDeviceFlowRecoversAuthenticatedProfileAfterRetryLimit(t *testing.T) {
	runtime := newFakeFeishuCLIRuntime()
	profiles, err := NewFeishuCLIProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.Ensure(context.Background(), "user-fixture", "conn_fixture")
	if err != nil {
		t.Fatal(err)
	}
	registry := &fakeFeishuCLIConnectionRegistry{}
	coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, registry, DefaultFeishuCLIReadScopes)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.sessions["fcli_recover"] = &FeishuCLISession{
		SessionID: "fcli_recover", Provider: "feishu", Status: "AUTH_TOKEN_EXPIRED",
		ExpiresAt: time.Now().Add(-time.Minute), AuthConnectionID: profile.ConnectionID,
		OwnerUserID: profile.LocalUserID, Profile: profile, ErrorCode: "AUTH_TOKEN_EXPIRED",
		AuthRetryCount: 2,
	}

	session, err := coordinator.Get(context.Background(), profile.LocalUserID, "fcli_recover")
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != FeishuCLIStatusAuthWaitingUser {
		t.Fatalf("recovery session status = %q", session.Status)
	}
	completed := waitForSessionStatus(t, coordinator, profile.LocalUserID, "fcli_recover", "COMPLETED")
	if completed.AuthConnectionID != profile.ConnectionID {
		t.Fatalf("completed connection = %q", completed.AuthConnectionID)
	}
}

func TestFeishuCLIDeviceFlowRecoversAuthenticatedProfileAfterRegistryUnavailable(t *testing.T) {
	runtime := newFakeFeishuCLIRuntime()
	profiles, err := NewFeishuCLIProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.Ensure(context.Background(), "user-fixture", "conn_fixture")
	if err != nil {
		t.Fatal(err)
	}
	registry := &fakeFeishuCLIConnectionRegistry{}
	coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, registry, DefaultFeishuCLIReadScopes)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.sessions["fcli_registry_retry"] = &FeishuCLISession{
		SessionID: "fcli_registry_retry", Provider: "feishu", Status: "CLI_UNAVAILABLE",
		ExpiresAt: time.Now().Add(-time.Minute), AuthConnectionID: profile.ConnectionID,
		OwnerUserID: profile.LocalUserID, Profile: profile, ErrorCode: "CLI_UNAVAILABLE",
	}

	session, err := coordinator.Get(context.Background(), profile.LocalUserID, "fcli_registry_retry")
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != FeishuCLIStatusAuthWaitingUser {
		t.Fatalf("registry retry session status = %q", session.Status)
	}
	completed := waitForSessionStatus(t, coordinator, profile.LocalUserID, "fcli_registry_retry", "COMPLETED")
	if completed.AuthConnectionID != profile.ConnectionID {
		t.Fatalf("completed connection = %q", completed.AuthConnectionID)
	}
}

func TestFeishuCLIDeviceFlowRestoresCompletedSessionMetadata(t *testing.T) {
	runtime := newFakeFeishuCLIRuntime()
	profiles, err := NewFeishuCLIProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.Ensure(context.Background(), "user-fixture", "conn_fixture")
	if err != nil {
		t.Fatal(err)
	}
	state := feishuCLISessionState{
		SessionID: "fcli_completed", OwnerUserID: profile.LocalUserID, Status: "COMPLETED",
		ExpiresAt: time.Now().Add(time.Hour), AuthConnectionID: profile.ConnectionID,
	}
	if err := profiles.WriteState(context.Background(), profile, state.SessionID, state); err != nil {
		t.Fatal(err)
	}
	coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, &fakeFeishuCLIConnectionRegistry{}, DefaultFeishuCLIReadScopes)
	if err != nil {
		t.Fatal(err)
	}

	session, err := coordinator.Get(context.Background(), profile.LocalUserID, state.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != "COMPLETED" || session.DisplayName == "" || len(session.Capabilities) != 5 {
		t.Fatalf("restored completed session = %#v", session)
	}
}

func waitForSessionStatus(t *testing.T, coordinator *FeishuCLIDeviceFlowCoordinator, ownerUserID, sessionID, expected string) cloudclient.ProviderConnectionSession {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		session, err := coordinator.Get(context.Background(), ownerUserID, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if session.Status == expected {
			return session
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("session did not reach status %s", expected)
	return cloudclient.ProviderConnectionSession{}
}
