package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/pdfir"
	"lazymind/core/store"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const pdfIRJobType = "document_pdf_ir"

type pdfIRPayload struct {
	Revision     string `json:"revision"`
	DatasetID    string `json:"dataset_id"`
	DocumentID   string `json:"document_id"`
	SourceSHA256 string `json:"source_sha256"`
}
type pdfIRResult struct {
	IRPath  string `json:"ir_path"`
	RawPath string `json:"raw_path"`
}
type pdfIRResponse struct {
	Status       string          `json:"status"`
	SourceSHA256 string          `json:"source_sha256,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	Document     *pdfir.Document `json:"document,omitempty"`
}

func pdfIRResource(dataset, document string) string { return pdfir.Digest(dataset + "\x00" + document) }
func pdfIRSource(ext documentExt) string {
	if ext.SourceStoredPath != "" {
		return ext.SourceStoredPath
	}
	return ext.StoredPath
}
func pdfIRHash(ext documentExt) (string, error) {
	p := pdfIRSource(ext)
	f, e := os.Open(p)
	if e != nil {
		return "", errors.New("Original PDF is unavailable")
	}
	var head [5]byte
	_, e = io.ReadFull(f, head[:])
	f.Close()
	if e != nil || string(head[:]) != "%PDF-" {
		return "", errors.New("PDF IR requires an original PDF")
	}
	return pdfir.SourceHash(p)
}
func pdfIRStatus(ctx context.Context, row orm.Document, hash string) (pdfIRResponse, error) {
	s := pdfIRResponse{Status: "not_requested", SourceSHA256: hash}
	var jobs []orm.AsyncJob
	if e := store.DB().WithContext(ctx).Where("job_type = ? AND resource_type = ? AND resource_id = ?", pdfIRJobType, "document_pdf_ir", pdfIRResource(row.DatasetID, row.ID)).Order("created_at DESC").Find(&jobs).Error; e != nil {
		return s, e
	}
	for _, j := range jobs {
		var p pdfIRPayload
		if json.Unmarshal(j.PayloadJSON, &p) != nil || p.SourceSHA256 != hash || p.Revision != pdfir.Revision {
			continue
		}
		switch j.Status {
		case "pending":
			s.Status = "queued"
		case "running":
			s.Status = "running"
		case "failed", "canceled":
			s.Status = "failed"
			s.ErrorMessage = "PDF parsing failed or was canceled. Retry parsing."
		case "succeeded":
			var result pdfIRResult
			var d pdfir.Document
			if json.Unmarshal(j.ResultJSON, &result) != nil {
				return s, nil
			}
			f, e := os.Open(result.IRPath)
			if e != nil {
				s.Status = "failed"
				s.ErrorMessage = "PDF IR artifact is missing; retry parsing"
				return s, nil
			}
			e = json.NewDecoder(io.LimitReader(f, 32<<20)).Decode(&d)
			f.Close()
			if e != nil || d.SourceSHA256 != hash {
				s.Status = "failed"
				s.ErrorMessage = "PDF IR artifact is invalid; retry parsing"
				return s, nil
			}
			s.Status = "ready"
			s.Document = &d
		}
		return s, nil
	}
	return s, nil
}
func GetPDFIR(w http.ResponseWriter, r *http.Request) {
	row, ext, _, ok := loadPDFDocument(r, false)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", 404)
		return
	}
	hash, e := pdfIRHash(ext)
	if e != nil {
		common.ReplyOK(w, pdfIRResponse{Status: "unavailable", ErrorMessage: e.Error()})
		return
	}
	s, e := pdfIRStatus(r.Context(), row, hash)
	if e != nil {
		common.ReplyErr(w, "PDF IR status unavailable", 500)
		return
	}
	common.ReplyOK(w, s)
}
func CreatePDFIR(w http.ResponseWriter, r *http.Request) {
	row, ext, _, ok := loadPDFDocument(r, true)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", 404)
		return
	}
	var req struct {
		Force bool `json:"force"`
	}
	if r.Body != nil {
		if e := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); e != nil && e != io.EOF {
			common.ReplyErr(w, "invalid body", 400)
			return
		}
	}
	hash, e := pdfIRHash(ext)
	if e != nil {
		common.ReplyOK(w, pdfIRResponse{Status: "unavailable", ErrorMessage: e.Error()})
		return
	}
	s, e := pdfIRStatus(r.Context(), row, hash)
	if e != nil {
		common.ReplyErr(w, "PDF IR status unavailable", 500)
		return
	}
	if s.Status == "running" || s.Status == "queued" || s.Status == "ready" && !req.Force {
		common.ReplyOK(w, s)
		return
	}
	cfg, e := modelconfig.LoadOCRConfig(r.Context(), store.DB(), store.UserID(r))
	if e != nil {
		common.ReplyOK(w, pdfIRResponse{Status: "unavailable", SourceSHA256: hash, ErrorMessage: "MinerU configuration unavailable"})
		return
	}
	if _, e = pdfir.FromConfig(cfg); e != nil {
		common.ReplyOK(w, pdfIRResponse{Status: "unavailable", SourceSHA256: hash, ErrorMessage: e.Error()})
		return
	}
	resource := pdfIRResource(row.DatasetID, row.ID)
	job, e := asyncjob.Enqueue(r.Context(), store.DB(), asyncjob.EnqueueRequest{JobType: pdfIRJobType, ResourceType: "document_pdf_ir", ResourceID: resource, IdempotencyKey: pdfir.Digest(resource + hash + pdfir.Revision), Payload: pdfIRPayload{DatasetID: row.DatasetID, DocumentID: row.ID, SourceSHA256: hash, Revision: pdfir.Revision}, MaxAttempts: 2, SkipSucceeded: true, CreateUserID: store.UserID(r)})
	if e != nil {
		common.ReplyErr(w, "enqueue PDF parsing failed", 500)
		return
	}
	status := "queued"
	if job.Status == "running" {
		status = "running"
	}
	common.ReplyOK(w, pdfIRResponse{Status: status, SourceSHA256: hash})
}
func RegisterPDFIRJobs() { asyncjob.Register(pdfIRJobType, handlePDFIRJob) }
func handlePDFIRJob(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
	fail := func(e error) (asyncjob.Result, error) {
		if errors.Is(e, context.Canceled) {
			return asyncjob.Result{Permanent: true, ErrorCode: asyncjob.ErrorCodeCanceled}, e
		}
		return asyncjob.Result{ErrorCode: "pdf_ir_failed"}, e
	}
	var p pdfIRPayload
	if json.Unmarshal(job.PayloadJSON, &p) != nil || p.DocumentID == "" || p.DatasetID == "" {
		return asyncjob.Result{Permanent: true}, errors.New("invalid PDF IR job")
	}
	// Durable jobs have no gateway request headers. Resolve the current account
	// rather than persisting tenant claims that can become stale after enqueue.
	identity, identityErr := common.GetCurrentIdentity(ctx, job.CreateUserID)
	if identityErr != nil {
		return asyncjob.Result{Permanent: true}, errors.New("PDF IR account identity unavailable or disabled")
	}
	r, _ := http.NewRequestWithContext(ctx, "POST", "http://localhost/", nil)
	r.Header.Set("X-User-Id", job.CreateUserID)
	r.Header.Set("X-Tenant-ID", identity.TenantID)
	r.Header.Set("X-User-Role", identity.Role)
	if _, _, ok := requireDatasetPermission(r, p.DatasetID, acl.PermissionDatasetWrite); !ok {
		return asyncjob.Result{Permanent: true}, errors.New("PDF IR permission revoked")
	}
	_, ext, e := loadDocumentFileMeta(ctx, p.DatasetID, p.DocumentID)
	if e != nil {
		return fail(errors.New("PDF document unavailable"))
	}
	hash, e := pdfIRHash(ext)
	if e != nil || hash != p.SourceSHA256 {
		return asyncjob.Result{Permanent: true}, errors.New("PDF source changed; request parsing again")
	}
	cfg, e := modelconfig.LoadOCRConfig(ctx, store.DB(), job.CreateUserID)
	if e != nil {
		return fail(errors.New("MinerU configuration unavailable"))
	}
	client, e := pdfir.FromConfig(cfg)
	if e != nil {
		return asyncjob.Result{Permanent: true}, e
	}
	dir := filepath.Join(filepath.Dir(pdfIRSource(ext)), ".lazymind-artifacts", p.DocumentID, "pdf-ir", job.ID, fmt.Sprintf("attempt-%d", job.AttemptCount))
	if e = os.MkdirAll(dir, 0700); e != nil {
		return fail(errors.New("create PDF IR artifact directory failed"))
	}
	snapshot := filepath.Join(dir, "source.pdf")
	src, e := os.Open(pdfIRSource(ext))
	if e != nil {
		return fail(errors.New("PDF source unavailable"))
	}
	dst, e := os.OpenFile(snapshot, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		src.Close()
		return fail(errors.New("create PDF snapshot failed"))
	}
	_, e = io.Copy(dst, io.LimitReader(src, pdfir.MaxSourceBytes+1))
	src.Close()
	closeErr := dst.Close()
	if e == nil {
		e = closeErr
	}
	defer os.Remove(snapshot)
	snapshotHash, he := pdfir.SourceHash(snapshot)
	if e != nil || he != nil || snapshotHash != hash {
		return asyncjob.Result{Permanent: true}, errors.New("PDF changed while preparing parsing")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				if reporter.Heartbeat(runCtx) != nil {
					cancel()
					return
				}
			}
		}
	}()
	raw, e := client.Parse(runCtx, snapshot)
	if e != nil {
		return fail(e)
	}
	rawPath := filepath.Join(dir, "mineru-result.zip")
	if e = os.WriteFile(rawPath, raw, 0600); e != nil {
		return fail(errors.New("save MinerU raw result failed"))
	}
	d, e := pdfir.AdaptZIP(raw, hash, client.Model)
	if e != nil {
		return fail(errors.New("MinerU result has invalid layout data"))
	}
	current, e := pdfIRHash(ext)
	if e != nil || current != hash {
		return asyncjob.Result{Permanent: true}, errors.New("PDF source changed during parsing")
	}
	if runCtx.Err() != nil {
		return fail(runCtx.Err())
	}
	irPath := filepath.Join(dir, "document-ir.json")
	b, _ := json.Marshal(d)
	if e = os.WriteFile(irPath, b, 0600); e != nil {
		return fail(errors.New("save PDF IR failed"))
	}
	result, _ := json.Marshal(pdfIRResult{IRPath: irPath, RawPath: rawPath})
	return asyncjob.Result{ResultJSON: result}, nil
}
