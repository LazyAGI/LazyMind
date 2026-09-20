package providerconnection

import (
	"testing"
	"time"
)

func TestFeishuCLISidecarSignatureBindsMethodPathBodyAndNonce(t *testing.T) {
	key := []byte("fixture-sidecar-hmac-key-12345678901234567890")
	now := time.Unix(1_800_000_000, 0)
	timestamp := "1800000000"
	nonce := "0123456789abcdef0123456789abcdef0123456789abcdef"
	body := []byte(`{"owner_user_id":"user-a"}`)
	signature := SignFeishuCLISidecarRequest(key, "POST", "/v1/sessions:get", timestamp, nonce, body)
	if err := VerifyFeishuCLISidecarRequest(key, "POST", "/v1/sessions:get", timestamp, nonce, signature, body, now); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFeishuCLISidecarRequest(key, "POST", "/v1/execute", timestamp, nonce, signature, body, now); err == nil {
		t.Fatal("signature accepted for a different path")
	}
	if err := VerifyFeishuCLISidecarRequest(key, "POST", "/v1/sessions:get", timestamp, nonce, signature, []byte(`{}`), now); err == nil {
		t.Fatal("signature accepted for a different body")
	}
}

func TestFeishuCLISidecarSignatureExpires(t *testing.T) {
	key := []byte("fixture-sidecar-hmac-key-12345678901234567890")
	timestamp := "1800000000"
	nonce := "0123456789abcdef0123456789abcdef0123456789abcdef"
	body := []byte(`{}`)
	signature := SignFeishuCLISidecarRequest(key, "POST", "/v1/execute", timestamp, nonce, body)
	if err := VerifyFeishuCLISidecarRequest(key, "POST", "/v1/execute", timestamp, nonce, signature, body, time.Unix(1_800_000_031, 0)); err == nil {
		t.Fatal("expired sidecar signature was accepted")
	}
}
