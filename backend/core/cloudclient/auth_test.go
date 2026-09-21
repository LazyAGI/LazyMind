package cloudclient

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func fixtureJWT(expiresAt time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, expiresAt.Unix())))
	return "header." + payload + ".signature"
}

func TestRefreshAndLogoutUsePublishedAuthContracts(t *testing.T) {
	expiresAt := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	accessToken := fixtureJWT(expiresAt)
	calls := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		switch request.URL.Path {
		case "/v1/auth/refresh":
			if request.Header.Get("Authorization") != "" {
				t.Fatal("refresh must not send the expired access token")
			}
			return jsonResponse(http.StatusOK, fmt.Sprintf(`{"access_token":%q,"refresh_token":"rotated-refresh","session_id":"session-1","reauthenticated_at":null}`, accessToken), nil), nil
		case "/v1/auth/logout":
			if request.Header.Get("Authorization") != "Bearer "+accessToken {
				t.Fatalf("logout authorization=%q", request.Header.Get("Authorization"))
			}
			return jsonResponse(http.StatusNoContent, "", nil), nil
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
			return nil, nil
		}
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := client.RefreshSession(context.Background(), "old-refresh")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.RefreshToken != "rotated-refresh" {
		t.Fatalf("tokens=%+v", tokens)
	}
	parsedExpiry, err := AccessTokenExpiresAt(tokens.AccessToken)
	if err != nil || !parsedExpiry.Equal(expiresAt) {
		t.Fatalf("expiry=%s err=%v", parsedExpiry, err)
	}
	if err := client.LogoutSession(context.Background(), tokens.AccessToken, tokens.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestAccessTokenExpiryRejectsMalformedJWT(t *testing.T) {
	for _, token := range []string{"", "not-a-jwt", "a.b.c", "a." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user"}`)) + ".c"} {
		if _, err := AccessTokenExpiresAt(token); err == nil || (token != "" && strings.Contains(err.Error(), token)) {
			t.Fatalf("unsafe or missing error for token %q: %v", token, err)
		}
	}
}
