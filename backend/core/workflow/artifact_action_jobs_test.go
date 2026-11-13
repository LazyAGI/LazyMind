package workflow

import (
	"encoding/json"
	"github.com/gorilla/mux"
	"lazymind/core/algo"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExternalActionReplayCancelAndIdempotency(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.WorkflowActionJob{}, &orm.WorkflowHostAction{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	sqlDB, _ := db.DB.DB()
	sqlDB.SetMaxOpenConns(1)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req algo.WorkflowActionInvokeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.LLMConfig != nil || req.GenerationResponses == nil || len(*req.GenerationResponses) != 1 {
			t.Errorf("model replay contract lost: %#v", req.GenerationResponses)
		}
		// The shared planner calls Core back to read its package. With one
		// SQLite connection this read deadlocks if invocation holds a transaction.
		var count int64
		if err := db.Model(&orm.WorkflowActionJob{}).Count(&count).Error; err != nil {
			t.Error(err)
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"artifact":{"content_type":"text","value":"candidate"},"commit":{"token":"private-token"},"preview":{"old_text":"old","new_text":"new"}}}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	responses := []map[string]any{}
	request, _ := json.Marshal(algo.WorkflowActionInvokeRequest{GenerationResponses: &responses})
	job := orm.WorkflowActionJob{ID: "job", SessionID: "session", SlotID: "preview_html", ListIndex: 0, BaseRevision: 1, Version: 1, Status: "waiting_agent", RequestJSON: string(request), ResultJSON: `{"status":"needs_generation","generation_request":{"request_hash":"hash"}}`}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	call := func(body string) int {
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		req = mux.SetURLVars(req, map[string]string{"session_id": "session", "job_id": "job"})
		rec := httptest.NewRecorder()
		SubmitArtifactActionJob(rec, req)
		return rec.Code
	}
	if status := call(`{"version":2,"request_hash":"hash","text":"answer"}`); status != 409 {
		t.Fatal(status)
	}
	body := `{"version":1,"request_hash":"hash","text":"answer"}`
	if status := call(body); status != 200 {
		t.Fatal(status)
	}
	if status := call(body); status != 200 || calls != 1 {
		t.Fatalf("retry re-executed: %d %d", status, calls)
	}
	if status := call(`{"version":1,"request_hash":"hash","text":"different"}`); status != 409 {
		t.Fatal(status)
	}
	if status := call(`{"cancel":true}`); status != 200 {
		t.Fatal(status)
	}
	if err := db.First(&job, "id = ?", "job").Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != "cancelled" {
		t.Fatal(job.Status)
	}
	target := &artifactActionTarget{db: db.DB, session: &orm.WorkflowSession{ID: "session"}, revision: &orm.WorkflowSlotRevision{SlotID: "preview_html"}}
	_, err := loadExternalActionCommit(httptest.NewRequest("POST", "/", nil), target, &artifactActionPreviewBody{BaseRevision: 1, Input: map[string]any{"commit_token": "external:job"}})
	if err == nil {
		t.Fatal("cancelled edit can be committed")
	}
}

func TestActionJobViewDoesNotExposeInternalCommitToken(t *testing.T) {
	job := orm.WorkflowActionJob{ID: "job", Status: "ready", BaseRevision: 3, ResultJSON: `{"commit":{"token":"private"}}`}
	raw, _ := json.Marshal(actionJobView(job))
	if strings.Contains(string(raw), "private") || !strings.Contains(string(raw), "external:job") {
		t.Fatal(string(raw))
	}
}
