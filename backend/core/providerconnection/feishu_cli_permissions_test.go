package providerconnection

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"lazymind/core/cloudclient"
)

// Independent fixtures: a change to the production defaults must not silently
// grant extra permissions in the fake provider response.
var fixtureFeishuReadScopes = []string{
	"offline_access", "drive:drive:readonly", "drive:drive.metadata:readonly",
	"wiki:space:retrieve", "wiki:node:read", "wiki:node:retrieve", "docx:document:readonly",
}

func fixtureFeishuReadWriteScopes() []string {
	return append(slices.Clone(fixtureFeishuReadScopes), "drive:drive", "wiki:wiki", "docx:document")
}

type permissionFeishuRuntime struct {
	*fakeFeishuCLIRuntime
	mu        sync.Mutex
	requested []string
	granted   []string
	readCalls int
}

func (runtime *permissionFeishuRuntime) RunAPI(_ context.Context, _ string, path string, _ map[string]any) (FeishuCLIAPIResult, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if path != "/open-apis/docx/v1/documents/doc_fixture/raw_content" {
		return FeishuCLIAPIResult{}, ErrCLIOutputInvalid
	}
	runtime.readCalls++
	return FeishuCLIAPIResult{Data: json.RawMessage(`{"content":"fixture legacy content"}`)}, nil
}

func assertFeishuReadCapabilities(t *testing.T, capabilities []cloudclient.ProviderConnectionCapability) {
	t.Helper()
	readAvailable := false
	for _, capability := range capabilities {
		if capability.Status != "AVAILABLE" {
			continue
		}
		if !slices.Contains([]string{"datasource.browse", "datasource.read", "datasource.download", "datasource.export", "datasource.parse"}, capability.Capability) {
			t.Errorf("read-only connection advertises unexpected available capability %q", capability.Capability)
		}
		readAvailable = readAvailable || capability.Capability == "datasource.read"
	}
	if !readAvailable {
		t.Error("read-only connection lost its datasource.read capability")
	}
}

func assertFeishuLegacyRead(t *testing.T, coordinator *FeishuCLIDeviceFlowCoordinator, runtime *permissionFeishuRuntime, profile FeishuCLIProfile) {
	t.Helper()
	result, err := coordinator.Execute(context.Background(), profile.LocalUserID, profile.ConnectionID, profile.Reference,
		"docx_raw", map[string]string{"document_token": "doc_fixture"})
	if err != nil || string(result.Data) != `{"content":"fixture legacy content"}` {
		t.Fatalf("old read path failed: data=%s, err=%v", result.Data, err)
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.readCalls != 1 {
		t.Fatalf("old read did not reach the CLI read operation: %d calls", runtime.readCalls)
	}
}

func (runtime *permissionFeishuRuntime) AuthLoginStart(ctx context.Context, dir string, scopes []string) (FeishuCLIAuthStart, error) {
	runtime.mu.Lock()
	runtime.requested = slices.Clone(scopes)
	runtime.mu.Unlock()
	return runtime.fakeFeishuCLIRuntime.AuthLoginStart(ctx, dir, scopes)
}

func (runtime *permissionFeishuRuntime) AuthLoginComplete(context.Context, string, string) (FeishuCLIAuthComplete, error) {
	return FeishuCLIAuthComplete{
		Event: "authorization_complete", UserOpenID: "ou_fixture", UserName: "Fixture User",
		Granted: slices.Clone(runtime.granted),
	}, nil
}

func (runtime *permissionFeishuRuntime) AuthCheck(_ context.Context, _ string, scopes []string) (FeishuCLIAuthCheck, error) {
	result := FeishuCLIAuthCheck{OK: true, Granted: slices.Clone(runtime.granted)}
	for _, scope := range scopes {
		if !slices.Contains(runtime.granted, scope) {
			result.OK = false
			result.Missing = append(result.Missing, scope)
		}
	}
	if !result.OK {
		return result, &FeishuCLICommandError{Code: "AUTH_WAITING_ADMIN"}
	}
	return result, nil
}

func waitForFeishuPermissionResult(t *testing.T, coordinator *FeishuCLIDeviceFlowCoordinator, sessionID string) cloudclient.ProviderConnectionSession {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		result, err := coordinator.Get(context.Background(), "user-fixture", sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status == "COMPLETED" || result.Status == FeishuCLIStatusAuthWaitingAdmin {
			return result
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("authorization produced neither success nor missing-permission result")
	return cloudclient.ProviderConnectionSession{}
}

func TestFeishuCLIAuthorizationRequestsReadWriteScopes(t *testing.T) {
	for _, reauthorize := range []bool{false, true} {
		name := "new"
		if reauthorize {
			name = "reauthorize"
		}
		t.Run(name, func(t *testing.T) {
			runtime := &permissionFeishuRuntime{fakeFeishuCLIRuntime: newFakeFeishuCLIRuntime(), granted: slices.Clone(fixtureOriginalFeishuScopes)}
			profiles, err := NewFeishuCLIProfileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			registry := &fakeFeishuCLIConnectionRegistry{}
			coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, registry, DefaultFeishuCLIScopes)
			if err != nil {
				t.Fatal(err)
			}
			connectionID := ""
			if reauthorize {
				profile, err := profiles.Ensure(context.Background(), "user-fixture", "conn_fixture")
				if err != nil {
					t.Fatal(err)
				}
				connectionID = profile.ConnectionID
			} else {
				runtime.verificationURL <- "https://open.feishu.cn/page/cli?user_code=fixture"
			}
			session, err := coordinator.Start(context.Background(), "user-fixture", connectionID)
			if err != nil {
				t.Fatal(err)
			}
			if !reauthorize {
				runtime.done <- FeishuCLIProcessResult{Stdout: []byte(`{"appId":"cli_fixture","appSecret":"****","brand":"feishu"}`)}
			}
			completed := waitForFeishuPermissionResult(t, coordinator, session.SessionID)
			if completed.Status != "COMPLETED" {
				t.Fatalf("full grant reported %s", completed.Status)
			}
			runtime.mu.Lock()
			requested := slices.Clone(runtime.requested)
			runtime.mu.Unlock()
			for _, scope := range []string{"offline_access", "drive:drive", "wiki:wiki", "docx:document"} {
				if !slices.Contains(requested, scope) {
					t.Errorf("authorization did not request %q; requested = %v", scope, requested)
				}
			}
			if reauthorize && (completed.AuthConnectionID != connectionID || runtime.configInitCalls != 0) {
				t.Fatal("reauthorization replaced the original connection or app")
			}
			registry.mu.Lock()
			defer registry.mu.Unlock()
			if len(registry.mirrors) != 1 || !slices.Equal(registry.mirrors[0].GrantedScopes, runtime.granted) {
				t.Fatalf("actual granted scopes were not preserved: %+v", registry.mirrors)
			}
		})
	}
}

func TestFeishuCLIIncompleteWriteGrantKeepsPreviousReadConnection(t *testing.T) {
	for _, missing := range []string{"drive:drive", "wiki:wiki", "docx:document"} {
		t.Run(missing, func(t *testing.T) {
			granted := slices.DeleteFunc(fixtureFeishuReadWriteScopes(), func(scope string) bool { return scope == missing })
			runtime := &permissionFeishuRuntime{fakeFeishuCLIRuntime: newFakeFeishuCLIRuntime(), granted: granted}
			profiles, err := NewFeishuCLIProfileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			profile, err := profiles.Ensure(context.Background(), "user-fixture", "conn_fixture")
			if err != nil {
				t.Fatal(err)
			}
			if err := profiles.BindIdentity(context.Background(), profile, "tenant_fixture", "ou_fixture", time.Now()); err != nil {
				t.Fatal(err)
			}
			registry := &fakeFeishuCLIConnectionRegistry{mirrors: []FeishuCLIConnectionMirror{{
				AuthConnectionID: profile.ConnectionID, OwnerUserID: profile.LocalUserID,
				Status: "ACTIVE", GrantedScopes: slices.Clone(fixtureFeishuReadScopes),
				Capabilities: []cloudclient.ProviderConnectionCapability{{Capability: "datasource.read", Status: "AVAILABLE", ContractVersion: "feishu-cli/v1"}},
			}}}
			coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, registry, fixtureFeishuReadWriteScopes())
			if err != nil {
				t.Fatal(err)
			}
			session, err := coordinator.Start(context.Background(), profile.LocalUserID, profile.ConnectionID)
			if err != nil {
				t.Fatal(err)
			}
			result := waitForFeishuPermissionResult(t, coordinator, session.SessionID)
			if result.Status != FeishuCLIStatusAuthWaitingAdmin {
				t.Errorf("missing %s reported %s instead of requiring authorization", missing, result.Status)
			}
			for _, capability := range result.Capabilities {
				if capability.Status == "AVAILABLE" && !slices.Contains([]string{"datasource.browse", "datasource.read", "datasource.download", "datasource.export", "datasource.parse"}, capability.Capability) {
					t.Errorf("incomplete authorization exposes %q as available", capability.Capability)
				}
			}
			assertFeishuLegacyRead(t, coordinator, runtime, profile)
			registry.mu.Lock()
			defer registry.mu.Unlock()
			if len(registry.mirrors) != 1 || registry.mirrors[0].Status != "ACTIVE" || !slices.Equal(registry.mirrors[0].GrantedScopes, fixtureFeishuReadScopes) {
				t.Fatal("incomplete authorization overwrote the existing read connection")
			}
			assertFeishuReadCapabilities(t, registry.mirrors[0].Capabilities)
		})
	}
}

func TestFeishuCLIReadOnlyProfileRestoresWithReadWriteDefaults(t *testing.T) {
	profiles, err := NewFeishuCLIProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.Ensure(context.Background(), "user-fixture", "conn_fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := profiles.BindIdentity(context.Background(), profile, "tenant_fixture", "ou_fixture", time.Now()); err != nil {
		t.Fatal(err)
	}
	state := feishuCLISessionState{
		SessionID: "fcli_legacy_read", OwnerUserID: profile.LocalUserID, AuthConnectionID: profile.ConnectionID,
		Status: "COMPLETED", ExpiresAt: time.Now().Add(time.Hour), GrantedScopes: slices.Clone(fixtureFeishuReadScopes),
	}
	if err := profiles.WriteState(context.Background(), profile, state.SessionID, state); err != nil {
		t.Fatal(err)
	}
	runtime := &permissionFeishuRuntime{fakeFeishuCLIRuntime: newFakeFeishuCLIRuntime(), granted: slices.Clone(fixtureFeishuReadScopes)}
	registry := &fakeFeishuCLIConnectionRegistry{}
	coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, registry, fixtureFeishuReadWriteScopes())
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Get(context.Background(), profile.LocalUserID, state.SessionID)
	if err != nil || result.Status != "COMPLETED" {
		t.Fatalf("old connection could not restore: result=%+v err=%v", result, err)
	}
	assertFeishuReadCapabilities(t, result.Capabilities)
	assertFeishuLegacyRead(t, coordinator, runtime, profile)
	if len(runtime.requested) != 0 || runtime.configInitCalls != 0 || len(registry.mirrors) != 0 {
		t.Fatal("reading an existing connection initiated reauthorization or changed registry state")
	}
}

func TestFeishuCLIReauthorizationRejectsAnotherOwner(t *testing.T) {
	profiles, err := NewFeishuCLIProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.Ensure(context.Background(), "user-owner", "conn_fixture"); err != nil {
		t.Fatal(err)
	}
	runtime := &permissionFeishuRuntime{fakeFeishuCLIRuntime: newFakeFeishuCLIRuntime(), granted: fixtureFeishuReadWriteScopes()}
	registry := &fakeFeishuCLIConnectionRegistry{}
	coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, registry, fixtureFeishuReadWriteScopes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Start(context.Background(), "user-other", "conn_fixture"); err == nil {
		t.Fatal("another owner could reauthorize the connection")
	}
	if len(runtime.requested) != 0 || runtime.configInitCalls != 0 || len(registry.mirrors) != 0 {
		t.Fatal("unauthorized request reached CLI authorization or changed the registry")
	}
}
