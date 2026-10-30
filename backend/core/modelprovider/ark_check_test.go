package modelprovider

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
)

func TestArkCredentialCheck(t *testing.T) {
	old := remoteModelsAllowPrivateHosts
	remoteModelsAllowPrivateHosts = true
	t.Cleanup(func() { remoteModelsAllowPrivateHosts = old })
	for _, service := range []string{"Doubao", "DoubaoCodingPlan", "DoubaoAgentPlan"} {
		for _, tc := range []struct {
			name         string
			status       int
			body, reason string
			success      bool
		}{
			{"recognized", 400, `{"error":{"code":"MissingParameter"}}`, "credentials_recognized", true},
			{"invalid key", 401, `{"error":{"code":"AuthenticationError"}}`, "authentication_failed", false},
			{"account", 401, `{"error":{"code":"InvalidAccountStatus"}}`, "permission_denied", false},
			{"unknown auth", 401, `{"error":{"code":"OtherError"}}`, "upstream_error", false},
			{"invalid parameter", 400, `{"error":{"code":"InvalidParameter"}}`, "invalid_request", false},
			{"wrong status", 500, `{"error":{"code":"MissingParameter"}}`, "upstream_error", false},
			{"not a model list", 404, `{"error":{"code":"UnsupportedModel"}}`, "model_not_found", false},
			{"rate limit", 429, `{}`, "rate_limited", false},
			{"bad json", 400, `oops`, "invalid_request", false},
			{"unexpected success", 200, `{}`, "invalid_response", false},
		} {
			t.Run(service+"/"+tc.name, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					body, _ := io.ReadAll(r.Body)
					if r.Method != "POST" || r.URL.Path != "/api/coding/v3/chat/completions" || string(body) != "{}" || r.Header.Get("Authorization") != "Bearer fixture-key" {
						t.Errorf("unexpected credential probe shape: %s %s %s", r.Method, r.URL.Path, body)
					}
					w.WriteHeader(tc.status)
					w.Write([]byte(tc.body))
				}))
				defer server.Close()
				parent := orm.UserModelProvider{Name: service}
				result, err := doProviderGroupCheck(t.Context(), "model", RuntimeProviderName(parent), server.URL+"/api/coding/v3/", "fixture-key", "", parent)
				if err != nil || result == nil {
					t.Fatalf("probe failed: %v", err)
				}
				if result.Success != tc.success || result.Reason != tc.reason || calls != 1 {
					t.Fatalf("result=%+v calls=%d", result, calls)
				}
			})
		}
	}
}

func TestArkCredentialCheckHonorsProtocolAndAdvancedOptions(t *testing.T) {
	old := remoteModelsAllowPrivateHosts
	remoteModelsAllowPrivateHosts = true
	t.Cleanup(func() { remoteModelsAllowPrivateHosts = old })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/coding/v1/messages" || r.URL.Query().Get("region") != "test" || r.Header.Get("X-Gateway") != "fixture" || r.Header.Get("x-api-key") != "fixture-key" {
			t.Errorf("incorrect request: %s", r.URL.Path)
		}
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"code":"MissingParameter"}}`))
	}))
	defer server.Close()
	parent := orm.UserModelProvider{Name: "DoubaoCodingPlan", ProtocolName: "Claude"}
	result, err := doArkCredentialCheck(t.Context(), RuntimeProviderName(parent), server.URL+"/api/coding/", "fixture-key", credentialvault.ProviderRequestAdvanced{Headers: map[string]string{"X-Gateway": "fixture"}, Query: map[string]string{"region": "test"}})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCustomSupplierNamedDoubaoKeepsGenericCheck(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/model/check" {
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	parent := orm.UserModelProvider{Name: "Doubao", IsCustom: true, ProtocolName: "OpenAI"}
	result, err := doProviderGroupCheck(t.Context(), "model", RuntimeProviderName(parent), "https://example.com/v1/", "fixture-key", "", parent)
	if err != nil || result == nil || !result.Success || calls != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
	}
}

func TestArkCredentialCheckDoesNotFollowRedirects(t *testing.T) {
	old := remoteModelsAllowPrivateHosts
	remoteModelsAllowPrivateHosts = true
	t.Cleanup(func() { remoteModelsAllowPrivateHosts = old })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/credential-sink", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	result, err := doArkCredentialCheck(t.Context(), "Doubao", server.URL, "fixture-key", credentialvault.ProviderRequestAdvanced{})
	if err != nil || result == nil || result.Success || result.Reason != "upstream_error" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
