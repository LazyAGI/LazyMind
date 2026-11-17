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
	"lazymind/core/pdfir"
	"lazymind/core/readingpolicy"
	"lazymind/core/store"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const pdfIRJobType = "document_pdf_ir"
const pdfIRMaxArtifactBytes int64 = 32 << 20
const pdfIRTooLargeCode = "pdf_ir_too_large"
const pdfIRTooLargeMessage = "PDF reading structure exceeds the 32 MiB limit. Use a smaller PDF or split the original PDF into smaller documents."

var errPDFIRTooLarge = errors.New("PDF reading structure exceeds the 32 MiB limit. Use a smaller PDF or split the original PDF into smaller documents.")

type pdfIRPayload struct {
	Force        bool   `json:"force,omitempty"`
	Automatic    bool   `json:"automatic,omitempty"`
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
	ErrorCode    string          `json:"error_code,omitempty"`
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
	return pdfIRStatusFromJobs(row, hash, jobs), nil
}

func pdfIRStatusFromJobs(row orm.Document, hash string, jobs []orm.AsyncJob) pdfIRResponse {
	s := pdfIRResponse{Status: "not_requested", SourceSHA256: hash}
	var ext documentExt
	_ = json.Unmarshal(row.Ext, &ext)
	source := pdfIRSource(ext)
	var shared *pdfir.Document
	if source != "" {
		if raw, d, incomplete := pdfir.LookupSharedArchive(source, hash); raw != nil {
			encoded, err := json.Marshal(d)
			if err == nil && int64(len(encoded)) <= pdfIRMaxArtifactBytes {
				shared = &d
			} else {
				s.Status, s.ErrorMessage, s.ErrorCode = "failed", pdfIRTooLargeMessage, pdfIRTooLargeCode
			}
		} else if incomplete {
			s.ErrorMessage = "Existing MinerU result lacks usable structure. Parse this PDF to add reading structure."
		}
	}
	latest := true
jobsLoop:
	for _, j := range jobs {
		var p pdfIRPayload
		if json.Unmarshal(j.PayloadJSON, &p) != nil || p.SourceSHA256 != hash || p.Revision != pdfir.Revision {
			continue
		}
		switch j.Status {
		case "pending":
			if latest {
				s.Status = "queued"
				if j.ErrorCode == readingpolicy.QuotaErrorCode {
					s.ErrorMessage = j.ErrorMessage
					s.ErrorCode = j.ErrorCode
				}
			}
		case "running":
			if latest {
				s.Status = "running"
			}
		case "failed", "canceled":
			if latest {
				s.Status = "failed"
				s.ErrorMessage = "PDF parsing failed or was canceled. Retry parsing."
				if j.ErrorCode == pdfIRTooLargeCode {
					s.ErrorMessage = pdfIRTooLargeMessage
				} else if j.ErrorCode == readingpolicy.QuotaErrorCode {
					s.ErrorMessage = j.ErrorMessage
					s.ErrorCode = j.ErrorCode
				}
			}
		case "succeeded":
			d, message := readPDFIRJobResult(j, hash)
			if d != nil {
				if latest {
					s.Status = "ready"
					s.ErrorMessage = ""
				}
				if shared == nil || shared.Parser.Model != "vlm" && d.Parser.Model == "vlm" {
					shared = d
				}
			} else if latest {
				s.Status = "failed"
				s.ErrorMessage = message
			}
			if d != nil {
				break jobsLoop
			}
		default:
			continue
		}
		latest = false
	}
	s.Document = shared
	if shared != nil && s.Status == "not_requested" {
		s.Status, s.ErrorMessage = "ready", ""
	}
	return s
}

func readPDFIRJobResult(j orm.AsyncJob, hash string) (*pdfir.Document, string) {
	var result pdfIRResult
	if json.Unmarshal(j.ResultJSON, &result) != nil {
		return nil, "PDF IR artifact is invalid; retry parsing"
	}
	f, err := os.Open(result.IRPath)
	if err != nil {
		return nil, "PDF IR artifact is missing; retry parsing"
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > pdfIRMaxArtifactBytes {
		return nil, pdfIRTooLargeMessage
	}
	var d pdfir.Document
	if err := json.NewDecoder(io.LimitReader(f, pdfIRMaxArtifactBytes)).Decode(&d); err != nil || d.SourceSHA256 != hash {
		return nil, "PDF IR artifact is invalid; retry parsing"
	}
	upgradePDFIRProjection(&d, result)
	return &d, ""
}

type pdfStructureStatus = pdfir.StructureStatus

// Called after the endpoint has established access to this exact source. Keep
// reading structure separate from the retrieval tasks managed by EnsureParsed.
func observePDFStructure(ctx context.Context, dataset, document string) *pdfStructureStatus {
	row, ext, err := loadDocumentFileMeta(ctx, dataset, document)
	if err != nil {
		return nil
	}
	hash, err := pdfIRHash(ext)
	if err != nil {
		return nil
	}
	state, err := pdfIRStatus(ctx, row, hash)
	if err != nil {
		return &pdfStructureStatus{Status: "unknown", SourceSHA256: hash}
	}
	result := &pdfStructureStatus{Status: state.Status, SourceSHA256: hash, ErrorMessage: state.ErrorMessage, ErrorCode: state.ErrorCode}
	if state.Document != nil {
		result.Available = true
		result.Model = state.Document.Parser.Model
	}
	return result
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
	if s.Status == "running" || s.Status == "queued" && s.ErrorCode != readingpolicy.QuotaErrorCode || s.Status == "ready" && !req.Force {
		common.ReplyOK(w, s)
		return
	}
	if err := readingpolicy.Check(r.Context(), store.DB(), store.UserID(r), readingpolicy.PDFParse); err != nil {
		common.ReplyErr(w, err.Error(), 403)
		return
	}
	cfg, e := loadReadingParser(r.Context(), store.UserID(r))
	if e != nil {
		common.ReplyOK(w, pdfIRResponse{Status: "unavailable", SourceSHA256: hash, ErrorMessage: "MinerU configuration unavailable"})
		return
	}
	if _, e = pdfir.FromConfig(cfg); e != nil {
		common.ReplyOK(w, pdfIRResponse{Status: "unavailable", SourceSHA256: hash, ErrorMessage: e.Error()})
		return
	}
	resource := pdfIRResource(row.DatasetID, row.ID)
	job, e := enqueuePDFIR(r.Context(), store.DB(), asyncjob.EnqueueRequest{JobType: pdfIRJobType, ResourceType: "document_pdf_ir", ResourceID: resource, IdempotencyKey: pdfir.Digest(resource + hash + pdfir.Revision), Payload: pdfIRPayload{Force: req.Force, DatasetID: row.DatasetID, DocumentID: row.ID, SourceSHA256: hash, Revision: pdfir.Revision}, MaxAttempts: 2, SkipSucceeded: true, CreateUserID: store.UserID(r)}, false)
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
	var p pdfIRPayload
	fail := func(e error) (asyncjob.Result, error) {
		if errors.Is(e, context.Canceled) {
			return asyncjob.Result{Permanent: true, ErrorCode: asyncjob.ErrorCodeCanceled}, e
		}
		return asyncjob.Result{Permanent: p.Automatic, ErrorCode: "pdf_ir_failed"}, e
	}
	if json.Unmarshal(job.PayloadJSON, &p) != nil || p.DocumentID == "" || p.DatasetID == "" {
		return asyncjob.Result{Permanent: true}, errors.New("invalid PDF IR job")
	}
	if p.Automatic {
		ctx = readingpolicy.AutomaticContext(ctx)
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
	if err := readingpolicy.Check(ctx, store.DB(), job.CreateUserID, readingpolicy.PDFParse); err != nil {
		return asyncjob.Result{Permanent: true}, err
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
	// Recheck under ingestion's lock before occupying quota or calling MinerU.
	source := pdfIRSource(ext)
	unlock, e := pdfir.LockCachedArchive(runCtx, source, hash, "vlm")
	if e != nil {
		return fail(e)
	}
	defer unlock()
	var raw []byte
	var d pdfir.Document
	if !p.Force {
		raw, d = pdfir.ReadSharedArchive(source, hash)
	}
	if raw == nil {
		cfg, err := loadReadingParser(runCtx, job.CreateUserID)
		if err != nil {
			return fail(errors.New("MinerU configuration unavailable"))
		}
		client, err := pdfir.FromConfig(cfg)
		if err != nil {
			return asyncjob.Result{Permanent: true}, err
		}
		if err := readingpolicy.Claim(runCtx, store.DB(), job.CreateUserID, readingpolicy.PDFParse, hash); err != nil {
			if errors.Is(err, readingpolicy.ErrQuotaExceeded) {
				return asyncjob.Result{ErrorCode: readingpolicy.QuotaErrorCode, RetryAt: readingpolicy.NextDay(time.Now()), DeferAttempt: true}, err
			}
			return fail(err)
		}
		raw, e = client.Parse(runCtx, snapshot)
		if e != nil {
			return fail(e)
		}
		d, e = pdfir.AdaptSharedZIP(raw, hash, client.Model)
		if e != nil {
			return fail(errors.New("MinerU result has invalid layout data"))
		}
	}
	rawPath := filepath.Join(dir, "mineru-result.zip")
	if e = os.WriteFile(rawPath, raw, 0600); e != nil {
		return fail(errors.New("save MinerU raw result failed"))
	}
	current, e := pdfIRHash(ext)
	if e != nil || current != hash {
		return asyncjob.Result{Permanent: true}, errors.New("PDF source changed during parsing")
	}
	if runCtx.Err() != nil {
		return fail(runCtx.Err())
	}
	irPath := filepath.Join(dir, "document-ir.json")
	if e = writePDFIRArtifact(irPath, d, pdfIRMaxArtifactBytes); e != nil {
		if errors.Is(e, errPDFIRTooLarge) {
			return asyncjob.Result{Permanent: true, ErrorCode: pdfIRTooLargeCode}, e
		}
		return fail(errors.New("save PDF IR failed"))
	}
	// The shared archive replaces the previous result only after all validation
	// and per-job artifact writes have succeeded.
	if p.Force || pdfir.ReadCachedArchive(source, hash, d.Parser.Model) == nil {
		if e := pdfir.SaveCachedArchive(source, hash, d.Parser.Model, raw); e != nil {
			return fail(errors.New("publish shared MinerU archive failed"))
		}
	}
	result, _ := json.Marshal(pdfIRResult{IRPath: irPath, RawPath: rawPath})
	return asyncjob.Result{ResultJSON: result}, nil
}
