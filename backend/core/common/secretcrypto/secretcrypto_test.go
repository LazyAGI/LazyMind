package secretcrypto

import (
	"encoding/json"
	"testing"
)

// TestEncodeDecodeRoundTrip verifies that encoding and decoding produces the original plaintext.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	key := "my-secret-key-12345"
	plaintext := []byte("hello world")

	encoded, err := EncodeAESGCM(plaintext, key)
	if err != nil {
		t.Fatalf("EncodeAESGCM: %v", err)
	}

	decoded, recognized, err := DecodeAESGCM(encoded, key)
	if err != nil {
		t.Fatalf("DecodeAESGCM: %v", err)
	}
	if !recognized {
		t.Fatal("DecodeAESGCM: not recognized")
	}
	if string(decoded) != string(plaintext) {
		t.Fatalf("round-trip mismatch: got %q, want %q", string(decoded), string(plaintext))
	}
}

// TestDecodeAESGCM_WrongKey ensures decryption with a mismatched key returns an error.
func TestDecodeAESGCM_WrongKey(t *testing.T) {
	plaintext := []byte("secret data")
	encoded, err := EncodeAESGCM(plaintext, "correct-key")
	if err != nil {
		t.Fatalf("EncodeAESGCM: %v", err)
	}

	_, _, err = DecodeAESGCM(encoded, "wrong-key")
	if err == nil {
		t.Fatal("expected error with wrong key")
	}
}

// TestDecodeAESGCM_EmptyInput checks that nil input is not recognized as a valid encoding.
func TestDecodeAESGCM_EmptyInput(t *testing.T) {
	_, recognized, err := DecodeAESGCM(nil, "some-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recognized {
		t.Fatal("expected not recognized for nil input")
	}
}

// TestDecodeAESGCM_InvalidJSON checks that non-JSON input is not recognized.
func TestDecodeAESGCM_InvalidJSON(t *testing.T) {
	_, recognized, err := DecodeAESGCM(json.RawMessage("not-json"), "some-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recognized {
		t.Fatal("expected not recognized for invalid JSON")
	}
}

// TestDecodeAESGCM_WrongEncType checks that an unrecognized encryption type is rejected.
func TestDecodeAESGCM_WrongEncType(t *testing.T) {
	raw := json.RawMessage(`{"enc":"aes-cbc","nonce":"AAAA","v":"AAAA"}`)
	_, recognized, err := DecodeAESGCM(raw, "some-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recognized {
		t.Fatal("expected not recognized for wrong enc type")
	}
}

// TestDecodeAESGCM_EmptyValue checks that an empty ciphertext value is not recognized.
func TestDecodeAESGCM_EmptyValue(t *testing.T) {
	raw := json.RawMessage(`{"enc":"aes-gcm","nonce":"AAAA","v":""}`)
	_, recognized, err := DecodeAESGCM(raw, "some-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recognized {
		t.Fatal("expected not recognized for empty value")
	}
}

// TestDecodeAESGCM_InvalidBase64 ensures that invalid base64 encoding produces an error.
func TestDecodeAESGCM_InvalidBase64(t *testing.T) {
	raw := json.RawMessage(`{"enc":"aes-gcm","nonce":"!!!","v":"!!!"}`)
	_, recognized, err := DecodeAESGCM(raw, "some-key")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
	if !recognized {
		t.Fatal("expected recognized for invalid base64")
	}
}

// TestEncodeAESGCM_EmptyPlaintext verifies that encoding and decoding an empty slice works.
func TestEncodeAESGCM_EmptyPlaintext(t *testing.T) {
	encoded, err := EncodeAESGCM([]byte{}, "some-key")
	if err != nil {
		t.Fatalf("EncodeAESGCM empty: %v", err)
	}

	decoded, recognized, err := DecodeAESGCM(encoded, "some-key")
	if err != nil {
		t.Fatalf("DecodeAESGCM empty: %v", err)
	}
	if !recognized {
		t.Fatal("expected recognized for empty plaintext")
	}
	if len(decoded) != 0 {
		t.Fatalf("expected empty decoded, got %q", string(decoded))
	}
}

// TestEncodeAESGCM_EmptyKey verifies that encoding with an empty key does not panic.
func TestEncodeAESGCM_EmptyKey(t *testing.T) {
	_, err := EncodeAESGCM([]byte("hello"), "")
	if err != nil {
		t.Fatalf("EncodeAESGCM with empty key: %v", err)
	}
}

// TestEncodeAESGCM_KeyWithWhitespace verifies that whitespace around the key is trimmed.
func TestEncodeAESGCM_KeyWithWhitespace(t *testing.T) {
	plaintext := []byte("test data")
	keyWithSpaces := "  my-key  "

	encoded, err := EncodeAESGCM(plaintext, keyWithSpaces)
	if err != nil {
		t.Fatalf("EncodeAESGCM: %v", err)
	}

	decoded, recognized, err := DecodeAESGCM(encoded, "my-key")
	if err != nil {
		t.Fatalf("DecodeAESGCM: %v", err)
	}
	if !recognized {
		t.Fatal("expected recognized with trimmed key")
	}
	if string(decoded) != string(plaintext) {
		t.Fatalf("round-trip mismatch: got %q, want %q", string(decoded), string(plaintext))
	}
}

// TestEncodeAESGCM_DeterministicOutputNotIdentical ensures each encryption produces
// a different ciphertext due to random nonce, but both decrypt to the same plaintext.
func TestEncodeAESGCM_DeterministicOutputNotIdentical(t *testing.T) {
	key := "stable-key"
	plaintext := []byte("data")

	a, err := EncodeAESGCM(plaintext, key)
	if err != nil {
		t.Fatalf("first encode: %v", err)
	}
	b, err := EncodeAESGCM(plaintext, key)
	if err != nil {
		t.Fatalf("second encode: %v", err)
	}

	if string(a) == string(b) {
		t.Fatal("expected different ciphertexts due to random nonce")
	}

	da, _, _ := DecodeAESGCM(a, key)
	db, _, _ := DecodeAESGCM(b, key)
	if string(da) != string(db) {
		t.Fatal("decoded values should be identical")
	}
}

// TestEncodeAESGCM_LargeData verifies correct round-trip for a large payload (10KB).
func TestEncodeAESGCM_LargeData(t *testing.T) {
	key := "test-key"
	large := make([]byte, 10000)
	for i := range large {
		large[i] = byte(i % 256)
	}

	encoded, err := EncodeAESGCM(large, key)
	if err != nil {
		t.Fatalf("EncodeAESGCM large: %v", err)
	}

	decoded, recognized, err := DecodeAESGCM(encoded, key)
	if err != nil {
		t.Fatalf("DecodeAESGCM large: %v", err)
	}
	if !recognized {
		t.Fatal("expected recognized for large data")
	}
	if len(decoded) != len(large) {
		t.Fatalf("length mismatch: got %d, want %d", len(decoded), len(large))
	}
}
