package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/larksuite/cli/extension/credential"
	"github.com/larksuite/cli/extension/transport"
)

type fixtureCredentials struct{}

func (fixtureCredentials) Name() string { return "fixture" }
func (fixtureCredentials) ResolveAccount(context.Context) (*credential.Account, error) {
	return &credential.Account{AppID: "cli_fixture", Brand: credential.BrandFeishu, DefaultAs: credential.IdentityUser, SupportedIdentities: credential.SupportsUser}, nil
}
func (fixtureCredentials) ResolveToken(_ context.Context, spec credential.TokenSpec) (*credential.Token, error) {
	if spec.Type != credential.TokenTypeUAT {
		return nil, errors.New("fixture only allows user identity")
	}
	return &credential.Token{Value: "fixture-user-access", Source: "fixture"}, nil
}

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Exercise the actual pinned CLI credential/SDK/transport pipeline with a
// public credential provider and an in-memory network fixture. No CLI private
// token-store format, real credentials, or test-only production branch is used.
func TestHelperExportsOnlyVerifiedUserAccessToken(t *testing.T) {
	credential.Register(fixtureCredentials{})
	for _, mode := range []string{"valid", "http denied", "api denied", "missing user", "missing tenant", "network failed", "extra argument", "environment override"} {
		t.Run(mode, func(t *testing.T) {
			for _, item := range os.Environ() {
				key, _, _ := strings.Cut(item, "=")
				if strings.HasPrefix(key, "LARK_") || strings.HasPrefix(key, "LARKSUITE_") {
					t.Setenv(key, "")
				}
			}
			root := t.TempDir()
			for key, value := range map[string]string{"HOME": root, "LARKSUITE_CLI_CONFIG_DIR": filepath.Join(root, "config"), "LARKSUITE_CLI_DATA_DIR": filepath.Join(root, "data"), "LARKSUITE_CLI_NO_UPDATE_NOTIFIER": "1", "LARKSUITE_CLI_NO_SKILLS_NOTIFIER": "1"} {
				t.Setenv(key, value)
			}
			oldTransport, oldHTTP, oldArgs := transport.GetProvider(), http.DefaultTransport, os.Args
			t.Cleanup(func() {
				transport.Register(oldTransport)
				http.DefaultTransport = oldHTTP
				os.Args = oldArgs
			})
			calls := 0
			http.DefaultTransport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if mode == "valid" && calls == 1 {
					provider := transport.GetProvider()
					if provider == nil {
						t.Fatal("helper did not register a transport boundary")
					}
					guard, ok := provider.ResolveInterceptor(r.Context()).(transport.AbortableInterceptor)
					if !ok {
						t.Fatal("helper cannot prevent credential requests outside its allowlist")
					}
					for _, test := range []struct {
						method, url string
						allowed     bool
					}{
						{"POST", "https://open.feishu.cn/open-apis/authen/v2/oauth/token", true},
						{"GET", "https://open.feishu.cn/open-apis/authen/v2/oauth/token", false},
						{"POST", "https://open.feishu.cn/open-apis/docx/v1/documents", false},
						{"GET", "http://open.feishu.cn/open-apis/authen/v1/user_info", false},
						{"GET", "https://example.invalid/open-apis/authen/v1/user_info", false},
						{"GET", "https://open.feishu.cn/open-apis/authen/v1/user_info?redirect=fixture", false},
					} {
						body := `{"grant_type":"refresh_token","refresh_token":"fixture-refresh-secret"}`
						request, _ := http.NewRequest(test.method, test.url, strings.NewReader(body))
						post, err := guard.PreRoundTripE(request)
						if (err == nil) != test.allowed {
							t.Fatal("helper network allowlist did not distinguish renewal from arbitrary traffic")
						}
						if test.allowed {
							remaining, _ := io.ReadAll(request.Body)
							if string(remaining) != body {
								t.Fatal("helper consumed the CLI-owned refresh body")
							}
							if post != nil {
								post(&http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"fixture-renewal-access","refresh_token":"fixture-refresh-secret"}`)), Request: request}, nil)
							}
						}
					}
				}
				if r.Method != "GET" || r.URL.Scheme != "https" || r.URL.Host != "open.feishu.cn" || r.URL.Path != "/open-apis/authen/v1/user_info" || r.URL.RawQuery != "" {
					t.Error("helper allowed unexpected network traffic")
					return nil, errors.New("fixture unexpected endpoint")
				}
				if r.Header.Get("Authorization") != "Bearer fixture-user-access" {
					t.Error("helper did not use authenticated user credentials")
				}
				if mode == "network failed" {
					return nil, errors.New("fixture-sensitive-upstream-error")
				}
				status := 200
				code := 0
				openID, tenant := "ou_fixture", "fixture-tenant"
				switch mode {
				case "http denied":
					status = 401
				case "api denied":
					code = 99991663
				case "missing user":
					openID = ""
				case "missing tenant":
					tenant = ""
				}
				payload, _ := json.Marshal(map[string]any{"code": code, "msg": "fixture-sensitive-upstream-error", "data": map[string]string{"open_id": openID, "tenant_key": tenant, "name": "fixture-private-profile", "email": "fixture@example.invalid"}})
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(payload)), Request: r}, nil
			})
			var output bytes.Buffer
			var args []string
			if mode == "extra argument" {
				args = []string{"api", "POST", "/open-apis/docx/v1/documents"}
			}
			if mode == "environment override" {
				t.Setenv("LARKSUITE_CLI_USER_ACCESS_TOKEN", "fixture-override-secret")
			}
			code := run(args, &output)
			if mode != "valid" {
				if code == 0 || output.Len() != 0 {
					t.Fatal("helper exposed credentials or raw diagnostics after verification failed")
				}
				if (mode == "extra argument" || mode == "environment override") && calls != 0 {
					t.Fatal("rejected helper invocation reached the network")
				}
				return
			}
			var result map[string]string
			if code != 0 || json.Unmarshal(output.Bytes(), &result) != nil || len(result) != 3 || result["access_token"] != "fixture-user-access" || result["open_id"] != "ou_fixture" || result["tenant_key"] != "fixture-tenant" || calls == 0 {
				t.Fatal("helper did not return only the verified access token and bound identity")
			}
		})
	}
}
