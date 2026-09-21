package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/knowledge_market"
	"lazymind/core/store"
)

// The real Core router and database are exercised. Only the unchanged Algorithm
// HTTP service is simulated, including WAITING-only cancellation and its races.
type marketControlFixture struct {
	t            *testing.T
	db           *orm.DB
	router       *mux.Router
	workerStatus atomic.Int32
	cancelStatus atomic.Int32
	mu           sync.Mutex
	calls        []string
	mode         string
	root         string
}

func newMarketControlFixture(t *testing.T, states ...string) *marketControlFixture {
	t.Helper()
	t.Setenv("LAZYMIND_READONLY_SCHEMA", "")
	// OpenTestDB appends an isolated search_path using URL query syntax. The
	// development container supplies keyword-form libpq configuration instead.
	if os.Getenv("TEST_DB_DRIVER") == "postgres" {
		cfg, err := pgx.ParseConfig(os.Getenv("TEST_DB_DSN"))
		if err != nil {
			t.Fatal("invalid PostgreSQL test configuration")
		}
		query := url.Values{}
		for key, value := range cfg.RuntimeParams {
			query.Set(key, value)
		}
		if cfg.TLSConfig == nil {
			query.Set("sslmode", "disable")
		}
		dsn := url.URL{Scheme: "postgresql", User: url.UserPassword(cfg.User, cfg.Password), Host: net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), Path: "/" + cfg.Database, RawQuery: query.Encode()}
		t.Setenv("TEST_DB_DSN", dsn.String())
	}
	db := orm.MigrateTestDB(t, &orm.KnowledgeMarketItem{}, &orm.KnowledgeMarketInstall{}, &orm.AsyncJob{},
		&orm.Dataset{}, &orm.Document{}, &orm.Task{}, &readonlyorm.LazyLLMDocServiceTaskRow{})
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	f := &marketControlFixture{t: t, db: db, root: t.TempDir()}
	f.workerStatus.Store(200)
	f.cancelStatus.Store(200)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", f.root)
	server := httptest.NewServer(http.HandlerFunc(f.algorithm))
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_DOCUMENT_WORKER_URL", server.URL)
	t.Setenv("LAZYMIND_DOCUMENT_SERVICE_URL", server.URL)
	t.Setenv("LAZYMIND_DOCUMENT_PROCESSOR_URL", server.URL)
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", server.URL)
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", server.URL)
	now := time.Now().UTC()
	base := orm.BaseModel{CreateUserID: "control-owner", CreatedAt: now, UpdatedAt: now}
	f.insert(&orm.KnowledgeMarketItem{ID: "control-item", Name: "Control fixture", Category: "industry", Status: "published", Tags: json.RawMessage(`[]`), SourceOptions: json.RawMessage(`{}`), SampleQuestions: json.RawMessage(`[]`), CreatedAt: now, UpdatedAt: now})
	f.insert(&orm.Dataset{ID: "control-dataset", KbID: "control-dataset", BaseModel: base, ProcessingLevel: "stored", Ext: json.RawMessage(`{}`)})
	ids := make([]string, 0, len(states))
	for i, state := range states {
		id := fmt.Sprintf("control-file-%d", i)
		ids = append(ids, id)
		path := filepath.Join(f.root, "tenants", "root", "datasets", "control-dataset", "docs", "files", id, id+".md")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		docExt, _ := json.Marshal(map[string]any{"stored_path": path, "stored_name": id + ".md", "original_filename": id + ".md", "content_type": "text/markdown"})
		taskExt, _ := json.Marshal(map[string]any{"data_source_type": "MARKET", "files": []map[string]any{{"stored_path": path, "display_name": id + ".md", "relative_path": ""}}})
		f.insert(&orm.Document{ID: id, FileID: id, DatasetID: "control-dataset", DisplayName: id + ".md", BaseModel: base, Ext: docExt})
		f.insert(&orm.Task{ID: id, DocID: id, DatasetID: "control-dataset", LazyllmTaskID: id + "-external", DisplayName: id + ".md", TaskType: "TASK_TYPE_PARSE_UPLOADED", BaseModel: base, Ext: taskExt})
		f.insert(&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: id + "-external", DocID: id, KbID: "control-dataset", TaskType: "DOC_ADD", Status: state, CreatedAt: now, UpdatedAt: now})
		if err := os.WriteFile(path, []byte("# Preserve successful content"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, _ := json.Marshal(map[string]any{"task_ids": ids})
	result, _ := json.Marshal(map[string]any{"dataset_id": "control-dataset", "task_ids": ids, "submitted": len(ids)})
	f.insert(&orm.KnowledgeMarketInstall{MarketItemID: "control-item", UserID: "control-owner", DatasetID: "control-dataset", InstallState: "done", Config: cfg, CreatedAt: now, UpdatedAt: now})
	f.insert(&orm.AsyncJob{ID: "control-job", JobType: "knowledge_market_install", ResourceType: "knowledge_market_item", ResourceID: "control-item", Status: "succeeded", IdempotencyKey: "control-owner-install", CreateUserID: "control-owner", PayloadJSON: json.RawMessage(`{"market_item_id":"control-item","user_id":"control-owner"}`), ResultJSON: result, CreatedAt: now, UpdatedAt: now, NextRunAt: now})
	f.router = mux.NewRouter()
	registerAllRoutes(f.router)
	return f
}

func (f *marketControlFixture) insert(value any) {
	f.t.Helper()
	if err := f.db.Create(value).Error; err != nil {
		f.t.Fatal(err)
	}
}

func (f *marketControlFixture) algorithm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "POST" && r.URL.Path == "/api/scan/internal/source-access/by-dataset:batch" {
		_, _ = w.Write([]byte(`{"items":[{"dataset_id":"control-dataset","exists":false,"allowed":true}]}`))
		return
	}
	if r.Method == "GET" && r.URL.Path == "/ready" {
		w.WriteHeader(int(f.workerStatus.Load()))
		_, _ = w.Write([]byte(`{"code":200,"msg":"success"}`))
		return
	}
	if r.Method == "GET" && r.URL.Path == "/v1/ready" {
		w.WriteHeader(int(f.cancelStatus.Load()))
		_, _ = w.Write([]byte(`{"code":200,"msg":"success"}`))
		return
	}
	if r.Method != "POST" || r.URL.Path != "/v1/tasks/cancel" {
		f.t.Errorf("unexpected Algorithm operation: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
		return
	}
	var body struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Error(err)
		w.WriteHeader(400)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, body.TaskID)
	if f.cancelStatus.Load() != 200 {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"code":503,"msg":"private-upstream-canary"}`))
		return
	}
	var task readonlyorm.LazyLLMDocServiceTaskRow
	if err := f.db.Where("task_id = ?", body.TaskID).Take(&task).Error; err != nil {
		f.t.Error(err)
		w.WriteHeader(404)
		return
	}
	if f.mode == "claimed" {
		if err := f.db.Model(&task).Update("status", "WORKING").Error; err != nil {
			f.t.Error(err)
		}
		task.Status = "WORKING"
	}
	if task.Status != "WAITING" {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"code":409,"msg":"task cannot be canceled","data":{"cancel_status":false}}`))
		return
	}
	if f.mode != "lost-unconfirmed" {
		if err := f.db.Model(&task).Update("status", "CANCELED").Error; err != nil {
			f.t.Error(err)
		}
	}
	if strings.HasPrefix(f.mode, "lost-") {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			f.t.Error(err)
			return
		}
		_ = conn.Close()
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "success", "data": map[string]any{"task_id": body.TaskID, "cancel_status": true, "status": "CANCELED"}})
}

func (f *marketControlFixture) request(method, suffix, owner string) *httptest.ResponseRecorder {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := httptest.NewRequest(method, "/knowledge-market/tasks"+suffix, nil).WithContext(ctx)
	if owner != "" {
		r.Header.Set("X-User-Id", owner)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w
}

func marketControlData(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var envelope struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != 0 {
		t.Fatalf("business failure: %s", w.Body.String())
	}
	return envelope.Data
}

func TestMarketControlPublicStates(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		states                []string
		health                int32
		display               string
		cancel, retry, remove bool
	}{
		{"waiting", []string{"WAITING"}, 200, "pending", true, false, false},
		{"working", []string{"WORKING"}, 200, "processing", false, false, false},
		{"blocked", []string{"WAITING"}, 503, "blocked", true, false, false},
		{"success-survives-outage", []string{"SUCCESS"}, 503, "done", false, false, true},
		{"failed", []string{"FAILED"}, 200, "failed", false, true, true},
		{"retry-unavailable", []string{"FAILED"}, 503, "failed", false, false, true},
		{"partial-failure", []string{"SUCCESS", "FAILED"}, 200, "partial_failed", false, true, true},
		{"canceled", []string{"CANCELED"}, 200, "canceled", false, false, true},
		{"partial-cancel", []string{"SUCCESS", "CANCELED"}, 200, "partial_canceled", false, false, true},
		{"mixed-active", []string{"SUCCESS", "FAILED", "WAITING"}, 200, "pending", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMarketControlFixture(t, tc.states...)
			f.workerStatus.Store(tc.health)
			for _, path := range []string{"/control-job", "?job_type=knowledge_market_install"} {
				data := marketControlData(t, f.request("GET", path, "control-owner"))
				if path[0] == '?' {
					data = data["items"].([]any)[0].(map[string]any)
				}
				if data["display_state"] != tc.display || data["can_cancel"] != tc.cancel || data["can_retry"] != tc.retry || data["can_delete"] != tc.remove {
					t.Errorf("wrong public state/capabilities: %v", data)
				}
			}
			var job orm.AsyncJob
			if err := f.db.Take(&job, "id = ?", "control-job").Error; err != nil {
				t.Fatal(err)
			}
			if job.Status != "succeeded" {
				t.Fatal("a read rewrote persisted submission status")
			}
		})
	}
}

func TestMarketControlUnavailableStatusIsUnknownNotAFileFailure(t *testing.T) {
	f := newMarketControlFixture(t, "WAITING")
	if err := f.db.Migrator().DropTable(&readonlyorm.LazyLLMDocServiceTaskRow{}); err != nil {
		t.Fatal(err)
	}
	data := marketControlData(t, f.request("GET", "/control-job", "control-owner"))
	if data["display_state"] != "unknown" || data["can_cancel"] != false || data["can_retry"] != false || data["can_delete"] != false {
		t.Fatalf("unknown Algorithm state offered unsafe actions: %v", data)
	}
}

func TestMarketControlCancelOnlyWaitingAndPreserveSuccess(t *testing.T) {
	f := newMarketControlFixture(t, "SUCCESS", "WORKING", "WAITING")
	f.workerStatus.Store(503) // Worker outage does not disable the live cancel service.
	var before orm.Task
	if err := f.db.Take(&before, "id = ?", "control-file-0").Error; err != nil {
		t.Fatal(err)
	}
	data := marketControlData(t, f.request("POST", "/control-job:cancel", "control-owner"))
	if data["canceled"] != float64(1) || data["running"] != float64(1) || data["unknown"] != float64(0) {
		t.Fatalf("wrong cancellation result: %v", data)
	}
	if !reflect.DeepEqual(f.calls, []string{"control-file-2-external"}) {
		t.Fatalf("unexpected canceled files: %v", f.calls)
	}
	var after orm.Task
	if err := f.db.Take(&after, "id = ?", before.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("successful task changed")
	}
	var count int64
	if err := f.db.Model(&orm.Document{}).Where("dataset_id = ? AND deleted_at IS NULL", "control-dataset").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("cancel removed documents: %d", count)
	}
	content, err := os.ReadFile(filepath.Join(f.root, "tenants", "root", "datasets", "control-dataset", "docs", "files", "control-file-0", "control-file-0.md"))
	if err != nil || string(content) != "# Preserve successful content" {
		t.Fatalf("success content lost: %v", err)
	}
	current := marketControlData(t, f.request("GET", "/control-job", "control-owner"))
	parse := current["parse"].(map[string]any)
	if parse["done"] != float64(1) || parse["canceled"] != float64(1) || parse["failed"] != float64(0) || parse["parsing"] != float64(1) || current["can_delete"] != false {
		t.Fatalf("partial cancellation was falsely terminal or counted as failure: %v", current)
	}
}

func TestMarketControlCancelClaimRaceAndLostResponse(t *testing.T) {
	for _, tc := range []struct {
		mode                       string
		canceled, running, unknown float64
	}{
		{"claimed", 0, 1, 0}, {"lost-confirmed", 1, 0, 0}, {"lost-unconfirmed", 0, 0, 1},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			f := newMarketControlFixture(t, "WAITING")
			f.mode = tc.mode
			data := marketControlData(t, f.request("POST", "/control-job:cancel", "control-owner"))
			if data["canceled"] != tc.canceled || data["running"] != tc.running || data["unknown"] != tc.unknown {
				t.Fatalf("wrong race outcome: %v", data)
			}
			if len(f.calls) != 1 {
				t.Fatalf("POST was replayed: %v", f.calls)
			}
		})
	}
}

func TestMarketControlCancelIsOwnerScopedAndIdempotent(t *testing.T) {
	f := newMarketControlFixture(t, "WAITING")
	for _, tc := range []struct {
		owner  string
		status int
	}{{"", 401}, {"other-owner", 404}} {
		w := f.request("POST", "/control-job:cancel", tc.owner)
		if w.Code != tc.status {
			t.Errorf("owner=%q got %d want %d", tc.owner, w.Code, tc.status)
		}
	}
	if len(f.calls) != 0 {
		t.Fatal("unauthorized request reached Algorithm")
	}
	for i := 0; i < 2; i++ {
		data := marketControlData(t, f.request("POST", "/control-job:cancel", "control-owner"))
		if data["canceled"] != float64(1) {
			t.Fatalf("repeat cancel lost confirmed outcome: %v", data)
		}
	}
	if len(f.calls) != 1 {
		t.Fatalf("repeat cancel resubmitted: %v", f.calls)
	}
}

func TestMarketControlCancelUnavailableDoesNotPretendSuccess(t *testing.T) {
	f := newMarketControlFixture(t, "WAITING")
	f.cancelStatus.Store(503)
	data := marketControlData(t, f.request("GET", "/control-job", "control-owner"))
	if data["can_cancel"] != false {
		t.Errorf("unavailable cancel service offered cancellation: %v", data)
	}
	w := f.request("POST", "/control-job:cancel", "control-owner")
	if w.Code < 400 || strings.Contains(w.Body.String(), "private-upstream-canary") {
		t.Fatalf("false success or leaked error: %d %s", w.Code, w.Body.String())
	}
	var task readonlyorm.LazyLLMDocServiceTaskRow
	if err := f.db.Take(&task, "task_id = ?", "control-file-0-external").Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "WAITING" {
		t.Fatal("unconfirmed cancellation overwrote Algorithm state")
	}
}

func TestMarketControlPendingJobStopsWithoutCallingAlgorithm(t *testing.T) {
	f := newMarketControlFixture(t)
	if err := f.db.Model(&orm.AsyncJob{}).Where("id = ?", "control-job").Update("status", "pending").Error; err != nil {
		t.Fatal(err)
	}
	marketControlData(t, f.request("POST", "/control-job:cancel", "control-owner"))
	var job orm.AsyncJob
	if err := f.db.Take(&job, "id = ?", "control-job").Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != "canceled" || len(f.calls) != 0 {
		t.Fatalf("queued job not stopped: %s calls=%v", job.Status, f.calls)
	}
}

func TestMarketControlCancelCannotUseStaleDatasetPermission(t *testing.T) {
	f := newMarketControlFixture(t, "WAITING")
	if err := f.db.Model(&orm.Dataset{}).Where("id = ?", "control-dataset").Update("create_user_id", "other-owner").Error; err != nil {
		t.Fatal(err)
	}
	w := f.request("POST", "/control-job:cancel", "control-owner")
	if w.Code != 403 || len(f.calls) != 0 {
		t.Fatalf("stale task ownership bypassed dataset permission: status=%d calls=%v", w.Code, f.calls)
	}
}

func TestMarketControlStopRunningDownload(t *testing.T) {
	f := newMarketControlFixture(t)
	started := make(chan struct{})
	interrupted := make(chan struct{})
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(interrupted)
	}))
	t.Cleanup(source.Close)
	if err := f.db.Model(&orm.KnowledgeMarketItem{}).Where("id = ?", "control-item").Update("package_url", source.URL+"/fixture.md").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&orm.AsyncJob{}).Where("id = ?", "control-job").Update("status", "pending").Error; err != nil {
		t.Fatal(err)
	}
	knowledge_market.RegisterAsyncJobs()
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	runner := asyncjob.Start(ctx, f.db.DB, asyncjob.Options{JobTypes: []string{"knowledge_market_install"}, Concurrency: 1, PollInterval: 10 * time.Millisecond, LockTTL: 300 * time.Millisecond})
	t.Cleanup(func() { stop(); <-runner.Done() })
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("real install runner did not start download")
	}
	marketControlData(t, f.request("POST", "/control-job:cancel", "control-owner"))
	select {
	case <-interrupted:
	case <-ctx.Done():
		t.Fatal("stop did not interrupt the Go-owned download")
	}
	stop()
	<-runner.Done()
	var job orm.AsyncJob
	if err := f.db.Take(&job, "id = ?", "control-job").Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != "canceled" {
		t.Fatalf("runner finalization overwrote stop: %s", job.Status)
	}
	var count int64
	if err := f.db.Model(&orm.Task{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("stopped download continued to document submission")
	}
}

func TestMarketControlConcurrentCancelPostgres(t *testing.T) {
	if os.Getenv("TEST_DB_DRIVER") != "postgres" {
		t.Skip("requires isolated PostgreSQL schemas")
	}
	f := newMarketControlFixture(t, "WAITING")
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; responses <- f.request("POST", "/control-job:cancel", "control-owner") }()
	}
	close(start)
	var received []*httptest.ResponseRecorder
	for i := 0; i < 2; i++ {
		select {
		case response := <-responses:
			received = append(received, response)
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent cancellation deadlocked")
		}
	}
	for _, response := range received {
		data := marketControlData(t, response)
		if data["canceled"] != float64(1) || data["unknown"] != float64(0) {
			t.Fatalf("concurrent cancellation lost confirmed outcome: %v", data)
		}
	}
	var task readonlyorm.LazyLLMDocServiceTaskRow
	if err := f.db.Take(&task, "task_id = ?", "control-file-0-external").Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "CANCELED" {
		t.Fatalf("unexpected final state: %s", task.Status)
	}
}

func TestMarketControlStoppedSubmissionWithWorkingFilesIsNotTerminal(t *testing.T) {
	f := newMarketControlFixture(t, "SUCCESS", "WORKING")
	if err := f.db.Model(&orm.AsyncJob{}).Where("id = ?", "control-job").Update("status", "canceled").Error; err != nil {
		t.Fatal(err)
	}
	data := marketControlData(t, f.request("GET", "/control-job", "control-owner"))
	if data["display_state"] != "processing" || data["can_retry"] != false || data["can_delete"] != false {
		t.Fatalf("submission stop hid still-working files: %v", data)
	}
}

func TestMarketControlRejectsHistoricalAndBatchCancellation(t *testing.T) {
	for _, kind := range []string{"historical", "batch"} {
		t.Run(kind, func(t *testing.T) {
			f := newMarketControlFixture(t, "WAITING")
			if kind == "historical" {
				var newer orm.AsyncJob
				if err := f.db.Take(&newer, "id = ?", "control-job").Error; err != nil {
					t.Fatal(err)
				}
				newer.ID, newer.IdempotencyKey = "newer-job", "newer-install"
				newer.CreatedAt = newer.CreatedAt.Add(time.Minute)
				f.insert(&newer)
			} else if err := f.db.Model(&orm.AsyncJob{}).Where("id = ?", "control-job").Update("job_type", "knowledge_market_update_all").Error; err != nil {
				t.Fatal(err)
			}
			w := f.request("POST", "/control-job:cancel", "control-owner")
			if w.Code != 409 || len(f.calls) != 0 {
				t.Fatalf("unsupported cancellation reached Algorithm: status=%d calls=%v", w.Code, f.calls)
			}
		})
	}
}

func TestMarketControlUnsafeRetryAndDeleteAreRejectedByRoutes(t *testing.T) {
	for _, tc := range []struct {
		name, method, suffix string
		unknown              bool
	}{
		{"unknown-retry", "POST", "/control-job:retry", true},
		{"unknown-delete", "DELETE", "/control-job", true},
		{"unavailable-retry", "POST", "/control-job:retry", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMarketControlFixture(t, "FAILED")
			var before orm.AsyncJob
			if err := f.db.Take(&before, "id = ?", "control-job").Error; err != nil {
				t.Fatal(err)
			}
			if tc.unknown {
				if err := f.db.Migrator().DropTable(&readonlyorm.LazyLLMDocServiceTaskRow{}); err != nil {
					t.Fatal(err)
				}
			} else {
				f.workerStatus.Store(503)
			}
			w := f.request(tc.method, tc.suffix, "control-owner")
			if w.Code < 400 {
				t.Errorf("unsafe operation accepted: %d %s", w.Code, w.Body.String())
			}
			var jobs []orm.AsyncJob
			if err := f.db.Find(&jobs).Error; err != nil {
				t.Fatal(err)
			}
			if len(jobs) != 1 || jobs[0].ID != "control-job" {
				t.Fatalf("unsafe operation created or deleted jobs: %+v", jobs)
			}
			if !reflect.DeepEqual(before, jobs[0]) {
				t.Fatal("rejected operation changed the original task")
			}
			if len(f.calls) != 0 {
				t.Fatal("unsafe operation called Algorithm")
			}
		})
	}
}

func waitMarketControlJob(t *testing.T, f *marketControlFixture, id string) orm.AsyncJob {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var job orm.AsyncJob
		if err := f.db.Take(&job, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if job.Status != "running" && job.Status != "pending" {
			return job
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("job did not reach a terminal submission state")
		}
	}
}

func TestMarketControlStoppedJobRetryRoutePreservesSuccessAndCancellation(t *testing.T) {
	f := newMarketControlFixture(t, "SUCCESS", "CANCELED", "FAILED")
	// Use Core's supported stored mode to observe exactly which tasks the
	// public retry route submits, without claiming Algorithm/vector behavior.
	if err := f.db.Where("1 = 1").Delete(&readonlyorm.LazyLLMDocServiceTaskRow{}).Error; err != nil {
		t.Fatal(err)
	}
	for i, state := range []string{"SUCCESS", "CANCELED", "FAILED"} {
		ext, _ := json.Marshal(map[string]any{"task_state": state, "data_source_type": "MARKET"})
		if err := f.db.Model(&orm.Task{}).Where("id = ?", fmt.Sprintf("control-file-%d", i)).Updates(map[string]any{"lazyllm_task_id": "", "ext": ext}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Model(&orm.AsyncJob{}).Where("id = ?", "control-job").Update("status", "canceled").Error; err != nil {
		t.Fatal(err)
	}
	var before []orm.Task
	if err := f.db.Where("id IN ?", []string{"control-file-0", "control-file-1"}).Order("id").Find(&before).Error; err != nil {
		t.Fatal(err)
	}
	data := marketControlData(t, f.request("POST", "/control-job:retry", "control-owner"))
	id, ok := data["job_id"].(string)
	if !ok || id == "control-job" {
		t.Fatalf("retry did not retain history: %v", data)
	}
	knowledge_market.RegisterAsyncJobs()
	ctx, stop := context.WithCancel(context.Background())
	runner := asyncjob.Start(ctx, f.db.DB, asyncjob.Options{JobTypes: []string{"knowledge_market_install", "knowledge_market_update"}, Concurrency: 1, PollInterval: 10 * time.Millisecond})
	t.Cleanup(func() { stop(); <-runner.Done() })
	job := waitMarketControlJob(t, f, id)
	stop()
	<-runner.Done()
	if job.Status != "succeeded" {
		t.Fatalf("retry failed: %+v", job)
	}
	var result struct {
		Submitted int `json:"submitted"`
	}
	if err := json.Unmarshal(job.ResultJSON, &result); err != nil {
		t.Fatal(err)
	}
	if result.Submitted != 1 {
		t.Errorf("retry submitted %d files instead of the one failed file", result.Submitted)
	}
	var after []orm.Task
	if err := f.db.Where("id IN ?", []string{"control-file-0", "control-file-1"}).Order("id").Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("public retry changed successful or canceled files")
	}
	for _, id := range []string{"control-file-0", "control-file-1"} {
		var document orm.Document
		if err := f.db.Where("id = ? AND deleted_at IS NULL", id).Take(&document).Error; err != nil {
			t.Fatal(err)
		}
		var ext struct {
			Path string `json:"stored_path"`
		}
		if err := json.Unmarshal(document.Ext, &ext); err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(ext.Path)
		if err != nil || string(content) != "# Preserve successful content" {
			t.Fatalf("retry altered retained file: %s %v", id, err)
		}
	}
}

// Full Handler/Runner interruption test. The Algorithm fixture commits the
// first batch before its HTTP response, a supported response-loss window.
func TestMarketControlStopDuringImportKeepsTraceableResults(t *testing.T) {
	for _, kind := range []string{"knowledge_market_install", "knowledge_market_update"} {
		t.Run(kind, func(t *testing.T) {
			f := newMarketControlFixture(t)
			if err := f.db.AutoMigrate(&orm.UserSelectedCloudModel{}, &orm.UserSelectedModel{}, &orm.UserUIPreferences{}, &orm.UserSelectedProvider{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{}, &orm.UserModelProvider{}); err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&orm.Dataset{}).Where("id = ?", "control-dataset").Update("processing_level", "indexed").Error; err != nil {
				t.Fatal(err)
			}
			var archive bytes.Buffer
			zw := zip.NewWriter(&archive)
			for i := 0; i < 51; i++ {
				file, err := zw.Create(fmt.Sprintf("fixture-%02d.md", i))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = file.Write([]byte("# Committed fixture")); err != nil {
					t.Fatal(err)
				}
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			accepted, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			var submitted atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == "GET" && r.URL.Path == "/fixture.zip":
					_, _ = w.Write(archive.Bytes())
				case r.URL.Path == "/api/model/role_type":
					_, _ = w.Write([]byte(`{"type":"online","is_dynamic":false}`))
				case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/algo/"):
					_, _ = w.Write([]byte(`{"code":200,"data":[]}`))
				case r.Method == "POST" && r.URL.Path == "/v1/kbs":
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					_ = json.NewEncoder(w).Encode(map[string]any{"kb_id": body["kb_id"]})
				case r.Method == "POST" && r.URL.Path == "/v1/docs/add":
					var body struct {
						KbID  string `json:"kb_id"`
						Items []struct {
							DocID    string         `json:"doc_id"`
							Metadata map[string]any `json:"metadata"`
						} `json:"items"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					first := submitted.Add(int32(len(body.Items))) == int32(len(body.Items))
					items := make([]map[string]any, 0, len(body.Items))
					for _, item := range body.Items {
						id := item.DocID + "-external"
						row := readonlyorm.LazyLLMDocServiceTaskRow{TaskID: id, DocID: item.DocID, KbID: body.KbID, TaskType: "DOC_ADD", Status: "SUCCESS", CreatedAt: time.Now(), UpdatedAt: time.Now()}
						if err := f.db.Create(&row).Error; err != nil {
							t.Error(err)
							w.WriteHeader(500)
							return
						}
						items = append(items, map[string]any{"task_id": id, "doc_id": item.DocID, "metadata": item.Metadata})
					}
					if first {
						close(accepted)
						select {
						case <-release:
						case <-r.Context().Done():
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"items": items}})
				default:
					f.algorithm(w, r)
				}
			}))
			t.Cleanup(server.Close)
			for _, name := range []string{"LAZYMIND_DOCUMENT_SERVICE_URL", "LAZYMIND_DOCUMENT_WORKER_URL", "LAZYMIND_ALGO_SERVICE_URL", "LAZYMIND_CHAT_SERVICE_URL"} {
				t.Setenv(name, server.URL)
			}
			if err := f.db.Model(&orm.KnowledgeMarketItem{}).Where("id = ?", "control-item").Update("package_url", server.URL+"/fixture.zip").Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&orm.AsyncJob{}).Where("id = ?", "control-job").Updates(map[string]any{"status": "pending", "job_type": kind}).Error; err != nil {
				t.Fatal(err)
			}
			knowledge_market.RegisterAsyncJobs()
			finished := make(chan struct{})
			asyncjob.Register(kind, func(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
				defer close(finished)
				if kind == "knowledge_market_install" {
					return knowledge_market.HandleInstallJob(ctx, job, reporter)
				}
				return knowledge_market.HandleUpdateJob(ctx, job, reporter)
			})
			t.Cleanup(knowledge_market.RegisterAsyncJobs)
			ctx, stop := context.WithTimeout(context.Background(), 8*time.Second)
			runner := asyncjob.Start(ctx, f.db.DB, asyncjob.Options{JobTypes: []string{kind}, Concurrency: 1, PollInterval: 10 * time.Millisecond, LockTTL: 300 * time.Millisecond})
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); stop(); <-runner.Done() })
			select {
			case <-accepted:
			case <-ctx.Done():
				t.Fatal("handler did not reach first Algorithm submission")
			}
			marketControlData(t, f.request("POST", "/control-job:cancel", "control-owner"))
			releaseOnce.Do(func() { close(release) })
			// Drain the real handler before inspecting the public result; a canceled
			// job flag alone is not proof that its import loop has stopped.
			select {
			case <-finished:
			case <-ctx.Done():
				t.Fatal("cancel did not stop the real import handler")
			}
			stop()
			<-runner.Done()
			if submitted.Load() != 50 {
				t.Errorf("stop allowed later batch: submitted=%d", submitted.Load())
			}
			data := marketControlData(t, f.request("GET", "/control-job", "control-owner"))
			parse, ok := data["parse"].(map[string]any)
			if !ok || parse["total"] != float64(51) || parse["done"] != float64(50) || parse["canceled"] != float64(1) || parse["pending"] != float64(0) || parse["parsing"] != float64(0) || data["display_state"] != "partial_canceled" {
				t.Fatalf("interrupted import lost associations or stranded files: %v", data)
			}
			var install orm.KnowledgeMarketInstall
			if err := f.db.Take(&install, "market_item_id = ? AND user_id = ?", "control-item", "control-owner").Error; err != nil {
				t.Fatal(err)
			}
			if install.DatasetID == "" || data["dataset_id"] != install.DatasetID {
				t.Fatal("dataset association was lost")
			}
			var documents []orm.Document
			if err := f.db.Where("dataset_id = ? AND deleted_at IS NULL", install.DatasetID).Find(&documents).Error; err != nil {
				t.Fatal(err)
			}
			if len(documents) != 51 {
				t.Fatalf("registered documents lost: %d", len(documents))
			}
			for _, document := range documents {
				var ext struct {
					Path string `json:"stored_path"`
				}
				if err := json.Unmarshal(document.Ext, &ext); err != nil {
					t.Fatal(err)
				}
				content, err := os.ReadFile(ext.Path)
				if err != nil || string(content) != "# Committed fixture" {
					t.Fatalf("actual stored file lost: %s %v", document.ID, err)
				}
			}
		})
	}
}
