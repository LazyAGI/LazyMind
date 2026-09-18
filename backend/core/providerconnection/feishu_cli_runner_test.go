package providerconnection

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFeishuCLICommandAllowlistRejectsBroadAndShellLikeInputs(t *testing.T) {
	for _, args := range [][]string{
		{"auth", "login", "--recommend", "--no-wait", "--json"},
		{"auth", "login", "--domain", "all", "--no-wait", "--json"},
		{"sh", "-c", "lark-cli auth status"},
		{"api", "DELETE", "/open-apis/drive/v1/files/file-a", "--as", "user", "--format", "json"},
	} {
		if allowedFeishuCLICommand(args) {
			t.Fatalf("unsafe CLI command was allowed: %#v", args)
		}
	}
	if !allowedFeishuCLICommand([]string{"auth", "status", "--json", "--verify"}) {
		t.Fatal("read-only auth status command was rejected")
	}
	if !allowedFeishuCLICommand([]string{"auth", "check", "--scope", "drive:drive:readonly", "--json"}) {
		t.Fatal("read-only auth scope check command was rejected")
	}
}

func TestFeishuCLIVerificationURLAllowlist(t *testing.T) {
	for _, value := range []string{
		"https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=fixture",
		"https://accounts.larksuite.com/oauth/v1/device/verify?flow_id=fixture",
		"https://open.feishu.cn/page/cli?user_code=fixture&from=cli",
		"https://open.larksuite.com/page/cli?user_code=fixture&from=cli",
	} {
		if !validFeishuVerificationURL(value) {
			t.Fatalf("official verification URL was rejected: %s", value)
		}
	}
	for _, value := range []string{
		"https://accounts.feishu.cn.example.invalid/oauth/v1/device/verify",
		"https://user@accounts.feishu.cn/oauth/v1/device/verify",
		"http://accounts.feishu.cn/oauth/v1/device/verify",
		"https://accounts.feishu.cn:444/oauth/v1/device/verify",
		"https://accounts.feishu.cn/page/cli",
		"https://open.feishu.cn/app",
	} {
		if validFeishuVerificationURL(value) {
			t.Fatalf("untrusted verification URL was allowed: %s", value)
		}
	}
}

func TestFeishuCLIErrorClassificationDoesNotReturnProviderMessage(t *testing.T) {
	err := classifyFeishuCLIOutputError([]byte(`{"ok":false,"error":{"type":"permission","subtype":"missing_scope","code":99991679,"message":"sensitive upstream text"}}`), "CLI_UNAVAILABLE", ErrCLIUnavailable)
	if safeFeishuCLIErrorCode(err) != "AUTH_WAITING_ADMIN" {
		t.Fatalf("safe error code = %q", safeFeishuCLIErrorCode(err))
	}
	if errors.Is(err, ErrCLIUnavailable) {
		t.Fatal("structured error was not classified")
	}
	if err.Error() != "Feishu CLI command failed" {
		t.Fatalf("error leaked provider detail: %q", err.Error())
	}
}

func TestFeishuCLIAuthStatusAcceptsOfficialHintField(t *testing.T) {
	var status FeishuCLIAuthStatus
	decoder := json.NewDecoder(strings.NewReader(`{
		"appId":"cli_fixture","brand":"feishu","defaultAs":"user","identity":"user","verified":true,
		"identities":{"user":{"available":true,"status":"ready","verified":true,"message":"","hint":"run auth login --help","userName":"Fixture User","openId":"ou_fixture","tokenStatus":"valid","scope":"drive:drive:readonly","expiresAt":"2026-09-05T12:00:00Z","refreshExpiresAt":"2026-10-05T12:00:00Z","grantedAt":"2026-09-04T12:00:00Z"},"bot":{"available":true,"status":"ready"}},
		"note":""
	}`))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&status); err != nil {
		t.Fatalf("official auth status schema was rejected: %v", err)
	}
	if status.Identity != "user" || !status.Verified || status.Identities.User.Hint == "" {
		t.Fatalf("auth status = %#v", status)
	}
}

func TestFeishuCLIUserIdentityWireAcceptsOfficialProfileFields(t *testing.T) {
	var identity feishuCLIUserIdentityWire
	if err := decodeFeishuCLIEnvelope([]byte(`{
		"avatar_big":"https://example.invalid/big","avatar_middle":"https://example.invalid/middle",
		"avatar_thumb":"https://example.invalid/thumb","avatar_url":"https://example.invalid/avatar",
		"en_name":"Fixture User","name":"测试用户","open_id":"ou_fixture",
		"tenant_key":"tenant_fixture","union_id":"on_fixture"
	}`), &identity); err != nil {
		t.Fatalf("official user identity schema was rejected: %v", err)
	}
	if identity.OpenID == "" || identity.TenantKey == "" || identity.Name == "" {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestFeishuCLIConfigInitAcceptsVerificationURLFromStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	root := t.TempDir()
	binaryPath := filepath.Join(root, "lark-cli")
	script := "#!/bin/sh\nprintf '%s\\n' 'https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=fixture'\nsleep 1\n"
	if err := os.WriteFile(binaryPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	profileDir := filepath.Join(root, "config")
	if err := os.Mkdir(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &FeishuCLIRunner{
		binaryPath: binaryPath, outputLimit: defaultFeishuCLIOutputLimit,
		commandTimeout: defaultFeishuCLICommandTimout,
	}
	process, err := runner.StartConfigInit(context.Background(), profileDir)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Cancel()
	dataInfo, err := os.Stat(filepath.Join(root, "state", "cli-data"))
	if err != nil {
		t.Fatal(err)
	}
	if dataInfo.Mode().Perm() != 0o700 {
		t.Fatalf("CLI data directory permissions = %04o, want 0700", dataInfo.Mode().Perm())
	}
	homeInfo, err := os.Stat(filepath.Join(root, "state", "home"))
	if err != nil {
		t.Fatal(err)
	}
	if homeInfo.Mode().Perm() != 0o700 {
		t.Fatalf("CLI home directory permissions = %04o, want 0700", homeInfo.Mode().Perm())
	}
	select {
	case got := <-process.VerificationURL:
		if got != "https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=fixture" {
			t.Fatalf("verification URL = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("verification URL from stdout was not detected")
	}
}

func TestFeishuCLIEnvironmentUsesConnectionScopedDataDirectory(t *testing.T) {
	t.Setenv("LARKSUITE_CLI_DATA_DIR", filepath.Join(t.TempDir(), "global-data"))
	t.Setenv("HOME", filepath.Join(t.TempDir(), "shared-home"))
	root := t.TempDir()
	profileDir := filepath.Join(root, "config")
	runner := &FeishuCLIRunner{}

	environment := runner.environment(profileDir)
	wantData := "LARKSUITE_CLI_DATA_DIR=" + filepath.Join(root, "state", "cli-data")
	wantHome := "HOME=" + filepath.Join(root, "state", "home")
	dataCount := 0
	homeCount := 0
	for _, item := range environment {
		if strings.HasPrefix(item, "LARKSUITE_CLI_DATA_DIR=") {
			dataCount++
			if item != wantData {
				t.Fatalf("CLI data directory = %q, want %q", item, wantData)
			}
		}
		if strings.HasPrefix(item, "HOME=") {
			homeCount++
			if item != wantHome {
				t.Fatalf("CLI home directory = %q, want %q", item, wantHome)
			}
		}
	}
	if dataCount != 1 {
		t.Fatalf("CLI data directory entries = %d, want 1", dataCount)
	}
	if homeCount != 1 {
		t.Fatalf("CLI home directory entries = %d, want 1", homeCount)
	}
}
