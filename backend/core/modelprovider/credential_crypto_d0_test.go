package modelprovider

import "testing"

func TestModelProviderEncryptionKeyHasNoBuiltInFallback(t *testing.T) {
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "")
	if got := modelProviderEncryptionKey(); got != "" {
		t.Fatalf("model provider encryption key falls back to a predictable built-in value: %q", got)
	}
}
