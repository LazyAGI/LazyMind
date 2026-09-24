package credentialvault

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInternalTemporaryCleanupRequiresOwnerToken(t *testing.T) {
	for _, configured := range []string{"", "owner-test-token"} {
		t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", configured)
		for _, supplied := range []string{"", "wrong", "owner-test-token"} {
			request := httptest.NewRequest(http.MethodPost, "/internal/credential-vault/restores:clear-temporary", nil)
			request.Header.Set("X-LazyMind-Internal-Token", supplied)
			response := httptest.NewRecorder()
			var handler *RestoreHandler
			handler.InternalClearTemporaryCredentials(response, request)
			want := http.StatusUnauthorized
			if configured != "" && supplied == configured {
				want = http.StatusNoContent
			}
			if response.Code != want {
				t.Fatalf("configured=%t matching=%t: status=%d, want=%d", configured != "", supplied == configured, response.Code, want)
			}
		}
	}
}

func TestInternalTemporaryCleanupDoesNotCreateRestoreSessions(t *testing.T) {
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "owner-test-token")
	handler, err := NewRestoreHandler(func(string) (*RestoreService, error) { t.Fatal("cleanup created a restore session"); return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	scope := AccountScope{CloudIssuer: "https://cloud.example.com", CloudAccountID: "test-account"}
	sink := &restoreTestSink{temporary: []RestoredCredential{{Provider: ProviderCredential{APIKey: []byte("temporary")}}}}
	call := func() {
		request := httptest.NewRequest(http.MethodPost, "/internal/credential-vault/restores:clear-temporary", nil)
		request.Header.Set("X-LazyMind-Internal-Token", "owner-test-token")
		response := httptest.NewRecorder()
		handler.InternalClearTemporaryCredentials(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("status=%d", response.Code)
		}
	}
	call()
	if len(handler.services) != 0 {
		t.Fatal("cleanup initialized a session")
	}
	handler.services["local-user"] = &RestoreService{deps: RestoreServiceDeps{Sink: sink}, temporaryScopes: map[AccountScope]struct{}{scope: {}}}
	call()
	call()
	if sink.clearCalls != 1 || len(sink.temporary) != 0 {
		t.Fatalf("cleanup calls=%d remaining=%d", sink.clearCalls, len(sink.temporary))
	}
}
