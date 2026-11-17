package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"io"
	"lazymind/core/acl"
	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
	"lazymind/core/readingpolicy"
	"lazymind/core/store"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const ReadingPreparationJob = "document_reading_preparation"

var errReadingStructureRequired = errors.New("等待 PDF 结构：请手动解析或开启自动解析，结构就绪后自动继续精读")

type readingPreparationPayload struct {
	DatasetID    string   `json:"dataset_id"`
	DocumentID   string   `json:"document_id"`
	Scopes       []string `json:"scopes"`
	Automatic    bool     `json:"automatic"`
	SourceSHA256 string   `json:"source_sha256"`
	ItemLimit    int      `json:"item_limit"`
}
type readingPreparationStatus struct {
	Capabilities    []readingCapabilityStatus `json:"capabilities,omitempty"`
	ModelRequests   int                       `json:"model_requests"`
	JobID           string                    `json:"job_id,omitempty"`
	Status          string                    `json:"status"`
	ProgressCurrent int64                     `json:"progress_current"`
	ProgressTotal   int64                     `json:"progress_total"`
	ErrorMessage    string                    `json:"error_message,omitempty"`
	ErrorCode       string                    `json:"error_code,omitempty"`
	Scopes          []string                  `json:"scopes,omitempty"`
}

func init() { asyncjob.Register(ReadingPreparationJob, runReadingPreparation) }
func preparationStatus(j orm.AsyncJob) readingPreparationStatus {
	var p readingPreparationPayload
	_ = json.Unmarshal(j.PayloadJSON, &p)
	var result struct {
		ModelRequests int `json:"model_requests"`
	}
	_ = json.Unmarshal(j.ResultJSON, &result)
	return readingPreparationStatus{ModelRequests: result.ModelRequests, JobID: j.ID, Status: j.Status, ProgressCurrent: j.ProgressCurrent, ProgressTotal: j.ProgressTotal, ErrorMessage: j.ErrorMessage, ErrorCode: j.ErrorCode, Scopes: p.Scopes}
}
func GetReadingPreparation(w http.ResponseWriter, r *http.Request) {
	row, ext, _, ok := loadPDFDocument(r, false)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", 404)
		return
	}
	hash, err := pdfIRHash(ext)
	if err != nil {
		common.ReplyErr(w, err.Error(), 409)
		return
	}
	jobs, err := loadReadingPreparationJobs(r.Context(), store.DB(), row, store.UserID(r), hash)
	if err != nil {
		common.ReplyErr(w, "读取准备状态失败", 500)
		return
	}
	state, err := pdfIRStatus(r.Context(), row, hash)
	if err != nil {
		common.ReplyErr(w, "读取准备状态失败", 500)
		return
	}
	capabilities, err := readReadingCapabilities(state, pdfIRSource(ext), row.DatasetID, row.ID, store.UserID(r), jobs)
	if err != nil {
		common.ReplyErr(w, "读取准备状态失败", 500)
		return
	}
	status := readingPreparationStatus{Status: "not_requested"}
	if len(jobs) > 0 {
		status = preparationStatus(jobs[0])
		// An independently completed parse must not hide a still queued study.
		for _, candidate := range jobs {
			if candidate.Status == "running" {
				status = preparationStatus(candidate)
				break
			}
			if candidate.Status == "pending" && status.Status != "pending" {
				status = preparationStatus(candidate)
			}
		}
	}
	status.Capabilities = capabilities
	common.ReplyOK(w, status)
}
func PrepareReading(w http.ResponseWriter, r *http.Request) {
	row, ext, _, ok := loadPDFDocument(r, true)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", 404)
		return
	}
	var req struct {
		Scopes []string `json:"scopes"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil || len(req.Scopes) == 0 {
		common.ReplyErr(w, "请选择本篇需要准备的功能", 400)
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		common.ReplyErr(w, "准备请求格式无效", 400)
		return
	}
	expanded := []string{}
	for _, scope := range req.Scopes {
		if scope == readingpolicy.AIStudy {
			expanded = append(expanded, readingpolicy.Formula, readingpolicy.Figure, readingpolicy.Table)
		} else {
			expanded = append(expanded, scope)
		}
	}
	req.Scopes = expanded
	seen := map[string]bool{}
	for _, k := range req.Scopes {
		if k != readingpolicy.PDFParse && k != readingpolicy.Formula && k != readingpolicy.Figure && k != readingpolicy.Table {
			common.ReplyErr(w, "本篇准备仅支持 PDF 解析、公式、图片和表格", 400)
			return
		}
		if seen[k] {
			common.ReplyErr(w, "准备范围重复", 400)
			return
		}
		seen[k] = true
	}
	hash, err := pdfIRHash(ext)
	if err != nil {
		common.ReplyErr(w, err.Error(), 409)
		return
	}
	_, err = readingpolicy.Load(r.Context(), store.DB(), store.UserID(r))
	if err != nil {
		common.ReplyErr(w, "读取准备设置失败", 500)
		return
	}
	job, err := enqueueReadingPreparation(r.Context(), store.DB(), row, store.UserID(r), req.Scopes, false, hash, 0)
	if err != nil {
		common.ReplyErr(w, err.Error(), 500)
		return
	}
	common.ReplyOK(w, preparationStatus(*job))
}
func enqueueReadingPreparation(ctx context.Context, db *gorm.DB, row orm.Document, user string, scopes []string, automatic bool, hash string, limit int) (*orm.AsyncJob, error) {
	kind := "reading_explicit"
	sorted := append([]string(nil), scopes...)
	sort.Strings(sorted)
	key := "explicit:" + user + ":" + pdfIRResource(row.DatasetID, row.ID) + ":" + hash + ":" + strings.Join(sorted, ",")
	if automatic {
		kind = "reading_automatic"
		key = "automatic:v2:" + user + ":" + pdfIRResource(row.DatasetID, row.ID) + ":" + hash + ":" + strings.Join(sorted, ",")
	}
	enqueue := asyncjob.Enqueue
	if automatic {
		enqueue = asyncjob.EnqueueInTransaction
	}
	return enqueue(ctx, db, asyncjob.EnqueueRequest{JobType: ReadingPreparationJob, ResourceType: kind, ResourceID: user, IdempotencyKey: pdfir.Digest(key), Payload: readingPreparationPayload{row.DatasetID, row.ID, scopes, automatic, hash, limit}, MaxAttempts: 1, SkipSucceeded: !automatic, CreateUserID: user})
}

// Enrollment is durable and bounded. Only documents newer than the saved policy
// epoch are considered. Existing job rows (including failures) never reenroll.
const readingEnrollmentBatch = 100

type readingEnrollmentCursor struct {
	Epoch     time.Time
	CreatedAt time.Time
	ID        string
}

// The scheduler owns these cursors. Restarting only rechecks unqueued candidates;
// durable job exclusion prevents duplicate generation, regardless of job status.
func enrollReadingPreparation(ctx context.Context, db *gorm.DB, cursors map[string]readingEnrollmentCursor) error {
	if cursors == nil {
		cursors = make(map[string]readingEnrollmentCursor)
	}
	var policies []orm.UserReadingPolicy
	if err := db.WithContext(ctx).Find(&policies).Error; err != nil {
		return err
	}
	for _, stored := range policies {
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var locked orm.UserReadingPolicy
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=?", stored.UserID).Take(&locked).Error; err != nil {
				return err
			}
			p, err := readingpolicy.Decode([]byte(locked.PolicyJSON))
			if err != nil {
				return err
			}
			if !readingpolicy.HasAutomatic(p) || p.AutomaticEligibleAfter == nil {
				return nil
			}

			cursor := cursors[stored.UserID]
			if !cursor.Epoch.Equal(*p.AutomaticEligibleAfter) {
				cursor = readingEnrollmentCursor{Epoch: *p.AutomaticEligibleAfter}
			}
			// Filter completed enrollment in SQL, not once per historical document.
			enrolled := tx.Model(&orm.AsyncJob{}).Select("1").Where("job_type=? AND resource_type=? AND create_user_id=?", ReadingPreparationJob, "reading_automatic", stored.UserID).
				Where("payload_json ->> 'dataset_id' = documents.dataset_id AND payload_json ->> 'document_id' = documents.id")
			query := tx.Table("documents").Select("documents.*").Joins("JOIN datasets ON datasets.id=documents.dataset_id").
				Where("datasets.scene=? AND datasets.deleted_at IS NULL AND documents.deleted_at IS NULL AND documents.create_user_id=? AND documents.created_at>?", "academic_papers", stored.UserID, *p.AutomaticEligibleAfter).
				Where("(LOWER(documents.document_type) = 'pdf' OR LOWER(documents.display_name) LIKE '%.pdf')").Where("NOT EXISTS (?)", enrolled)
			if cursor.ID != "" {
				query = query.Where("(documents.created_at > ? OR (documents.created_at = ? AND documents.id > ?))", cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
			}
			var rows []orm.Document
			if err := query.Order("documents.created_at ASC, documents.id ASC").Limit(readingEnrollmentBatch).Find(&rows).Error; err != nil {
				return err
			}
			processed := 0
			for _, row := range rows {
				cursor.CreatedAt, cursor.ID = row.CreatedAt, row.ID
				processed++

				var ext documentExt
				_ = json.Unmarshal(row.Ext, &ext)
				hash, hashErr := pdfIRHash(ext)
				if hashErr != nil {
					continue
				}
				// Separate jobs allow exhausted study credit to yield to parsing.
				for _, category := range readingpolicy.Keys {
					if readingpolicy.Mode(p, category) != readingpolicy.Automatic {
						continue
					}
					scopes := []string{readingpolicy.PDFParse}
					if category == readingpolicy.AIStudy {
						scopes = []string{readingpolicy.Formula, readingpolicy.Figure, readingpolicy.Table}
					}
					if _, err := enqueueReadingPreparation(ctx, tx, row, stored.UserID, scopes, true, hash, 0); err != nil {
						return err
					}
				}
			}
			// Wrap only at exhaustion. Bad files cannot starve the next batch;
			// wrapping gives a repaired/unavailable file another chance later.
			if processed == len(rows) && len(rows) < readingEnrollmentBatch {
				cursor.CreatedAt, cursor.ID = time.Time{}, ""
			}
			cursors[stored.UserID] = cursor
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
func StartReadingPreparation(ctx context.Context, db *gorm.DB) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		cursors := make(map[string]readingEnrollmentCursor)
		for {
			if err := enrollReadingPreparation(ctx, db, cursors); err != nil && ctx.Err() == nil {
				log.Printf("reading preparation enrollment failed: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}
func preparationRequest(ctx context.Context, user string, p readingPreparationPayload) (*http.Request, error) {
	return preparationRequestWithPermission(ctx, user, p, acl.PermissionDatasetWrite)
}

func preparationRequestWithPermission(ctx context.Context, user string, p readingPreparationPayload, permission string) (*http.Request, error) {
	identity, err := common.GetCurrentIdentity(ctx, user)
	if err != nil {
		return nil, errors.New("阅读准备账户不可用")
	}
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/", nil)
	r.Header.Set("X-User-Id", user)
	r.Header.Set("X-Tenant-ID", identity.TenantID)
	r.Header.Set("X-User-Role", identity.Role)
	r = mux.SetURLVars(r, map[string]string{"dataset": p.DatasetID, "document": p.DocumentID})
	if _, _, ok := requireDatasetPermission(r, p.DatasetID, permission); !ok {
		return nil, errors.New("阅读准备权限已撤销")
	}
	return r, nil
}
func runReadingPreparation(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (result asyncjob.Result, resultErr error) {
	result = asyncjob.Result{Permanent: true}
	counter := &readingCallCounter{jobID: job.ID, user: job.CreateUserID}
	ctx = context.WithValue(ctx, readingCallCounterKey{}, counter)
	defer func() {
		counter.mu.Lock()
		defer counter.mu.Unlock()
		var details map[string]any
		_ = json.Unmarshal(result.ResultJSON, &details)
		if details == nil {
			details = map[string]any{}
		}
		details["model_requests"] = counter.calls
		if errors.Is(resultErr, errReadingStructureRequired) {
			result.Permanent = false
			result.ErrorCode = "reading_structure_required"
			result.RetryAt = time.Now().UTC().Add(time.Minute)
			result.DeferAttempt = true
		}
		if errors.Is(resultErr, readingpolicy.ErrQuotaExceeded) {
			result.Permanent = false
			result.ErrorCode = readingpolicy.QuotaErrorCode
			result.RetryAt = readingpolicy.NextDay(time.Now())
			result.DeferAttempt = true
		}
		result.ResultJSON = mustJSON(details)
	}()
	var p readingPreparationPayload
	if err := json.Unmarshal(job.PayloadJSON, &p); err != nil {
		return result, err
	}
	counter.hash = p.SourceSHA256
	if p.Automatic {
		ctx = readingpolicy.AutomaticContext(ctx)
	}
	r, err := preparationRequest(ctx, job.CreateUserID, p)
	if err != nil {
		return result, err
	}
	row, ext, _, ok := loadPDFDocument(r, true)
	if !ok {
		return result, errors.New("原始文档不可用")
	}
	hash, err := pdfIRHash(ext)
	if err != nil || hash != p.SourceSHA256 {
		return result, errors.New("原始 PDF 已变化，请重新准备")
	}
	policy, err := readingpolicy.Load(ctx, store.DB(), job.CreateUserID)
	if err != nil {
		return result, err
	}
	// Enrollment already checked the policy epoch. A later mode change must
	// not retroactively discard waiting work; per-call checks enforce the
	// current modes, while the new epoch only filters future enrollment.
	if p.Automatic && !readingpolicy.HasAutomatic(policy) {
		return result, errors.New("自动准备策略已停止")
	}
	scopes := map[string]bool{}
	for _, k := range p.Scopes {
		scopes[k] = true
	}
	total := int64(1)
	if err := reporter.SetProgress(ctx, 0, total); err != nil {
		return result, err
	}
	needsIR := scopes[readingpolicy.PDFParse] || scopes[readingpolicy.Formula] || scopes[readingpolicy.Figure] || scopes[readingpolicy.Table]
	if needsIR {
		state, e := pdfIRStatus(ctx, row, hash)
		if e != nil {
			return result, e
		}
		if state.Document == nil {
			if p.Automatic && readingpolicy.Mode(policy, readingpolicy.PDFParse) != readingpolicy.Automatic {
				return result, errReadingStructureRequired
			}
			for key := range scopes {
				if err := readingpolicy.Check(ctx, store.DB(), job.CreateUserID, key); err != nil {
					return result, err
				}
			}
			if err := readingpolicy.Check(ctx, store.DB(), job.CreateUserID, readingpolicy.PDFParse); err != nil {
				return result, err
			}
			resource := pdfIRResource(row.DatasetID, row.ID)
			parseJob, e := enqueuePDFIR(ctx, store.DB(), asyncjob.EnqueueRequest{JobType: pdfIRJobType, ResourceType: "document_pdf_ir", ResourceID: resource, IdempotencyKey: pdfir.Digest(resource + hash + pdfir.Revision), Payload: pdfIRPayload{Revision: pdfir.Revision, DatasetID: p.DatasetID, DocumentID: p.DocumentID, SourceSHA256: hash, Automatic: p.Automatic}, CreateUserID: job.CreateUserID, MaxAttempts: 2, SkipSucceeded: true}, p.Automatic)
			if e != nil {
				return result, e
			}
			waitCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			defer cancel()
			for {
				var current orm.AsyncJob
				if e = store.DB().WithContext(waitCtx).Where("id=?", parseJob.ID).Take(&current).Error; e != nil {
					return result, e
				}
				if current.ErrorCode == readingpolicy.QuotaErrorCode && current.Status == "pending" {
					return result, fmt.Errorf("等待 PDF 解析自动额度：%s（%w）", current.ErrorMessage, readingpolicy.ErrQuotaExceeded)
				}
				if current.Status == "succeeded" {
					break
				}
				if current.Status == "failed" || current.Status == "canceled" {
					return result, fmt.Errorf("结构化解析失败: %s", current.ErrorMessage)
				}
				if e = readingpolicy.Check(waitCtx, store.DB(), job.CreateUserID, readingpolicy.PDFParse); e != nil {
					return result, e
				}
				timer := time.NewTimer(time.Second)
				select {
				case <-waitCtx.Done():
					timer.Stop()
					return result, waitCtx.Err()
				case <-timer.C:
				}
			}
		}
	}
	if err := reporter.SetProgress(ctx, 1, total); err != nil {
		return result, err
	}
	if scopes[readingpolicy.ChatPrepare] {
		if err := readingpolicy.Check(ctx, store.DB(), job.CreateUserID, readingpolicy.ChatPrepare); err != nil {
			return result, err
		}
		service, e := NewDocumentService(DocumentServiceDeps{DB: store.DB(), LazyDB: store.LazyLLMDB()})
		if e != nil {
			return result, e
		}
		caller, e := resolveDatasetCatalogCallerFromRequest(r)
		if e != nil {
			return result, e
		}
		if e = service.EnsureDocumentParsedAndWait(r, EnsureDocumentParsedRequest{UserID: job.CreateUserID, DatasetID: p.DatasetID, DocumentID: p.DocumentID, Caller: caller}); e != nil {
			return result, e
		}
	}
	count, completed := 0, 0
	if needsIR {
		state, e := pdfIRStatus(ctx, row, hash)
		if e != nil {
			return result, e
		}
		if state.Document == nil {
			return result, errors.New("PDF 结构尚未就绪")
		}
		total = 1
		for _, block := range state.Document.Blocks {
			key := map[string]string{"formula": readingpolicy.Formula, "image": readingpolicy.Figure, "table": readingpolicy.Table}[block.Type]
			if key != "" && scopes[key] {
				total++
			}
		}
		if e := reporter.SetProgress(ctx, 1, total); e != nil {
			return result, e
		}
		for _, block := range state.Document.Blocks {
			key := ""
			switch block.Type {
			case "formula":
				key = readingpolicy.Formula
			case "image":
				key = readingpolicy.Figure
			case "table":
				key = readingpolicy.Table
			}
			if key == "" || !scopes[key] {
				continue
			}

			if e = readingpolicy.Check(ctx, store.DB(), job.CreateUserID, key); p.Automatic && e != nil {
				return result, e
			}
			rr, e := preparationRequest(ctx, job.CreateUserID, p)
			if e != nil {
				return result, e
			}
			rr = mux.SetURLVars(rr, map[string]string{"dataset": p.DatasetID, "document": p.DocumentID, "block": block.ID})
			var cache string
			if key == readingpolicy.Formula {
				_, cache, e = defaultFormulaHandler().explain(rr, formulaReadingRequest{Language: "zh"})
			} else {
				_, cache, e = defaultVisualHandler().read(rr, paperReadingRequest{Language: "zh"})
			}
			if e != nil {
				result.ResultJSON = mustJSON(map[string]any{"failed_scope": key})
				return result, readingPreparationModelError(key, e)
			}
			// Progress counts all finished blocks; model calls are tracked separately.
			completed++
			if cache != "hit" {
				count++
			}
			if e = reporter.SetProgress(ctx, int64(1+completed), total); e != nil {
				return result, e
			}
		}
	}
	if err := reporter.SetProgress(ctx, int64(1+completed), int64(1+completed)); err != nil {
		return result, err
	}
	result.ResultJSON = mustJSON(map[string]any{"study_items": count, "source_sha256": hash})
	return result, nil
}

// The cause remains inspectable by errors.Is/As, but provider text must never
// become the durable job error shown in preparation status.
type readingPreparationFailure struct {
	message string
	cause   error
}

func (e *readingPreparationFailure) Error() string { return e.message }
func (e *readingPreparationFailure) Unwrap() error { return e.cause }
func readingPreparationModelError(scope string, err error) error {
	if errors.Is(err, readingpolicy.ErrQuotaExceeded) {
		return err
	}
	name := map[string]string{readingpolicy.Formula: "公式", readingpolicy.Figure: "图片", readingpolicy.Table: "表格"}[scope]
	message := name + "研读生成失败，请重试或在 AI 阅读准备中更换研读模型。"
	if isReadingModelTimeout(err) {
		message = name + "研读模型响应超时（单次请求上限为 2 分钟），请重试或在 AI 阅读准备中更换研读模型。"
	} else if errors.Is(err, context.Canceled) {
		message = name + "研读已中断，请重新发起准备。"
	}
	return &readingPreparationFailure{message: message, cause: err}
}

func defaultFormulaHandler() formulaExplanationHandler {
	return formulaExplanationHandler{loadModel: loadFormulaExplanationModel, generate: func(ctx context.Context, content, instruction string, cfg map[string]any) (string, error) {
		if err := recordReadingModelCall(ctx); err != nil {
			return "", err
		}
		return generateStructuredPaperResult(ctx, content, algo.FormulaExplanationTask, algo.FormulaExplanationTaskVersion, formulaExplanationGenerationConfig(cfg))
	}}
}
func defaultVisualHandler() paperReadingHandler {
	return paperReadingHandler{model: formulaExplanationHandler{loadModel: loadFormulaExplanationModel, generate: func(ctx context.Context, content, instruction string, cfg map[string]any) (string, error) {
		if err := recordReadingModelCall(ctx); err != nil {
			return "", err
		}
		return generateStructuredPaperResult(ctx, content, algo.VisualExplanationTask, algo.VisualExplanationTaskVersion, formulaExplanationGenerationConfig(cfg))
	}}}
}

type readingCallCounterKey struct{}
type readingCallCounter struct {
	mu    sync.Mutex
	jobID string
	user  string
	hash  string
	calls int
}

func recordReadingModelCall(ctx context.Context) error {
	counter, ok := ctx.Value(readingCallCounterKey{}).(*readingCallCounter)
	if !ok {
		return nil
	}
	counter.mu.Lock()
	defer counter.mu.Unlock()
	if err := readingpolicy.Claim(ctx, store.DB(), counter.user, readingpolicy.AIStudy, counter.hash); err != nil {
		return err
	}
	counter.calls++
	return store.DB().WithContext(ctx).Model(&orm.AsyncJob{}).Where("id=?", counter.jobID).Update("result_json", mustJSON(map[string]any{"model_requests": counter.calls})).Error
}
