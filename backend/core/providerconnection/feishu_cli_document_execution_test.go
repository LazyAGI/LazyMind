package providerconnection

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Existing authorization fixtures do not execute document mutations. This
// method keeps those fixtures usable when the runtime gains this operation.
func (*fakeFeishuCLIRuntime) RunDocumentAPI(context.Context, string, string, string, map[string]any, json.RawMessage) (FeishuCLIAPIResult, error) {
	return FeishuCLIAPIResult{}, ErrCLIUnavailable
}

// The test executable acts as a CLI fixture only in the child process. It never
// loads a real Feishu profile or sends a provider request.
func TestMain(m *testing.M) {
	if capture := os.Getenv("LAZYMIND_TEST_DOCUMENT_CLI_CAPTURE"); capture != "" {
		body, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(2)
		}
		payload, _ := json.Marshal(struct {
			Args []string
			Body string
		}{os.Args[1:], string(body)})
		file, err := os.OpenFile(capture, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			os.Exit(2)
		}
		_, writeErr := file.Write(append(payload, '\n'))
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			os.Exit(2)
		}
		switch os.Getenv("LAZYMIND_TEST_DOCUMENT_CLI_RESULT") {
		case "permission":
			_, _ = os.Stderr.WriteString(`{"ok":false,"error":{"type":"permission","subtype":"permission_denied","message":"fixture-private-document-body"}}`)
			os.Exit(1)
		case "failed":
			_, _ = os.Stderr.WriteString(`{"ok":false,"error":{"type":"api","subtype":"invalid_parameters","code":1770001,"message":"fixture-private-document-body"}}`)
			os.Exit(1)
		case "unknown":
			_, _ = os.Stderr.WriteString(`{"ok":false,"error":{"type":"network","subtype":"timeout","message":"fixture-private-document-body"}}`)
			os.Exit(1)
		case "invalid-response":
			_, _ = os.Stdout.WriteString(`not-json fixture-private-document-body`)
			os.Exit(0)
		}
		_, _ = os.Stdout.WriteString(`{"ok":true,"identity":"user","data":{"document":{"document_id":"doc_fixture"}}}`)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func documentExecutionProfile(t *testing.T) (*FeishuCLIProfileStore, FeishuCLIProfile) {
	t.Helper()
	profiles, err := NewFeishuCLIProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.Ensure(context.Background(), "fixture-owner", "fixture-connection")
	if err != nil {
		t.Fatal(err)
	}
	if err := profiles.BindIdentity(context.Background(), profile, "fixture-tenant", "fixture-open-id", time.Now()); err != nil {
		t.Fatal(err)
	}
	return profiles, profile
}

func TestFeishuDocumentRunnerPassesBodyThroughStdin(t *testing.T) {
	for _, tc := range []struct{ name, code string }{
		{"success", ""}, {"permission", "AUTH_SCOPE_MISSING"}, {"failed", "CLI_EXECUTION_FAILED"},
		{"unknown", "CLI_WRITE_OUTCOME_UNKNOWN"}, {"invalid-response", "CLI_WRITE_OUTCOME_UNKNOWN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profiles, profile := documentExecutionProfile(t)
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			capture := filepath.Join(t.TempDir(), "request.json")
			t.Setenv("LAZYMIND_TEST_DOCUMENT_CLI_CAPTURE", capture)
			t.Setenv("LAZYMIND_TEST_DOCUMENT_CLI_RESULT", tc.name)
			runner := &FeishuCLIRunner{binaryPath: binary, outputLimit: 2 << 20, commandTimeout: time.Second * 5}
			coordinator, err := NewFeishuCLIDeviceFlowCoordinator(runner, profiles, &fakeFeishuCLIConnectionRegistry{}, fixtureFeishuReadWriteScopes())
			if err != nil {
				t.Fatal(err)
			}
			params := documentCreateParams()
			params["body"] = `{"title":"` + strings.Repeat("fixture-content-", 4000) + `"}`
			result, err := coordinator.Execute(context.Background(), profile.LocalUserID, profile.ConnectionID, profile.Reference, "document_request", params)
			if (err != nil) != (tc.code != "") {
				t.Fatalf("error=%v expected code=%s", err, tc.code)
			}
			if err != nil && (safeFeishuCLIErrorCode(err) != tc.code || strings.Contains(err.Error(), "fixture-private-document-body")) {
				t.Fatalf("incorrect/unsafe failure classification: %v", err)
			}
			if tc.code == "" && string(result.Data) != `{"document":{"document_id":"doc_fixture"}}` {
				t.Fatalf("wrong result: %s", result.Data)
			}
			payload, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal("document request never reached CLI fixture:", err)
			}
			var got struct {
				Args []string
				Body string
			}
			decoder := json.NewDecoder(bytes.NewReader(payload))
			if err := decoder.Decode(&got); err != nil {
				t.Fatal(err)
			}
			if decoder.Decode(&struct{}{}) != io.EOF {
				t.Fatal("CLI process was invoked more than once")
			}
			if len(got.Args) < 3 || !slices.Equal(got.Args[:3], []string{"api", "POST", "/open-apis/docx/v1/documents"}) {
				t.Fatalf("wrong API invocation: %v", got.Args)
			}
			for flag, want := range map[string]string{"--as": "user", "--format": "json", "--data": "-"} {
				index := slices.Index(got.Args, flag)
				if index < 0 || index+1 >= len(got.Args) || got.Args[index+1] != want {
					t.Errorf("missing %s %s", flag, want)
				}
			}
			if got.Body != params["body"] || strings.Contains(strings.Join(got.Args, " "), "fixture-content-") {
				t.Fatal("document body was changed or exposed in argv")
			}
		})
	}
}

func TestFeishuDocumentCommandAllowlistIsLimitedToDocumentOperations(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		allowed      bool
	}{
		{"POST", "/open-apis/docx/v1/documents", true},
		{"POST", "/open-apis/docx/v1/documents/doc_fixture/blocks/block_fixture/children", true},
		{"POST", "/open-apis/docx/v1/documents/doc_fixture/blocks/block_fixture/descendant", true},
		{"PATCH", "/open-apis/docx/v1/documents/doc_fixture/blocks/batch_update", true},
		{"PATCH", "/open-apis/docx/v1/documents/doc_fixture/blocks/block_fixture", true},
		{"DELETE", "/open-apis/docx/v1/documents/doc_fixture/blocks/block_fixture/children/batch_delete", true},
		{"POST", "/open-apis/wiki/v2/spaces/space_fixture/nodes", true},
		{"POST", "/open-apis/wiki/v2/nodes/search", true},
		{"POST", "/open-apis/im/v1/messages", false},
		{"POST", "/open-apis/drive/v1/permissions/doc_fixture/members", false},
		{"DELETE", "/open-apis/drive/v1/files/doc_fixture", false},
		{"DELETE", "/open-apis/wiki/v2/spaces/space_fixture/nodes/node_fixture", false},
		{"POST", "/open-apis/docx/v1/documents/../im/v1/messages", false},
		{"POST", "/open-apis/docx/v1/documents?redirect=other", false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			args := []string{"api", tc.method, tc.path, "--data", "-", "--as", "user", "--format", "json"}
			if got := allowedFeishuCLICommand(args); got != tc.allowed {
				t.Fatalf("allow=%v want=%v", got, tc.allowed)
			}
		})
	}
}

func TestFeishuDocumentBridgeAcceptsBoundedLargeJSONBody(t *testing.T) {
	service, _, backend, _ := newDocumentConnectionService(t)
	lease, err := service.ResolveAccessToken(context.Background(), documentAccessRequest("chat.write"))
	if err != nil {
		t.Fatal(err)
	}
	params := documentCreateParams()
	params["body"] = `{"title":"` + strings.Repeat("fixture", 8000) + `"}`
	payload, err := json.Marshal(map[string]any{"handle": lease.AccessToken, "operation": "document_request", "identity": "user", "params": params})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/internal/provider-connections/feishu-cli:execute", bytes.NewReader(payload))
	request.Header.Set("X-LazyMind-Internal-Token", "fixture-internal-token")
	response := httptest.NewRecorder()
	TokenBridge{Service: service, InternalToken: "fixture-internal-token"}.ExecuteFeishuCLI(response, request)
	if response.Code != http.StatusOK || backend.calls != 1 {
		t.Fatalf("large document body rejected: status=%d calls=%d", response.Code, backend.calls)
	}
	params["body"] = `{"title":"` + strings.Repeat("x", 3<<20) + `"}`
	payload, err = json.Marshal(map[string]any{"handle": lease.AccessToken, "operation": "document_request", "identity": "user", "params": params})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/v1/internal/provider-connections/feishu-cli:execute", bytes.NewReader(payload))
	request.Header.Set("X-LazyMind-Internal-Token", "fixture-internal-token")
	response = httptest.NewRecorder()
	TokenBridge{Service: service, InternalToken: "fixture-internal-token"}.ExecuteFeishuCLI(response, request)
	if response.Code < 400 || backend.calls != 1 {
		t.Fatalf("valid oversized JSON reached Core executor: status=%d calls=%d", response.Code, backend.calls)
	}
}
