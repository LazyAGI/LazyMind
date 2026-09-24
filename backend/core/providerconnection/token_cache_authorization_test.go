package providerconnection

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"lazymind/core/cloudclient"
	"lazymind/core/cloudsession"
)

// Read and write leases must be authorized independently even when the provider
// returns the same Bearer token. This fixture denies a write lease after a read
// succeeds, matching the Cloud broker's capability check before opening tokens.
func TestManagedTokenCacheCannotUpgradeReadToWrite(t *testing.T) {
	for _, offline := range []bool{false, true} {
		name := "Cloud denies write"
		if offline {
			name = "Cloud unavailable after read"
		}
		t.Run(name, func(t *testing.T) {
			service, requests := newCapabilityCacheFixture(t)
			read := cacheRequest("chat.read")
			if _, err := service.ResolveAccessToken(t.Context(), read); err != nil {
				t.Fatal(err)
			}
			if offline {
				service.Cloud = nil
			}
			write := cacheRequest("chat.write")
			resolved, err := service.ResolveAccessToken(t.Context(), write)
			if err == nil || resolved.AccessToken != "" {
				t.Fatal("read-authorized cache was reused for a write without write authorization")
			}
			want := 2
			if offline {
				want = 1
			}
			if len(requests) != want {
				t.Fatalf("lease requests = %d, want %d", len(requests), want)
			}
		})
	}
}

func TestManagedTokenCacheSeparatesAuthorizationContexts(t *testing.T) {
	for _, field := range []string{"capability", "source", "binding", "tenant", "consumer", "context mode"} {
		t.Run(field, func(t *testing.T) {
			service, requests := newCapabilityCacheFixture(t)
			request := cacheRequest("chat.read")
			if field == "consumer" {
				request.ContextMode = ContextModeSourceBinding
			}
			if _, err := service.ResolveAccessToken(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "capability":
				request.RequiredCapability = "datasource.read"
			case "source":
				request.SourceID = "source-two"
			case "binding":
				request.BindingID = "binding-two"
			case "tenant":
				request.TenantID = "tenant-two"
			case "consumer":
				request.Consumer = "scheduler"
			case "context mode":
				request.ContextMode = ContextModeSourceBinding
			}
			if _, err := service.ResolveAccessToken(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			if len(requests) != 2 {
				t.Fatal("another authorization context reused the first lease")
			}
		})
	}
}

func TestManagedTokenCacheReusesMatchingAuthorizationAndInvalidatesAllLeases(t *testing.T) {
	service, requests := newCapabilityCacheFixture(t)
	request := cacheRequest("chat.read")
	first, err := service.ResolveAccessToken(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ResolveAccessToken(t.Context(), request)
	if err != nil || second.AccessToken != first.AccessToken || len(requests) != 1 {
		t.Fatal("identical authorized request did not reuse the lease")
	}
	request.SourceID, request.BindingID = "source-two", "binding-two"
	request.ContextMode, request.Consumer, request.RequiredCapability = ContextModeSourceBinding, "datasource", "datasource.read"
	if _, err := service.ResolveAccessToken(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	beforeReport := len(requests)
	if err := service.ReportAccessTokenFailure(t.Context(), AccessTokenFailureReport{
		AuthConnectionID: request.AuthConnectionID, UserID: request.UserID, SourceID: request.SourceID, BindingID: request.BindingID,
		ContextMode: request.ContextMode, Consumer: request.Consumer, RequiredCapability: request.RequiredCapability,
		TokenVersion: 1, ErrorClass: "invalid_token", RequestID: "fixture-failure-report",
	}); err != nil {
		t.Fatal(err)
	}
	// Invalidating the credential must evict every capability/context lease for
	// that connection, not just the context that happened to report the failure.
	if _, err := service.ResolveAccessToken(t.Context(), cacheRequest("chat.read")); err != nil {
		t.Fatal(err)
	}
	if len(requests) != beforeReport+1 {
		t.Fatal("failure report left another context's stale token cached")
	}
	if _, err := service.ResolveAccessToken(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if len(requests) != beforeReport+2 {
		t.Fatal("failure report did not evict all context leases")
	}
}

func TestManagedTokenCacheLifecycleInvalidatesAllContexts(t *testing.T) {
	for _, action := range []string{"complete authorization", "synchronize connections", "revoke"} {
		t.Run(action, func(t *testing.T) {
			service, requests := newCapabilityCacheFixture(t)
			chat := cacheRequest("chat.read")
			source := ResolveRequest{AuthConnectionID: chat.AuthConnectionID, UserID: chat.UserID, SourceID: "fixture-source", BindingID: "fixture-binding", ContextMode: ContextModeSourceBinding, Consumer: "datasource", RequiredCapability: "datasource.read"}
			for _, request := range []ResolveRequest{chat, source} {
				if _, err := service.ResolveAccessToken(t.Context(), request); err != nil {
					t.Fatal(err)
				}
			}
			before := len(requests)
			var err error
			switch action {
			case "complete authorization":
				_, err = service.GetSession(t.Context(), chat.UserID, "fixture-session")
			case "synchronize connections":
				_, err = service.ListConnections(t.Context(), chat.UserID)
			case "revoke":
				err = service.Revoke(t.Context(), chat.AuthConnectionID)
			}
			if err != nil {
				t.Fatal(err)
			}
			for i, request := range []ResolveRequest{chat, source} {
				resolved, err := service.ResolveAccessToken(t.Context(), request)
				if action == "revoke" {
					if err == nil || resolved.AccessToken != "" {
						t.Fatal("revoked connection returned a cached token")
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if len(requests) != before+i+1 {
					t.Fatal("lifecycle transition left an authorization context cached")
				}
			}
		})
	}
}

func cacheRequest(capability string) ResolveRequest {
	return ResolveRequest{AuthConnectionID: "fixture-connection", UserID: "fixture-owner", SourceID: "chat:notion", BindingID: "chat:fixture-connection", Consumer: "chat", RequiredCapability: capability}
}

func newCapabilityCacheFixture(t *testing.T) (*Service, <-chan cloudclient.ProviderAccessTokenLeaseRequest) {
	t.Helper()
	requests := make(chan cloudclient.ProviderAccessTokenLeaseRequest, 10)
	var revoked atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-cloud-access" {
			t.Error("invalid Cloud authorization")
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		connection := cloudclient.ProviderConnection{AuthConnectionID: "fixture-connection", Provider: "notion", Status: "ACTIVE", ConnectionMethod: "managed_oauth", CredentialLocation: "cloud"}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/provider-connections/sessions/fixture-session":
			_ = json.NewEncoder(w).Encode(cloudclient.ProviderConnectionSession{SessionID: "fixture-session", AuthConnectionID: connection.AuthConnectionID, Provider: "notion", Status: "COMPLETED", ExpiresAt: time.Now().Add(time.Hour)})
			return
		case r.Method == http.MethodGet && r.URL.Path == "/v1/provider-connections/fixture-connection":
			_ = json.NewEncoder(w).Encode(connection)
			return
		case r.Method == http.MethodGet && r.URL.Path == "/v1/provider-connections":
			_ = json.NewEncoder(w).Encode(cloudclient.ProviderConnectionPage{Items: []cloudclient.ProviderConnection{connection}})
			return
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account/me":
			_, _ = w.Write([]byte(`{"id":"fixture-cloud-owner","username":"fixture","roles":["user"],"status":"active","rbac_version":1,"policy_revision":1}`))
			return
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/provider-connections/fixture-connection":
			revoked.Store(true)
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost {
			t.Error("invalid Cloud request method")
			w.WriteHeader(405)
			return
		}
		if r.URL.Path == "/v1/provider-connections/fixture-connection/access-token:report" {
			w.WriteHeader(204)
			return
		}
		if r.URL.Path != "/v1/provider-connections/fixture-connection/access-token:lease" {
			t.Error("unexpected Cloud endpoint")
			w.WriteHeader(404)
			return
		}
		var request cloudclient.ProviderAccessTokenLeaseRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		requests <- request
		if request.RequiredCapability == "chat.write" || revoked.Load() {
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cloudclient.ProviderAccessTokenLease{AuthConnectionID: "fixture-connection", Provider: "notion", AccessToken: "fixture-provider-access", TokenType: "Bearer", Status: "ACTIVE", TokenVersion: 1, ExpiresAt: time.Now().Add(time.Hour)})
	}))
	t.Cleanup(server.Close)
	cloud, err := cloudclient.New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	session := cloudsession.NewService(cloudsession.ServiceDeps{Store: &mirrorRecoveryStore{}})
	if err := session.Establish(t.Context(), cloudsession.TokenPair{AccessToken: "fixture-cloud-access", RefreshToken: "fixture-cloud-refresh", AccessExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	registryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/cloud/connections/internal/managed:upsert" {
			w.WriteHeader(204)
			return
		}
		if r.URL.Path != "/v1/cloud/connections/internal/fixture-connection" || r.URL.Query().Get("user_id") != "fixture-owner" {
			t.Error("incorrect registry identity")
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": ConnectionMeta{AuthConnectionID: "fixture-connection", OwnerUserID: "fixture-owner", Provider: "notion", ConnectionMethod: "managed_oauth", CloudConnectionID: "fixture-connection", Status: "ACTIVE", ProviderOptions: map[string]any{"chat_enabled": true}}})
	}))
	t.Cleanup(registryServer.Close)
	service, err := NewService(cloud, session, HTTPRegistry{BaseURL: registryServer.URL, HTTPClient: registryServer.Client()}, mirrorRecoveryAuthorizer{}, "fixture-client-instance")
	if err != nil {
		t.Fatal(err)
	}
	return service, requests
}
