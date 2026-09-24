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
	"sync"

	"github.com/larksuite/cli/cmd"
	"github.com/larksuite/cli/extension/transport"
)

type userCredential struct {
	AccessToken string `json:"access_token"`
	OpenID      string `json:"open_id"`
	TenantKey   string `json:"tenant_key"`
}

type credentialTransport struct {
	mu     sync.Mutex
	result userCredential
}

func (*credentialTransport) Name() string { return "lazymind-credential-handoff" }
func (capture *credentialTransport) ResolveInterceptor(context.Context) transport.Interceptor {
	return capture
}
func (*credentialTransport) PreRoundTrip(*http.Request) func(*http.Response, error) { return nil }

func (capture *credentialTransport) PreRoundTripE(request *http.Request) (func(*http.Response, error), error) {
	u := request.URL
	if u == nil || u.Scheme != "https" || u.Host != "open.feishu.cn" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" ||
		(request.Host != "" && request.Host != u.Host) {
		return nil, errors.New("credential request is not permitted")
	}
	// Renewal stays entirely in the official CLI, including its request body,
	// refresh token, storage and retry policy. Never capture renewal responses.
	if request.Method == http.MethodPost && u.Path == "/open-apis/authen/v2/oauth/token" {
		return nil, nil
	}
	if request.Method != http.MethodGet || u.Path != "/open-apis/authen/v1/user_info" {
		return nil, errors.New("credential request is not permitted")
	}
	capture.mu.Lock()
	capture.result = userCredential{}
	capture.mu.Unlock()
	authorization := request.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") {
		return nil, errors.New("user credential is unavailable")
	}
	token := strings.TrimPrefix(authorization, "Bearer ")
	if token == "" || len(token) > 8192 || strings.ContainsAny(token, " \t\r\n") {
		return nil, errors.New("user credential is unavailable")
	}
	return func(response *http.Response, err error) {
		if err != nil || response == nil || response.StatusCode != http.StatusOK || response.Body == nil {
			return
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
		_ = response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(body))
		var envelope struct {
			Code *int `json:"code"`
			Data struct {
				OpenID    string `json:"open_id"`
				TenantKey string `json:"tenant_key"`
			} `json:"data"`
		}
		if readErr != nil || len(body) > 16<<10 || json.Unmarshal(body, &envelope) != nil || envelope.Code == nil || *envelope.Code != 0 ||
			strings.TrimSpace(envelope.Data.OpenID) == "" || strings.TrimSpace(envelope.Data.TenantKey) == "" || len(envelope.Data.OpenID) > 512 || len(envelope.Data.TenantKey) > 512 {
			return
		}
		capture.mu.Lock()
		capture.result = userCredential{AccessToken: token, OpenID: envelope.Data.OpenID, TenantKey: envelope.Data.TenantKey}
		capture.mu.Unlock()
	}, nil
}

func run(args []string, output io.Writer) int {
	if len(args) != 0 {
		return 1
	}
	for _, item := range os.Environ() {
		key, value, _ := strings.Cut(item, "=")
		key = strings.ToUpper(key)
		if value == "" || (!strings.HasPrefix(key, "LARK_") && !strings.HasPrefix(key, "LARKSUITE_")) {
			continue
		}
		switch key {
		case "LARKSUITE_CLI_CONFIG_DIR", "LARKSUITE_CLI_DATA_DIR", "LARKSUITE_CLI_NO_UPDATE_NOTIFIER", "LARKSUITE_CLI_NO_SKILLS_NOTIFIER":
		default:
			return 1
		}
	}
	for _, key := range []string{"HOME", "LARKSUITE_CLI_CONFIG_DIR", "LARKSUITE_CLI_DATA_DIR"} {
		if !filepath.IsAbs(os.Getenv(key)) {
			return 1
		}
	}
	_ = os.Setenv("LARKSUITE_CLI_NO_UPDATE_NOTIFIER", "1")
	_ = os.Setenv("LARKSUITE_CLI_NO_SKILLS_NOTIFIER", "1")
	capture := &credentialTransport{}
	previous := transport.GetProvider()
	transport.Register(capture)
	defer transport.Register(previous)
	previousArgs := os.Args
	os.Args = []string{"feishu-credential-helper", "api", "GET", "/open-apis/authen/v1/user_info", "--as", "user", "--format", "json"}
	defer func() { os.Args = previousArgs }()
	// No env credential provider or third-party plugins are linked/loaded here.
	// The CLI resolves and refreshes its native profile; ordinary output is not
	// part of the credential protocol and must not reach the calling service.
	if cmd.ExecuteWithOptions(cmd.WithIO(strings.NewReader(""), io.Discard, io.Discard), cmd.WithoutPlugins(), cmd.WithoutServiceCommands()) != 0 {
		return 1
	}
	capture.mu.Lock()
	result := capture.result
	capture.mu.Unlock()
	if result.AccessToken == "" {
		return 1
	}
	if json.NewEncoder(output).Encode(result) != nil {
		return 1
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }
