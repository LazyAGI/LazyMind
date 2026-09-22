package modelprovider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
)

func TestV2CredentialCryptoKeepsLegacyKeyLookupSeparate(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate model-provider D2 test")
	}
	payload, err := os.ReadFile(filepath.Join(filepath.Dir(file), "credential_crypto.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := strings.ToLower(string(payload))
	for _, forbidden := range []string{
		"lazymind_model_provider_secret_key",
		"lazymind-core-model-provider-default-secret",
		"os.getenv",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("runtime provider credential crypto still contains forbidden key source %q", forbidden)
		}
	}
}

func TestV2CredentialsDoNotDowngradeWithoutOSProtectedRootKey(t *testing.T) {
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "available-legacy-test-key")
	db, provider, group := setupEncryptedGroupKeyTest(t, "existing-v2-key")
	restore := SetCredentialKeyManager(nil)
	t.Cleanup(restore)
	if _, err := apiKeyForGroup(db, &group); !errors.Is(err, credentialvault.ErrLocalSecureStoreUnavailable) {
		t.Fatalf("V2 read did not fail closed: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/keys", strings.NewReader(`{"api_key":"replacement-key"}`))
	request.Header.Set("X-User-Id", "user-1")
	request = mux.SetURLVars(request, map[string]string{"model_provider_id": provider.ID, "group_id": group.ID})
	recorder := httptest.NewRecorder()
	AddKey(recorder, request)
	if recorder.Code < 400 {
		t.Fatal("V2 credentials were silently downgraded")
	}
	var stored orm.UserModelProviderGroup
	if err := db.First(&stored, "id = ?", group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CredentialVersion != 2 || stored.APIKeyCiphertext != group.APIKeyCiphertext || stored.APIKey != "" {
		t.Fatal("failed V2 update changed the stored credential")
	}
}
