package modelprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func TestProviderConnectionCheckDoesNotChooseDefaultModel(t *testing.T) {
	checker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request algoModelCheckBody
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "" {
			t.Error("read-only supplier check must not choose an inference model")
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true})
	}))
	defer checker.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", checker.URL)
	if result, err := doCheck(context.Background(), defaultProviderCategory, "SenseNova", sensenovaNewPlatformBaseURL, "fixture-key", ""); err != nil || !result.Success {
		t.Fatal("supplier check failed", err)
	}
}

func TestCheckGroupReturnsStructuredReasonWithoutProviderText(t *testing.T) {
	for _, reason := range []string{"authentication_failed", "permission_denied", "model_list_unavailable", "quota_exceeded", "rate_limited", "timeout", "forged-private-key"} {
		t.Run(reason, func(t *testing.T) {
			_, parent, group, _ := setupProbeTest(t)
			checker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				json.NewDecoder(r.Body).Decode(&request)
				if request["models_url"] != "https://probe.example/v1/models" {
					t.Error("check and discovery must receive the same API-root models URL")
				}
				json.NewEncoder(w).Encode(map[string]any{"success": false, "reason": reason, "message": "raw-secret-do-not-forward"})
			}))
			defer checker.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", checker.URL)
			req := httptest.NewRequest(http.MethodPost, "/check", strings.NewReader(`{}`))
			req.Header.Set("X-User-Id", parent.CreateUserID)
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
			rec := httptest.NewRecorder()
			CheckGroup(rec, req)
			var response struct {
				Data struct {
					Reason string `json:"reason"`
				} `json:"data"`
			}
			json.Unmarshal(rec.Body.Bytes(), &response)
			want := reason
			if reason == "forged-private-key" {
				want = "upstream_error"
			}
			if rec.Code != http.StatusOK || response.Data.Reason != want || strings.Contains(rec.Body.String(), "raw-secret") || strings.Contains(rec.Body.String(), "forged-private-key") {
				t.Fatalf("unsafe/ambiguous result: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
