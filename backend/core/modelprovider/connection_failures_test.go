package modelprovider

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestFailureCodeForRemoteModels(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		err    error
		want   string
	}{
		{"authentication", 401, `{"error":{"message":"secret"}}`, nil, "authentication_failed"},
		{"permission", 403, "", nil, "permission_denied"},
		{"not found", 404, "", nil, "model_list_unavailable"},
		{"quota HTTP", 402, "", nil, "quota_exceeded"},
		{"rate limit", 429, "", nil, "rate_limited"},
		{"quota before rate", 429, `{"error":{"code":"insufficient_quota","message":"secret"}}`, nil, "quota_exceeded"},
		{"quota before permission", 403, `{"error":{"code":"BalanceNotEnough"}}`, nil, "quota_exceeded"},
		{"deadline", 0, "", context.DeadlineExceeded, "timeout"},
		{"transport", 0, "", errors.New("secret host and key"), "connection_error"},
		{"upstream", 503, "", nil, "upstream_error"},
		{"bad body", http.StatusOK, `bad`, nil, "invalid_response"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FailureCodeForRemoteModels(tc.status, []byte(tc.body), tc.err); got != tc.want {
				t.Fatalf("reason=%q want %q", got, tc.want)
			}
		})
	}
}
