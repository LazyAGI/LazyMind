package doc

import (
	"encoding/json"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/readingpolicy"
	"testing"
	"time"
)

func TestExplicitPDFParsePromotesQuotaDeferredJob(t *testing.T) {
	db := newDocumentTestDB(t)
	db.AutoMigrate(&orm.AsyncJob{})
	payload := pdfIRPayload{DatasetID: "ds", DocumentID: "doc", SourceSHA256: "hash", Automatic: true}
	req := asyncjob.EnqueueRequest{JobType: pdfIRJobType, IdempotencyKey: "same", ResourceType: "document_pdf_ir", ResourceID: "doc", Payload: payload, CreateUserID: "owner"}
	old, err := asyncjob.Enqueue(t.Context(), db.DB, req)
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&orm.AsyncJob{}).Where("id=?", old.ID).Updates(map[string]any{"error_code": readingpolicy.QuotaErrorCode, "next_run_at": time.Now().Add(24 * time.Hour)})
	payload.Automatic = false
	payload.Force = true
	req.Payload = payload
	job, err := enqueuePDFIR(t.Context(), db.DB, req, false)
	if err != nil {
		t.Fatal(err)
	}
	var got pdfIRPayload
	json.Unmarshal(job.PayloadJSON, &got)
	if job.ID != old.ID || got.Automatic || !got.Force || job.ErrorCode != "" || job.NextRunAt.After(time.Now()) {
		t.Fatalf("manual parse stuck behind auto quota: %+v %+v", job, got)
	}
	// A normal queued/running job must not be stolen or duplicated.
	db.Model(&orm.AsyncJob{}).Where("id=?", old.ID).Updates(map[string]any{"status": "running", "error_code": readingpolicy.QuotaErrorCode})
	req.Payload = payload
	again, err := enqueuePDFIR(t.Context(), db.DB, req, false)
	if err != nil || again.ID != old.ID || again.Status != "running" {
		t.Fatalf("active job replaced: %+v %v", again, err)
	}
}
