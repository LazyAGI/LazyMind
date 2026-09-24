package providerconnection

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Wire contract: HMAC-SHA256(key, domain label) is the AES-256-GCM key;
// the authenticated request signature is the response's associated data.
// Nonces and ciphertext are standard base64 JSON byte slices. Binding the
// response to each request prevents a captured response serving another user.
func TestCLIUserTokenSidecarRequiresConfidentialBoundResponse(t *testing.T) {
	for _, mode := range []string{"valid", "plaintext", "tampered", "wrong key", "replayed", "oversized", "invalid nonce", "unknown field", "trailing JSON"} {
		t.Run(mode, func(t *testing.T) {
			key := []byte(strings.Repeat("fixture-sidecar-key-", 2))
			var firstResponse []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.URL.Path != "/v1/user-access-token" || VerifyFeishuCLISidecarRequest(key, r.Method, r.URL.Path, r.Header.Get("X-LazyMind-CLI-Timestamp"), r.Header.Get("X-LazyMind-CLI-Nonce"), r.Header.Get("X-LazyMind-CLI-Signature"), body, time.Now()) != nil {
					t.Error("unsigned credential request")
					w.WriteHeader(403)
					return
				}
				var input map[string]string
				if json.Unmarshal(body, &input) != nil || len(input) != 4 || input["owner_user_id"] != "fixture-owner" || input["connection_id"] != "fixture-connection" || input["profile_ref"] != "fixture-profile" || input["required_capability"] != "chat.write" {
					t.Error("credential request lost its identity binding")
					w.WriteHeader(400)
					return
				}
				plain, _ := json.Marshal(ResolvedToken{AuthConnectionID: "fixture-connection", Provider: "feishu", AccessToken: "fixture-user-access", TokenType: "Bearer", SubjectType: "user", Status: "ACTIVE"})
				if mode == "plaintext" {
					_, _ = w.Write(plain)
					return
				}
				if mode == "oversized" {
					_, _ = w.Write([]byte(strings.Repeat("x", 65536)))
					return
				}
				if mode == "replayed" && firstResponse != nil {
					_, _ = w.Write(firstResponse)
					return
				}
				responseKey := key
				if mode == "wrong key" {
					responseKey = []byte("fixture-other-key")
				}
				mac := hmac.New(sha256.New, responseKey)
				_, _ = mac.Write([]byte("lazymind-feishu-cli-user-token-response-v1"))
				block, _ := aes.NewCipher(mac.Sum(nil))
				aead, _ := cipher.NewGCM(block)
				nonce := make([]byte, aead.NonceSize()) // A single test fixture encryption, not a production nonce source.
				sealed := aead.Seal(nil, nonce, plain, []byte(r.Header.Get("X-LazyMind-CLI-Signature")))
				if mode == "tampered" {
					sealed[len(sealed)-1] ^= 1
				}
				if mode == "invalid nonce" {
					nonce = []byte{1}
				}
				envelope := map[string][]byte{"nonce": nonce, "ciphertext": sealed}
				if mode == "unknown field" {
					envelope["unexpected"] = []byte("fixture")
				}
				firstResponse, _ = json.Marshal(envelope)
				if mode == "trailing JSON" {
					firstResponse = append(firstResponse, []byte(`{}`)...)
				}
				_, _ = w.Write(firstResponse)
			}))
			defer server.Close()
			client, err := NewFeishuCLISidecarClient(server.URL, key, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			backend, ok := any(client).(cliUserTokenBackend)
			if !ok {
				t.Fatal("Sidecar has no confidential credential handoff")
			}
			invoke := func() (ResolvedToken, error) {
				return backend.UserAccessToken(context.Background(), "fixture-owner", "fixture-connection", "fixture-profile", "chat.write")
			}
			result, err := invoke()
			if mode == "valid" || mode == "replayed" {
				if err != nil || result.AccessToken != "fixture-user-access" || result.AuthConnectionID != "fixture-connection" {
					t.Fatal("valid encrypted credential response failed")
				}
				if mode == "valid" {
					return
				}
				result, err = invoke()
			}
			if err == nil || result.AccessToken != "" {
				t.Fatal("untrusted credential response was accepted")
			}
			if strings.Contains(err.Error(), "fixture-user-access") {
				t.Fatal("credential appeared in the error")
			}
		})
	}
}
