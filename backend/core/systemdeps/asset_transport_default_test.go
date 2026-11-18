package systemdeps

import "testing"

func TestAssetHTTPEnvironmentCannotEnableTestTransport(t *testing.T) {
	t.Setenv("LAZYMIND_DESKTOP_ALLOW_UNSIGNED_UPDATE_TEST", "true")
	for _, address := range []string{"http://127.0.0.1:18765/rag.zip", "http://localhost/rag.zip", "http://example.invalid/rag.zip"} {
		if validateAssetURL(address) == nil {
			t.Fatal("ordinary build accepted HTTP", address)
		}
	}
	if err := validateAssetURL("https://example.invalid/rag.zip"); err != nil {
		t.Fatal(err)
	}
}
