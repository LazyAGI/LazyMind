package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/knowledge_market"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestMarketControlPendingUpdateCancellationPreservesPreviousContent(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(map[bool]string{false: "owner", true: "revoked"}[revoked], func(t *testing.T) {
			f := newMarketControlFixture(t, "SUCCESS")
			if err := f.db.Model(&orm.AsyncJob{}).Where("id = ?", "control-job").Updates(map[string]any{"status": "pending", "job_type": "knowledge_market_update", "result_json": nil}).Error; err != nil {
				t.Fatal(err)
			}
			if revoked {
				if err := f.db.Model(&orm.Dataset{}).Where("id = ?", "control-dataset").Update("create_user_id", "other-owner").Error; err != nil {
					t.Fatal(err)
				}
			}
			response := f.request("POST", "/control-job:cancel", "control-owner")
			var job orm.AsyncJob
			if err := f.db.Take(&job, "id = ?", "control-job").Error; err != nil {
				t.Fatal(err)
			}
			if revoked {
				if response.Code != 403 || job.Status != "pending" {
					t.Fatalf("revoked owner canceled pending update: %d %s", response.Code, job.Status)
				}
				return
			}
			marketControlData(t, response)
			data := marketControlData(t, f.request("GET", "/control-job", "control-owner"))
			if data["display_state"] != "canceled" {
				t.Fatalf("borrowed previous successful result: %v", data)
			}
			var count int64
			f.db.Model(&readonlyorm.LazyLLMDocServiceTaskRow{}).Where("status = ?", "SUCCESS").Count(&count)
			if count != 1 || len(f.calls) != 0 {
				t.Fatal("queued stop changed successful files")
			}
		})
	}
}

func TestMarketControlCanStopGoWorkWhenCancelServiceIsDown(t *testing.T) {
	f := newMarketControlFixture(t, "WAITING")
	f.cancelStatus.Store(503)
	if err := f.db.Model(&orm.AsyncJob{}).Where("id = ?", "control-job").Update("status", "running").Error; err != nil {
		t.Fatal(err)
	}
	data := marketControlData(t, f.request("GET", "/control-job", "control-owner"))
	if data["can_cancel"] != true {
		t.Errorf("Go stop was disabled by Algorithm outage: %v", data)
	}
	response := marketControlData(t, f.request("POST", "/control-job:cancel", "control-owner"))
	if response["stop_requested"] != true || len(f.calls) != 0 {
		t.Fatalf("Go stop depended on Algorithm: %v", response)
	}
}

func TestMarketControlCancelAfterHandlerReturnsClearsLease(t *testing.T) {
	f := newMarketControlFixture(t, "SUCCESS")
	payload := json.RawMessage(`{"market_item_id":"control-item","user_id":"control-owner","retry_only":true}`)
	if err := f.db.Model(&orm.AsyncJob{}).Where("id = ?", "control-job").Updates(map[string]any{"status": "pending", "job_type": "knowledge_market_update", "payload_json": payload}).Error; err != nil {
		t.Fatal(err)
	}
	returned, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	knowledge_market.RegisterAsyncJobs()
	t.Cleanup(knowledge_market.RegisterAsyncJobs)
	asyncjob.Register("knowledge_market_update", func(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
		result, err := knowledge_market.HandleUpdateJob(ctx, job, reporter)
		close(returned)
		<-release
		return result, err
	})
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	runner := asyncjob.Start(ctx, f.db.DB, asyncjob.Options{JobTypes: []string{"knowledge_market_update"}, Concurrency: 1, PollInterval: 10 * time.Millisecond})
	t.Cleanup(func() { once.Do(func() { close(release) }); stop(); <-runner.Done() })
	select {
	case <-returned:
	case <-ctx.Done():
		t.Fatal("handler did not finish")
	}
	marketControlData(t, f.request("POST", "/control-job:cancel", "control-owner"))
	once.Do(func() { close(release) })
	stop()
	<-runner.Done()
	var job orm.AsyncJob
	if err := f.db.Take(&job, "id = ?", "control-job").Error; err != nil {
		t.Fatal(err)
	}
	if job.LockedBy != "" || job.LockUntil != nil {
		t.Fatalf("finished canceled attempt kept lease: %s %v", job.LockedBy, job.LockUntil)
	}
	data := marketControlData(t, f.request("GET", "/control-job", "control-owner"))
	if data["display_state"] == "processing" || data["can_delete"] != true {
		t.Fatalf("finished attempt still drains: %v", data)
	}
}

func TestMarketControlIndexedRetryResponseLossDoesNotUseOldFailure(t *testing.T) {
	for _, repair := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry", true: "repair"}[repair], func(t *testing.T) {
			f := newMarketControlFixture(t, "SUCCESS", "FAILED")
			if err := f.db.AutoMigrate(&orm.UserUIPreferences{}, &orm.UserSelectedModel{}, &orm.UserSelectedProvider{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{}, &orm.UserModelProvider{}, &orm.UserSelectedCloudModel{}); err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&orm.Dataset{}).Where("id = ?", "control-dataset").Update("processing_level", "indexed").Error; err != nil {
				t.Fatal(err)
			}
			var archive bytes.Buffer
			zw := zip.NewWriter(&archive)
			for _, name := range []string{"control-file-0.md", "control-file-1.md", "new.md"} {
				file, err := zw.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = file.Write([]byte("# Synthetic retry"))
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			accepted, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/fixture.zip" {
					_, _ = w.Write(archive.Bytes())
					return
				}
				if r.URL.Path != "/v1/docs/add" {
					f.algorithm(w, r)
					return
				}
				var body struct {
					Items []struct {
						DocID string `json:"doc_id"`
					} `json:"items"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if len(body.Items) != 1 || body.Items[0].DocID != "control-file-1" {
					t.Error("retry selected non-failed files")
					return
				}
				if err := f.db.Create(&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "new-attempt", DocID: "control-file-1", KbID: "control-dataset", TaskType: "DOC_ADD", Status: "WORKING", CreatedAt: time.Now(), UpdatedAt: time.Now()}).Error; err != nil {
					t.Error(err)
					return
				}
				close(accepted)
				select {
				case <-release:
				case <-r.Context().Done():
				}
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
			}))
			t.Cleanup(server.Close)
			t.Setenv("LAZYMIND_DOCUMENT_SERVICE_URL", server.URL)
			if repair {
				cfg := json.RawMessage(`{"task_ids":["control-file-0","control-file-1"],"failures":[{"name":"new.md","reason":"import_failed"}]}`)
				if err := f.db.Model(&orm.KnowledgeMarketInstall{}).Where("market_item_id = ?", "control-item").Update("config", cfg).Error; err != nil {
					t.Fatal(err)
				}
				if err := f.db.Model(&orm.KnowledgeMarketItem{}).Where("id = ?", "control-item").Update("package_url", server.URL+"/fixture.zip").Error; err != nil {
					t.Fatal(err)
				}
			}
			retry := marketControlData(t, f.request("POST", "/control-job:retry", "control-owner"))
			id := retry["job_id"].(string)
			knowledge_market.RegisterAsyncJobs()
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			runner := asyncjob.Start(ctx, f.db.DB, asyncjob.Options{JobTypes: []string{"knowledge_market_update"}, Concurrency: 1, PollInterval: 10 * time.Millisecond, LockTTL: 300 * time.Millisecond})
			t.Cleanup(func() { once.Do(func() { close(release) }); stop(); <-runner.Done() })
			select {
			case <-accepted:
			case <-ctx.Done():
				t.Fatal("retry never reached Algorithm")
			}
			marketControlData(t, f.request("POST", "/"+id+":cancel", "control-owner"))
			once.Do(func() { close(release) })
			stop()
			<-runner.Done()
			data := marketControlData(t, f.request("GET", "/"+id, "control-owner"))
			if data["display_state"] != "unknown" || data["can_retry"] != false || data["can_delete"] != false {
				t.Errorf("old FAILED masked new active execution: %v", data)
			}
			read := httptest.NewRequest("GET", "/datasets/control-dataset/tasks/control-file-1", nil)
			read.Header.Set("X-User-Id", "control-owner")
			response := httptest.NewRecorder()
			f.router.ServeHTTP(response, read)
			var fileState map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &fileState); err != nil {
				t.Fatal(err)
			}
			if fileState["task_state"] != "UNKNOWN" {
				t.Errorf("file detail reused old failure: %s", response.Body.String())
			}
			resume := httptest.NewRequest("POST", "/datasets/control-dataset/tasks/control-file-1:resume", nil)
			resume.Header.Set("X-User-Id", "control-owner")
			resumed := httptest.NewRecorder()
			f.router.ServeHTTP(resumed, resume)
			if resumed.Code != 400 {
				t.Errorf("file resume accepted uncertain submission: %d", resumed.Code)
			}
			for _, op := range []struct{ method, path string }{{"POST", "/" + id + ":retry"}, {"DELETE", "/" + id}} {
				response := f.request(op.method, op.path, "control-owner")
				if response.Code < 400 {
					t.Errorf("unsafe action accepted on uncertain retry: %s %d", op.path, response.Code)
				}
			}
		})
	}
}
