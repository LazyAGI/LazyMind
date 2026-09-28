package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInternalDesktopRenewalContract(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "revoked"}[revoked], func(t *testing.T) {
			t.Setenv("LAZYMIND_HOME", t.TempDir())
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/authservice/auth/refresh" {
					if revoked {
						w.WriteHeader(401)
						_, _ = w.Write([]byte(`{"detail":"synthetic-provider-secret"}`))
						return
					}
					_, _ = w.Write([]byte(`{"access_token":"synthetic-new","refresh_token":"synthetic-rotated"}`))
					return
				}
				if r.URL.Path != "/api/authservice/auth/me" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"user_id":"alice","status":"active"}`))
			}))
			defer server.Close()
			input, _ := json.Marshal(map[string]string{"server_url": server.URL, "access_token": "synthetic-old", "refresh_token": "synthetic-refresh", "user_id": "alice"})
			var output bytes.Buffer
			if err := runInternalSession([]string{"set"}, bytes.NewReader(input), &output); err != nil {
				t.Fatal(err)
			}
			output.Reset()
			if err := runInternalSession([]string{"renew"}, bytes.NewReader(input), &output); err != nil {
				t.Fatal(err)
			}
			var response struct {
				OK      bool   `json:"ok"`
				Code    string `json:"code"`
				Session struct {
					AccessToken  string `json:"access_token"`
					RefreshToken string `json:"refresh_token"`
				} `json:"session"`
			}
			if err := json.Unmarshal(output.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if revoked {
				if response.OK || response.Code != "DESKTOP_SESSION_AUTHENTICATION_REQUIRED" || strings.Contains(output.String(), "synthetic-") {
					t.Fatal("revocation response must contain only a stable code")
				}
			} else if !response.OK || response.Session.AccessToken != "synthetic-new" || response.Session.RefreshToken != "synthetic-rotated" {
				t.Fatal("invalid renewal IPC contract")
			}
		})
	}
}
