package attempt

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

func request(t *testing.T, handler http.HandlerFunc, method, path string, body any, vars map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	encoded, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	r.Header.Set("X-Workflow-Executor-Id", "executor-1")
	r.Header.Set("Workflow-Contract-Version", ContractVersion)
	r = mux.SetURLVars(r, vars)
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func TestHandlerReturnsVersionedLeaseLostEnvelope(t *testing.T) {
	service, _ := testService(t)
	queue(t, service, "a1", "s1", "step")
	claim, err := service.Claim(context.Background(), "executor-1")
	if err != nil {
		t.Fatal(err)
	}
	handler := Handler{Service: service}
	w := request(t, handler.Progress, http.MethodPost, "/attempts/a1:progress",
		map[string]any{"lease_token": claim.LeaseToken + "stale", "progress": map[string]any{"pct": 1}},
		map[string]string{"attempt_id": "a1"})
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var result envelope
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ContractVersion != ContractVersion || result.OK || result.Error == nil || result.Error.Code != CodeLeaseLost {
		t.Fatalf("envelope=%#v", result)
	}
}

func TestHandlerClaimCompleteAndIdempotentRepeat(t *testing.T) {
	service, _ := testService(t)
	queue(t, service, "a1", "s1", "step")
	handler := Handler{Service: service}
	claimed := request(t, handler.Claim, http.MethodPost, "/attempts:claim", map[string]any{}, nil)
	if claimed.Code != http.StatusOK {
		t.Fatal(claimed.Body.String())
	}
	var claimEnvelope struct {
		Data Claim `json:"data"`
	}
	if err := json.Unmarshal(claimed.Body.Bytes(), &claimEnvelope); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"lease_token": claimEnvelope.Data.LeaseToken, "result": map[string]any{"ok": true}}
	for range 2 {
		completed := request(t, handler.Complete, http.MethodPost, "/attempts/a1:complete", body, map[string]string{"attempt_id": "a1"})
		if completed.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", completed.Code, completed.Body.String())
		}
	}
}

func TestHandlerRejectsInvalidExecutorCredentialAndContractVersion(t *testing.T) {
	service, _ := testService(t)
	queue(t, service, "a1", "s1", "step")
	handler := Handler{Service: service}
	t.Setenv("LAZYMIND_WORKFLOW_EXECUTOR_TOKEN", "secret")

	badToken := httptest.NewRequest(http.MethodPost, "/attempts:claim", nil)
	badToken.Header.Set("X-Workflow-Executor-Id", "executor-1")
	badToken.Header.Set("Authorization", "Bearer wrong")
	badTokenRecorder := httptest.NewRecorder()
	handler.Claim(badTokenRecorder, badToken)
	if badTokenRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", badTokenRecorder.Code, badTokenRecorder.Body.String())
	}

	badVersion := httptest.NewRequest(http.MethodPost, "/attempts:claim", nil)
	badVersion.Header.Set("X-Workflow-Executor-Id", "executor-1")
	badVersion.Header.Set("Authorization", "Bearer secret")
	badVersion.Header.Set("Workflow-Contract-Version", "workflow.v999")
	badVersionRecorder := httptest.NewRecorder()
	handler.Claim(badVersionRecorder, badVersion)
	if badVersionRecorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", badVersionRecorder.Code, badVersionRecorder.Body.String())
	}
}
