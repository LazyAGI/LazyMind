package providerconnection

import (
	"context"
	"slices"
	"testing"
	"time"
)

var fixtureOriginalFeishuScopes = []string{
	"offline_access", "drive:drive", "drive:drive:readonly", "drive:drive.metadata:readonly",
	"wiki:wiki", "wiki:wiki:readonly", "wiki:node:retrieve", "docx:document",
}

type scopeCompatibilityRuntime struct {
	*credentialRuntime
	grants []string
	err    error
}

func (r *scopeCompatibilityRuntime) AuthCheck(_ context.Context, _ string, required []string) (FeishuCLIAuthCheck, error) {
	r.scopeCalls++
	if r.err != nil {
		return FeishuCLIAuthCheck{}, r.err
	}
	checked := FeishuCLIAuthCheck{OK: true, Granted: slices.Clone(r.grants)}
	for _, scope := range required {
		if !slices.Contains(r.grants, scope) {
			checked.OK = false
			checked.Missing = append(checked.Missing, scope)
		}
	}
	if !checked.OK {
		return checked, &FeishuCLICommandError{Code: "AUTH_SCOPE_MISSING"}
	}
	return checked, nil
}

func TestFeishuCLICompatibleScopesForTokenHandoff(t *testing.T) {
	for _, fixture := range []struct {
		name        string
		grants      []string
		read, write bool
	}{
		{"original", fixtureOriginalFeishuScopes, true, true},
		{"legacy-read", fixtureFeishuReadScopes, true, false},
		{"legacy-write", fixtureFeishuReadWriteScopes(), true, true},
		{"missing-document", slices.DeleteFunc(slices.Clone(fixtureOriginalFeishuScopes), func(s string) bool { return s == "docx:document" }), false, false},
		{"missing-wiki", slices.DeleteFunc(slices.Clone(fixtureOriginalFeishuScopes), func(s string) bool { return s == "wiki:wiki" }), false, false},
	} {
		for _, capability := range []string{"chat.read", "chat.search", "chat.write"} {
			t.Run(fixture.name+"/"+capability, func(t *testing.T) {
				profiles, err := NewFeishuCLIProfileStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				profile, err := profiles.Ensure(t.Context(), "fixture-owner", "fixture-connection")
				if err != nil {
					t.Fatal(err)
				}
				if err := profiles.BindIdentity(t.Context(), profile, "fixture-tenant", "ou_fixture", time.Now()); err != nil {
					t.Fatal(err)
				}
				runtime := &scopeCompatibilityRuntime{credentialRuntime: &credentialRuntime{
					fakeFeishuCLIRuntime: newFakeFeishuCLIRuntime(),
					identity:             FeishuCLIUserIdentity{OpenID: "ou_fixture", TenantKey: "fixture-tenant"}, token: "fixture-token",
				}, grants: slices.Clone(fixture.grants)}
				coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, &fakeFeishuCLIConnectionRegistry{}, DefaultFeishuCLIScopes)
				if err != nil {
					t.Fatal(err)
				}
				allowed := fixture.read
				if capability == "chat.write" {
					allowed = fixture.write
				}
				result, err := coordinator.UserAccessToken(t.Context(), profile.LocalUserID, profile.ConnectionID, profile.Reference, capability)
				if allowed {
					if err != nil || result.AccessToken != "fixture-token" || runtime.tokenCalls != 1 {
						t.Fatalf("compatible grant rejected: result=%+v err=%v helper calls=%d", result, err, runtime.tokenCalls)
					}
				} else if err == nil || result.AccessToken != "" || runtime.tokenCalls != 0 {
					t.Fatal("incomplete grant reached the credential helper")
				}
				runtime.grants = nil
				calls := runtime.tokenCalls
				result, err = coordinator.UserAccessToken(t.Context(), profile.LocalUserID, profile.ConnectionID, profile.Reference, capability)
				if err == nil || result.AccessToken != "" || runtime.tokenCalls != calls {
					t.Fatal("revoked grants exported a token")
				}
				runtime.err = ErrCLIUnavailable
				checks := runtime.scopeCalls
				_, err = coordinator.UserAccessToken(t.Context(), profile.LocalUserID, profile.ConnectionID, profile.Reference, capability)
				if err == nil || runtime.scopeCalls != checks+1 || runtime.tokenCalls != calls {
					t.Fatal("CLI failure was retried as a permission fallback")
				}
			})
		}
	}
}

func TestFeishuCLIOriginalScopesRestoreCompletedSession(t *testing.T) {
	profiles, err := NewFeishuCLIProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.Ensure(t.Context(), "user-fixture", "conn_fixture")
	if err != nil {
		t.Fatal(err)
	}
	state := feishuCLISessionState{
		SessionID: "fcli_original", OwnerUserID: profile.LocalUserID, AuthConnectionID: profile.ConnectionID,
		Status: "COMPLETED", ExpiresAt: time.Now().Add(time.Hour), GrantedScopes: slices.Clone(fixtureOriginalFeishuScopes),
	}
	if err := profiles.WriteState(t.Context(), profile, state.SessionID, state); err != nil {
		t.Fatal(err)
	}
	runtime := &permissionFeishuRuntime{fakeFeishuCLIRuntime: newFakeFeishuCLIRuntime(), granted: slices.Clone(fixtureOriginalFeishuScopes)}
	coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runtime, profiles, &fakeFeishuCLIConnectionRegistry{}, DefaultFeishuCLIScopes)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := coordinator.Get(t.Context(), profile.LocalUserID, state.SessionID)
	if err != nil || restored.Status != "COMPLETED" {
		t.Fatalf("restore failed: %+v %v", restored, err)
	}
	assertFeishuReadCapabilities(t, restored.Capabilities)
	if len(runtime.requested) != 0 || runtime.configInitCalls != 0 {
		t.Fatal("restoring a completed session started authorization")
	}
}
