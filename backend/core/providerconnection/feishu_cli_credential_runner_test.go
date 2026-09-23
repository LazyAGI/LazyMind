package providerconnection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type credentialHelperRuntime interface {
	ConfigureCredentialHelper(string, string) error
	UserAccessToken(context.Context, string) (string, FeishuCLIUserIdentity, error)
}

func TestCLICredentialRunnerVerifiesHelperAndIsolatesSecrets(t *testing.T) {
	root := t.TempDir()
	cli := filepath.Join(root, "fixture-cli")
	if err := os.WriteFile(cli, []byte("fixture CLI binary"), 0700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("fixture CLI binary"))
	runner, err := NewFeishuCLIRunner(cli, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	credentialRunner, ok := any(runner).(credentialHelperRuntime)
	if !ok {
		t.Fatal("CLI runner has no verified credential-helper handoff")
	}
	missingProfiles, err := NewFeishuCLIProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	missingProfile, err := missingProfiles.Ensure(t.Context(), "fixture-owner", "fixture-connection")
	if err != nil {
		t.Fatal(err)
	}
	if token, _, err := credentialRunner.UserAccessToken(t.Context(), missingProfile.ConfigDir); err == nil || token != "" {
		t.Fatal("missing helper produced a usable credential")
	}
	// A Go fixture executable runs on all target OSes. It checks that the
	// caller does not expose inherited application secrets or select another
	// CLI account, and emits only test-controlled protocol bytes.
	source := filepath.Join(root, "helper.go")
	const fixture = `package main
import("os";"path/filepath";"encoding/json";"fmt";"strings";"time")
func main(){
 if len(os.Args)!=1{os.Exit(90)}
 cwd,_:=os.Getwd()
 if os.Getenv("LARKSUITE_CLI_CONFIG_DIR")!=cwd||os.Getenv("LARKSUITE_CLI_DATA_DIR")!=filepath.Join(filepath.Dir(cwd),"state","cli-data"){os.Exit(91)}
 if os.Getenv("HOME")!=filepath.Join(filepath.Dir(cwd),"state","home"){os.Exit(92)}
 allowed:=map[string]bool{}
 for _,key:=range []string{"HOME","PATH","TMPDIR","TMP","TEMP","SYSTEMROOT","WINDIR","COMSPEC","USERPROFILE","APPDATA","LOCALAPPDATA","SSL_CERT_FILE","SSL_CERT_DIR","HTTP_PROXY","HTTPS_PROXY","NO_PROXY","DBUS_SESSION_BUS_ADDRESS","LARKSUITE_CLI_CONFIG_DIR","LARKSUITE_CLI_DATA_DIR","LARKSUITE_CLI_NO_UPDATE_NOTIFIER","LARKSUITE_CLI_NO_SKILLS_NOTIFIER"}{allowed[key]=true}
 for _,entry:=range os.Environ(){key,_,_:=strings.Cut(entry,"=");if !allowed[strings.ToUpper(key)]{os.Exit(93)}}
 raw,_:=os.ReadFile("fixture-result.json")
 var input struct{Body string;Fail bool;Delay bool;Overflow bool};json.Unmarshal(raw,&input)
 if input.Delay{time.Sleep(time.Second)}
 if input.Fail{fmt.Fprint(os.Stderr,"fixture-sensitive-stderr");os.Exit(1)}
 if input.Overflow{fmt.Print(strings.Repeat("x",65536));return}
 fmt.Print(input.Body)
}`
	if err := os.WriteFile(source, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(root, "fixture-helper")
	if runtime.GOOS == "windows" {
		helper += ".exe"
	}
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", helper, source)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v: %s", err, output)
	}
	binary, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	helperDigest := sha256.Sum256(binary)
	if err := credentialRunner.ConfigureCredentialHelper(helper, strings.Repeat("0", 64)); err == nil {
		t.Fatal("unverified helper was accepted")
	}
	if err := credentialRunner.ConfigureCredentialHelper(helper, hex.EncodeToString(helperDigest[:])); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"LARKSUITE_CLI_APP_ID", "LARKSUITE_CLI_APP_SECRET", "LARKSUITE_CLI_USER_ACCESS_TOKEN", "LARKSUITE_CLI_PROFILE", "LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "OPENAI_API_KEY", "DATABASE_PASSWORD", "FIXTURE_UNRELATED_SECRET"} {
		t.Setenv(key, "fixture-inherited-secret")
	}
	t.Setenv("HOME", filepath.Join(root, "unrelated-user-home"))
	for _, mode := range []string{"valid", "helper failure", "trailing output", "unknown secret field", "missing identity", "empty token", "oversized output", "timeout", "runner timeout"} {
		t.Run(mode, func(t *testing.T) {
			profiles, err := NewFeishuCLIProfileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			profile, err := profiles.Ensure(t.Context(), "fixture-owner", "fixture-connection")
			if err != nil {
				t.Fatal(err)
			}
			body := `{"access_token":"fixture-user-access","open_id":"ou_fixture","tenant_key":"fixture-tenant"}`
			input := map[string]any{"Body": body}
			switch mode {
			case "helper failure":
				input["Fail"] = true
			case "trailing output":
				input["Body"] = body + `{}`
			case "unknown secret field":
				input["Body"] = `{"access_token":"fixture-user-access","open_id":"ou_fixture","tenant_key":"fixture-tenant","refresh_token":"fixture-refresh-secret"}`
			case "missing identity":
				input["Body"] = `{"access_token":"fixture-user-access","open_id":"","tenant_key":"fixture-tenant"}`
			case "empty token":
				input["Body"] = `{"access_token":" ","open_id":"ou_fixture","tenant_key":"fixture-tenant"}`
			case "oversized output":
				input["Overflow"] = true
			case "timeout", "runner timeout":
				input["Delay"] = true
			}
			payload, _ := json.Marshal(input)
			if err := os.WriteFile(filepath.Join(profile.ConfigDir, "fixture-result.json"), payload, 0600); err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if mode == "timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			if mode == "runner timeout" {
				ctx = context.Background()
				previous := runner.commandTimeout
				runner.commandTimeout = 100 * time.Millisecond
				defer func() { runner.commandTimeout = previous }()
			}
			token, identity, err := credentialRunner.UserAccessToken(ctx, profile.ConfigDir)
			if mode == "valid" {
				if err != nil || token != "fixture-user-access" || identity.OpenID != "ou_fixture" || identity.TenantKey != "fixture-tenant" {
					t.Fatal("helper credentials were not decoded with isolated environment")
				}
				return
			}
			if err == nil || token != "" || identity.OpenID != "" {
				t.Fatal("failed helper returned credentials")
			}
			if strings.Contains(err.Error(), "fixture-") {
				t.Fatal("helper error leaked sensitive output")
			}
			if (mode == "timeout" || mode == "runner timeout") && !errors.Is(err, ErrCLITimeout) {
				t.Fatal("helper did not terminate because of its applicable timeout")
			}
		})
	}
}
