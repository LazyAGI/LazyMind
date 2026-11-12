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

type readingPreparationPayload struct {
	DatasetID    string   `json:"dataset_id"`
	DocumentID   string   `json:"document_id"`
	Scopes       []string `json:"scopes"`
	Automatic    bool     `json:"automatic"`
	SourceSHA256 string   `json:"source_sha256"`
	ItemLimit    int      `json:"item_limit"`
}
type readingPreparationStatus struct {
	ModelRequests   int      `json:"model_requests"`
	JobID           string   `json:"job_id,omitempty"`
	Status          string   `json:"status"`
	ProgressCurrent int64    `json:"progress_current"`
	ProgressTotal   int64    `json:"progress_total"`
	ErrorMessage    string   `json:"error_message,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`
}

func init() { asyncjob.Register(ReadingPreparationJob, runReadingPreparation) }
func preparationStatus(j orm.AsyncJob) readingPreparationStatus {
	var p readingPreparationPayload
	_ = json.Unmarshal(j.PayloadJSON, &p)
	var result struct {
		ModelRequests int `json:"model_requests"`
	}
	_ = json.Unmarshal(j.ResultJSON, &result)
	return readingPreparationStatus{ModelRequests: result.ModelRequests, JobID: j.ID, Status: j.Status, ProgressCurrent: j.ProgressCurrent, ProgressTotal: j.ProgressTotal, ErrorMessage: j.ErrorMessage, Scopes: p.Scopes}
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
	var job orm.AsyncJob
	err = store.DB().WithContext(r.Context()).Where("job_type=? AND create_user_id=?", ReadingPreparationJob, store.UserID(r)).
		Where("payload_json ->> 'dataset_id' = ? AND payload_json ->> 'document_id' = ? AND payload_json ->> 'source_sha256' = ?", row.DatasetID, row.ID, hash).
		Order("created_at DESC, id DESC").Take(&job).Error
	if err == nil {
		common.ReplyOK(w, preparationStatus(job))
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "读取准备状态失败", 500)
		return
	}
	common.ReplyOK(w, readingPreparationStatus{Status: "not_requested"})
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
	p, err := readingpolicy.Load(r.Context(), store.DB(), store.UserID(r))
	if err != nil {
		common.ReplyErr(w, "读取准备设置失败", 500)
		return
	}
	job, err := enqueueReadingPreparation(r.Context(), store.DB(), row, store.UserID(r), req.Scopes, false, hash, p.Limits.StudyItemsPerDocument)
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
		key = "automatic:" + user + ":" + pdfIRResource(row.DatasetID, row.ID)
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
	if ctx.Err() != nil || !deferredIndexingWork.Enter() {
		return nil
	}
	defer deferredIndexingWork.Leave()
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
			var p readingpolicy.Policy
			if err := json.Unmarshal([]byte(locked.PolicyJSON), &p); err != nil {
				return err
			}
			if !readingpolicy.HasAutomatic(p) || p.AutomaticEligibleAfter == nil {
				return nil
			}
			now := time.Now().UTC()
			day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			var used int64
			if err := tx.Model(&orm.AsyncJob{}).Where("job_type=? AND resource_type=? AND create_user_id=? AND created_at>=?", ReadingPreparationJob, "reading_automatic", stored.UserID, day).Count(&used).Error; err != nil {
				return err
			}
			remaining := p.Limits.DailyDocuments - int(used)
			if remaining <= 0 {
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
				if remaining <= 0 {
					break
				}
				cursor.CreatedAt, cursor.ID = row.CreatedAt, row.ID
				processed++
				scopes := []string{}
				for _, k := range readingpolicy.Keys {
					if p.Modes[k] == readingpolicy.Automatic {
						scopes = append(scopes, k)
					}
				}
				var ext documentExt
				_ = json.Unmarshal(row.Ext, &ext)
				hash, hashErr := pdfIRHash(ext)
				if hashErr != nil {
					continue
				}
				if _, err := enqueueReadingPreparation(ctx, tx, row, stored.UserID, scopes, true, hash, p.Limits.StudyItemsPerDocument); err != nil {
					return err
				}
				remaining--
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
	counter := &readingCallCounter{jobID: job.ID}
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
		result.ResultJSON = mustJSON(details)
	}()
	var p readingPreparationPayload
	if err := json.Unmarshal(job.PayloadJSON, &p); err != nil {
		return result, err
	}
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
	if p.Automatic {
		if !readingpolicy.Eligible(policy, row.CreatedAt) {
			return result, errors.New("自动准备策略已变化，本篇不再符合条件")
		}
		now := time.Now().UTC()
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		var used int64
		if err := store.DB().WithContext(ctx).Model(&orm.AsyncJob{}).Where("job_type=? AND resource_type=? AND create_user_id=? AND started_at>=? AND id<>?", ReadingPreparationJob, "reading_automatic", job.CreateUserID, day, job.ID).Count(&used).Error; err != nil {
			return result, err
		}
		if used >= int64(policy.Limits.DailyDocuments) {
			return result, errors.New("已达到每日自动准备文档上限")
		}
	}
	limit := p.ItemLimit
	if policy.Limits.StudyItemsPerDocument < limit {
		limit = policy.Limits.StudyItemsPerDocument
	}
	if limit < 1 || limit > 100 {
		return result, errors.New("无效的研读数量上限")
	}
	scopes := map[string]bool{}
	for _, k := range p.Scopes {
		scopes[k] = true
	}
	total := int64(1 + limit)
	if err := reporter.SetProgress(ctx, 0, total); err != nil {
		return result, err
	}
	needsIR := scopes[readingpolicy.PDFParse] || scopes[readingpolicy.Formula] || scopes[readingpolicy.Figure] || scopes[readingpolicy.Table]
	if needsIR {
		state, e := pdfIRStatus(ctx, row, hash)
		if e != nil {
			return result, e
		}
		if state.Status != "ready" {
			for key := range scopes {
				if err := readingpolicy.Check(ctx, store.DB(), job.CreateUserID, key); err != nil {
					return result, err
				}
			}
			if err := readingpolicy.Check(ctx, store.DB(), job.CreateUserID, readingpolicy.PDFParse); err != nil {
				return result, err
			}
			resource := pdfIRResource(row.DatasetID, row.ID)
			parseJob, e := asyncjob.Enqueue(ctx, store.DB(), asyncjob.EnqueueRequest{JobType: pdfIRJobType, ResourceType: "document_pdf_ir", ResourceID: resource, IdempotencyKey: pdfir.Digest(resource + hash + pdfir.Revision), Payload: pdfIRPayload{Revision: pdfir.Revision, DatasetID: p.DatasetID, DocumentID: p.DocumentID, SourceSHA256: hash, Automatic: p.Automatic}, CreateUserID: job.CreateUserID, MaxAttempts: 2, SkipSucceeded: true})
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
	count := 0
	if needsIR {
		state, e := pdfIRStatus(ctx, row, hash)
		if e != nil {
			return result, e
		}
		if state.Document == nil {
			return result, errors.New("PDF 结构尚未就绪")
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
			if count >= limit {
				break
			}
			current, e := readingpolicy.Load(ctx, store.DB(), job.CreateUserID)
			if e != nil {
				return result, e
			}
			if count >= current.Limits.StudyItemsPerDocument {
				break
			}
			if e = readingpolicy.Check(ctx, store.DB(), job.CreateUserID, key); p.Automatic && e != nil {
				return result, e
			}
			rr, e := preparationRequest(ctx, job.CreateUserID, p)
			if e != nil {
				return result, e
			}
			rr = mux.SetURLVars(rr, map[string]string{"dataset": p.DatasetID, "document": p.DocumentID, "block": block.ID})
			if key == readingpolicy.Formula {
				_, _, e = defaultFormulaHandler().explain(rr, formulaReadingRequest{Language: "zh"})
			} else {
				_, _, e = defaultVisualHandler().read(rr, paperReadingRequest{Language: "zh"})
			}
			if e != nil {
				return result, fmt.Errorf("%s 准备失败: %w", key, e)
			}
			count++
			if e = reporter.SetProgress(ctx, int64(1+count), total); e != nil {
				return result, e
			}
		}
	}
	if err := reporter.SetProgress(ctx, int64(1+count), int64(1+count)); err != nil {
		return result, err
	}
	result.ResultJSON = mustJSON(map[string]any{"study_items": count, "source_sha256": hash})
	return result, nil
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
	calls int
}

func recordReadingModelCall(ctx context.Context) error {
	counter, ok := ctx.Value(readingCallCounterKey{}).(*readingCallCounter)
	if !ok {
		return nil
	}
	counter.mu.Lock()
	defer counter.mu.Unlock()
	counter.calls++
	return store.DB().WithContext(ctx).Model(&orm.AsyncJob{}).Where("id=?", counter.jobID).Update("result_json", mustJSON(map[string]any{"model_requests": counter.calls})).Error
}
