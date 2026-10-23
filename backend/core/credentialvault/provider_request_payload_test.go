package credentialvault

import "testing"

func TestProviderRequestConfigSurvivesEncryptedPayloadRoundTrip(t *testing.T) {
	config := `{"default_protocol":"anthropic","endpoints":{"openai":"https://proxy.example/v1","anthropic":"https://proxy.example"},"defaults":{"openai":"https://default.example/v1","anthropic":"https://default.example"}}`
	original := ProviderCredential{ProviderCatalogKey: "Claude", DisplayName: "Gateway", BaseURL: "https://proxy.example", APIKey: []byte("saved-key"), RequestConfig: config}
	payload, err := EncodeProviderCredentialPayload(original)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeRestoredProvider(payload)
	if err != nil {
		t.Fatal(err)
	}
	if restored.RequestConfig != config {
		t.Fatal("backup discarded inactive protocol endpoints or selected default")
	}
	for _, invalid := range []string{`{"default_protocol":"gemini","endpoints":{},"defaults":{}}`, `{"default_protocol":"openai","endpoints":{"openai":"https://user:secret@example.com"},"defaults":{}}`, `{"default_protocol":"openai","endpoints":{},"defaults":{},"unknown":true}`} {
		original.RequestConfig = invalid
		if _, err := EncodeProviderCredentialPayload(original); err == nil {
			t.Fatal("invalid request configuration accepted into backup")
		}
	}
}
