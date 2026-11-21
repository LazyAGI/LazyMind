package doc

import (
	"encoding/json"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/readingpolicy"
	"net/http"
	"testing"
	"time"
)

func TestAutomaticPDFIRDoesNotReviveTerminalJobs(t *testing.T) {
	for _, status := range []string{"failed", "canceled"} {
		t.Run(status, func(t *testing.T) {
			db := newDocumentTestDB(t)
			if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
				t.Fatal(err)
			}
			req := asyncjob.EnqueueRequest{JobType: pdfIRJobType, IdempotencyKey: "same", Payload: pdfIRPayload{Automatic: true}, MaxAttempts: 2}
			old, err := asyncjob.Enqueue(t.Context(), db.DB, req)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Model(old).Updates(map[string]any{"status": status, "attempt_count": 1, "error_message": "original failure"}).Error; err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				got, err := enqueuePDFIR(t.Context(), db.DB, req, true)
				if err != nil {
					t.Fatal(err)
				}
				if got.ID != old.ID || got.Status != status || got.AttemptCount != 1 || got.ErrorMessage != "original failure" {
					t.Fatalf("automatic revived terminal job: %+v", got)
				}
			}
			req.Payload = pdfIRPayload{Automatic: false}
			got, err := enqueuePDFIR(t.Context(), db.DB, req, false)
			if err != nil {
				t.Fatal(err)
			}
			var payload pdfIRPayload
			if err := json.Unmarshal(got.PayloadJSON, &payload); err != nil {
				t.Fatal(err)
			}
			if got.ID != old.ID || got.Status != "pending" || got.AttemptCount != 0 || payload.Automatic {
				t.Fatalf("manual retry unavailable: %+v", got)
			}
		})
	}
}

func TestAutomaticPDFIRPreservesQuotaDeferral(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	req := asyncjob.EnqueueRequest{JobType: pdfIRJobType, IdempotencyKey: "quota", Payload: pdfIRPayload{Automatic: true}, MaxAttempts: 2}
	old, err := enqueuePDFIR(t.Context(), db.DB, req, true)
	if err != nil {
		t.Fatal(err)
	}
	if old.MaxAttempts != 1 {
		t.Fatalf("automatic attempts=%d", old.MaxAttempts)
	}
	next := readingpolicy.NextDay(time.Now())
	if err := db.Model(old).Updates(map[string]any{"error_code": readingpolicy.QuotaErrorCode, "next_run_at": next}).Error; err != nil {
		t.Fatal(err)
	}
	got, err := enqueuePDFIR(t.Context(), db.DB, req, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != old.ID || got.Status != "pending" || got.ErrorCode != readingpolicy.QuotaErrorCode || !got.NextRunAt.Equal(next) {
		t.Fatalf("quota deferral reset: %+v", got)
	}
}

func TestPDFIRAutomaticOrdinaryFailureIsPermanent(t *testing.T) {
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "ds", "owner")
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return testJSONResponse(200, `{"sub":"owner","tenant_id":"default","role":"user"}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "test-token")
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", "http://auth.test")
	for _, automatic := range []bool{true, false} {
		result, err := handlePDFIRJob(t.Context(), asyncjob.Job{CreateUserID: "owner", AttemptCount: 1, MaxAttempts: 2, PayloadJSON: mustJSON(pdfIRPayload{DatasetID: "ds", DocumentID: "missing", Automatic: automatic})}, nil)
		if err == nil || err.Error() != "PDF document unavailable" {
			t.Fatalf("unexpected failure: %v", err)
		}
		if result.Permanent != automatic || result.ErrorCode != "pdf_ir_failed" {
			t.Fatalf("automatic=%v result=%+v", automatic, result)
		}
	}
}

func TestAutomaticPDFIRReusesActiveAndRegeneratesMissingSuccess(t *testing.T) {
	for _, status := range []string{"pending", "running", "succeeded"} {
		t.Run(status, func(t *testing.T) {
			db := newDocumentTestDB(t)
			if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
				t.Fatal(err)
			}
			req := asyncjob.EnqueueRequest{JobType: pdfIRJobType, IdempotencyKey: "same", Payload: pdfIRPayload{Automatic: true}, SkipSucceeded: true}
			old, err := asyncjob.Enqueue(t.Context(), db.DB, req)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Model(old).Update("status", status).Error; err != nil {
				t.Fatal(err)
			}
			got, err := enqueuePDFIR(t.Context(), db.DB, req, true)
			if err != nil {
				t.Fatal(err)
			}
			if status == "succeeded" {
				if got.ID == old.ID || got.Status != "pending" {
					t.Fatalf("missing successful artifact cannot regenerate: %+v", got)
				}
			} else if got.ID != old.ID || got.Status != status {
				t.Fatalf("active parse replaced: %+v", got)
			}
		})
	}
}

func TestAutomaticPDFIRConcurrentEnqueueDeduplicates(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	// Match PostgreSQL's production partial index; SQLite's write transaction
	// and busy retry must also protect this same identity.
	if err := db.Exec("CREATE UNIQUE INDEX pdf_ir_test_key ON async_jobs(job_type,idempotency_key) WHERE idempotency_key <> ''").Error; err != nil {
		t.Fatal(err)
	}
	req := asyncjob.EnqueueRequest{JobType: pdfIRJobType, IdempotencyKey: "same", Payload: pdfIRPayload{Automatic: true}}
	type outcome struct {
		job *orm.AsyncJob
		err error
	}
	results := make(chan outcome, 8)
	for i := 0; i < 8; i++ {
		go func() { job, err := enqueuePDFIR(t.Context(), db.DB, req, true); results <- outcome{job, err} }()
	}
	id := ""
	for i := 0; i < 8; i++ {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		if id == "" {
			id = got.job.ID
		}
		if got.job.ID != id {
			t.Fatal("concurrent duplicate parse")
		}
	}
	var count int64
	if err := db.Model(&orm.AsyncJob{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("jobs=%d err=%v", count, err)
	}
}
