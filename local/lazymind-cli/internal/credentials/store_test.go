package credentials

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestRuntimeServerCandidatesPreferConfiguredServer(t *testing.T) {
	t.Setenv("LAZYMIND_SERVER_URL", "http://127.0.0.1:18090/")
	candidates := runtimeServerCandidates()
	if len(candidates) == 0 || candidates[0] != "http://127.0.0.1:18090" {
		t.Fatalf("candidates=%#v", candidates)
	}
}

func TestSaveAndClearSession(t *testing.T) {
	home := t.TempDir()
	store, err := NewStore(home, "")
	if err != nil {
		t.Fatal(err)
	}
	value := Credentials{
		ServerURL: "http://127.0.0.1:8090/", AccessToken: "access", RefreshToken: "refresh",
	}
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(home, credentialFile))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("saved credential permissions=%o", info.Mode().Perm())
	}
	loaded, err := store.loadUnlocked()
	if err != nil || loaded.ServerURL != "http://127.0.0.1:8090" || loaded.AccessToken != "access" {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, credentialFile)); !os.IsNotExist(err) {
		t.Fatalf("credential was not cleared: %v", err)
	}
}

func TestSaveRejectsInvalidSession(t *testing.T) {
	store, err := NewStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []Credentials{
		{ServerURL: "file:///tmp/server", AccessToken: "access", RefreshToken: "refresh"},
		{ServerURL: "http://user:pass@127.0.0.1:8090", AccessToken: "access", RefreshToken: "refresh"},
		{ServerURL: "http://127.0.0.1:8090", AccessToken: "", RefreshToken: "refresh"},
	} {
		if err := store.Save(value); err == nil {
			t.Fatalf("Save(%#v) succeeded", value)
		}
	}
}

func TestForceRefreshBootstrapsLocalSessionAfterUnauthorized(t *testing.T) {
	var refreshCalls atomic.Int32
	var bootstrapCalls atomic.Int32
	serverURL := "http://127.0.0.1:18090"
	t.Setenv("LAZYMIND_SERVER_URL", serverURL)

	store, err := NewStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	store.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		response := &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Request:    request,
		}
		switch request.URL.Path {
		case authPath + "/refresh":
			refreshCalls.Add(1)
			response.StatusCode = http.StatusUnauthorized
			response.Body = io.NopCloser(strings.NewReader(`{"message":"refresh_token is invalid or expired"}`))
		case localSessionPath:
			bootstrapCalls.Add(1)
			if request.URL.Query().Get("force") != "true" {
				response.StatusCode = http.StatusConflict
				response.Body = io.NopCloser(strings.NewReader(`{"message":"force refresh required"}`))
				break
			}
			response.Header.Set("Content-Type", "application/json")
			response.Body = io.NopCloser(strings.NewReader(`{"token":"new-access","refreshToken":"new-refresh","username":"admin","role":"admin","tenantId":"default"}`))
		default:
			response.StatusCode = http.StatusNotFound
			response.Body = io.NopCloser(strings.NewReader(`{"message":"not found"}`))
		}
		return response, nil
	})}
	if err := store.Save(Credentials{
		ServerURL: serverURL, AccessToken: "old-access", RefreshToken: "old-refresh",
	}); err != nil {
		t.Fatal(err)
	}

	token, err := store.ForceRefresh(context.Background(), "old-access")
	if err != nil {
		t.Fatal(err)
	}
	if token != "new-access" {
		t.Fatalf("token=%q", token)
	}
	if refreshCalls.Load() != 1 || bootstrapCalls.Load() != 1 {
		t.Fatalf("refresh calls=%d bootstrap calls=%d", refreshCalls.Load(), bootstrapCalls.Load())
	}
	loaded, err := store.loadUnlocked()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AccessToken != "new-access" || loaded.RefreshToken != "new-refresh" {
		t.Fatalf("saved credentials=%#v", loaded)
	}
}

func TestLocalSessionRecoveryRequiresLoopbackUnauthorized(t *testing.T) {
	unauthorized := &apiError{StatusCode: http.StatusUnauthorized, Message: "expired"}
	for _, test := range []struct {
		server string
		err    error
		want   bool
	}{
		{server: "http://localhost:8090", err: unauthorized, want: true},
		{server: "http://127.0.0.1:8090", err: unauthorized, want: true},
		{server: "http://[::1]:8090", err: unauthorized, want: true},
		{server: "https://lazymind.example.com", err: unauthorized, want: false},
		{server: "http://127.0.0.1:8090", err: &apiError{StatusCode: http.StatusBadRequest}, want: false},
	} {
		if got := localSessionCanRecover(test.server, test.err); got != test.want {
			t.Errorf("localSessionCanRecover(%q, %v)=%v want %v", test.server, test.err, got, test.want)
		}
	}
}

func TestAuthenticationRequiredRecognizesMissingAndRejectedSessions(t *testing.T) {
	for _, err := range []error{
		ErrAuthenticationRequired,
		&apiError{StatusCode: http.StatusUnauthorized, Message: "refresh token expired"},
	} {
		if !IsAuthenticationRequired(err) {
			t.Errorf("IsAuthenticationRequired(%v)=false", err)
		}
	}
	if IsAuthenticationRequired(&apiError{StatusCode: http.StatusServiceUnavailable}) {
		t.Fatal("service unavailability was classified as an authentication failure")
	}
}
