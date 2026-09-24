package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	pc "lazymind/core/providerconnection"
)

// The child uses the real production run(), including configuration and router.
func TestSidecarCredentialStartupProcess(t *testing.T) {
	if os.Getenv("LAZYMIND_SIDECAR_STARTUP_TEST") != "1" {
		return
	}
	if err := run(); err != nil {
		t.Fatal("Sidecar production startup failed")
	}
}

func TestSidecarProductionStartupWiresHelperAndPreservesCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fixture-runtime")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, "../../providerconnection/testdata/credential-runtime")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v: %s", err, output)
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
			profiles, err := pc.NewFeishuCLIProfileStore(filepath.Join(root, "profiles"))
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
			secretFile := func(name, value string) string {
				file := filepath.Join(root, name)
				if err := os.WriteFile(file, []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
				return file
			}
			key := []byte(strings.Repeat("fixture-sidecar-key-", 2))
			t.Setenv("LAZYMIND_FEISHU_CLI_PATH", binary)
			t.Setenv("LAZYMIND_FEISHU_CLI_SHA256_FILE", secretFile("cli.sha256", checksum))
			t.Setenv("LAZYMIND_FEISHU_CLI_RUNTIME_ROOT", filepath.Join(root, "profiles"))
			t.Setenv("LAZYMIND_AUTH_SERVICE_URL", "http://127.0.0.1:1")
			t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN_FILE", secretFile("auth.key", "fixture-internal-token"))
			t.Setenv("LAZYMIND_FEISHU_CLI_SIDECAR_HMAC_KEY_FILE", secretFile("sidecar.key", string(key)))
			t.Setenv("LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_PATH", binary)
			helperChecksum := checksum
			if mode == "invalid checksum" {
				helperChecksum = strings.Repeat("0", 64)
			}
			t.Setenv("LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_SHA256_FILE", secretFile("helper.sha256", helperChecksum))
			if mode == "missing" {
				t.Setenv("LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_PATH", "")
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			_ = listener.Close()
			t.Setenv("LAZYMIND_FEISHU_CLI_SIDECAR_LISTEN", address)
			t.Setenv("LAZYMIND_SIDECAR_STARTUP_TEST", "1")
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			process := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSidecarCredentialStartupProcess$")
			process.Stdout = io.Discard
			process.Stderr = io.Discard
			if err := process.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { _ = process.Wait(); close(done) }()
			t.Cleanup(func() { _ = process.Process.Kill(); <-done })
			client := &http.Client{Timeout: 3 * time.Second}
			origin := "http://" + address
			ready := false
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for !ready {
				select {
				case <-ctx.Done():
					t.Fatal("Sidecar did not become healthy")
				case <-done:
					t.Fatal("helper configuration stopped the existing Sidecar")
				case <-ticker.C:
				}
				response, err := client.Get(origin + "/healthz")
				if err == nil {
					ready = response.StatusCode == 204
					_ = response.Body.Close()
				}
			}
			counter := 0
			var lastRequest *http.Request
			post := func(path string, input any) (int, []byte, string) {
				counter++
				body, _ := json.Marshal(input)
				timestamp := strconv.FormatInt(time.Now().Unix(), 10)
				nonce := fmt.Sprintf("%048x", counter)
				signature := pc.SignFeishuCLISidecarRequest(key, http.MethodPost, path, timestamp, nonce, body)
				request, _ := http.NewRequestWithContext(ctx, http.MethodPost, origin+path, bytes.NewReader(body))
				request.Header.Set("X-LazyMind-CLI-Timestamp", timestamp)
				request.Header.Set("X-LazyMind-CLI-Nonce", nonce)
				request.Header.Set("X-LazyMind-CLI-Signature", signature)
				lastRequest = request
				response, err := client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				payload, _ := io.ReadAll(response.Body)
				return response.StatusCode, payload, signature
			}
			status, raw, _ := post("/v1/execute", map[string]any{"owner_user_id": profile.LocalUserID, "connection_id": profile.ConnectionID, "profile_ref": profile.Reference, "operation": "docx_raw", "params": map[string]string{"document_token": "fixture-document"}})
			if status != 200 || !bytes.Contains(raw, []byte("fixture read content")) {
				t.Fatal("helper configuration broke the existing signed CLI read endpoint")
			}
			status, raw, signature := post("/v1/user-access-token", map[string]string{"owner_user_id": profile.LocalUserID, "connection_id": profile.ConnectionID, "profile_ref": profile.Reference, "required_capability": "chat.write"})
			if mode != "valid" {
				if status < 400 || bytes.Contains(raw, []byte("fixture-startup-access")) {
					t.Fatal("invalid helper exported a credential")
				}
				return
			}
			if status != 200 || bytes.Contains(raw, []byte("fixture-startup-access")) {
				t.Fatal("production Sidecar did not wire the confidential credential handler")
			}
			var envelope struct {
				Nonce      []byte `json:"nonce"`
				Ciphertext []byte `json:"ciphertext"`
			}
			if json.Unmarshal(raw, &envelope) != nil {
				t.Fatal("invalid encrypted envelope")
			}
			mac := hmac.New(sha256.New, key)
			_, _ = mac.Write([]byte("lazymind-feishu-cli-user-token-response-v1"))
			block, _ := aes.NewCipher(mac.Sum(nil))
			aead, _ := cipher.NewGCM(block)
			if len(envelope.Nonce) != aead.NonceSize() {
				t.Fatal("invalid encryption nonce")
			}
			plain, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, []byte(signature))
			if err != nil {
				t.Fatal("production response was not bound to its request")
			}
			var token pc.ResolvedToken
			if json.Unmarshal(plain, &token) != nil || token.AccessToken != "fixture-startup-access" || token.TokenType != "Bearer" || token.SubjectType != "user" {
				t.Fatal("production helper did not yield the expected credential")
			}
			replay := lastRequest.Clone(ctx)
			replay.Body, _ = lastRequest.GetBody()
			replayResponse, err := client.Do(replay)
			if err != nil {
				t.Fatal(err)
			}
			_ = replayResponse.Body.Close()
			if replayResponse.StatusCode != 403 {
				t.Fatal("production credential route accepted a replayed request")
			}
			unsigned := lastRequest.Clone(ctx)
			unsigned.Body, _ = lastRequest.GetBody()
			unsigned.Header.Set("X-LazyMind-CLI-Nonce", strings.Repeat("f", 48))
			unsigned.Header.Set("X-LazyMind-CLI-Signature", strings.Repeat("0", 64))
			unsignedResponse, err := client.Do(unsigned)
			if err != nil {
				t.Fatal(err)
			}
			_ = unsignedResponse.Body.Close()
			if unsignedResponse.StatusCode != 403 {
				t.Fatal("production credential route did not authenticate the request")
			}
			firstNonce := append([]byte(nil), envelope.Nonce...)
			status, raw, _ = post("/v1/user-access-token", map[string]string{"owner_user_id": profile.LocalUserID, "connection_id": profile.ConnectionID, "profile_ref": profile.Reference, "required_capability": "chat.write"})
			if status != 200 || json.Unmarshal(raw, &envelope) != nil || bytes.Equal(firstNonce, envelope.Nonce) {
				t.Fatal("production Sidecar reused an encryption nonce")
			}
			for _, field := range []string{"owner_user_id", "connection_id", "profile_ref", "required_capability"} {
				input := map[string]string{"owner_user_id": profile.LocalUserID, "connection_id": profile.ConnectionID, "profile_ref": profile.Reference, "required_capability": "chat.write"}
				input[field] = "fixture-other"
				status, raw, _ := post("/v1/user-access-token", input)
				if status < 400 || bytes.Contains(raw, []byte("fixture-startup-access")) {
					t.Fatal("production credential route accepted a signed but unauthorized request")
				}
			}
		})
	}
}
