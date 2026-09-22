package providerconnection

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

type preBindingRegistryStub struct {
	connectionCalls int
}

func (registry *preBindingRegistryStub) Connection(_ context.Context, userID, connectionID string) (ConnectionMeta, error) {
	registry.connectionCalls++
	return ConnectionMeta{AuthConnectionID: connectionID, OwnerUserID: userID, ConnectionMethod: "unsupported"}, nil
}

func (*preBindingRegistryStub) LegacyAccessToken(context.Context, string, string) (string, error) {
	return "", nil
}

func (*preBindingRegistryStub) UpsertManaged(context.Context, ManagedMirror) error { return nil }

func TestTokenBridgeRejectsMismatchedUserAndConnection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		headerUserID     string
		pathConnectionID string
	}{
		{name: "user mismatch", headerUserID: "user-other", pathConnectionID: "connection-1"},
		{name: "connection mismatch", headerUserID: "user-owner", pathConnectionID: "connection-other"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := resolveThroughTokenBridge(t, TokenBridge{Service: &Service{}, InternalToken: "internal-token"},
				test.headerUserID, test.pathConnectionID, `{
					"auth_connection_id":"connection-1",
					"user_id":"user-owner",
					"source_id":"source-real",
					"binding_id":"binding-real",
					"consumer":"datasource",
					"required_capability":"datasource.read"
				}`)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", response.Code)
			}
			if strings.Contains(response.Body.String(), "access_token") {
				t.Fatalf("rejection leaked token response fields: %q", response.Body.String())
			}
		})
	}
}

func TestTokenBridgeRejectsMissingSourceOrBinding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "source missing", body: `{
			"auth_connection_id":"connection-1",
			"user_id":"user-owner",
			"source_id":"",
			"binding_id":"binding-real",
			"consumer":"datasource",
			"required_capability":"datasource.read"
		}`},
		{name: "binding missing", body: `{
			"auth_connection_id":"connection-1",
			"user_id":"user-owner",
			"source_id":"source-real",
			"binding_id":"",
			"consumer":"datasource",
			"required_capability":"datasource.read"
		}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := resolveThroughTokenBridge(t, TokenBridge{Service: &Service{}, InternalToken: "internal-token"},
				"user-owner", "connection-1", test.body)
			if response.Code < 400 {
				t.Fatalf("status = %d, want safe failure", response.Code)
			}
			if strings.Contains(response.Body.String(), "access_token") {
				t.Fatalf("rejection leaked token response fields: %q", response.Body.String())
			}
		})
	}
}

func TestTokenBridgeAcceptsExplicitPreBindingBrowseShape(t *testing.T) {
	t.Parallel()

	registry := &preBindingRegistryStub{}
	bridge := TokenBridge{Service: &Service{Registry: registry}, InternalToken: "internal-token"}
	body := `{
		"auth_connection_id":"connection-1",
		"user_id":"user-owner",
		"tenant_id":"tenant-owner",
		"source_id":"",
		"binding_id":"",
		"context_mode":"pre_binding_browse",
		"consumer":"datasource",
		"required_capability":"datasource.browse"
	}`
	request := httptest.NewRequest(http.MethodPost, "/api/core/v1/internal/provider-connections/connection-1/access-token:resolve", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-LazyMind-Internal-Token", "internal-token")
	request.Header.Set("X-User-Id", "user-owner")
	request.Header.Set("X-Tenant-Id", "tenant-owner")
	request = mux.SetURLVars(request, map[string]string{"auth_connection_id": "connection-1"})
	response := httptest.NewRecorder()

	bridge.Resolve(response, request)

	if response.Code == http.StatusUnprocessableEntity {
		t.Fatalf("explicit pre-binding browse was rejected as an unknown or invalid request: %s", response.Body.String())
	}
	if registry.connectionCalls != 1 {
		t.Fatalf("registry connection calls = %d, want 1", registry.connectionCalls)
	}
}

func resolveThroughTokenBridge(t *testing.T, bridge TokenBridge, userID, connectionID, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/core/v1/internal/provider-connections/"+connectionID+"/access-token:resolve", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-LazyMind-Internal-Token", "internal-token")
	request.Header.Set("X-User-Id", userID)
	request = mux.SetURLVars(request, map[string]string{"auth_connection_id": connectionID})
	response := httptest.NewRecorder()
	bridge.Resolve(response, request)
	return response
}

func TestLegacyAccessTokenAcceptsAuthResponseMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, token string
		code        int
		wantError   bool
	}{
		{"full response", "fixture-token", 200, false},
		{"missing token", "", 200, true},
		{"rejected response", "fixture-token", 403, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/cloud/connections/connection-1/token" || r.URL.Query().Get("user_id") != "user-owner" || r.Header.Get("X-LazyMind-Internal-Token") != "internal-token" {
					t.Error("incorrect token request identity or authentication")
					http.Error(w, "forbidden", 403)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"code":%d,"message":"test","data":{"connection_id":"connection-1","provider":"feishu","auth_mode":"oauth_user","access_token":%q,"token_type":"Bearer","expires_at":null,"status":"ACTIVE"}}`, tc.code, tc.token)
			}))
			defer server.Close()
			registry := HTTPRegistry{BaseURL: server.URL, InternalToken: "internal-token"}
			token, err := registry.LegacyAccessToken(context.Background(), "user-owner", "connection-1")
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v, wantError=%v", err, tc.wantError)
			}
			if !tc.wantError && token != tc.token {
				t.Fatal("token was not preserved")
			}
			if tc.wantError && token != "" {
				t.Fatal("failed request returned a token")
			}
		})
	}
}
