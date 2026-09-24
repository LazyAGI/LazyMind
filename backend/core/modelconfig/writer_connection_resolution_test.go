package modelconfig

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lazymind/core/cloudclient"
	"lazymind/core/cloudsession"
	"lazymind/core/providerconnection"
)

type writerResolutionRegistry struct {
	metas              map[string]providerconnection.ConnectionMeta
	calls, legacyCalls int
}

func (r *writerResolutionRegistry) Connection(_ context.Context, owner, id string) (providerconnection.ConnectionMeta, error) {
	r.calls++
	return r.metas[id], nil
}
func (r *writerResolutionRegistry) LegacyAccessToken(_ context.Context, owner, id string) (string, error) {
	r.legacyCalls++
	return "fixture-legacy-" + id, nil
}
func (*writerResolutionRegistry) UpsertManaged(context.Context, providerconnection.ManagedMirror) error {
	return nil
}

type writerResolutionStore struct{}

func (writerResolutionStore) Load(context.Context) (string, error) { return "fixture-refresh", nil }
func (writerResolutionStore) Save(context.Context, string) error   { return nil }
func (writerResolutionStore) Delete(context.Context) error         { return nil }

// Exercise the real Core bridge and HTTP lease client. The Cloud fixture models
// an explicitly granted chat.write capability; it does not claim the current
// Notion Cloud capability verifier already grants that capability.
func TestWriterUsesUnifiedConnectionResolution(t *testing.T) {
	for _, tc := range []struct {
		name, provider, method, failure string
		bridge, multiple                bool
	}{
		{name: "Notion legacy without Cloud", provider: "notion", method: "legacy_byo", bridge: true},
		{name: "Feishu legacy without Cloud", provider: "feishu", method: "legacy_byo", bridge: true},
		{name: "Notion original startup fallback", provider: "notion", method: "legacy_byo"},
		{name: "Notion managed write lease", provider: "notion", method: "managed_oauth", bridge: true},
		{name: "multiple managed accounts", provider: "notion", method: "managed_oauth", bridge: true, multiple: true},
		{name: "managed must not use local token endpoint", provider: "notion", method: "managed_oauth", bridge: true, failure: "denied"},
		{name: "one failed account rejects whole write", provider: "notion", method: "managed_oauth", bridge: true, multiple: true, failure: "second denied"},
		{name: "Cloud unavailable", provider: "notion", method: "managed_oauth", bridge: true, failure: "offline"},
		{name: "lease provider mismatch", provider: "notion", method: "managed_oauth", bridge: true, failure: "provider"},
		{name: "lease inactive", provider: "notion", method: "managed_oauth", bridge: true, failure: "status"},
		{name: "lease unsupported token type", provider: "notion", method: "managed_oauth", bridge: true, failure: "token type"},
		{name: "CLI handle is not an algorithm token", provider: "feishu", method: "cli_personal_app", bridge: true, failure: "opaque"},
		{name: "lease missing token", provider: "notion", method: "managed_oauth", bridge: true, failure: "empty"},
		{name: "lease wrong connection", provider: "notion", method: "managed_oauth", bridge: true, failure: "connection"},
		{name: "owner changed since listing", provider: "notion", method: "legacy_byo", bridge: true, failure: "owner"},
		{name: "provider changed since listing", provider: "notion", method: "legacy_byo", bridge: true, failure: "meta provider"},
		{name: "connection revoked since listing", provider: "notion", method: "legacy_byo", bridge: true, failure: "meta status"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := providerconnection.DefaultService()
			providerconnection.SetDefaultService(nil)
			t.Cleanup(func() { providerconnection.SetDefaultService(previous) })
			ids := []string{"fixture-connection-1"}
			if tc.multiple {
				ids = append(ids, "fixture-connection-2")
			}
			registry := &writerResolutionRegistry{metas: map[string]providerconnection.ConnectionMeta{}}
			items := []map[string]any{}
			for _, id := range ids {
				items = append(items, map[string]any{"connection_id": id, "owner_user_id": "fixture-owner", "provider": tc.provider, "status": "ACTIVE"})
				meta := providerconnection.ConnectionMeta{AuthConnectionID: id, OwnerUserID: "fixture-owner", Provider: tc.provider, ConnectionMethod: tc.method, Status: "ACTIVE", CloudConnectionID: id, ProviderOptions: map[string]any{"chat_enabled": true}}
				if tc.method == "cli_personal_app" {
					meta.ProfileRef = "fixture-profile"
					meta.CredentialLocation = "local"
				}
				switch tc.failure {
				case "owner":
					meta.OwnerUserID = "fixture-other-owner"
				case "meta provider":
					meta.Provider = "feishu"
				case "meta status":
					meta.Status = "REVOKED"
				}
				registry.metas[id] = meta
			}
			var localTokenCalls, leaseCalls atomic.Int32
			auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("X-LazyMind-Internal-Token") != "fixture-internal" {
					t.Error("missing internal authentication")
					w.WriteHeader(403)
					return
				}
				if r.URL.Path == "/api/authservice/v1/cloud/connections/internal/chat-enabled" {
					if r.URL.Query().Get("provider") != tc.provider || r.URL.Query().Get("owner_user_id") != "fixture-owner" {
						t.Error("incorrect listing identity")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": items}})
					return
				}
				if strings.HasSuffix(r.URL.Path, "/token") {
					localTokenCalls.Add(1)
					id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/authservice/v1/cloud/connections/"), "/token")
					// Deliberately succeeds: a bypass must be detected even if the old
					// endpoint returns a superficially valid but inappropriate token.
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"connection_id": id, "provider": tc.provider, "status": "ACTIVE", "access_token": "fixture-fallback-" + id}})
					return
				}
				t.Error("unexpected auth endpoint")
				w.WriteHeader(404)
			}))
			defer auth.Close()
			t.Setenv("LAZYMIND_AUTH_SERVICE_URL", auth.URL)
			t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "fixture-internal")
			authorizer := &unavailableSourceAuthorizer{}
			if tc.bridge {
				bridge, err := providerconnection.NewLocalService(registry, authorizer, "fixture-client-instance")
				if err != nil {
					t.Fatal(err)
				}
				bridge.FeishuCLI = &unavailableFeishuBackend{}
				if tc.method == "managed_oauth" && tc.failure != "offline" {
					cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						leaseCalls.Add(1)
						if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer fixture-cloud-access" {
							t.Error("invalid Cloud request authentication")
						}
						var request cloudclient.ProviderAccessTokenLeaseRequest
						if json.NewDecoder(r.Body).Decode(&request) != nil || request.Consumer != "chat" || request.RequiredCapability != "chat.write" {
							t.Error("Writer did not request write authorization")
						}
						id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/provider-connections/"), "/access-token:lease")
						if request.SourceID != "chat:"+tc.provider || request.BindingID != "chat:"+id || request.ContextMode != "source_binding" {
							t.Error("Writer changed the existing chat lease context contract")
						}
						if tc.failure == "denied" || tc.failure == "second denied" && leaseCalls.Load() == 2 {
							w.WriteHeader(403)
							return
						}
						lease := cloudclient.ProviderAccessTokenLease{AuthConnectionID: id, Provider: tc.provider, AccessToken: "fixture-managed-" + id, TokenType: "Bearer", Status: "ACTIVE", ExpiresAt: time.Now().Add(time.Hour), TokenVersion: 1}
						switch tc.failure {
						case "provider":
							lease.Provider = "feishu"
							lease.SubjectType = "user"
						case "status":
							lease.Status = "REVOKED"
						case "token type":
							lease.TokenType = "CLIProfile"
						case "empty":
							lease.AccessToken = ""
						case "connection":
							lease.AuthConnectionID = "fixture-other-connection"
						}
						w.Header().Set("Content-Type", "application/json")
						_ = json.NewEncoder(w).Encode(lease)
					}))
					defer cloud.Close()
					bridge.Cloud, err = cloudclient.New(cloud.URL, cloud.Client())
					if err != nil {
						t.Fatal(err)
					}
					bridge.Session = cloudsession.NewService(cloudsession.ServiceDeps{Store: writerResolutionStore{}})
					if err := bridge.Session.Establish(t.Context(), cloudsession.TokenPair{AccessToken: "fixture-cloud-access", RefreshToken: "fixture-cloud-refresh", AccessExpiresAt: time.Now().Add(time.Hour)}); err != nil {
						t.Fatal(err)
					}
				}
				providerconnection.SetDefaultService(bridge)
			}
			config, err := LoadWriterProviderToolConfig(t.Context(), tc.provider, "fixture-owner")
			if tc.failure != "" {
				if !errors.Is(err, errWriterCredentialLoad) || len(config) != 0 {
					t.Error("credential failure did not reject the whole write")
				}
			} else {
				prefix := "fixture-fallback-"
				if tc.bridge {
					prefix = "fixture-legacy-"
				}
				if tc.method == "managed_oauth" {
					prefix = "fixture-managed-"
				}
				var want any = prefix + ids[0]
				if tc.multiple {
					want = []string{prefix + ids[0], prefix + ids[1]}
				}
				if err != nil || !reflect.DeepEqual(config, map[string]any{tc.provider: want}) {
					t.Error("Writer did not preserve the algorithm Bearer-token contract")
				}
			}
			if tc.bridge && (registry.calls == 0 || localTokenCalls.Load() != 0) {
				t.Error("Writer bypassed the registered connection bridge")
			}
			if tc.bridge && tc.method == "managed_oauth" && tc.failure != "offline" && leaseCalls.Load() == 0 {
				t.Error("managed connection never reached Cloud lease boundary")
			}
			if tc.method == "legacy_byo" && (leaseCalls.Load() != 0 || authorizer.calls.Load() != 0) {
				t.Error("legacy authentication acquired a Cloud or source-binding dependency")
			}
		})
	}
}
