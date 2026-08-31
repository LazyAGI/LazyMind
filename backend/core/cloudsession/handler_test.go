package cloudsession

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/cloudclient"
)

type handlerAccountClient struct{}

func (handlerAccountClient) GetCurrentAccount(context.Context, string) (cloudclient.Account, error) {
	return cloudclient.Account{ID: "account-1", Username: "fixture", EmailMasked: "f***@example.com"}, nil
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

func TestSessionHandlerReturnsMinimalAccountWithoutTokens(t *testing.T) {
	service := NewService(ServiceDeps{Store: &fakeSecureStore{token: "refresh"}, Auth: &fakeAuthClient{}})
	if err := service.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	Handler{Service: service, Accounts: handlerAccountClient{}}.Get(recorder, httptest.NewRequest(http.MethodGet, "/cloud/session", nil))
	if !strings.Contains(recorder.Body.String(), `"username":"fixture"`) || strings.Contains(recorder.Body.String(), "new-access") {
		t.Fatalf("unsafe account response: %s", recorder.Body.String())
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
