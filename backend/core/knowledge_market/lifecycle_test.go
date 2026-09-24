package knowledge_market

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

func TestMarketPendingUpdateDoesNotInheritPreviousFailure(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	now := time.Now().UTC()
	insertInstallJob(t, db, "new-update", "user-a", "law-cn", "pending", now, 0, 0, "")
	if err := db.Model(&orm.AsyncJob{}).Where("id = ?", "new-update").Update("job_type", updateJobType).Error; err != nil {
		t.Fatal(err)
	}
	insertInstallWithConfig(t, db, "law-cn", "user-a", "done", "ds", `{"task_ids":["old-failure"]}`, now)
	insertTask(t, db, "old-failure", "ds", "FAILED")
	data := mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks/new-update", "user-a"))
	if data["stage"] != "pending" || data["overall_percent"] != float64(0) {
		t.Fatalf("new queued update inherited old result: stage=%v percent=%v", data["stage"], data["overall_percent"])
	}
}

func TestFailedUpdateRetryReplaysDownload(t *testing.T) {
	router := newTaskTestRouter(t)
	router.HandleFunc("/knowledge-market/tasks/{job_id}:retry", MarketRetryTask).Methods(http.MethodPost)
	db := store.DB()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.Document{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Dataset{ID: "ds", KbID: "ds", ProcessingLevel: "stored", Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: "user-a", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.KnowledgeMarketItem{}).Where("id = ?", "law-cn").Update("package_url", "https://example.test/new.md").Error; err != nil {
		t.Fatal(err)
	}
	insertInstallWithConfig(t, db, "law-cn", "user-a", "done", "ds", `{"task_ids":["old-success"]}`, now)
	insertTask(t, db, "old-success", "ds", "SUCCEEDED")
	insertInstallJob(t, db, "download-failed", "user-a", "law-cn", "failed", now, 0, 3, "")
	if err := db.Model(&orm.AsyncJob{}).Where("id = ?", "download-failed").Update("job_type", updateJobType).Error; err != nil {
		t.Fatal(err)
	}
	data := mustTaskData(t, performPost(t, router, "/knowledge-market/tasks/download-failed:retry", "user-a"))
	var retry orm.AsyncJob
	if err := db.Take(&retry, "id = ?", data["job_id"]).Error; err != nil {
		t.Fatal(err)
	}
	var payload updateJobPayload
	if err := json.Unmarshal(retry.PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.RetryOnly {
		t.Fatal("download failure was converted to parse-only retry")
	}
	previous := http.DefaultTransport
	calls := 0
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://example.test/new.md" {
			t.Fatalf("unexpected request %s", r.URL)
		}
		calls++
		return nil, errors.New("controlled download failure")
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	_, err := HandleUpdateJob(context.Background(), asyncjob.Job{ID: retry.ID, PayloadJSON: retry.PayloadJSON}, nil)
	if err == nil || calls != 1 {
		t.Fatalf("retry skipped failed download: calls=%d error=%v", calls, err)
	}
	// Once the source recovers, the same retry payload must import V2 rather
	// than report no_change based on V1's successful parse results.
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://example.test/new.md" {
			t.Fatalf("unexpected request %s", r.URL)
		}
		calls++
		return testJSONResponse(http.StatusOK, "# V2\nUpdated synthetic content"), nil
	})
	result, err := HandleUpdateJob(context.Background(), asyncjob.Job{ID: retry.ID, PayloadJSON: retry.PayloadJSON}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var completed struct {
		Updated   bool     `json:"updated"`
		TaskIDs   []string `json:"task_ids"`
		Submitted int      `json:"submitted"`
	}
	if err := json.Unmarshal(result.ResultJSON, &completed); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !completed.Updated || completed.Submitted != 1 || len(completed.TaskIDs) != 1 || completed.TaskIDs[0] == "old-success" {
		t.Fatalf("retry did not update V2: %+v", completed)
	}
}

func TestDeletedMarketTasksAreMissingNotUsable(t *testing.T) {
	for _, state := range []string{"SUCCEEDED", "FAILED"} {
		t.Run(state, func(t *testing.T) {
			newTaskTestRouter(t)
			db := store.DB()
			now := time.Now().UTC()
			insertInstallWithConfig(t, db, "law-cn", "user-a", "done", "ds", `{"task_ids":["deleted"]}`, now)
			insertTask(t, db, "deleted", "ds", state)
			if err := db.Model(&orm.Task{}).Where("id = ?", "deleted").Update("deleted_at", now).Error; err != nil {
				t.Fatal(err)
			}
			var install orm.KnowledgeMarketInstall
			if err := db.Take(&install, "market_item_id = ? AND user_id = ?", "law-cn", "user-a").Error; err != nil {
				t.Fatal(err)
			}
			parse := parseProgress((&http.Request{}).WithContext(context.Background()), db, &install)
			if parse.Done != 0 || parse.Failed != 1 || parse.Failures[0].Reason != "missing_task" || canRetryMarketParse(parse) {
				t.Fatalf("deleted file reused: %+v", parse)
			}
			if err := db.AutoMigrate(&orm.Dataset{}, &orm.Document{}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
			if err := db.Create(&orm.Dataset{ID: "ds", KbID: "ds", ProcessingLevel: "stored", Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: "user-a", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "restored.md")
			if err := os.WriteFile(path, []byte("# Restored synthetic file"), 0600); err != nil {
				t.Fatal(err)
			}
			result, err := repairMarketImport(context.Background(), db, &install, "Test", []doc.MarketImportFile{{LocalPath: path, DisplayName: "restored.md"}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Submitted != 1 || len(result.TaskIDs) != 1 || result.TaskIDs[0] == "deleted" {
				t.Fatalf("deleted file was not restored: %+v", result)
			}
		})
	}
}

func TestBatchRetryPreservesFailedAttempt(t *testing.T) {
	router := newTaskTestRouter(t)
	router.HandleFunc("/knowledge-market/tasks/{job_id}:retry", MarketRetryTask).Methods(http.MethodPost)
	db := store.DB()
	now := time.Now().UTC()
	job := orm.AsyncJob{ID: "failed-batch", JobType: updateAllJobType, ResourceID: "user-a", ResourceType: "knowledge_market_user", Status: "failed", IdempotencyKey: "kb_update_all:user-a", CreateUserID: "user-a", CreatedAt: now, UpdatedAt: now, NextRunAt: now, ErrorMessage: "original failure", PayloadJSON: json.RawMessage(`{"user_id":"user-a"}`)}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	data := mustTaskData(t, performPost(t, router, "/knowledge-market/tasks/failed-batch:retry", "user-a"))
	if data["job_id"] == job.ID {
		t.Fatal("batch retry overwrote history")
	}
	var old orm.AsyncJob
	if err := db.Take(&old, "id = ?", job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if old.Status != "failed" || old.ErrorMessage != "original failure" {
		t.Fatal("batch failure history was changed")
	}
}

func TestRepairMarketImportRetainsSuccessAndRestoresUnregisteredFiles(t *testing.T) {
	newTaskTestRouter(t)
	db := store.DB()
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.Document{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	now := time.Now().UTC()
	if err := db.Create(&orm.Dataset{ID: "ds", KbID: "ds", ProcessingLevel: "stored", Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: "user-a", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	insertInstallWithConfig(t, db, "law-cn", "user-a", "done", "ds", `{"task_ids":["ok"],"failures":[{"name":"bad.md","reason":"import_failed"}]}`, now)
	insertTask(t, db, "ok", "ds", "SUCCEEDED")
	if err := db.Model(&orm.Task{}).Where("id = ?", "ok").Update("display_name", "good.md").Error; err != nil {
		t.Fatal(err)
	}
	var install orm.KnowledgeMarketInstall
	if err := db.Take(&install, "market_item_id = ? AND user_id = ?", "law-cn", "user-a").Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "small.md")
	if err := os.WriteFile(path, []byte("# Small repair fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := repairMarketImport(context.Background(), db, &install, "Test", []doc.MarketImportFile{{LocalPath: path, DisplayName: "good.md"}, {LocalPath: path, DisplayName: "bad.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.TaskIDs) != 2 || len(result.Failures) != 0 || result.Submitted != 1 {
		t.Fatalf("repair duplicated success or failed to import missing file: %+v", result)
	}
}

func TestMarketRetryResubmitsOnlyFailedFiles(t *testing.T) {
	newTaskTestRouter(t)
	db := store.DB()
	if err := db.AutoMigrate(&orm.Dataset{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Dataset{ID: "ds", KbID: "ds", ProcessingLevel: "stored", Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: "user-a", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	insertInstallWithConfig(t, db, "law-cn", "user-a", "done", "ds", `{"task_ids":["ok","bad"]}`, now)
	insertTask(t, db, "ok", "ds", "SUCCEEDED")
	insertTask(t, db, "bad", "ds", "FAILED")
	var before orm.Task
	if err := db.Take(&before, "id = ?", "ok").Error; err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(updateJobPayload{MarketItemID: "law-cn", UserID: "user-a", RetryOnly: true})
	result, err := HandleUpdateJob(context.Background(), asyncjob.Job{ID: "retry", PayloadJSON: payload}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Submitted int `json:"submitted"`
	}
	if err := json.Unmarshal(result.ResultJSON, &out); err != nil {
		t.Fatal(err)
	}
	if out.Submitted != 1 {
		t.Fatalf("resubmitted %d files, want only the failed file", out.Submitted)
	}
	var after orm.Task
	if err := db.Take(&after, "id = ?", "ok").Error; err != nil {
		t.Fatal(err)
	}
	if string(after.Ext) != string(before.Ext) || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatal("retry modified successful file")
	}
	var install orm.KnowledgeMarketInstall
	if err := db.Take(&install, "market_item_id = ? AND user_id = ?", "law-cn", "user-a").Error; err != nil {
		t.Fatal(err)
	}
	p := parseProgress((&http.Request{}).WithContext(context.Background()), db, &install)
	if p.Done != 2 || p.Failed != 0 {
		t.Fatalf("retry didn't recover file: %+v", p)
	}
}

func TestMarketTaskReadDoesNotTerminateRunningRetry(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	now := time.Now().UTC()
	insertInstallJob(t, db, "retry", "user-a", "law-cn", "running", now, 0, 0, "")
	insertInstall(t, db, "law-cn", "user-a", "failed", "ds", now)
	for _, path := range []string{"/knowledge-market/tasks/retry", "/knowledge-market/tasks", "/knowledge-market/installs"} {
		mustTaskData(t, performGetWithUser(t, router, path, "user-a"))
		var job orm.AsyncJob
		if err := db.Take(&job, "id = ?", "retry").Error; err != nil {
			t.Fatal(err)
		}
		if job.Status != "running" {
			t.Fatalf("GET %s changed executing job to %s", path, job.Status)
		}
	}
}

func TestMarketHistoricalTaskUsesItsOwnFiles(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	now := time.Now().UTC()
	insertInstallJob(t, db, "old-job", "user-a", "law-cn", "succeeded", now.Add(-time.Hour), 2, 2,
		`{"dataset_id":"ds-old","submitted":1,"task_ids":["old-success"]}`)
	insertInstallWithConfig(t, db, "law-cn", "user-a", "done", "ds-new", `{"task_ids":["new-failure"]}`, now)
	insertTask(t, db, "old-success", "ds-old", "SUCCEEDED")
	insertTask(t, db, "new-failure", "ds-new", "FAILED")
	data := mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks/old-job", "user-a"))
	if data["stage"] != "done" || data["dataset_id"] != "ds-old" {
		t.Fatalf("historical result changed: %+v", data)
	}
}

func TestMarketPartialFailureFinishesProcessing(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	now := time.Now().UTC()
	insertInstallJob(t, db, "partial", "user-a", "law-cn", "succeeded", now, 2, 2, `{"dataset_id":"ds","task_ids":["ok","bad"]}`)
	insertInstallWithConfig(t, db, "law-cn", "user-a", "done", "ds", `{"task_ids":["ok","bad"]}`, now)
	insertTask(t, db, "ok", "ds", "SUCCEEDED")
	insertTask(t, db, "bad", "ds", "FAILED")
	data := mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks/partial", "user-a"))
	if data["stage"] != "partial_failed" || data["overall_percent"] != float64(100) {
		t.Fatalf("partial result is not fully processed: %+v", data)
	}
	parse := data["parse"].(map[string]any)
	if parse["done"] != float64(1) || parse["failed"] != float64(1) {
		t.Fatalf("incorrect file counts: %+v", parse)
	}
	files, ok := parse["failures"].([]any)
	if !ok || len(files) != 1 || files[0].(map[string]any)["name"] != "file_bad" {
		t.Fatalf("missing failure detail: %+v", parse)
	}
}

func TestMarketMissingParseTaskDoesNotRunForever(t *testing.T) {
	router := newTaskTestRouter(t)
	db := store.DB()
	now := time.Now().UTC()
	insertInstallJob(t, db, "missing", "user-a", "law-cn", "succeeded", now, 2, 2, `{"dataset_id":"ds","task_ids":["ok","missing"]}`)
	insertInstallWithConfig(t, db, "law-cn", "user-a", "done", "ds", `{"task_ids":["ok","missing"]}`, now)
	insertTask(t, db, "ok", "ds", "SUCCEEDED")
	data := mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks/missing", "user-a"))
	if data["stage"] != "partial_failed" {
		t.Fatalf("missing file never terminates: %+v", data)
	}
}

func TestMarketInstallAfterParseFailureEnqueuesNewWork(t *testing.T) {
	newTaskTestRouter(t)
	db := store.DB()
	if err := db.Model(&orm.KnowledgeMarketItem{}).Where("id = ?", "law-cn").Update("package_url", "https://example.test/tiny.zip").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	insertInstallJob(t, db, "submitted", "user-a", "law-cn", "succeeded", now, 2, 2, `{"dataset_id":"ds","task_ids":["bad"]}`)
	insertInstallWithConfig(t, db, "law-cn", "user-a", "done", "ds", `{"task_ids":["bad"]}`, now)
	insertTask(t, db, "bad", "ds", "FAILED")
	req := httptest.NewRequest(http.MethodPost, "/knowledge-market/items/law-cn:install", nil)
	req.Header.Set("X-User-Id", "user-a")
	req = mux.SetURLVars(req, map[string]string{"market_item_id": "law-cn"})
	rec := httptest.NewRecorder()
	MarketInstall(rec, req)
	data := mustTaskData(t, rec)
	if data["job_id"] == "submitted" {
		t.Fatal("reinstall reused submission success without retrying parsing")
	}
	var old orm.AsyncJob
	if err := db.Take(&old, "id = ?", "submitted").Error; err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(old.ResultJSON, &result); err != nil {
		t.Fatal(err)
	}
}

func TestMarketTaskActionsRespectOwnershipAndProcessing(t *testing.T) {
	router := newTaskTestRouter(t)
	router.HandleFunc("/knowledge-market/tasks/{job_id}", MarketDeleteTask).Methods(http.MethodDelete)
	router.HandleFunc("/knowledge-market/tasks/{job_id}:retry", MarketRetryTask).Methods(http.MethodPost)
	db := store.DB()
	now := time.Now().UTC()
	insertInstallJob(t, db, "active", "user-a", "law-cn", "running", now, 0, 2, "")
	insertInstall(t, db, "law-cn", "user-a", "done", "ds", now)
	request := func(method, path, user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("X-User-Id", user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	for _, method := range []string{http.MethodDelete, http.MethodPost} {
		path := "/knowledge-market/tasks/active"
		if method == http.MethodPost {
			path += ":retry"
		}
		if got := request(method, path, "other"); got.Code != http.StatusNotFound {
			t.Fatalf("other owner: %d %s", got.Code, got.Body.String())
		}
		if got := request(method, path, "user-a"); got.Code != http.StatusConflict {
			t.Fatalf("active task: %d %s", got.Code, got.Body.String())
		}
	}
	if err := db.Model(&orm.AsyncJob{}).Where("id = ?", "active").Updates(map[string]any{"status": "succeeded", "result_json": json.RawMessage(`{"dataset_id":"ds","task_ids":["working"]}`)}).Error; err != nil {
		t.Fatal(err)
	}
	insertTask(t, db, "working", "ds", "RUNNING")
	if got := request(http.MethodDelete, "/knowledge-market/tasks/active", "user-a"); got.Code != http.StatusConflict {
		t.Fatalf("parsing task must not be deleted: %d", got.Code)
	}
	if err := db.Model(&orm.Task{}).Where("id = ?", "working").Update("ext", json.RawMessage(`{"task_state":"SUCCEEDED"}`)).Error; err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodDelete, "/knowledge-market/tasks/active", "user-a"); got.Code != http.StatusOK {
		t.Fatalf("terminal delete: %d %s", got.Code, got.Body.String())
	}
	var installs, files int64
	db.Model(&orm.KnowledgeMarketInstall{}).Count(&installs)
	db.Model(&orm.Task{}).Count(&files)
	if installs != 1 || files != 1 {
		t.Fatalf("history deletion touched knowledge: installs=%d files=%d", installs, files)
	}
}

func TestMarketRetryKeepsPreviousFailureHistory(t *testing.T) {
	router := newTaskTestRouter(t)
	router.HandleFunc("/knowledge-market/tasks/{job_id}:retry", MarketRetryTask).Methods(http.MethodPost)
	db := store.DB()
	now := time.Now().UTC()
	if err := db.Create(&orm.Dataset{ID: "ds", KbID: "ds", ProcessingLevel: "stored", Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: "user-a", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	insertInstallJob(t, db, "old-failed", "user-a", "law-cn", "succeeded", now, 2, 2, `{"dataset_id":"ds","task_ids":["bad"]}`)
	insertInstallWithConfig(t, db, "law-cn", "user-a", "done", "ds", `{"task_ids":["bad"]}`, now)
	insertTask(t, db, "bad", "ds", "FAILED")
	data := mustTaskData(t, performPost(t, router, "/knowledge-market/tasks/old-failed:retry", "user-a"))
	if data["job_id"] == "old-failed" {
		t.Fatal("retry must preserve history")
	}
	if err := db.Model(&orm.Task{}).Where("id = ?", "bad").Update("ext", json.RawMessage(`{"task_state":"RUNNING"}`)).Error; err != nil {
		t.Fatal(err)
	}
	history := mustTaskData(t, performGetWithUser(t, router, "/knowledge-market/tasks/old-failed", "user-a"))
	if history["stage"] != "failed" {
		t.Fatalf("retry changed original history: %+v", history)
	}
}
