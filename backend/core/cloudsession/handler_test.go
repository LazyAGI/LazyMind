package cloudsession

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type logoutTestStore struct {
	fakeSecureStore
	loadErr     error
	deleteErr   error
	deleteCalls int
}

func (store *logoutTestStore) Load(ctx context.Context) (RefreshToken, error) {
	if store.loadErr != nil {
		return "", store.loadErr
	}
	return store.fakeSecureStore.Load(ctx)
}

func (store *logoutTestStore) Delete(ctx context.Context) error {
	store.deleteCalls++
	if store.deleteErr != nil {
		return store.deleteErr
	}
	return store.fakeSecureStore.Delete(ctx)
}

type logoutTestAuth struct {
	fakeAuthClient
	err   error
	calls int
}

func (auth *logoutTestAuth) Logout(context.Context, string, RefreshToken) error {
	auth.calls++
	return auth.err
}

type logoutTestCleaner struct {
	err   error
	calls int
}

func (cleaner *logoutTestCleaner) ClearTemporary(context.Context) error {
	cleaner.calls++
	return cleaner.err
}

func TestLogoutDistinguishesLocalCleanupFromRemoteFailure(t *testing.T) {
	localFailure := errors.New("private-storage-error-canary")
	remoteFailure := errors.New("private-network-error-canary")
	for _, test := range []struct {
		name                                     string
		localErr, remoteErr, loadErr, cleanupErr error
		wantStatus                               int
	}{
		{name: "successful logout", wantStatus: http.StatusOK},
		{name: "Cloud unavailable but local deletion succeeds", remoteErr: remoteFailure, wantStatus: http.StatusOK},
		{name: "local deletion fails", localErr: localFailure, wantStatus: http.StatusServiceUnavailable},
		{name: "both remote and local fail", localErr: localFailure, remoteErr: remoteFailure, wantStatus: http.StatusServiceUnavailable},
		{name: "unreadable token can still be deleted", loadErr: localFailure, wantStatus: http.StatusOK},
		{name: "temporary cleanup fails but token is still deleted", cleanupErr: localFailure, wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &logoutTestStore{loadErr: test.loadErr, deleteErr: test.localErr}
			auth := &logoutTestAuth{err: test.remoteErr}
			cleaner := &logoutTestCleaner{err: test.cleanupErr}
			service := NewService(ServiceDeps{Store: store, Auth: auth})
			if err := service.Establish(context.Background(), TokenPair{AccessToken: "access-canary", RefreshToken: "refresh-canary", AccessExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			Handler{Service: service, TemporaryCredentials: cleaner}.Logout(recorder, httptest.NewRequest(http.MethodPost, "/cloud/logout", nil))
			if recorder.Code != test.wantStatus {
				t.Errorf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
			if strings.Contains(recorder.Body.String(), "canary") {
				t.Error("logout exposed credentials or internal error details")
			}
			if store.deleteCalls != 1 || cleaner.calls != 1 {
				t.Error("logout skipped local cleanup")
			}
			if test.localErr == nil && store.token != "" {
				t.Error("refresh token was not removed")
			}
			if service.accessToken != "" || service.Status(context.Background()).State != StateSignedOut || service.CloudBusinessAvailable() {
				t.Error("logout kept Cloud credentials usable in memory")
			}
			if test.loadErr != nil && auth.calls != 0 {
				t.Error("logout tried a remote call without a readable refresh token")
			}
		})
	}
}

func TestLogoutCleanupCanBeRetriedAndCannotRestoreAfterSuccess(t *testing.T) {
	store := &logoutTestStore{deleteErr: errors.New("temporary storage failure")}
	auth := &logoutTestAuth{err: errors.New("Cloud is offline")}
	service := NewService(ServiceDeps{Store: store, Auth: auth})
	if err := service.Establish(context.Background(), TokenPair{AccessToken: "access", RefreshToken: "refresh", AccessExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	handler := Handler{Service: service}
	first := httptest.NewRecorder()
	handler.Logout(first, httptest.NewRequest(http.MethodPost, "/cloud/logout", nil))
	if first.Code != http.StatusServiceUnavailable || store.token == "" {
		t.Fatal("failed local deletion was not reported")
	}
	store.deleteErr = nil
	second := httptest.NewRecorder()
	handler.Logout(second, httptest.NewRequest(http.MethodPost, "/cloud/logout", nil))
	if second.Code != http.StatusOK || store.token != "" {
		t.Fatal("local logout retry failed")
	}
	restarted := NewService(ServiceDeps{Store: store, Auth: auth})
	if err := restarted.Restore(context.Background()); !errors.Is(err, ErrNoRefreshToken) {
		t.Fatalf("successful logout left a restorable session: %v", err)
	}
	if auth.refreshCalls != 0 || restarted.CloudBusinessAvailable() {
		t.Fatal("restart reauthenticated after successful logout")
	}
}

func TestSessionHandlerDefaultsToSignedOutWithoutExposingToken(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/cloud/session", nil)
	Handler{}.Get(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "access_token") {
		t.Fatalf("session response exposed token field: %s", recorder.Body.String())
	}
	var response struct {
		Data Status `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.State != StateSignedOut {
		t.Fatalf("state = %q", response.Data.State)
	}
}

func TestSessionLogoutIsIdempotentBeforeConfiguration(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/cloud/logout", nil)
	Handler{Service: NewService(ServiceDeps{})}.Logout(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), string(StateSignedOut)) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestBeginLoginReportsUnavailableWithoutHandoffContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	Handler{Service: NewService(ServiceDeps{})}.BeginLogin(recorder, httptest.NewRequest(http.MethodPost, "/cloud/login", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSessionHandlerUsesOnlyTheLocalSnapshot(t *testing.T) {
	now := time.Now()
	auth := &fakeAuthClient{}
	service := NewService(ServiceDeps{Store: &fakeSecureStore{token: "refresh"}, Auth: auth, Now: func() time.Time { return now }})
	if err := service.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour) // Even an expired token must not trigger refresh during GET.
	recorder := httptest.NewRecorder()
	Handler{Service: service}.Get(recorder, httptest.NewRequest(http.MethodGet, "/cloud/session", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if auth.refreshCalls != 1 {
		t.Fatalf("session status refreshed Cloud credentials: %d calls", auth.refreshCalls)
	}
	if strings.Contains(recorder.Body.String(), "new-access") {
		t.Fatalf("session response exposed access-token material: %s", recorder.Body.String())
	}
}

func TestSessionHandlerReportsConfigurationAndReachability(t *testing.T) {
	recorder := httptest.NewRecorder()
	Handler{}.Get(recorder, httptest.NewRequest(http.MethodGet, "/cloud/session", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	configured, configuredPresent := response.Data["configured"].(bool)
	reachability, reachabilityPresent := response.Data["reachability"].(string)
	if !configuredPresent || configured {
		t.Fatalf("configured=%v present=%v data=%#v", configured, configuredPresent, response.Data)
	}
	if !reachabilityPresent || reachability != "unknown" {
		t.Fatalf("reachability=%q present=%v data=%#v", reachability, reachabilityPresent, response.Data)
	}
}

func TestSessionHandlerReturnsFixedRegistrationURLWithoutTokens(t *testing.T) {
	recorder := httptest.NewRecorder()
	Handler{
		Service:         NewService(ServiceDeps{}),
		RegistrationURL: "http://127.0.0.1:8080/zh/register",
	}.Get(recorder, httptest.NewRequest(http.MethodGet, "/cloud/session", nil))
	if !strings.Contains(recorder.Body.String(), `"registration_url":"http://127.0.0.1:8080/zh/register"`) {
		t.Fatalf("registration URL missing: %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "token") {
		t.Fatalf("session response exposed token material: %s", recorder.Body.String())
	}
}

func TestCloudLogoutClearsTemporaryRestoredCredentialsEvenWhenSessionIsAlreadySignedOut(t *testing.T) {
	cleaner := &logoutTestCleaner{}
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
