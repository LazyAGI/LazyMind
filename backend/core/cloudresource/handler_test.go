package cloudresource

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerSignedOutReturnsUnauthorizedWithoutTokenDetails(t *testing.T) {
	service := &Service{Session: fakeSession{err: errors.New("refresh secret-canary missing")}, Cloud: &fakeCloud{}, Bindings: &fakeBindings{}}
	handler := Handler{Service: service, ResourceType: "skill", Adapter: &fakeAdapter{}}
	request := httptest.NewRequest(http.MethodGet, "/cloud/skills", nil)
	request.Header.Set("X-User-Id", "local-user")
	recorder := httptest.NewRecorder()

	handler.List(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); strings.Contains(body, "secret-canary") {
		t.Fatalf("response leaked session error: %s", body)
	}
}
