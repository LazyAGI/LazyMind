package providerconnection

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lazymind/core/cloudclient"
)

func TestHTTPRegistryAuthorizationMarkerUsesExistingInternalMirrorContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if r.URL.Path != "/v1/cloud/connections/internal/feishu-cli:upsert" || r.Header.Get("X-LazyMind-Internal-Token") != "fixture-internal" {
			http.Error(w, "fixture forbidden", http.StatusForbidden)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		// The existing Auth service schema accepts empty arrays, but rejects null lists.
		if string(body["granted_scopes"]) != "[]" || string(body["capabilities"]) != "[]" ||
			string(body["profile_ref"]) != `"fixture-owner/fixture-id"` || string(body["provider_account_meta"]) != `{"authorization_pending":true}` {
			http.Error(w, "fixture invalid mirror", http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	registry := HTTPRegistry{BaseURL: server.URL, InternalToken: "fixture-internal"}
	if err := registry.MarkAuthorizationPending(t.Context(), "fixture-owner", "fixture-id"); err != nil {
		t.Fatal(err)
	}
}

type statusMarkerRegistry struct {
	userTokenRegistry
	marked bool
}

func (registry *statusMarkerRegistry) Connection(ctx context.Context, owner, id string) (ConnectionMeta, error) {
	if owner != registry.meta.OwnerUserID {
		return ConnectionMeta{}, ErrCLIProfileOwnerMismatch
	}
	return registry.meta, nil
}
func (registry *statusMarkerRegistry) MarkAuthorizationPending(context.Context, string, string) error {
	registry.marked = true
	return nil
}

type statusMarkerBackend struct {
	FeishuCLIBackend
	registry *statusMarkerRegistry
	calls    int
}

func (backend *statusMarkerBackend) Start(context.Context, string, string) (cloudclient.ProviderConnectionSession, error) {
	backend.calls++
	if !backend.registry.marked {
		return cloudclient.ProviderConnectionSession{}, errors.New("fixture marker missing")
	}
	return cloudclient.ProviderConnectionSession{SessionID: "fcli_fixture"}, nil
}

func TestCLIReauthorizationStoresMarkerWithinExistingCoreOwnerBoundary(t *testing.T) {
	registry := &statusMarkerRegistry{userTokenRegistry: userTokenRegistry{meta: ConnectionMeta{
		AuthConnectionID: "fixture-id", OwnerUserID: "fixture-owner", Provider: "feishu", ConnectionMethod: "cli_personal_app",
	}}}
	backend := &statusMarkerBackend{registry: registry}
	service := &Service{Registry: registry, FeishuCLI: backend}
	result, err := service.ReauthorizeForUser(t.Context(), "fixture-owner", "fixture-id")
	if err != nil || result.SessionID == "" {
		t.Fatalf("reauthorization failed: %+v %v", result, err)
	}
	registry.marked = false
	if _, err := service.ReauthorizeForUser(t.Context(), "other-owner", "fixture-id"); err == nil {
		t.Fatal("another owner could authorize")
	}
	if registry.marked || backend.calls != 1 {
		t.Fatal("another owner changed the account or started authorization")
	}
}

type retryCompletionRegistry struct {
	mu      sync.Mutex
	failed  bool
	mirrors []FeishuCLIConnectionMirror
}

func (registry *retryCompletionRegistry) UpsertCLI(_ context.Context, mirror FeishuCLIConnectionMirror) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if !registry.failed {
		registry.failed = true
		return errors.New("fixture registry unavailable")
	}
	registry.mirrors = append(registry.mirrors, mirror)
	return nil
}

func TestCLIProfileRecoveryCannotCompleteCanceledAuthorization(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-profile", true: "late-success-after-cancel"}[canceled], func(t *testing.T) {
			profiles, err := NewFeishuCLIProfileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			profile, err := profiles.Ensure(t.Context(), "user-fixture", "conn_fixture")
			if err != nil {
				t.Fatal(err)
			}
			registry := &fakeFeishuCLIConnectionRegistry{}
			coordinator, err := NewFeishuCLIDeviceFlowCoordinator(newFakeFeishuCLIRuntime(), profiles, registry, DefaultFeishuCLIScopes)
			if err != nil {
				t.Fatal(err)
			}
			coordinator.sessions["fcli_fixture"] = &FeishuCLISession{
				SessionID: "fcli_fixture", Status: FeishuCLIStatusAuthWaitingUser,
				OwnerUserID: profile.LocalUserID, AuthConnectionID: profile.ConnectionID, Profile: profile,
			}
			if canceled {
				if err := coordinator.Cancel(t.Context(), profile.LocalUserID, "fcli_fixture"); err != nil {
					t.Fatal(err)
				}
				coordinator.finalizeAuthenticatedProfile(t.Context(), "fcli_fixture", profile, profile.LocalUserID, profile.ConnectionID,
					"Fixture", "ou_fixture", DefaultFeishuCLIScopes, true)
				if len(registry.mirrors) != 0 {
					t.Fatal("canceled authorization published a late completion")
				}
			} else {
				if !coordinator.recoverAuthenticatedProfile("fcli_fixture", profile) {
					t.Fatal("old profile recovery failed")
				}
				if len(registry.mirrors) != 1 || registry.mirrors[0].ProviderAccountMeta["authorization_completed"] != false {
					t.Fatal("old profile recovery claimed a new authorization completed")
				}
			}
		})
	}
}

func TestCLICompletionRemainsConfirmedAcrossRegistryRetryAndSessionReload(t *testing.T) {
	runtime := newFakeFeishuCLIRuntime()
	profiles, err := NewFeishuCLIProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.Ensure(t.Context(), "user-fixture", "conn_fixture")
	if err != nil {
		t.Fatal(err)
	}
	registry := &retryCompletionRegistry{}
	coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, registry, DefaultFeishuCLIScopes)
	if err != nil {
		t.Fatal(err)
	}
	session, err := coordinator.Start(t.Context(), profile.LocalUserID, profile.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var stored feishuCLISessionState
	for {
		if err := profiles.ReadState(t.Context(), profile, session.SessionID, &stored); err == nil && stored.Status == "CLI_UNAVAILABLE" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial synchronization did not fail")
		}
		time.Sleep(5 * time.Millisecond)
	}
	restored, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, registry, DefaultFeishuCLIScopes)
	if err != nil {
		t.Fatal(err)
	}
	waitForSessionStatus(t, restored, profile.LocalUserID, session.SessionID, "COMPLETED")
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if len(registry.mirrors) != 1 || registry.mirrors[0].ProviderAccountMeta["authorization_completed"] != true {
		t.Fatal("successful authorization was lost during synchronization recovery")
	}
}

type earlyCompletionFailureRuntime struct {
	*fakeFeishuCLIRuntime
	failStatus       bool
	recoveryOpenID   string
	completionScopes []string
	statusCalls      atomic.Int32
}

func (runtime *earlyCompletionFailureRuntime) AuthLoginComplete(context.Context, string, string) (FeishuCLIAuthComplete, error) {
	return FeishuCLIAuthComplete{UserOpenID: "ou_fixture", UserName: "Fixture", Granted: append([]string(nil), runtime.completionScopes...)}, nil
}

func (runtime *earlyCompletionFailureRuntime) AuthStatus(ctx context.Context, config string) (FeishuCLIAuthStatus, error) {
	if runtime.statusCalls.Add(1) == 1 && runtime.failStatus {
		return FeishuCLIAuthStatus{}, errors.New("fixture temporary status failure")
	}
	status, err := runtime.fakeFeishuCLIRuntime.AuthStatus(ctx, config)
	if runtime.recoveryOpenID != "" {
		status.Identities.User.OpenID = runtime.recoveryOpenID
	}
	return status, err
}

func (runtime *earlyCompletionFailureRuntime) ResolveUserIdentity(ctx context.Context, config string) (FeishuCLIUserIdentity, error) {
	identity, err := runtime.fakeFeishuCLIRuntime.ResolveUserIdentity(ctx, config)
	if runtime.recoveryOpenID != "" {
		identity.OpenID = runtime.recoveryOpenID
	}
	return identity, err
}

func TestCLICompletionSurvivesEarlyValidationFailureAndReload(t *testing.T) {
	for _, failure := range []string{"status-query", "identity-bind", "status-query-other-user", "status-query-missing-grant"} {
		t.Run(failure, func(t *testing.T) {
			profiles, err := NewFeishuCLIProfileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			profile, err := profiles.Ensure(t.Context(), "user-fixture", "conn_fixture")
			if err != nil {
				t.Fatal(err)
			}
			identityPath := filepath.Join(profile.StateDir, "identity.json")
			if failure == "identity-bind" {
				// A directory at the identity target causes a real atomic rename I/O failure.
				if err := os.Mkdir(identityPath, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			runtime := &earlyCompletionFailureRuntime{fakeFeishuCLIRuntime: newFakeFeishuCLIRuntime(), failStatus: failure != "identity-bind", completionScopes: DefaultFeishuCLIScopes}
			if failure == "status-query-other-user" {
				runtime.recoveryOpenID = "ou_other_fixture"
			}
			if failure == "status-query-missing-grant" {
				runtime.completionScopes = DefaultFeishuCLIScopes[:len(DefaultFeishuCLIScopes)-1]
			}
			registry := &fakeFeishuCLIConnectionRegistry{}
			coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, registry, DefaultFeishuCLIScopes)
			if err != nil {
				t.Fatal(err)
			}
			session, err := coordinator.Start(t.Context(), profile.LocalUserID, profile.ConnectionID)
			if err != nil {
				t.Fatal(err)
			}
			expected := "AUTH_TOKEN_EXPIRED"
			if failure == "identity-bind" {
				expected = "CLI_UNAVAILABLE"
			}
			deadline := time.Now().Add(2 * time.Second)
			var stored feishuCLISessionState
			for {
				if err := profiles.ReadState(t.Context(), profile, session.SessionID, &stored); err == nil && stored.Status == expected {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("expected early validation failure was not persisted")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if failure == "identity-bind" {
				if err := os.Remove(identityPath); err != nil {
					t.Fatal(err)
				}
			}
			restored, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, registry, DefaultFeishuCLIScopes)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "status-query-other-user" || failure == "status-query-missing-grant" {
				expected = "AUTH_TOKEN_EXPIRED"
				if failure == "status-query-missing-grant" {
					expected = FeishuCLIStatusAuthWaitingAdmin
				}
				waitForSessionStatus(t, restored, profile.LocalUserID, session.SessionID, expected)
				registry.mu.Lock()
				defer registry.mu.Unlock()
				if len(registry.mirrors) != 0 {
					t.Fatal("recovery published success without matching completed identity and grants")
				}
				return
			}
			waitForSessionStatus(t, restored, profile.LocalUserID, session.SessionID, "COMPLETED")
			registry.mu.Lock()
			defer registry.mu.Unlock()
			if len(registry.mirrors) != 1 || registry.mirrors[0].ProviderAccountMeta["authorization_completed"] != true {
				t.Fatal("confirmed authorization was lost after early validation failure")
			}
		})
	}
}
