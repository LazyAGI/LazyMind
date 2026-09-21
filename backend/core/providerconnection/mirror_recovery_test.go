package providerconnection

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/cloudclient"
	"lazymind/core/cloudsession"
)

func TestListHandlerReturnsAnEmptyOptionalCloudPageWithoutACloudSession(t *testing.T) {
	service, err := NewLocalService(
		&mirrorRecoveryRegistry{},
		mirrorRecoveryAuthorizer{},
		"desktop-client-123456",
	)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/core/provider-connections", nil)
	request.Header.Set("X-User-Id", "local-owner-1")
	Handler{Service: service}.List(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var page cloudclient.ProviderConnectionPage
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("optional Cloud page=%#v want an explicit empty list", page)
	}
}

type mirrorRecoveryStore struct{ refreshToken string }

func (store *mirrorRecoveryStore) Load(context.Context) (string, error) {
	return store.refreshToken, nil
}

func (store *mirrorRecoveryStore) Save(_ context.Context, token string) error {
	store.refreshToken = token
	return nil
}

func (*mirrorRecoveryStore) Delete(context.Context) error { return nil }

type mirrorRecoveryRegistry struct{ mirrors []ManagedMirror }

func (*mirrorRecoveryRegistry) Connection(context.Context, string, string) (ConnectionMeta, error) {
	return ConnectionMeta{}, nil
}

func (*mirrorRecoveryRegistry) LegacyAccessToken(context.Context, string, string) (string, error) {
	return "", nil
}

func (registry *mirrorRecoveryRegistry) UpsertManaged(_ context.Context, mirror ManagedMirror) error {
	registry.mirrors = append(registry.mirrors, mirror)
	return nil
}

type mirrorRecoveryAuthorizer struct{}

func (mirrorRecoveryAuthorizer) Authorize(context.Context, string, string, string, string, string) error {
	return nil
}

func TestListConnectionsRecoversMissingManagedMirrorForLocalOwner(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/provider-connections":
			_, _ = w.Write([]byte(`{"items":[{"auth_connection_id":"connection-1","provider":"feishu","status":"ACTIVE","display_name":"fixture","provider_tenant_key":"tenant-1","provider_workspace_id":"tenant-1","provider_account_meta":{"open_id":"open-1"},"connection_method":"managed_oauth","credential_location":"cloud","capabilities":[{"capability":"datasource.browse","status":"AVAILABLE","contract_version":"provider-capabilities/v1"}]}]}`))
		case "/v1/account/me":
			_, _ = w.Write([]byte(`{"id":"cloud-owner-1","username":"fixture","email_masked":"f***@example.com","roles":["user"],"effective_permission_keys":[],"status":"active","rbac_version":1,"policy_revision":1}`))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	cloud, err := cloudclient.New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	session := cloudsession.NewService(cloudsession.ServiceDeps{Store: &mirrorRecoveryStore{}})
	if err := session.Establish(context.Background(), cloudsession.TokenPair{
		AccessToken: "access-token", RefreshToken: "refresh-token", AccessExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	registry := &mirrorRecoveryRegistry{}
	service, err := NewService(cloud, session, registry, mirrorRecoveryAuthorizer{}, "desktop-client-123456")
	if err != nil {
		t.Fatal(err)
	}

	page, err := service.ListConnections(context.Background(), "local-owner-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || len(registry.mirrors) != 1 {
		t.Fatalf("connections=%d mirrors=%d, want 1/1", len(page.Items), len(registry.mirrors))
	}
	mirror := registry.mirrors[0]
	if mirror.AuthConnectionID != "connection-1" || mirror.LocalOwnerUserID != "local-owner-1" ||
		mirror.CloudOwnerUserID != "cloud-owner-1" || mirror.Provider != "feishu" || mirror.Status != "ACTIVE" {
		t.Fatalf("unexpected recovered mirror: %+v", mirror)
	}
}
