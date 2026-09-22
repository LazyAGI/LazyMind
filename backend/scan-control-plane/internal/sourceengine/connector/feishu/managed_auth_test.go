package feishu

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

type recordingFeishuTokenResolver struct {
	requests []TokenRequest
	token    Token
	err      error
}

func (resolver *recordingFeishuTokenResolver) GetToken(_ context.Context, request TokenRequest) (Token, error) {
	resolver.requests = append(resolver.requests, request)
	return resolver.token, resolver.err
}

func TestManagedFeishuTokenContextByOperation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		requiredCapability string
		invoke             func(*FeishuConnector) error
	}{
		{
			name:               "list uses browse",
			requiredCapability: "datasource.browse",
			invoke: func(connectorUnderTest *FeishuConnector) error {
				_, err := connectorUnderTest.ListChildren(context.Background(), connector.ListChildrenRequest{
					TargetType:       TargetTypeDriveFolder,
					TargetRef:        "drive:folder-root",
					NodeRef:          "drive:folder-root",
					AuthConnectionID: "feishu-connection",
					PageSize:         10,
					ProviderOptions: connector.ProviderOptions{
						"user_id": "user-owner", "source_id": "source-real", "binding_id": "binding-real",
					},
				})
				return err
			},
		},
		{
			name:               "search uses browse",
			requiredCapability: "datasource.browse",
			invoke: func(connectorUnderTest *FeishuConnector) error {
				_, err := connectorUnderTest.Search(context.Background(), connector.SearchRequest{
					TargetType:       TargetTypeDriveFolder,
					TargetRef:        "drive:folder-root",
					NodeRef:          "drive:folder-root",
					Keyword:          "release notes",
					AuthConnectionID: "feishu-connection",
					PageSize:         10,
					ProviderOptions: connector.ProviderOptions{
						"user_id": "user-owner", "source_id": "source-real", "binding_id": "binding-real",
					},
				})
				return err
			},
		},
		{
			name:               "fetch uses read",
			requiredCapability: "datasource.read",
			invoke: func(connectorUnderTest *FeishuConnector) error {
				_, err := connectorUnderTest.FetchPage(context.Background(), connector.FetchPageRequest{
					SourceID:          "source-real",
					BindingID:         "binding-real",
					BindingGeneration: 1,
					TargetType:        TargetTypeDriveFolder,
					TargetRef:         "drive:folder-root",
					ScopeType:         connector.ScopeTypeFull,
					PageSize:          10,
					AuthConnectionID:  "feishu-connection",
					ProviderOptions:   connector.ProviderOptions{"user_id": "user-owner"},
				})
				return err
			},
		},
		{
			name:               "export uses parse",
			requiredCapability: "datasource.parse",
			invoke: func(connectorUnderTest *FeishuConnector) error {
				_, err := connectorUnderTest.ExportObject(context.Background(), connector.ExportObjectRequest{
					SourceID:        "source-real",
					BindingID:       "binding-real",
					ObjectKey:       "feishu:drive:file-1",
					SourceVersion:   "revision-1",
					ExportFormat:    connector.ExportFormatOriginal,
					ProviderOptions: connector.ProviderOptions{"user_id": "user-owner"},
					ProviderMeta: connector.ProviderMeta{
						"auth_connection_id": "feishu-connection", "kind": string(ObjectKindDriveFile), "token": "file-1", "file_type": "file",
					},
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stop := connector.NewError(connector.ErrorCodeTransient, "stop after token request")
			resolver := &recordingFeishuTokenResolver{err: stop}
			connectorUnderTest := NewFeishuConnector(resolver, newFeishuAPIStub())

			if err := test.invoke(connectorUnderTest); err != stop {
				t.Fatalf("operation error = %v, want resolver sentinel", err)
			}
			if len(resolver.requests) != 1 {
				t.Fatalf("token requests = %d, want 1", len(resolver.requests))
			}
			assertFeishuTokenContext(t, resolver.requests[0], test.requiredCapability)
		})
	}
}

func TestManagedFeishuRejectsNotionConnection(t *testing.T) {
	t.Parallel()

	resolver := &recordingFeishuTokenResolver{token: Token{
		AccessToken: "fake-notion-access", Provider: "notion", Status: "ACTIVE",
	}}
	connectorUnderTest := NewFeishuConnector(resolver, newFeishuAPIStub())
	if _, err := connectorUnderTest.loadToken(context.Background(), "notion-connection", "user-owner"); err == nil {
		t.Fatal("Feishu connector accepted a Notion Provider token")
	}
}

func TestManagedFeishuRejectsTenantTokenForPersonalDocuments(t *testing.T) {
	t.Parallel()

	token := Token{AccessToken: "fake-tenant-access", Provider: "feishu", Status: "ACTIVE"}
	subject := reflect.ValueOf(&token).Elem().FieldByName("SubjectType")
	if !subject.IsValid() || subject.Kind() != reflect.String || !subject.CanSet() {
		t.Fatal("managed Provider Token must expose a writable SubjectType string")
	}
	subject.SetString("tenant")
	resolver := &recordingFeishuTokenResolver{token: token}
	connectorUnderTest := NewFeishuConnector(resolver, newFeishuAPIStub())
	if _, err := connectorUnderTest.loadToken(context.Background(), "feishu-connection", "user-owner"); err == nil {
		t.Fatal("Feishu personal document connector accepted a tenant token")
	}
}

func TestManagedFeishuPreBindingBrowseUsesExplicitContextMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		invoke func(*FeishuConnector) error
	}{
		{
			name: "list",
			invoke: func(connectorUnderTest *FeishuConnector) error {
				_, err := connectorUnderTest.ListChildren(context.Background(), connector.ListChildrenRequest{
					TargetType: TargetTypeDriveFolder, TargetRef: "drive:folder-root", NodeRef: "drive:folder-root",
					AuthConnectionID: "feishu-connection", PageSize: 10,
					ProviderOptions: connector.ProviderOptions{"user_id": "user-owner", "tenant_id": "tenant-owner"},
				})
				return err
			},
		},
		{
			name: "search",
			invoke: func(connectorUnderTest *FeishuConnector) error {
				_, err := connectorUnderTest.Search(context.Background(), connector.SearchRequest{
					TargetType: TargetTypeDriveFolder, TargetRef: "drive:folder-root", Keyword: "release",
					AuthConnectionID: "feishu-connection", PageSize: 10,
					ProviderOptions: connector.ProviderOptions{"user_id": "user-owner", "tenant_id": "tenant-owner"},
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stop := connector.NewError(connector.ErrorCodeTransient, "stop after token request")
			resolver := &recordingFeishuTokenResolver{err: stop}
			if err := test.invoke(NewFeishuConnector(resolver, newFeishuAPIStub())); err != stop {
				t.Fatalf("operation error = %v, want resolver sentinel", err)
			}
			if len(resolver.requests) != 1 {
				t.Fatalf("token requests = %d, want 1", len(resolver.requests))
			}
			request := resolver.requests[0]
			if request.UserID != "user-owner" || request.TenantID != "tenant-owner" || request.SourceID != "" || request.BindingID != "" ||
				request.Consumer != "datasource" || request.RequiredCapability != "datasource.browse" {
				t.Fatalf("pre-binding token context = %+v", request)
			}
			if mode := reflectedFeishuTokenContextMode(t, request); mode != "pre_binding_browse" {
				t.Fatalf("context mode = %q, want pre_binding_browse", mode)
			}
		})
	}
}

func TestManagedResolverFailureDoesNotFallbackToLegacyTokenEndpoint(t *testing.T) {
	t.Parallel()

	legacyCalls := 0
	legacyServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		legacyCalls++
		writeFeishuJSON(t, response, http.StatusOK, map[string]any{"access_token": "legacy-token"})
	}))
	defer legacyServer.Close()
	coreServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		writeFeishuJSON(t, response, http.StatusServiceUnavailable, map[string]any{"code": "CLOUD_UNAVAILABLE"})
	}))
	defer coreServer.Close()

	client := newHTTPAuthTestClient(t, legacyServer.URL)
	if err := client.UseCoreTokenBridge(coreServer.URL); err != nil {
		t.Fatalf("enable Core bridge: %v", err)
	}
	_, err := client.GetToken(context.Background(), TokenRequest{
		AuthConnectionID:   "feishu-connection",
		UserID:             "user-owner",
		SourceID:           "source-real",
		BindingID:          "binding-real",
		Consumer:           "datasource",
		RequiredCapability: "datasource.read",
	})
	if err == nil {
		t.Fatal("managed resolver failure unexpectedly succeeded")
	}
	if legacyCalls != 0 {
		t.Fatalf("managed resolver failure called legacy token endpoint %d times", legacyCalls)
	}
}

func assertFeishuTokenContext(t *testing.T, request TokenRequest, requiredCapability string) {
	t.Helper()
	if request.AuthConnectionID != "feishu-connection" || request.UserID != "user-owner" ||
		request.SourceID != "source-real" || request.BindingID != "binding-real" ||
		request.Consumer != "datasource" || request.RequiredCapability != requiredCapability {
		t.Fatalf("token context = %+v, want owner/source/binding datasource context with capability %s", request, requiredCapability)
	}
}

func reflectedFeishuTokenContextMode(t *testing.T, request TokenRequest) string {
	t.Helper()
	field := reflect.ValueOf(request).FieldByName("ContextMode")
	if !field.IsValid() || field.Kind() != reflect.String {
		t.Fatal("TokenRequest must declare an explicit ContextMode")
	}
	return field.String()
}
