package scheduler

import (
	"context"
	"lazymind/core/common"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScheduleDescriptionValidationContract(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		status, code int
	}{
		{"allowed", `{"passed":true,"matched_word":null}`, 200, 0},
		{"structured match", `{"passed":false,"matched_word":{"word":"fixture","action":"block"}}`, 200, 2003104},
		{"legacy match", `{"passed":false,"matched_word":"fixture"}`, 200, 2003104},
		{"no match detail", `{"passed":false}`, 200, 2003104},
		{"missing decision", `{}`, 200, 2003105},
		{"null decision", `{"passed":null}`, 200, 2003105},
		{"invalid decision", `{"passed":"true"}`, 200, 2003105},
		{"invalid json", `{`, 200, 2003105},
		{"unavailable", `internal dependency details`, 503, 2003105},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/chat/sensitive-check" {
					t.Errorf("unexpected check request")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)
			err := validateScheduleDescription(context.Background(), "fixture description")
			if tc.code == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			appErr, ok := any(err).(*common.AppError)
			if !ok || appErr.Code != tc.code {
				t.Fatalf("expected stable code %d, got %v", tc.code, err)
			}
			if appErr.Detail != nil {
				t.Fatal("must not expose provider details")
			}
		})
	}
}
