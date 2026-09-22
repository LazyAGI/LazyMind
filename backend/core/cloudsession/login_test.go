package cloudsession

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type fakeDesktopHandoff struct {
	begin         DesktopAuthorizationRequest
	exchange      DesktopCodeExchange
	exchangeCalls int
	pair          TokenPair
	url           string
}

func TestCallbackHTMLClosesScriptOpenedWindowWithStrictBoundaries(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeCallbackHTML(recorder, http.StatusOK, "Authorization completed")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	for _, required := range []string{"window.close()", "window.opener.focus()", "frame-ancestors 'none'", "base-uri 'none'", "form-action 'none'"} {
		value := recorder.Body.String() + recorder.Header().Get("Content-Security-Policy") + recorder.Header().Get("Cache-Control")
		if !strings.Contains(value, required) {
			t.Errorf("callback response omitted %q", required)
		}
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("callback Cache-Control=%q", recorder.Header().Get("Cache-Control"))
	}
	if !recorder.Flushed || recorder.Result().ContentLength != int64(recorder.Body.Len()) {
		t.Fatal("callback must flush its complete response before closing the server")
	}
	for _, forbidden := range []string{"access_token", "refresh_token", "code_verifier"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Errorf("callback response exposed %q", forbidden)
		}
	}
}

func TestCallbackHTMLErrorRemainsVisible(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeCallbackHTML(recorder, http.StatusBadRequest, "Authorization was rejected")
	if strings.Contains(recorder.Body.String(), "window.close()") {
		t.Fatal("failed authorization closed before the user could read the error")
	}
}

func (f *fakeDesktopHandoff) BeginDesktopAuthorization(_ context.Context, request DesktopAuthorizationRequest) (DesktopAuthorization, error) {
	f.begin = request
	return DesktopAuthorization{AuthorizationURL: f.url, TransactionID: "transaction-1"}, nil
}

func (f *fakeDesktopHandoff) ExchangeDesktopCode(_ context.Context, exchange DesktopCodeExchange) (TokenPair, error) {
	f.exchange = exchange
	f.exchangeCalls++
	return f.pair, nil
}

func TestLoginCoordinatorCompletesOneLoopbackPKCEExchange(t *testing.T) {
	store := &fakeSecureStore{}
	session := NewService(ServiceDeps{Store: store})
	handoff := &fakeDesktopHandoff{
		url:  "https://cloud.example/zh/desktop/authorize?client_id=lazymind-desktop",
		pair: TokenPair{AccessToken: "access", RefreshToken: "refresh", AccessExpiresAt: time.Now().Add(time.Minute)},
	}
	coordinator, err := NewLoginCoordinator(LoginCoordinatorDeps{
		Session: session, Handoff: handoff, CloudOrigin: "https://cloud.example", AttemptTTL: time.Minute,
		AuthorizationPath: "/zh/desktop/authorize",
		Random:            strings.NewReader(strings.Repeat("a", 64)),
	})
	if err != nil {
		t.Fatal(err)
	}
	start, err := coordinator.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if start.AuthorizationURL != handoff.url || session.Status(context.Background()).State != StateAuthorizing {
		t.Fatalf("start=%+v status=%+v", start, session.Status(context.Background()))
	}
	badResponse, err := http.Get(handoff.begin.CallbackURL + "?state=wrong&code=code-1")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, badResponse.Body)
	_ = badResponse.Body.Close()
	if badResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad callback status=%d", badResponse.StatusCode)
	}
	callback, err := url.Parse(handoff.begin.CallbackURL)
	if err != nil {
		t.Fatal(err)
	}
	query := callback.Query()
	query.Set("state", handoff.begin.State)
	query.Set("code", "code-1")
	callback.RawQuery = query.Encode()
	response, err := http.Get(callback.String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status=%d", response.StatusCode)
	}
	challenge := sha256.Sum256([]byte(handoff.exchange.CodeVerifier))
	if handoff.exchange.Code != "code-1" || handoff.exchange.CallbackURL != handoff.begin.CallbackURL ||
		base64.RawURLEncoding.EncodeToString(challenge[:]) != handoff.begin.CodeChallenge {
		t.Fatalf("begin=%+v exchange=%+v", handoff.begin, handoff.exchange)
	}
	if store.token != "refresh" || session.Status(context.Background()).State != StateSignedIn {
		t.Fatalf("store=%q status=%+v", store.token, session.Status(context.Background()))
	}
}

func TestLoginCoordinatorAdvertisesLoopbackForConfiguredReachableListener(t *testing.T) {
	store := &fakeSecureStore{}
	session := NewService(ServiceDeps{Store: store})
	handoff := &fakeDesktopHandoff{
		url:  "https://cloud.example/zh/desktop/authorize?client_id=lazymind-desktop",
		pair: TokenPair{AccessToken: "access", RefreshToken: "refresh", AccessExpiresAt: time.Now().Add(time.Minute)},
	}
	coordinator, err := NewLoginCoordinator(LoginCoordinatorDeps{
		Session: session, Handoff: handoff, CloudOrigin: "https://cloud.example", AttemptTTL: time.Minute,
		AuthorizationPath:     "/zh/desktop/authorize",
		CallbackListenAddress: "0.0.0.0:0",
		Random:                strings.NewReader(strings.Repeat("a", 64)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(coordinator.Cancel)

	callback, err := url.Parse(handoff.begin.CallbackURL)
	if err != nil {
		t.Fatal(err)
	}
	if callback.Scheme != "http" || callback.Hostname() != "127.0.0.1" || callback.Port() == "" || callback.Path != "/cloud-auth/callback" {
		t.Fatalf("callback is not the strict Cloud loopback contract: %s://%s%s", callback.Scheme, callback.Host, callback.Path)
	}
	coordinator.mu.Lock()
	listenHost, _, splitErr := net.SplitHostPort(coordinator.active.listener.Addr().String())
	coordinator.mu.Unlock()
	if splitErr != nil || listenHost != "0.0.0.0" {
		t.Fatalf("configured listener host=%q err=%v", listenHost, splitErr)
	}

	query := callback.Query()
	query.Set("state", handoff.begin.State)
	query.Set("code", "code-1")
	callback.RawQuery = query.Encode()
	response, err := http.Get(callback.String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status=%d", response.StatusCode)
	}
	if store.token != "refresh" || session.Status(context.Background()).State != StateSignedIn {
		t.Fatalf("store=%q status=%+v", store.token, session.Status(context.Background()))
	}
}

func TestLoginCoordinatorRejectsNonLocalCallbackListener(t *testing.T) {
	_, err := NewLoginCoordinator(LoginCoordinatorDeps{
		Session:               NewService(ServiceDeps{Store: &fakeSecureStore{}}),
		Handoff:               &fakeDesktopHandoff{url: "https://cloud.example/zh/desktop/authorize"},
		CloudOrigin:           "https://cloud.example",
		AuthorizationPath:     "/zh/desktop/authorize",
		CallbackListenAddress: "198.51.100.20:18081",
	})
	if err == nil {
		t.Fatal("non-local callback listener was accepted")
	}
}

func TestLoginCoordinatorTreatsBrowserAccessDeniedAsCancellation(t *testing.T) {
	store := &fakeSecureStore{}
	session := NewService(ServiceDeps{Store: store})
	session.setState(StateOffline)
	handoff := &fakeDesktopHandoff{
		url:  "https://cloud.example/zh/desktop/authorize?client_id=lazymind-desktop",
		pair: TokenPair{AccessToken: "must-not-be-used", RefreshToken: "must-not-be-saved", AccessExpiresAt: time.Now().Add(time.Minute)},
	}
	reportedErrors := 0
	coordinator, err := NewLoginCoordinator(LoginCoordinatorDeps{
		Session: session, Handoff: handoff, CloudOrigin: "https://cloud.example", AttemptTTL: time.Minute,
		AuthorizationPath: "/zh/desktop/authorize",
		Random:            strings.NewReader(strings.Repeat("a", 64)),
		ReportError:       func(error) { reportedErrors++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(coordinator.Cancel)
	coordinator.mu.Lock()
	attempt := coordinator.active
	coordinator.mu.Unlock()

	callback, err := url.Parse(handoff.begin.CallbackURL)
	if err != nil {
		t.Fatal(err)
	}
	query := callback.Query()
	query.Set("state", handoff.begin.State)
	query.Set("error", "access_denied")
	callback.RawQuery = query.Encode()
	response, err := http.Get(callback.String())
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatalf("callback response was truncated: %v", readErr)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("cancel callback status=%d body=%q", response.StatusCode, body)
	}
	if handoff.exchangeCalls != 0 || handoff.exchange.Code != "" {
		t.Fatalf("cancel exchanged a code: calls=%d exchange=%+v", handoff.exchangeCalls, handoff.exchange)
	}
	if store.token != "" {
		t.Fatalf("cancel saved a refresh token: %q", store.token)
	}
	if got := session.Status(context.Background()).State; got != StateOffline {
		t.Fatalf("state=%q want previous state %q", got, StateOffline)
	}
	if reportedErrors != 0 {
		t.Fatalf("user cancellation reported %d errors", reportedErrors)
	}

	replay := httptest.NewRecorder()
	replayRequest := httptest.NewRequest(http.MethodGet, "/cloud-auth/callback?state="+url.QueryEscape(handoff.begin.State)+"&code=late-code", nil)
	coordinator.callback(attempt, replay, replayRequest)
	if replay.Code != http.StatusConflict {
		t.Fatalf("callback after cancellation status=%d want=%d", replay.Code, http.StatusConflict)
	}
	if handoff.exchangeCalls != 0 {
		t.Fatalf("callback after cancellation exchanged %d codes", handoff.exchangeCalls)
	}
}

func TestLoginCoordinatorRejectsAmbiguousOrInvalidBrowserDenial(t *testing.T) {
	tests := []struct {
		name  string
		query func(state string) url.Values
	}{
		{
			name: "code and error together",
			query: func(state string) url.Values {
				return url.Values{"state": {state}, "code": {"code-1"}, "error": {"access_denied"}}
			},
		},
		{
			name: "unknown error",
			query: func(state string) url.Values {
				return url.Values{"state": {state}, "error": {"server_error"}}
			},
		},
		{
			name: "duplicate error",
			query: func(state string) url.Values {
				return url.Values{"state": {state}, "error": {"access_denied", "access_denied"}}
			},
		},
		{
			name: "missing state",
			query: func(string) url.Values {
				return url.Values{"error": {"access_denied"}}
			},
		},
		{
			name: "wrong state",
			query: func(string) url.Values {
				return url.Values{"state": {"wrong"}, "error": {"access_denied"}}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := NewService(ServiceDeps{Store: &fakeSecureStore{}})
			handoff := &fakeDesktopHandoff{
				url:  "https://cloud.example/zh/desktop/authorize?client_id=lazymind-desktop",
				pair: TokenPair{AccessToken: "access", RefreshToken: "refresh", AccessExpiresAt: time.Now().Add(time.Minute)},
			}
			coordinator, err := NewLoginCoordinator(LoginCoordinatorDeps{
				Session: session, Handoff: handoff, CloudOrigin: "https://cloud.example", AttemptTTL: time.Minute,
				AuthorizationPath: "/zh/desktop/authorize",
				Random:            strings.NewReader(strings.Repeat("a", 64)),
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := coordinator.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(coordinator.Cancel)

			callback, err := url.Parse(handoff.begin.CallbackURL)
			if err != nil {
				t.Fatal(err)
			}
			callback.RawQuery = test.query(handoff.begin.State).Encode()
			response, err := http.Get(callback.String())
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d want=%d", response.StatusCode, http.StatusBadRequest)
			}
			if handoff.exchangeCalls != 0 {
				t.Fatalf("invalid denial exchanged %d codes", handoff.exchangeCalls)
			}
			if got := session.Status(context.Background()).State; got != StateAuthorizing {
				t.Fatalf("state=%q want=%q", got, StateAuthorizing)
			}
		})
	}
}

func TestLoginCoordinatorRejectsUntrustedAuthorizationURL(t *testing.T) {
	coordinator, err := NewLoginCoordinator(LoginCoordinatorDeps{
		Session:     NewService(ServiceDeps{Store: &fakeSecureStore{}}),
		Handoff:     &fakeDesktopHandoff{url: "https://evil.example/zh/desktop/authorize"},
		CloudOrigin: "https://cloud.example", Random: strings.NewReader(strings.Repeat("a", 64)),
		AuthorizationPath: "/zh/desktop/authorize",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Start(context.Background()); err == nil {
		t.Fatal("untrusted authorization URL was accepted")
	}
}

func TestLoginCoordinatorAllowsExplicitLoopbackHTTPCloudInLocalDevelopment(t *testing.T) {
	coordinator, err := NewLoginCoordinator(LoginCoordinatorDeps{
		Session:     NewService(ServiceDeps{Store: &fakeSecureStore{}}),
		Handoff:     &fakeDesktopHandoff{url: "http://127.0.0.1:8080/zh/desktop/authorize?client_id=lazymind-desktop"},
		CloudOrigin: "http://127.0.0.1:8080", AuthorizationPath: "/zh/desktop/authorize",
		Random: strings.NewReader(strings.Repeat("a", 64)),
	})
	if err != nil {
		t.Fatal(err)
	}
	start, err := coordinator.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	coordinator.Cancel()
	if start.AuthorizationURL == "" {
		t.Fatal("local Cloud login returned no authorization URL")
	}
}
