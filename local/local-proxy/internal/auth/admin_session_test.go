package auth

import (
	"context"
	"net/http"
	"testing"
)

func TestAdminSessionManager_EnsureLogsInAndCachesSession(t *testing.T) {
	t.Parallel()

	loginCalls := 0
	meCalls := 0
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/api/authservice/auth/login":
				loginCalls++
				return makeResponse(http.StatusOK, `{"access_token":"token","refresh_token":"refresh-1","role":"system-admin","expires_in":3600}`), nil
			case "/api/authservice/auth/me":
				meCalls++
				if got := r.Header.Get("Authorization"); got != "Bearer token" {
					t.Fatalf("Authorization = %q, want Bearer token", got)
				}
				return makeResponse(http.StatusOK, `{"user_id":"u-1","username":"admin","role":"system-admin","tenant_id":"tenant","dynamic":true,"chat_unlike_switch":true}`), nil
			default:
				t.Fatalf("unexpected path %s", r.URL.Path)
				return nil, nil
			}
		}),
	}

	manager := NewAdminSessionManager("http://auth", client)
	session, err := manager.Ensure(context.Background(), false)
	if err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}
	if session.Token != "token" || session.RefreshToken != "refresh-1" || session.UserID != "u-1" || !session.Dynamic || !session.ChatUnlikeSwitch {
		t.Fatalf("unexpected session %#v", session)
	}
	if _, err := manager.Ensure(context.Background(), false); err != nil {
		t.Fatalf("second Ensure failed: %v", err)
	}
	if loginCalls != 1 || meCalls != 1 {
		t.Fatalf("loginCalls=%d meCalls=%d, want 1/1", loginCalls, meCalls)
	}
}

func TestAdminSessionManager_EnsureRefreshesExpiredSession(t *testing.T) {
	t.Parallel()

	loginCalls := 0
	refreshCalls := 0
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/api/authservice/auth/login":
				loginCalls++
				return makeResponse(http.StatusOK, `{"access_token":"expired-token","refresh_token":"refresh-1","role":"system-admin","expires_in":1}`), nil
			case "/api/authservice/auth/refresh":
				refreshCalls++
				return makeResponse(http.StatusOK, `{"access_token":"fresh-token","refresh_token":"refresh-2","role":"system-admin","expires_in":3600}`), nil
			case "/api/authservice/auth/me":
				return makeResponse(http.StatusOK, `{"user_id":"u-1","username":"admin","role":"system-admin"}`), nil
			default:
				t.Fatalf("unexpected path %s", r.URL.Path)
				return nil, nil
			}
		}),
	}

	manager := NewAdminSessionManager("http://auth", client)
	session, err := manager.Ensure(context.Background(), false)
	if err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}
	if session.Token != "expired-token" {
		t.Fatalf("first token = %q", session.Token)
	}

	session, err = manager.Ensure(context.Background(), false)
	if err != nil {
		t.Fatalf("refresh Ensure failed: %v", err)
	}
	if session.Token != "fresh-token" || session.RefreshToken != "refresh-2" {
		t.Fatalf("unexpected refreshed session %#v", session)
	}
	if loginCalls != 1 || refreshCalls != 1 {
		t.Fatalf("loginCalls=%d refreshCalls=%d, want 1/1", loginCalls, refreshCalls)
	}
}

func TestAdminSessionManager_RefreshFailureFallsBackToLogin(t *testing.T) {
	t.Parallel()

	loginCalls := 0
	refreshCalls := 0
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/api/authservice/auth/login":
				loginCalls++
				token := "expired-token"
				refresh := "refresh-1"
				if loginCalls > 1 {
					token = "relogin-token"
					refresh = "refresh-2"
				}
				return makeResponse(http.StatusOK, `{"access_token":"`+token+`","refresh_token":"`+refresh+`","role":"system-admin","expires_in":1}`), nil
			case "/api/authservice/auth/refresh":
				refreshCalls++
				return makeResponse(http.StatusUnauthorized, `{"detail":"invalid refresh token"}`), nil
			case "/api/authservice/auth/me":
				return makeResponse(http.StatusOK, `{"user_id":"u-1","username":"admin","role":"system-admin"}`), nil
			default:
				t.Fatalf("unexpected path %s", r.URL.Path)
				return nil, nil
			}
		}),
	}

	manager := NewAdminSessionManager("http://auth", client)
	if _, err := manager.Ensure(context.Background(), false); err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}
	session, err := manager.Ensure(context.Background(), false)
	if err != nil {
		t.Fatalf("fallback Ensure failed: %v", err)
	}
	if session.Token != "relogin-token" || session.RefreshToken != "refresh-2" {
		t.Fatalf("unexpected relogin session %#v", session)
	}
	if loginCalls != 2 || refreshCalls != 1 {
		t.Fatalf("loginCalls=%d refreshCalls=%d, want 2/1", loginCalls, refreshCalls)
	}
}
