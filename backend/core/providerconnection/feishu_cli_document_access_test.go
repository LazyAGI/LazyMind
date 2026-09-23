package providerconnection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/cloudclient"
)

type documentConnectionRegistry struct{ meta ConnectionMeta }

func (registry *documentConnectionRegistry) Connection(context.Context, string, string) (ConnectionMeta, error) {
	return registry.meta, nil
}
func (*documentConnectionRegistry) LegacyAccessToken(context.Context, string, string) (string, error) {
	return "fixture-legacy-token", nil
}
func (*documentConnectionRegistry) UpsertManaged(context.Context, ManagedMirror) error { return nil }

type documentCLIBackend struct {
	calls int
	err   error
}

func (*documentCLIBackend) Start(context.Context, string, string) (cloudclient.ProviderConnectionSession, error) {
	return cloudclient.ProviderConnectionSession{}, ErrCLIUnavailable
}
func (*documentCLIBackend) Get(context.Context, string, string) (cloudclient.ProviderConnectionSession, error) {
	return cloudclient.ProviderConnectionSession{}, ErrCLIUnavailable
}
func (*documentCLIBackend) Cancel(context.Context, string, string) error { return ErrCLIUnavailable }
func (backend *documentCLIBackend) Execute(_ context.Context, owner, connection, profile, operation string, _ map[string]string) (FeishuCLIExecutionResult, error) {
	backend.calls++
	if owner != "fixture-owner" || connection != "fixture-connection" || profile != "fixture-profile" || operation != "document_request" {
		return FeishuCLIExecutionResult{}, errors.New("wrong document execution identity")
	}
	return FeishuCLIExecutionResult{Data: json.RawMessage(`{"document":{"document_id":"doc_fixture"}}`)}, backend.err
}

type rejectDocumentSourceBinding struct{ calls int }

func (authorizer *rejectDocumentSourceBinding) Authorize(context.Context, string, string, string, string, string) error {
	authorizer.calls++
	return errors.New("document has no datasource binding")
}

func newDocumentConnectionService(t *testing.T) (*Service, *documentConnectionRegistry, *documentCLIBackend, *rejectDocumentSourceBinding) {
	t.Helper()
	registry := &documentConnectionRegistry{meta: ConnectionMeta{
		AuthConnectionID: "fixture-connection", OwnerUserID: "fixture-owner", Provider: "feishu",
		ConnectionMethod: "cli_personal_app", CredentialLocation: "local", ProfileRef: "fixture-profile",
		Status: "ACTIVE", Scope: strings.Join(fixtureFeishuReadWriteScopes(), " "),
		ProviderOptions: map[string]any{"chat_enabled": true},
	}}
	authorizer := &rejectDocumentSourceBinding{}
	service, err := NewLocalService(registry, authorizer, "fixture-client-instance")
	if err != nil {
		t.Fatal(err)
	}
	backend := &documentCLIBackend{}
	service.FeishuCLI = backend
	return service, registry, backend, authorizer
}

func documentAccessRequest(capability string) ResolveRequest {
	return ResolveRequest{AuthConnectionID: "fixture-connection", UserID: "fixture-owner",
		Consumer: "chat", ContextMode: "connection", RequiredCapability: capability}
}

func documentCreateParams() map[string]string {
	return map[string]string{"method": "POST", "path": "/open-apis/docx/v1/documents",
		"query": `{}`, "body": `{"title":"Fixture document"}`}
}

func TestFeishuDocumentWriteUsesOwnedConnectionWithoutSourceBinding(t *testing.T) {
	service, _, backend, authorizer := newDocumentConnectionService(t)
	lease, err := service.ResolveAccessToken(context.Background(), documentAccessRequest("chat.write"))
	if err != nil {
		t.Fatal(err)
	}
	if lease.TokenType != "CLIProfile" || !strings.HasPrefix(lease.AccessToken, "lmc_fcli_") {
		t.Fatalf("CLI connection did not yield an execution handle: %+v", lease)
	}
	result, err := service.ExecuteFeishuCLI(context.Background(), lease.AccessToken, "document_request", documentCreateParams())
	if err != nil || string(result.Data) != `{"document":{"document_id":"doc_fixture"}}` {
		t.Fatalf("document creation failed: data=%s err=%v", result.Data, err)
	}
	if backend.calls != 1 || authorizer.calls != 0 {
		t.Fatalf("incorrect execution boundary: CLI=%d source=%d", backend.calls, authorizer.calls)
	}
	for _, change := range []map[string]string{
		{"path": "https://other.fixture.invalid/open-apis/docx/v1/documents"},
		{"path": "/open-apis/im/v1/messages"},
		{"path": "/open-apis/docx/v1/documents/../im/v1/messages"},
		{"path": "/open-apis/authen/v1/user_info", "method": "GET"},
		{"method": "DELETE", "path": "/open-apis/drive/v1/files/doc_fixture"},
		{"body": "not-json"},
		{"query": "[]"},
		{"extra": "unsupported"},
	} {
		params := documentCreateParams()
		for key, value := range change {
			params[key] = value
		}
		if _, err := service.ExecuteFeishuCLI(context.Background(), lease.AccessToken, "document_request", params); err == nil {
			t.Errorf("invalid document request accepted: %v", change)
		}
	}
	if backend.calls != 1 {
		t.Fatal("invalid document request reached CLI")
	}
}

func TestFeishuDocumentReadHandleCannotWrite(t *testing.T) {
	service, registry, backend, _ := newDocumentConnectionService(t)
	lease, err := service.ResolveAccessToken(context.Background(), documentAccessRequest("chat.read"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteFeishuCLI(context.Background(), lease.AccessToken, "document_request", documentCreateParams()); err == nil {
		t.Fatal("read handle executed a document write")
	}
	if backend.calls != 0 {
		t.Fatal("denied write reached CLI")
	}
	_, err = service.ExecuteFeishuCLI(context.Background(), lease.AccessToken, "document_request", map[string]string{
		"method": "POST", "path": "/open-apis/wiki/v2/nodes/search", "query": `{}`, "body": `{"query":"fixture"}`,
	})
	if err != nil || backend.calls != 1 {
		t.Fatalf("read-only Wiki search was rejected merely because it uses POST: %v", err)
	}
	// A second handle created before additional consent remains read-only after
	// the connection's scopes are upgraded.
	registry.meta.Scope = strings.Join(fixtureFeishuReadScopes, " ")
	oldRead, err := service.ResolveAccessToken(context.Background(), documentAccessRequest("chat.read"))
	if err != nil {
		t.Fatal(err)
	}
	registry.meta.Scope = strings.Join(fixtureFeishuReadWriteScopes(), " ")
	if _, err := service.ExecuteFeishuCLI(context.Background(), oldRead.AccessToken, "document_request", documentCreateParams()); err == nil {
		t.Fatal("scope upgrade also upgraded an existing read handle")
	}
	if backend.calls != 1 {
		t.Fatal("upgraded connection allowed a write through its old read handle")
	}
}

func TestFeishuDocumentWriteRejectsInvalidConnectionContext(t *testing.T) {
	cases := []struct {
		name   string
		change func(*ConnectionMeta, *ResolveRequest)
	}{
		{"other owner", func(meta *ConnectionMeta, _ *ResolveRequest) { meta.OwnerUserID = "other-owner" }},
		{"revoked", func(meta *ConnectionMeta, _ *ResolveRequest) { meta.Status = "REVOKED" }},
		{"disabled", func(meta *ConnectionMeta, _ *ResolveRequest) { meta.ProviderOptions["chat_enabled"] = false }},
		{"read-only", func(meta *ConnectionMeta, _ *ResolveRequest) { meta.Scope = strings.Join(fixtureFeishuReadScopes, " ") }},
		{"wrong connection", func(meta *ConnectionMeta, _ *ResolveRequest) { meta.AuthConnectionID = "other-connection" }},
		{"unsupported capability", func(_ *ConnectionMeta, req *ResolveRequest) { req.RequiredCapability = "admin.write" }},
		{"datasource bypass", func(_ *ConnectionMeta, req *ResolveRequest) { req.Consumer = "datasource" }},
		{"mixed binding context", func(_ *ConnectionMeta, req *ResolveRequest) { req.SourceID = "source"; req.BindingID = "binding" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, registry, backend, _ := newDocumentConnectionService(t)
			req := documentAccessRequest("chat.write")
			tc.change(&registry.meta, &req)
			if _, err := service.ResolveAccessToken(context.Background(), req); err == nil {
				t.Fatal("invalid document access was accepted")
			}
			if backend.calls != 0 {
				t.Fatal("invalid context reached CLI")
			}
		})
	}
}

func TestFeishuDocumentHandleRechecksRevocationAndExpiry(t *testing.T) {
	for _, invalidate := range []string{"revoked", "expired", "disabled", "lost-write-scope"} {
		t.Run(invalidate, func(t *testing.T) {
			service, registry, backend, _ := newDocumentConnectionService(t)
			now := time.Unix(1_800_000_000, 0)
			service.Now = func() time.Time { return now }
			lease, err := service.ResolveAccessToken(context.Background(), documentAccessRequest("chat.write"))
			if err != nil {
				t.Fatal(err)
			}
			switch invalidate {
			case "revoked":
				registry.meta.Status = "REVOKED"
			case "expired":
				now = lease.ExpiresAt.Add(time.Second)
			case "disabled":
				registry.meta.ProviderOptions["chat_enabled"] = false
			case "lost-write-scope":
				registry.meta.Scope = strings.Join(fixtureFeishuReadScopes, " ")
			}
			if _, err := service.ExecuteFeishuCLI(context.Background(), lease.AccessToken, "document_request", documentCreateParams()); err == nil {
				t.Fatal("invalidated handle wrote a document")
			}
			if backend.calls != 0 {
				t.Fatal("invalidated handle reached CLI")
			}
		})
	}
}

func TestFeishuDocumentWriteFailureIsNotReplayed(t *testing.T) {
	service, _, backend, _ := newDocumentConnectionService(t)
	lease, err := service.ResolveAccessToken(context.Background(), documentAccessRequest("chat.write"))
	if err != nil {
		t.Fatal(err)
	}
	backend.err = context.DeadlineExceeded
	if _, err := service.ExecuteFeishuCLI(context.Background(), lease.AccessToken, "document_request", documentCreateParams()); err == nil {
		t.Fatal("unknown write outcome was reported as success")
	} else if safeFeishuCLIErrorCode(err) != "CLI_WRITE_OUTCOME_UNKNOWN" {
		t.Fatalf("write timeout lost unknown-outcome classification: %v", err)
	}
	if backend.calls != 1 {
		t.Fatalf("write was replayed: %d executions", backend.calls)
	}
}

func TestFeishuDocumentBridgePreservesWriteFailureClasses(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
		cause  error
	}{
		{"AUTH_SCOPE_MISSING", http.StatusForbidden, &FeishuCLICommandError{Code: "AUTH_SCOPE_MISSING"}},
		{"AUTH_TOKEN_EXPIRED", http.StatusUnauthorized, &FeishuCLICommandError{Code: "AUTH_TOKEN_EXPIRED"}},
		{"CLI_EXECUTION_FAILED", http.StatusUnprocessableEntity, &FeishuCLICommandError{Code: "CLI_EXECUTION_FAILED"}},
		{"CLI_WRITE_OUTCOME_UNKNOWN", http.StatusGatewayTimeout, context.DeadlineExceeded},
	} {
		t.Run(tc.code, func(t *testing.T) {
			service, _, backend, _ := newDocumentConnectionService(t)
			lease, err := service.ResolveAccessToken(context.Background(), documentAccessRequest("chat.write"))
			if err != nil {
				t.Fatal(err)
			}
			backend.err = fmt.Errorf("fixture-private-document-body: %w", tc.cause)
			payload, _ := json.Marshal(map[string]any{"handle": lease.AccessToken, "operation": "document_request", "identity": "user", "params": documentCreateParams()})
			request := httptest.NewRequest(http.MethodPost, "/v1/internal/provider-connections/feishu-cli:execute", bytes.NewReader(payload))
			request.Header.Set("X-LazyMind-Internal-Token", "fixture-internal-token")
			result := httptest.NewRecorder()
			TokenBridge{Service: service, InternalToken: "fixture-internal-token"}.ExecuteFeishuCLI(result, request)
			var body struct {
				Code string `json:"code"`
			}
			if result.Code != tc.status || json.Unmarshal(result.Body.Bytes(), &body) != nil || body.Code != tc.code {
				t.Fatalf("classification lost: status=%d body=%s", result.Code, result.Body.String())
			}
			if strings.Contains(result.Body.String(), "fixture-private-document-body") {
				t.Fatal("private provider details leaked")
			}
			if backend.calls != 1 {
				t.Fatalf("failure replayed: %d calls", backend.calls)
			}
		})
	}
}
