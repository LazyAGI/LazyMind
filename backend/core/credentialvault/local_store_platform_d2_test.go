package credentialvault

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCredentialVaultLocalKeyStoreUsesNativeOSCredentialStorageAndNoFileFallback(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate Credential Vault platform tests")
	}
	directory := filepath.Dir(file)
	files := map[string][]string{
		"local_store_darwin.go":      {"framework security", "secitemcopymatching", "secitemadd"},
		"local_store_windows.go":     {"credreadw", "credwritew", "creddeletew"},
		"local_store_unsupported.go": {"errlocalsecurestoreunavailable"},
	}
	for name, required := range files {
		payload, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Errorf("Credential Vault OS key-store implementation %s is absent: %v", name, err)
			continue
		}
		source := strings.ToLower(string(payload))
		for _, marker := range required {
			if !strings.Contains(source, marker) {
				t.Errorf("%s omitted native secure-store marker %q", name, marker)
			}
		}
		for _, forbidden := range []string{"os.writefile", "os.readfile", "model_provider_secret_key", "default-secret", "private_key.pem"} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s contains forbidden fallback marker %q", name, forbidden)
			}
		}
	}
}
