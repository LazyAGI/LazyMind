package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lazymind/core/providerconnection"
)

type documentSidecarBackend struct {
	calls     atomic.Int32
	wantBody  string
	fail      bool
	errorCode string
}

func (*documentSidecarBackend) VerifyVersion(context.Context, string) error { return nil }
func (*documentSidecarBackend) StartConfigInit(context.Context, string) (*providerconnection.FeishuCLIProcess, error) {
	return nil, providerconnection.ErrCLIUnavailable
}
func (*documentSidecarBackend) AuthLoginStart(context.Context, string, []string) (providerconnection.FeishuCLIAuthStart, error) {
	return providerconnection.FeishuCLIAuthStart{}, providerconnection.ErrCLIUnavailable
}
func (*documentSidecarBackend) AuthLoginComplete(context.Context, string, string) (providerconnection.FeishuCLIAuthComplete, error) {
	return providerconnection.FeishuCLIAuthComplete{}, providerconnection.ErrCLIUnavailable
}
func (*documentSidecarBackend) AuthStatus(context.Context, string) (providerconnection.FeishuCLIAuthStatus, error) {
	return providerconnection.FeishuCLIAuthStatus{}, providerconnection.ErrCLIUnavailable
}
func (*documentSidecarBackend) AuthCheck(context.Context, string, []string) (providerconnection.FeishuCLIAuthCheck, error) {
	return providerconnection.FeishuCLIAuthCheck{}, providerconnection.ErrCLIUnavailable
}
func (*documentSidecarBackend) ResolveUserIdentity(context.Context, string) (providerconnection.FeishuCLIUserIdentity, error) {
	return providerconnection.FeishuCLIUserIdentity{}, providerconnection.ErrCLIUnavailable
}
func (*documentSidecarBackend) RunAPI(context.Context, string, string, map[string]any) (providerconnection.FeishuCLIAPIResult, error) {
	return providerconnection.FeishuCLIAPIResult{}, providerconnection.ErrCLIUnavailable
}
func (*documentSidecarBackend) DownloadFile(context.Context, providerconnection.FeishuCLIProfile, string) ([]byte, error) {
	return nil, providerconnection.ErrCLIUnavailable
}
func (*documentSidecarBackend) UpsertCLI(context.Context, providerconnection.FeishuCLIConnectionMirror) error {
	return errors.New("unexpected profile registration")
}

func (backend *documentSidecarBackend) RunDocumentAPI(_ context.Context, _ string, method, path string, _ map[string]any, body json.RawMessage) (providerconnection.FeishuCLIAPIResult, error) {
	backend.calls.Add(1)
	if method != "POST" || path != "/open-apis/docx/v1/documents" || string(body) != backend.wantBody {
		return providerconnection.FeishuCLIAPIResult{}, errors.New("fixture request method/path/body mismatch")
	}
	if backend.fail {
		return providerconnection.FeishuCLIAPIResult{}, context.DeadlineExceeded
	}
	if backend.errorCode != "" {
		return providerconnection.FeishuCLIAPIResult{}, fmt.Errorf("fixture-private-document-body: %w", &providerconnection.FeishuCLICommandError{Code: backend.errorCode})
	}
	return providerconnection.FeishuCLIAPIResult{Data: json.RawMessage(`{"document":{"document_id":"doc_fixture"}}`)}, nil
}

type sidecarDocumentRegistry struct {
	meta providerconnection.ConnectionMeta
}

func (r *sidecarDocumentRegistry) Connection(context.Context, string, string) (providerconnection.ConnectionMeta, error) {
	return r.meta, nil
}
func (*sidecarDocumentRegistry) LegacyAccessToken(context.Context, string, string) (string, error) {
	return "", errors.New("unused")
}
func (*sidecarDocumentRegistry) UpsertManaged(context.Context, providerconnection.ManagedMirror) error {
	return errors.New("unused")
}
func (*sidecarDocumentRegistry) Authorize(context.Context, string, string, string, string, string) error {
	return errors.New("document is not source-bound")
}

func TestDocumentWriteThroughSignedSidecarPreservesBodyAndDoesNotReplay(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{
		{"", http.StatusOK}, {"AUTH_SCOPE_MISSING", http.StatusForbidden}, {"AUTH_TOKEN_EXPIRED", http.StatusUnauthorized},
		{"CLI_EXECUTION_FAILED", http.StatusUnprocessableEntity}, {"CLI_WRITE_OUTCOME_UNKNOWN", http.StatusGatewayTimeout},
	} {
		name := tc.code
		if name == "" {
			name = "success"
		}
		t.Run(name, func(t *testing.T) {
			body := `{"title":"` + strings.Repeat("fixture-content", 8000) + `"}`
			backend := &documentSidecarBackend{wantBody: body, fail: tc.code == "CLI_WRITE_OUTCOME_UNKNOWN", errorCode: tc.code}
			profiles, err := providerconnection.NewFeishuCLIProfileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			profile, err := profiles.Ensure(context.Background(), "fixture-owner", "fixture-connection")
			if err != nil {
				t.Fatal(err)
			}
			if err := profiles.BindIdentity(context.Background(), profile, "fixture-tenant", "fixture-open", time.Now()); err != nil {
				t.Fatal(err)
			}
			coordinator, err := providerconnection.NewFeishuCLIDeviceFlowCoordinator(backend, profiles, backend, providerconnection.DefaultFeishuCLIReadScopes)
			if err != nil {
				t.Fatal(err)
			}
			key := []byte("fixture-sidecar-key-12345678901234567890")
			server := &sidecarServer{backend: coordinator, hmacKey: key, now: time.Now, nonces: map[string]time.Time{}}
			httpServer := httptest.NewServer(server.signed(server.execute))
			defer httpServer.Close()
			client, err := providerconnection.NewFeishuCLISidecarClient(httpServer.URL, key, httpServer.Client())
			if err != nil {
				t.Fatal(err)
			}
			params := map[string]string{"method": "POST", "path": "/open-apis/docx/v1/documents", "query": "{}", "body": body}
			result, err := client.Execute(context.Background(), "fixture-owner", "fixture-connection", profile.Reference, "document_request", params)
			if (err != nil) != (tc.code != "") {
				t.Fatalf("error=%v expected failure=%s", err, tc.code)
			}
			if tc.code == "" && string(result.Data) != `{"document":{"document_id":"doc_fixture"}}` {
				t.Fatalf("incorrect result: %s", result.Data)
			}
			if backend.calls.Load() != 1 {
				t.Fatalf("write missing or replayed: %d calls", backend.calls.Load())
			}
			if tc.code != "" {
				var commandError *providerconnection.FeishuCLICommandError
				if !errors.As(err, &commandError) || commandError.Code != tc.code || strings.Contains(err.Error(), "fixture-private-document-body") {
					t.Fatalf("Sidecar lost/ leaked write classification: %v", err)
				}
			}
			registry := &sidecarDocumentRegistry{meta: providerconnection.ConnectionMeta{AuthConnectionID: profile.ConnectionID, OwnerUserID: profile.LocalUserID, Provider: "feishu", ConnectionMethod: "cli_personal_app", CredentialLocation: "cli_sidecar", ProfileRef: profile.Reference, Status: "ACTIVE", Scope: strings.Join(providerconnection.DefaultFeishuCLIReadScopes, " "), ProviderOptions: map[string]any{"chat_enabled": true}}}
			service, err := providerconnection.NewLocalService(registry, registry, "fixture-client-instance")
			if err != nil {
				t.Fatal(err)
			}
			service.FeishuCLI = client
			lease, err := service.ResolveAccessToken(context.Background(), providerconnection.ResolveRequest{AuthConnectionID: profile.ConnectionID, UserID: profile.LocalUserID, Consumer: "chat", ContextMode: "connection", RequiredCapability: "chat.write"})
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(map[string]any{"handle": lease.AccessToken, "operation": "document_request", "identity": "user", "params": params})
			request := httptest.NewRequest(http.MethodPost, "/v1/internal/provider-connections/feishu-cli:execute", bytes.NewReader(payload))
			request.Header.Set("X-LazyMind-Internal-Token", "fixture-internal-token")
			coreResult := httptest.NewRecorder()
			providerconnection.TokenBridge{Service: service, InternalToken: "fixture-internal-token"}.ExecuteFeishuCLI(coreResult, request)
			if coreResult.Code != tc.status {
				t.Fatalf("Core/Sidecar failure status changed: %d", coreResult.Code)
			}
			if tc.code != "" {
				var failure struct {
					Code string `json:"code"`
				}
				if json.Unmarshal(coreResult.Body.Bytes(), &failure) != nil || failure.Code != tc.code {
					t.Fatalf("Core lost Sidecar error class: %s", coreResult.Body.String())
				}
			}
			if strings.Contains(coreResult.Body.String(), "fixture-private-document-body") || backend.calls.Load() != 2 {
				t.Fatal("Core leaked or replayed the Sidecar operation")
			}
			params["body"] = `{"title":"` + strings.Repeat("x", 3<<20) + `"}`
			if _, err := client.Execute(context.Background(), "fixture-owner", "fixture-connection", profile.Reference, "document_request", params); err == nil {
				t.Fatal("unbounded document body accepted")
			}
			payload, _ = json.Marshal(map[string]any{"owner_user_id": profile.LocalUserID, "connection_id": profile.ConnectionID, "profile_ref": profile.Reference, "operation": "document_request", "params": params})
			request = httptest.NewRequest(http.MethodPost, "/v1/execute", bytes.NewReader(payload))
			timestamp := fmt.Sprint(time.Now().Unix())
			nonce := strings.Repeat("a", 48)
			request.Header.Set("X-LazyMind-CLI-Timestamp", timestamp)
			request.Header.Set("X-LazyMind-CLI-Nonce", nonce)
			request.Header.Set("X-LazyMind-CLI-Signature", providerconnection.SignFeishuCLISidecarRequest(key, http.MethodPost, "/v1/execute", timestamp, nonce, payload))
			rejected := httptest.NewRecorder()
			server.signed(server.execute)(rejected, request)
			if rejected.Code < 400 {
				t.Fatal("Sidecar receiver accepted valid oversized JSON")
			}
			if backend.calls.Load() != 2 {
				t.Fatal("oversized request reached the write backend")
			}
		})
	}
}
