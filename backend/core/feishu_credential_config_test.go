package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pc "lazymind/core/providerconnection"
)

type startupCredentialRegistry struct{ meta pc.ConnectionMeta }

func (r startupCredentialRegistry) Connection(context.Context, string, string) (pc.ConnectionMeta, error) {
	return r.meta, nil
}
func (startupCredentialRegistry) LegacyAccessToken(context.Context, string, string) (string, error) {
	return "fixture-legacy", nil
}
func (startupCredentialRegistry) UpsertManaged(context.Context, pc.ManagedMirror) error { return nil }

type startupCredentialAuthorizer struct{}

func (startupCredentialAuthorizer) Authorize(context.Context, string, string, string, string, string) error {
	return nil
}

func TestConfigureFeishuCLIWiresCredentialHelperWithoutDisablingScanner(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fixture-runtime")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, "./providerconnection/testdata/credential-runtime")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v: %s", err, out)
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	checksum := hex.EncodeToString(digest[:])
	for _, mode := range []string{"valid", "missing", "invalid checksum"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			profiles, err := pc.NewFeishuCLIProfileStore(root)
			if err != nil {
				t.Fatal(err)
			}
			profile, err := profiles.Ensure(t.Context(), "fixture-owner", "fixture-connection")
			if err != nil {
				t.Fatal(err)
			}
			if err := profiles.BindIdentity(t.Context(), profile, "fixture-tenant", "ou_fixture", time.Now()); err != nil {
				t.Fatal(err)
			}
			t.Setenv("LAZYMIND_FEISHU_CLI_SIDECAR_URL", "")
			t.Setenv("LAZYMIND_FEISHU_CLI_PATH", binary)
			t.Setenv("LAZYMIND_FEISHU_CLI_SHA256", checksum)
			t.Setenv("LAZYMIND_FEISHU_CLI_RUNTIME_ROOT", root)
			t.Setenv("LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_PATH", binary)
			t.Setenv("LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_SHA256", checksum)
			if mode == "missing" {
				t.Setenv("LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_PATH", "")
			}
			if mode == "invalid checksum" {
				t.Setenv("LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_SHA256", strings.Repeat("0", 64))
			}
			registry := startupCredentialRegistry{meta: pc.ConnectionMeta{AuthConnectionID: profile.ConnectionID, OwnerUserID: profile.LocalUserID, Provider: "feishu", ConnectionMethod: "cli_personal_app", CredentialLocation: "local", ProfileRef: profile.Reference, Status: "ACTIVE", ProviderOptions: map[string]any{"chat_enabled": true}}}
			service, err := pc.NewLocalService(registry, startupCredentialAuthorizer{}, "fixture-client-instance")
			if err != nil {
				t.Fatal(err)
			}
			configureFeishuCLI(service, pc.HTTPRegistry{BaseURL: "http://127.0.0.1:1", InternalToken: "fixture-internal"})
			read, err := service.ResolveAccessToken(t.Context(), pc.ResolveRequest{AuthConnectionID: profile.ConnectionID, UserID: profile.LocalUserID, ContextMode: pc.ContextModeSourceBinding, Consumer: "datasource", RequiredCapability: "datasource.read", SourceID: "fixture-source", BindingID: "fixture-binding"})
			if err != nil || read.TokenType != "CLIProfile" {
				t.Fatal("helper configuration disabled scanning")
			}
			result, err := service.ExecuteFeishuCLI(t.Context(), read.AccessToken, "docx_raw", map[string]string{"document_token": "fixture-document"})
			if err != nil || !strings.Contains(string(result.Data), "fixture read content") {
				t.Fatal("old CLI execution stopped working")
			}
			token, err := service.ResolveAccessToken(t.Context(), pc.ResolveRequest{AuthConnectionID: profile.ConnectionID, UserID: profile.LocalUserID, Consumer: "chat", RequiredCapability: "chat.write", SourceID: "chat:feishu", BindingID: "chat:fixture-connection"})
			if mode == "valid" {
				if err != nil || token.AccessToken != "fixture-startup-access" || token.TokenType != "Bearer" {
					t.Fatal("real Core startup did not configure the credential helper")
				}
				return
			}
			if err == nil || token.AccessToken != "" {
				t.Fatal("unconfigured helper returned chat credentials")
			}
		})
	}
}
