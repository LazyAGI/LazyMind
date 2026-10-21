package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestRemovalCancellationIsBoundedAndRetriesUpstreamFailure(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.WorkflowSession{})
			before := time.Now().UTC()
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var payload struct {
					TaskID         string `json:"task_id"`
					ConversationID string `json:"conversation_id"`
					Before         *int64 `json:"started_before_ms"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.TaskID != "removed_100" || payload.ConversationID != "" || payload.Before != nil || r.URL.Path != "/api/workflow/task-cancel" {
					t.Errorf("invalid stop request: %+v %v", payload, err)
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			err := CancelRemovedTaskExecutions(t.Context(), db.DB, nil, "owner", "removed", before, []string{"removed_100"})
			if calls != 1 || (err != nil) != (status != http.StatusOK) {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestRemovalCancellationRetriesRejectedOrInvalidAcknowledgement(t *testing.T) {
	for _, body := range []string{`{"ok":false}`, `invalid response`} {
		t.Run(body, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.WorkflowSession{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			if err := CancelRemovedTaskExecutions(t.Context(), db.DB, nil, "owner", "removed", time.Now().UTC(), []string{"removed_100"}); err == nil {
				t.Fatal("unacknowledged cancellation must remain retryable")
			}
		})
	}
}

func TestRemovalCancellationNeverResolvesTheCurrentConversationExecution(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.WorkflowSession{})
	before := time.UnixMilli(150)
	var canceled []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload) != 1 || payload["task_id"] != "removed_100" {
			t.Errorf("cancel must target the original execution: %v %v", payload, err)
		}
		canceled = append(canceled, payload["task_id"])
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	// Repeating the old job must not address a later execution removed_200.
	for _, cutoff := range []time.Time{before, time.UnixMilli(100)} {
		if err := CancelRemovedTaskExecutions(t.Context(), db.DB, nil, "owner", "removed", cutoff, []string{"removed_100"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(canceled) != 2 {
		t.Fatalf("canceled=%v", canceled)
	}
	for _, ids := range [][]string{nil, {}, {"other_100"}, {"removed_unknown"}, {"removed_200"}} {
		_ = CancelRemovedTaskExecutions(t.Context(), db.DB, nil, "owner", "removed", before, ids)
		if len(canceled) != 2 {
			t.Fatalf("invalid or missing execution used a broad cancellation: %v", ids)
		}
	}
}
