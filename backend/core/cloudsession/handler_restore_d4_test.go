package cloudsession

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type temporaryCredentialCleanerFake struct{ calls int }

func (cleaner *temporaryCredentialCleanerFake) ClearTemporary(context.Context) error {
	cleaner.calls++
	return nil
}

func TestCloudLogoutClearsTemporaryRestoredCredentialsEvenWhenSessionIsAlreadySignedOut(t *testing.T) {
	cleaner := &temporaryCredentialCleanerFake{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/cloud/logout", nil)
	Handler{Service: NewService(ServiceDeps{}), TemporaryCredentials: cleaner}.Logout(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("logout status = %d", recorder.Code)
	}
	if cleaner.calls != 1 {
		t.Fatalf("temporary credential cleanup calls = %d, want 1", cleaner.calls)
	}
}
