package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func makeResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestRBACAdapter_AuthorizeAndInjectIdentity_InjectsTrustedHeadersAndStripsClientHeaders(t *testing.T) {
	t.Parallel()

	expectedToken := "Bearer local-token"
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost {
				t.Fatalf("expected method %q, got %q", http.MethodPost, r.Method)
			}
			if r.URL.Path != "/api/authservice/auth/authorize" {
				t.Fatalf("expected path %q, got %q", "/api/authservice/auth/authorize", r.URL.Path)
			}

			var body struct {
				Method string `json:"method"`
				Path   string `json:"path"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode request body: %v", err)
			}
			if body.Method != http.MethodGet {
				t.Fatalf("expected authorization body method %q, got %q", http.MethodGet, body.Method)
			}
			if body.Path != "/api/core/conversations" {
				t.Fatalf("expected authorization body path %q, got %q", "/api/core/conversations", body.Path)
			}
			if got := r.Header.Get("Authorization"); got != expectedToken {
				t.Fatalf("expected forwarded Authorization %q, got %q", expectedToken, got)
			}

			return makeResponse(http.StatusOK, `{"user_id":"u-1","username":"alice","tenant_id":"t-1","role":"admin"}`), nil
		}),
	}

	adapter := NewRBACAdapter("https://auth-service:8000", client)
	req, err := http.NewRequest(http.MethodGet, "https://example.com/api/core/conversations", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Authorization", expectedToken)
	req.Header.Set("X-User-Id", "spoofed-user-id")
	req.Header.Set("X-User-Name", "spoofed-user-name")
	req.Header.Set("X-Tenant-Id", "spoofed-tenant-id")
	req.Header.Set("X-User-Role", "spoofed-role")

	claims, authErr := adapter.AuthorizeAndInjectIdentity(context.Background(), req)
	if authErr != nil {
		t.Fatalf("expected success, got %v", authErr)
	}
	if claims.UserID != "u-1" || claims.UserName != "alice" || claims.TenantID != "t-1" || claims.Role != "admin" {
		t.Fatalf("unexpected claims %#v", claims)
	}
	if got := req.Header.Get("X-User-Id"); got != "u-1" {
		t.Fatalf("X-User-Id = %q, want %q", got, "u-1")
	}
	if got := req.Header.Get("X-User-Name"); got != "alice" {
		t.Fatalf("X-User-Name = %q, want %q", got, "alice")
	}
	if got := req.Header.Get("X-Tenant-Id"); got != "t-1" {
		t.Fatalf("X-Tenant-Id = %q, want %q", got, "t-1")
	}
	if got := req.Header.Get("X-User-Role"); got != "admin" {
		t.Fatalf("X-User-Role = %q, want %q", got, "admin")
	}
}

func TestRBACAdapter_AuthorizeAndInjectIdentity_ReadsDataWrapper(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return makeResponse(http.StatusOK, `{"data":{"user_id":"u-2","username":"bob","tenant_id":"t-2","role":"member"}}`), nil
		}),
	}

	adapter := NewRBACAdapter("https://auth-service:8000", client)
	req, err := http.NewRequest(http.MethodPost, "https://example.com/api/core/messages", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}

	claims, authErr := adapter.AuthorizeAndInjectIdentity(context.Background(), req)
	if authErr != nil {
		t.Fatalf("expected success, got %v", authErr)
	}
	if claims.UserID != "u-2" || claims.UserName != "bob" || claims.TenantID != "t-2" || claims.Role != "member" {
		t.Fatalf("unexpected claims %#v", claims)
	}
}

func TestRBACAdapter_AuthorizeAndInjectIdentity_AnonymousAllowed(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if got := r.Header.Get("Authorization"); got != "" {
				t.Fatalf("expected empty Authorization forwarded, got %q", got)
			}
			return makeResponse(http.StatusOK, `{"allowed":true}`), nil
		}),
	}

	adapter := NewRBACAdapter("https://auth-service:8000", client)
	req, err := http.NewRequest(http.MethodGet, "https://example.com/api/core/public", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}

	claims, authErr := adapter.AuthorizeAndInjectIdentity(context.Background(), req)
	if authErr != nil {
		t.Fatalf("expected success, got %v", authErr)
	}
	if claims != (IdentityClaims{}) {
		t.Fatalf("claims = %#v, want zero values", claims)
	}
}

func TestRBACAdapter_AuthorizeAndInjectIdentity_IgnoresNonStringClaims(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return makeResponse(http.StatusOK, `{"user_id":123,"username":"","tenant_id":"tenant","role":"  "}`), nil
		}),
	}

	adapter := NewRBACAdapter("https://auth-service:8000", client)
	req, err := http.NewRequest(http.MethodGet, "https://example.com/api/core", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}

	claims, authErr := adapter.AuthorizeAndInjectIdentity(context.Background(), req)
	if authErr != nil {
		t.Fatalf("expected success, got %v", authErr)
	}
	if claims.UserID != "" || claims.UserName != "" || claims.Role != "" || claims.TenantID != "tenant" {
		t.Fatalf("unexpected claims %#v", claims)
	}
	if got := req.Header.Get("X-User-Id"); got != "" {
		t.Fatalf("X-User-Id should be empty")
	}
	if got := req.Header.Get("X-Tenant-Id"); got != "tenant" {
		t.Fatalf("X-Tenant-Id = %q, want %q", got, "tenant")
	}
}

func TestRBACAdapter_AuthorizeAndInjectIdentity_Unauthorized(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return makeResponse(http.StatusUnauthorized, `{"detail":"nope"}`), nil
		}),
	}

	adapter := NewRBACAdapter("https://auth-service:8000", client)
	req, err := http.NewRequest(http.MethodGet, "https://example.com/api/core", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}

	_, authErr := adapter.AuthorizeAndInjectIdentity(context.Background(), req)
	if authErr == nil || authErr.Status != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized error, got %v", authErr)
	}
	if authErr.Detail != "Unauthorized" {
		t.Fatalf("detail = %q, want %q", authErr.Detail, "Unauthorized")
	}
	if body := authErr.Body(); body["detail"] != "Unauthorized" {
		t.Fatalf("body = %#v, want detail=Unauthorized", body)
	}
}

func TestRBACAdapter_AuthorizeAndInjectIdentity_Forbidden(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return makeResponse(http.StatusForbidden, `{"detail":"nope"}`), nil
		}),
	}

	adapter := NewRBACAdapter("https://auth-service:8000", client)
	req, err := http.NewRequest(http.MethodGet, "https://example.com/api/core", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}

	_, authErr := adapter.AuthorizeAndInjectIdentity(context.Background(), req)
	if authErr == nil || authErr.Status != http.StatusForbidden {
		t.Fatalf("expected forbidden error, got %v", authErr)
	}
	if authErr.Detail != "Forbidden" {
		t.Fatalf("detail = %q, want %q", authErr.Detail, "Forbidden")
	}
}

func TestRBACAdapter_AuthorizeAndInjectIdentity_FailedGatewayOnAuthError(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return makeResponse(http.StatusInternalServerError, `{"message":"boom"}`), nil
		}),
	}

	adapter := NewRBACAdapter("https://auth-service:8000", client)
	req, err := http.NewRequest(http.MethodGet, "https://example.com/api/core", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}

	_, authErr := adapter.AuthorizeAndInjectIdentity(context.Background(), req)
	if authErr == nil || authErr.Status != http.StatusBadGateway {
		t.Fatalf("expected bad gateway error, got %v", authErr)
	}
	if authErr.Message != "Authorization check failed" {
		t.Fatalf("message = %q, want %q", authErr.Message, "Authorization check failed")
	}
}

func TestRBACAdapter_AuthorizeAndInjectIdentity_MalformedJSON(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return makeResponse(http.StatusOK, `{`), nil
		}),
	}

	adapter := NewRBACAdapter("https://auth-service:8000", client)
	req, err := http.NewRequest(http.MethodGet, "https://example.com/api/core", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}

	_, authErr := adapter.AuthorizeAndInjectIdentity(context.Background(), req)
	if authErr == nil || authErr.Status != http.StatusBadGateway {
		t.Fatalf("expected bad gateway error, got %v", authErr)
	}
	if authErr.Message != "Authorization check failed" {
		t.Fatalf("message = %q, want %q", authErr.Message, "Authorization check failed")
	}
}

func TestRBACAdapter_AuthorizeAndInjectIdentity_ServiceUnavailableOnNetworkFailure(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("network unavailable")
		}),
	}

	adapter := NewRBACAdapter("https://auth-service:8000", client)
	req, err := http.NewRequest(http.MethodGet, "https://example.com/api/core", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}

	_, authErr := adapter.AuthorizeAndInjectIdentity(context.Background(), req)
	if authErr == nil || authErr.Status != http.StatusServiceUnavailable {
		t.Fatalf("expected service unavailable error, got %v", authErr)
	}
	if authErr.Message != "Authorization service unavailable" {
		t.Fatalf("message = %q, want %q", authErr.Message, "Authorization service unavailable")
	}
}

func TestRBACAdapter_AuthorizeAndInjectIdentity_UsesConfiguredTimeout(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Timeout: 10 * time.Millisecond,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}),
	}

	adapter := NewRBACAdapter("https://auth-service:8000", client)
	req, err := http.NewRequest(http.MethodGet, "https://example.com/api/core", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}

	start := time.Now()
	_, authErr := adapter.AuthorizeAndInjectIdentity(context.Background(), req)
	elapsed := time.Since(start)
	if authErr == nil || authErr.Status != http.StatusServiceUnavailable {
		t.Fatalf("expected service unavailable error, got %v", authErr)
	}
	if elapsed > 30*time.Millisecond {
		t.Fatalf("expected timeout to be respected, request took %s", elapsed)
	}
}

func TestStripIdentityHeaders(t *testing.T) {
	t.Parallel()

	h := make(http.Header)
	h.Add("X-User-Id", "user")
	h.Add("x-user-name", "name")
	h.Add("X-Tenant-Id", "tenant")
	h.Add("X-User-Role", "role")
	h.Add("X-Other", "keep")

	StripIdentityHeaders(h)

	for _, key := range identityHeaders {
		if got := h.Get(key); got != "" {
			t.Fatalf("%s header should be removed, got %q", key, got)
		}
	}
	if got := h.Get("X-Other"); got != "keep" {
		t.Fatalf("other headers should be preserved")
	}
}

func TestApplyTrustedIdentityHeaders_StripsIdentityHeaders(t *testing.T) {
	t.Parallel()

	header := make(http.Header)
	header.Set("X-User-Id", "preexisting")
	header.Set("X-Other", "keep")
	ApplyTrustedIdentityHeaders(header, IdentityClaims{
		UserID:   "user-id",
		UserName: "alice",
		TenantID: "",
		Role:     "",
	})

	if got := header.Get("X-User-Id"); got != "user-id" {
		t.Fatalf("X-User-Id = %q, want %q", got, "user-id")
	}
	if got := header.Get("X-User-Name"); got != "alice" {
		t.Fatalf("X-User-Name = %q, want %q", got, "alice")
	}
	if got := header.Get("X-Tenant-Id"); got != "" {
		t.Fatalf("X-Tenant-Id should not be set when claim is empty")
	}
	if got := header.Get("X-User-Role"); got != "" {
		t.Fatalf("X-User-Role should not be set when claim is empty")
	}
	if got := header.Get("X-Other"); got != "keep" {
		t.Fatalf("other header should be preserved")
	}
}
