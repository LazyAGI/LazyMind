package credentialvault

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestProviderPayloadRoundTripSupportsMultipleSavedKeys(t *testing.T) {
	pool := strings.Repeat("a", 300) + "\n" + strings.Repeat("b", 300)
	original := ProviderCredential{ProviderCatalogKey: "OpenAI", DisplayName: "Personal", BaseURL: "https://api.openai.com/v1/", APIKey: []byte(pool)}
	encoded, err := EncodeProviderCredentialPayload(original)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(encoded)
	decoded, err := decodeRestoredProvider(encoded)
	if err != nil {
		t.Fatal("valid combined pool >512 could not restore", err)
	}
	defer clear(decoded.APIKey)
	if !bytes.Equal(decoded.APIKey, original.APIKey) {
		t.Fatal("roundtrip lost saved keys")
	}
}

func TestEncryptedProviderPayloadRetainsSelectionAndLegacyCompatibility(t *testing.T) {
	for _, state := range []string{"second", "disabled", "legacy"} {
		t.Run(state, func(t *testing.T) {
			index := 1
			original := ProviderCredential{ProviderCatalogKey: "OpenAI", DisplayName: "Personal", BaseURL: "https://api.example/v1", APIKey: []byte("first\n\n second \n"), ActiveKeyIndex: &index}
			if state == "disabled" {
				index = -1
			} else if state == "legacy" {
				original.ActiveKeyIndex = nil
			}
			encoded, err := EncodeProviderCredentialPayload(original)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(encoded)
			key, aad := bytes.Repeat([]byte{0x3a}, 32), []byte("selection-test")
			nonce, ciphertext, err := EncryptPayload(rand.Reader, key, aad, encoded)
			if err != nil {
				t.Fatal(err)
			}
			plaintext, err := DecryptPayload(key, nonce, aad, ciphertext)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(plaintext)
			decoded, err := decodeRestoredProvider(plaintext)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(decoded.APIKey)
			if state == "legacy" {
				if decoded.ActiveKeyIndex != nil {
					t.Fatal("legacy payload acquired an explicit selection")
				}
			} else if decoded.ActiveKeyIndex == nil || *decoded.ActiveKeyIndex != index {
				t.Fatal("encrypted backup lost its selection")
			}
		})
	}
}

func TestRestoredProviderRejectsInvalidSelectionMetadata(t *testing.T) {
	for _, raw := range []string{"null", "-2", "2", "1.5", `"1"`, `{}`, `true`} {
		payload, err := json.Marshal(map[string]any{"schema_version": 1, "provider_catalog_key": "OpenAI", "display_name": "Test", "base_url": "https://api.example/v1", "credential_type": "api_key", "api_key": "first\nsecond", "provider_specific_fields": map[string]json.RawMessage{"active_key_index": json.RawMessage(raw)}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeRestoredProvider(payload); !errors.Is(err, ErrInvalidContract) {
			t.Fatal("invalid selection accepted")
		}
	}
	for _, index := range []int{-2, 2} {
		if _, err := EncodeProviderCredentialPayload(ProviderCredential{APIKey: []byte("first\nsecond"), ActiveKeyIndex: &index}); !errors.Is(err, ErrInvalidContract) {
			t.Fatal("invalid selection encoded")
		}
	}
}

func TestProviderPayloadBoundsEachKeyAndCompleteRecord(t *testing.T) {
	for _, pool := range []string{strings.Repeat("a", MaximumAPIKeyLength+1), strings.Repeat(strings.Repeat("a", 512)+"\n", 33)} {
		if _, err := EncodeProviderCredentialPayload(ProviderCredential{APIKey: []byte(pool)}); !errors.Is(err, ErrInvalidContract) {
			t.Fatal("invalid pool accepted")
		}
	}
	// Quotes occupy more JSON payload bytes than the key-pool byte count.
	pool := strings.Repeat(strings.Repeat("\"", 500)+"\n", 20)
	if !ValidAPIKeyPool([]byte(pool)) {
		t.Fatal("fixture should meet per-key/raw-pool bounds")
	}
	if _, err := EncodeProviderCredentialPayload(ProviderCredential{APIKey: []byte(pool)}); !errors.Is(err, ErrInvalidContract) {
		t.Fatal("oversized serialized record accepted")
	}
}
