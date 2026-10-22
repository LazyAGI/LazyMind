package credentialvault

import (
	"bytes"
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
