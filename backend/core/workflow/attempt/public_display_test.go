package attempt

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func publicTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	if !strings.EqualFold(os.Getenv("TEST_DB_DRIVER"), "postgres") {
		service, db := testService(t)
		connection, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		connection.SetMaxOpenConns(1)
		return service, db
	}
	db := orm.MigrateTestDB(t, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowOutbox{}, &orm.WorkflowEvent{})
	if err := db.Create(&orm.WorkflowSession{ID: "s1", ConversationID: "conversation", CreateUserID: "owner"}).Error; err != nil {
		t.Fatal(err)
	}
	return New(db.DB, Config{LeaseDuration: time.Minute}), db.DB
}

func TestPublicDisplayPersistsOnceAndRejectsConflictRegressionAndStaleLease(t *testing.T) {
	service, db := publicTestService(t)
	queue(t, service, "a1", "s1", "write")
	claim, err := service.Claim(t.Context(), "executor")
	if err != nil {
		t.Fatal(err)
	}
	progress := json.RawMessage(`{"private_log":"SECRET","public_display":{"schema_version":1,"event_key":"event-1","process_steps":[{"step_id":"read","revision":1,"order":0,"title":"Read sources","status":"succeeded"}],"sources":[{"url":"https://example.com/report","title":"Report","raw_content":"SECRET"}]}}`)
	if err := service.Progress(t.Context(), "a1", claim.LeaseToken, progress); err != nil {
		t.Fatal(err)
	}
	if err := service.Progress(t.Context(), "a1", claim.LeaseToken, progress); err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	var events []orm.WorkflowEvent
	if err := db.Where("event_type = ?", "attempt.public_display").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || strings.Contains(string(events[0].PayloadJSON), "SECRET") {
		t.Fatalf("public event: %#v", events)
	}
	for _, raw := range []string{
		strings.Replace(string(progress), "Read sources", "Different title", 1),
		`{"public_display":{"schema_version":1,"event_key":"event-2","process_steps":[{"step_id":"read","revision":2,"order":0,"title":"Read sources","status":"running"}]}}`,
		`{"public_display":{"schema_version":1,"event_key":"event-3","process_steps":[{"step_id":"read","revision":0,"order":0,"title":"Read sources","status":"succeeded"}]}}`,
	} {
		if err := service.Progress(t.Context(), "a1", claim.LeaseToken, json.RawMessage(raw)); !errors.Is(err, ErrInvalidPublicDisplay) {
			t.Fatalf("invalid update accepted: %v", err)
		}
	}
	if err := service.Progress(t.Context(), "a1", "stale-token", progress); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale lease accepted: %v", err)
	}
	if err := service.Complete(t.Context(), "a1", claim.LeaseToken, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := service.Progress(t.Context(), "a1", claim.LeaseToken, progress); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("terminal attempt revived: %v", err)
	}
}

func TestPublicDisplayValidationDoesNotMutateAttempt(t *testing.T) {
	service, _ := publicTestService(t)
	queue(t, service, "a1", "s1", "write")
	claim, err := service.Claim(t.Context(), "executor")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`null`, `[]`, `{"public_display":null}`, `{"public_display":{"schema_version":2,"event_key":"x"}}`,
		`{"public_display":{"schema_version":1,"event_key":"x","prompt":"secret"}}`,
		`{"public_display":{"schema_version":1,"event_key":"x","sources":{}}}`,
		`{"public_display":{"schema_version":1,"event_key":"x","process_steps":[{"step_id":"a","revision":1,"order":0,"title":"a","status":"running","elapsed_ms":-1}]}}`,
	} {
		if err := service.Progress(t.Context(), "a1", claim.LeaseToken, json.RawMessage(raw)); !errors.Is(err, ErrInvalidPublicDisplay) {
			t.Fatalf("payload %s accepted: %v", raw, err)
		}
	}
	row, err := service.Attempt(t.Context(), "a1")
	if err != nil || row.Status != "claimed" || row.ProgressJSON != "{}" {
		t.Fatalf("invalid input mutated attempt: %#v %v", row, err)
	}
}

func TestPublicDisplayConcurrentDuplicateIsPersistedOnce(t *testing.T) {
	service, db := publicTestService(t)
	queue(t, service, "a1", "s1", "write")
	claim, err := service.Claim(t.Context(), "executor")
	if err != nil {
		t.Fatal(err)
	}
	progress := json.RawMessage(`{"public_display":{"schema_version":1,"event_key":"same","process_steps":[{"step_id":"read","revision":1,"order":0,"title":"Read sources","status":"running"}],"sources":[{"url":"https://example.com","title":"Source"}]}}`)
	var group sync.WaitGroup
	results := make(chan error, 4)
	for range 4 {
		group.Add(1)
		go func() { defer group.Done(); results <- service.Progress(t.Context(), "a1", claim.LeaseToken, progress) }()
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := db.Model(&orm.WorkflowEvent{}).Where("event_type = ? AND entity_id = ?", "attempt.public_display", "a1").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate public events: %d", count)
	}
}

func TestPublicDisplayHTTPRejectionHasServerRequestID(t *testing.T) {
	t.Setenv("LAZYMIND_WORKFLOW_EXECUTOR_TOKEN", "")
	req := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/attempts/a1:progress", strings.NewReader(`{"lease_token":"test-lease","progress":{"public_display":{"schema_version":0}}}`)), map[string]string{"attempt_id": "a1"})
	req.Header.Set("X-Workflow-Executor-Id", "test-executor")
	req.Header.Set("X-Request-ID", "DO_NOT_REFLECT_CLIENT_VALUE")
	response := httptest.NewRecorder()
	(Handler{Service: &Service{}}).Progress(response, req)
	var body envelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusUnprocessableEntity || body.Error == nil || body.Error.Code != "INVALID_PUBLIC_DISPLAY" {
		t.Fatalf("invalid public response: %d %#v", response.Code, body)
	}
	if body.RequestID == "" || body.RequestID != response.Header().Get("X-Request-ID") || body.RequestID == "DO_NOT_REFLECT_CLIENT_VALUE" {
		t.Fatalf("request correlation: %q", body.RequestID)
	}
}
