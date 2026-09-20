package cloudclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func TestDesktopHandoffBuildsAuthorizationURLAndExchangesCode(t *testing.T) {
	expiresAt := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	accessToken := fixtureJWT(expiresAt)
	calls := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodPost || request.URL.Path != "/v1/desktop-auth/token" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Fatalf("Desktop exchange leaked ambient credentials: %v", request.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"grant_type": "authorization_code", "client_id": "lazymind-desktop",
			"code": "fixture-code", "code_verifier": "fixture-verifier",
			"redirect_uri": "http://127.0.0.1:49152/cloud-auth/callback",
		}
		if !reflect.DeepEqual(body, want) {
			t.Fatalf("exchange body=%v want=%v", body, want)
		}
		return jsonResponse(http.StatusOK, fmt.Sprintf(`{"access_token":%q,"refresh_token":"fixture-refresh","session_id":"session-1","reauthenticated_at":null}`, accessToken), nil), nil
	})}
	client, err := New("http://127.0.0.1:8080", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := client.BeginDesktopAuthorization(context.Background(), DesktopAuthorizationRequest{
		RedirectURI: "http://127.0.0.1:49152/cloud-auth/callback",
		State:       "fixture-state", CodeChallenge: "K0gU2qf7R3xd8BtW7PpW3EVSP4qdK3hjijR8Jw9I6Ns", Locale: "zh",
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorization.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "http" || parsed.Host != "127.0.0.1:8080" || parsed.Path != "/zh/desktop/authorize" {
		t.Fatalf("authorization URL=%s", authorization.AuthorizationURL)
	}
	wantQuery := map[string]string{
		"client_id": "lazymind-desktop", "redirect_uri": "http://127.0.0.1:49152/cloud-auth/callback",
		"state": "fixture-state", "code_challenge": "K0gU2qf7R3xd8BtW7PpW3EVSP4qdK3hjijR8Jw9I6Ns",
		"code_challenge_method": "S256",
	}
	for key, want := range wantQuery {
		if got := parsed.Query().Get(key); got != want {
			t.Errorf("query %s=%q want=%q", key, got, want)
		}
	}
	if calls != 0 {
		t.Fatalf("building the browser URL made %d HTTP calls", calls)
	}

	tokens, err := client.ExchangeDesktopCode(context.Background(), DesktopCodeExchange{
		Code: "fixture-code", CodeVerifier: "fixture-verifier",
		RedirectURI: "http://127.0.0.1:49152/cloud-auth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tokens.RefreshToken != "fixture-refresh" || calls != 1 {
		t.Fatalf("tokens=%+v calls=%d", tokens, calls)
	}
}

func TestDesktopHandoffRejectsUnsafeLoopbackAndLocaleInputs(t *testing.T) {
	client, err := New("https://cloud.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []DesktopAuthorizationRequest{
		{RedirectURI: "https://127.0.0.1:49152/cloud-auth/callback", State: "state", CodeChallenge: "K0gU2qf7R3xd8BtW7PpW3EVSP4qdK3hjijR8Jw9I6Ns", Locale: "zh"},
		{RedirectURI: "http://localhost:49152/cloud-auth/callback", State: "state", CodeChallenge: "K0gU2qf7R3xd8BtW7PpW3EVSP4qdK3hjijR8Jw9I6Ns", Locale: "zh"},
		{RedirectURI: "http://127.0.0.1:49152/other", State: "state", CodeChallenge: "K0gU2qf7R3xd8BtW7PpW3EVSP4qdK3hjijR8Jw9I6Ns", Locale: "zh"},
		{RedirectURI: "http://127.0.0.1:49152/cloud-auth/callback", State: "state", CodeChallenge: "K0gU2qf7R3xd8BtW7PpW3EVSP4qdK3hjijR8Jw9I6Ns", Locale: "fr"},
	} {
		if _, err := client.BeginDesktopAuthorization(context.Background(), request); err == nil {
			t.Errorf("unsafe request accepted: %+v", request)
		}
	}
}
