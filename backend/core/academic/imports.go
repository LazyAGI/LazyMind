package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/academic/arxivsource"
	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

const paperImportJobType = "academic.paper_import"

type selectedImportItem struct {
	WorkID       string            `json:"work_id"`
	ReferenceIDs []string          `json:"reference_ids,omitempty"`
	Candidate    FulltextCandidate `json:"candidate"`
}

type createImportRequest struct {
	PreviewToken      string               `json:"preview_token,omitempty"`
	EntryType         string               `json:"entry_type"`
	TargetDatasetID   string               `json:"target_dataset_id"`
	TargetPID         string               `json:"target_pid,omitempty"`
	SourceDocumentIDs []string             `json:"source_document_ids,omitempty"`
	Items             []selectedImportItem `json:"items"`
	IdempotencyKey    string               `json:"idempotency_key,omitempty"`
}

type paperImportPayload struct {
	ItemIDs  []string `json:"item_ids,omitempty"`
	BatchID  string   `json:"batch_id"`
	UserID   string   `json:"user_id"`
	UserName string   `json:"user_name"`
}

func init() { asyncjob.Register(paperImportJobType, handlePaperImportJob) }

func CreateImport(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req createImportRequest
	if !parseJSON(r, &req) || req.TargetDatasetID == "" || len(req.Items) == 0 || len(req.Items) > 500 {
		replyError(w, http.StatusBadRequest, "INVALID_REQUEST", "target_dataset_id and 1 to 500 items are required")
		return
	}
	if !doc.HasDatasetPermission(r, req.TargetDatasetID, acl.PermissionDatasetUpload) {
		replyError(w, http.StatusForbidden, "TARGET_DATASET_FORBIDDEN", "target knowledge base is not writable")
		return
	}
	entryType := strings.TrimSpace(req.EntryType)
	if entryType == "" {
		entryType = "single_reference"
	}
	if entryType != "single_reference" && entryType != "paper_references" && entryType != "collection_references" && entryType != "paper_link" {
		replyError(w, 400, "INVALID_ENTRY_TYPE", "invalid entry_type")
		return
	}
	if req.IdempotencyKey != "" {
		requestGuard, err := lockArxivImport(r.Context(), store.DB(), "request:"+userID+":"+strings.TrimSpace(req.IdempotencyKey))
		if err != nil {
			replyError(w, 500, "IMPORT_CREATE_FAILED", err.Error())
			return
		}
		defer requestGuard.release()
		r = r.WithContext(requestGuard.ctx)
		var existing orm.PaperImportBatch
		err = store.DB().WithContext(r.Context()).Where("created_by=? AND idempotency_key=?", userID, strings.TrimSpace(req.IdempotencyKey)).Take(&existing).Error
		if err == nil {
			if existing.TargetDatasetID != req.TargetDatasetID {
				replyError(w, 409, "INVALID_REQUEST", "导入请求标识已用于其他论文集")
				return
			}
			common.ReplyJSON(w, map[string]any{"batch_id": existing.ID, "job_id": existing.AsyncJobID, "status": existing.Status, "total_items": existing.TotalItems})
			return
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
			return
		}
	}
	var preview *importPreviewReceipt
	if req.PreviewToken != "" {
		receipt, err := loadImportPreview(r.Context(), req.PreviewToken, userID, req.TargetDatasetID)
		if err != nil {
			replyError(w, 409, "IMPORT_PREVIEW_EXPIRED", err.Error())
			return
		}
		preview = &receipt
	}
	now := time.Now().UTC()
	batchID := uuid.NewString()
	sources, _ := json.Marshal(req.SourceDocumentIDs)
	policySnapshot := map[string]any{"version_policy": "skip_exact"}
	sourceContext := map[string]any{}
	if preview != nil && preview.ExpansionID != "" {
		record, ok := ownedExpansion(w, r, userID, preview.ExpansionID)
		if !ok {
			return
		}
		if record.DatasetID != req.TargetDatasetID {
			replyError(w, 400, "INVALID_REQUEST", "target must be the expansion collection")
			return
		}
		entryType = "paper_expansion"
		sourceContext["expansion_id"], sourceContext["expansion_version"], sourceContext["title"] = record.ID, preview.ExpansionVersion, record.Title
		policySnapshot["expansion_id"], policySnapshot["expansion_version"] = preview.ExpansionID, preview.ExpansionVersion
	}
	policy, _ := json.Marshal(policySnapshot)
	batch := orm.PaperImportBatch{ID: batchID, EntryType: entryType, TargetDatasetID: req.TargetDatasetID, TargetPID: req.TargetPID, SourceDocumentIDsJSON: sources, PolicySnapshotJSON: policy, Status: "pending", TotalItems: len(req.Items), IdempotencyKey: strings.TrimSpace(req.IdempotencyKey), CreatedBy: userID, CreatedAt: now, UpdatedAt: now}
	batch.SourceContextJSON = marshal(sourceContext)
	items := make([]orm.PaperImportItem, 0, len(req.Items))
	seen := map[string]bool{}
	alreadyQueued := 0
	for _, input := range req.Items {
		if input.WorkID == "" && len(input.ReferenceIDs) > 0 {
			var ref orm.AcademicReference
			if err := store.DB().WithContext(r.Context()).Where("id = ?", input.ReferenceIDs[0]).Take(&ref).Error; err != nil {
				replyError(w, http.StatusUnprocessableEntity, "REFERENCE_NOT_FOUND", err.Error())
				return
			}
			work, _, _, err := resolveReferenceWithExternal(r.Context(), store.DB().WithContext(r.Context()), &ref, false)
			if err != nil {
				replyError(w, http.StatusUnprocessableEntity, "RESOLUTION_FAILED", err.Error())
				return
			}
			input.WorkID = work.ID
		}
		if input.WorkID == "" {
			replyError(w, http.StatusUnprocessableEntity, "WORK_NOT_FOUND", "one or more works are unresolved")
			return
		}
		var verified *PaperRef
		if preview != nil {
			var err error
			verified, err = preview.selected(input)
			if err != nil {
				replyError(w, 409, "IMPORT_PREVIEW_MISMATCH", err.Error())
				return
			}
		}
		key := input.WorkID + "/" + input.Candidate.Version
		if seen[key] {
			continue
		}
		seen[key] = true
		var count int64
		if store.DB().WithContext(r.Context()).Model(&orm.AcademicWork{}).Where("id = ?", input.WorkID).Count(&count).Error != nil || count == 0 {
			replyError(w, 422, "WORK_NOT_FOUND", "one or more works are unresolved")
			return
		}
		active, err := activePaperImport(r.Context(), userID, req.TargetDatasetID, input.WorkID, input.Candidate.Version)
		if err != nil {
			replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
			return
		}
		if active {
			alreadyQueued++
			continue
		}

		refs, _ := json.Marshal(input.ReferenceIDs)
		candidate, _ := json.Marshal(savedImportCandidate{FulltextCandidate: input.Candidate, VerifiedPaper: verified})
		stage := "acquire"
		if input.Candidate.URL == "" {
			stage = "resolve"
		}
		var work orm.AcademicWork
		if err := store.DB().WithContext(r.Context()).Select("id, canonical_title").Where("id=?", input.WorkID).Take(&work).Error; err != nil {
			replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
			return
		}
		items = append(items, orm.PaperImportItem{ID: uuid.NewString(), BatchID: batchID, AcademicWorkID: input.WorkID, TitleSnapshot: work.CanonicalTitle, ReferenceIDsJSON: refs, PresenceSnapshotJSON: json.RawMessage("{}"), SelectedCandidateJSON: candidate, Stage: stage, Status: "pending", ErrorDetailsJSON: json.RawMessage("{}"), CreatedAt: now, UpdatedAt: now})
	}
	if len(items) == 0 {
		if alreadyQueued > 0 {
			common.ReplyJSON(w, map[string]any{"status": "already_queued", "total_items": 0})
			return
		}
		replyError(w, 400, "NO_IMPORTABLE_ITEMS", "no unique downloadable items were selected")
		return
	}
	batch.TotalItems = len(items)
	var job *orm.AsyncJob
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if batch.IdempotencyKey != "" {
			var existing orm.PaperImportBatch
			if tx.Where("created_by = ? AND idempotency_key = ?", userID, batch.IdempotencyKey).Take(&existing).Error == nil {
				batch = existing
				return nil
			}
		}
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		if err := tx.Create(&items).Error; err != nil {
			return err
		}
		queued, err := asyncjob.EnqueueInTransaction(r.Context(), tx, asyncjob.EnqueueRequest{JobType: paperImportJobType, ResourceType: "paper_import_batch", ResourceID: batch.ID, IdempotencyKey: "paper-import:" + batch.ID, Payload: paperImportPayload{BatchID: batch.ID, UserID: userID, UserName: store.UserName(r)}, CreateUserID: userID, CreateUserName: store.UserName(r)})
		if err != nil {
			return err
		}
		job = queued
		return tx.Model(&batch).Updates(map[string]any{"async_job_id": job.ID, "updated_at": now}).Error
	})
	if err != nil {
		replyError(w, 500, "IMPORT_CREATE_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"batch_id": batch.ID, "job_id": batch.AsyncJobID, "status": batch.Status, "total_items": batch.TotalItems})
}

func ListImports(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	datasetID := strings.TrimSpace(r.URL.Query().Get("target_dataset_id"))
	sourceDocumentID := strings.TrimSpace(r.URL.Query().Get("source_document_id"))
	query := store.DB().WithContext(r.Context()).Table("paper_import_items pi").
		Select(`pi.id AS item_id, pi.batch_id, pi.academic_work_id AS work_id, aw.canonical_title AS title,
			pi.status, pi.stage, pi.document_id, pi.error_code, pi.error_details_json,
			pi.created_at, pi.updated_at, pi.selected_candidate_json, pb.target_dataset_id, pb.status AS batch_status, pb.failed_items AS batch_failed_items`).
		Joins("JOIN paper_import_batches pb ON pb.id = pi.batch_id").
		Joins("JOIN academic_works aw ON aw.id = pi.academic_work_id").
		Where("pb.created_by = ?", userID)
	if datasetID != "" {
		query = query.Where("pb.target_dataset_id = ?", datasetID)
	}
	if sourceDocumentID != "" {
		if store.DB().Dialector.Name() == "sqlite" {
			query = query.Where("EXISTS (SELECT 1 FROM json_each(pb.source_document_ids_json) WHERE json_each.value = ?)", sourceDocumentID)
		} else {
			sourceIDs, _ := json.Marshal([]string{sourceDocumentID})
			query = query.Where("pb.source_document_ids_json::jsonb @> ?::jsonb", string(sourceIDs))
		}
	}
	var records []struct {
		ImportQueueItem
		ErrorDetailsJSON      json.RawMessage
		SelectedCandidateJSON json.RawMessage
	}
	if err := query.Order("pi.created_at DESC").Limit(200).Scan(&records).Error; err != nil {
		replyError(w, http.StatusInternalServerError, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	rows := make([]ImportQueueItem, 0, len(records))
	current := map[string]*ImportQueueDocument{}
	for _, record := range records {
		var details struct {
			Message string `json:"message"`
		}
		if len(record.ErrorDetailsJSON) > 0 {
			if err := json.Unmarshal(record.ErrorDetailsJSON, &details); err != nil {
				replyError(w, http.StatusInternalServerError, "IMPORT_QUERY_FAILED", "invalid import error details")
				return
			}
		}
		record.ErrorMessage = details.Message
		var candidate FulltextCandidate
		if err := json.Unmarshal(record.SelectedCandidateJSON, &candidate); err != nil {
			replyError(w, 500, "IMPORT_QUERY_FAILED", "invalid import source")
			return
		}
		record.SourceProvider, record.SourceVersion = candidate.SourceProvider, candidate.Version
		key := string(marshal([]string{record.TargetDatasetID, record.WorkID, record.SourceProvider, record.SourceVersion, record.DocumentID}))
		document, exists := current[key]
		if !exists {
			var err error
			document, err = currentImportDocument(r.Context(), store.DB(), record.ImportQueueItem)
			if err != nil {
				replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
				return
			}
			current[key] = document
		}
		record.CurrentDocument = document
		rows = append(rows, record.ImportQueueItem)
	}
	common.ReplyJSON(w, map[string]any{"items": rows, "total": len(rows)})
}

func GetImport(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	batchID := mux.Vars(r)["batch_id"]
	var batch orm.PaperImportBatch
	if err := store.DB().WithContext(r.Context()).Where("id = ? AND created_by = ?", batchID, userID).Take(&batch).Error; err != nil {
		replyError(w, 404, "IMPORT_NOT_FOUND", "import batch not found")
		return
	}
	var items []orm.PaperImportItem
	if err := store.DB().WithContext(r.Context()).Where("batch_id = ?", batchID).Order("created_at ASC").Find(&items).Error; err != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"batch": batch, "items": items})
}

func CancelImport(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	batchID := mux.Vars(r)["batch_id"]
	var batch orm.PaperImportBatch
	if err := store.DB().WithContext(r.Context()).Where("id = ? AND created_by = ?", batchID, userID).Take(&batch).Error; err != nil {
		replyError(w, 404, "IMPORT_NOT_FOUND", "import batch not found")
		return
	}
	if batch.AsyncJobID != "" {
		if _, err := asyncjob.CancelResourceJobs(r.Context(), store.DB(), paperImportJobType, "paper_import_batch", batch.ID, "user canceled paper import"); err != nil {
			replyError(w, http.StatusInternalServerError, "IMPORT_CANCEL_FAILED", err.Error())
			return
		}
	}
	if err := store.DB().WithContext(r.Context()).Model(&batch).Updates(map[string]any{"status": "canceled", "updated_at": time.Now().UTC()}).Error; err != nil {
		replyError(w, http.StatusInternalServerError, "IMPORT_CANCEL_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"batch_id": batch.ID, "status": "canceled"})
}

func RetryImport(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	batchID := mux.Vars(r)["batch_id"]
	var batch orm.PaperImportBatch
	if err := store.DB().WithContext(r.Context()).Where("id = ? AND created_by = ?", batchID, userID).Take(&batch).Error; err != nil {
		replyError(w, http.StatusNotFound, "IMPORT_NOT_FOUND", "import batch not found")
		return
	}
	if !doc.HasDatasetPermission(r, batch.TargetDatasetID, acl.PermissionDatasetUpload) {
		replyError(w, http.StatusForbidden, "TARGET_DATASET_FORBIDDEN", "目标论文集当前不可写，无法重试导入")
		return
	}
	guard, err := lockArxivImport(r.Context(), store.DB(), "batch:"+batchID)
	if err != nil {
		replyError(w, 500, "IMPORT_RETRY_FAILED", err.Error())
		return
	}
	defer guard.release()
	if err = store.DB().WithContext(r.Context()).Where("id=? AND created_by=?", batchID, userID).Take(&batch).Error; err != nil {
		replyError(w, 404, "IMPORT_NOT_FOUND", "import batch not found")
		return
	}
	if batch.Status == "pending" || batch.Status == "running" {
		common.ReplyJSON(w, map[string]any{"batch_id": batch.ID, "job_id": batch.AsyncJobID, "status": batch.Status})
		return
	}
	operations, err := loadImportOperations(r.Context(), store.DB(), []orm.PaperImportBatch{batch}, true, map[string]*ImportQueueDocument{})
	if err != nil {
		replyError(w, 500, "IMPORT_RETRY_FAILED", err.Error())
		return
	}
	ids := []string{}
	resumed := 0
	for _, item := range operations[0].Items {
		if !item.CanRetry {
			continue
		}
		if item.CurrentDocument != nil {
			imported, resumeErr := doc.ResumeLocalFileImport(r.Context(), doc.ResumeLocalFileImportRequest{DatasetID: batch.TargetDatasetID, DocumentID: item.CurrentDocument.DocumentID, TaskID: item.taskID, UserID: userID, UserName: store.UserName(r)})
			if resumeErr != nil {
				replyError(w, 500, "IMPORT_RETRY_FAILED", resumeErr.Error())
				return
			}
			if err := store.DB().WithContext(r.Context()).Model(&orm.PaperImportItem{}).Where("id=?", item.ItemID).Updates(map[string]any{"document_task_id": imported.TaskID, "status": "succeeded", "result_kind": "imported", "error_code": "", "error_details_json": json.RawMessage(`{}`), "updated_at": time.Now().UTC()}).Error; err != nil {
				replyError(w, 500, "IMPORT_RETRY_FAILED", err.Error())
				return
			}
			resumed++
		} else if batch.EntryType != "pdf_upload" {
			ids = append(ids, item.ItemID)
		}
	}
	if len(ids) == 0 {
		if resumed == 0 {
			replyError(w, 409, "NO_FAILED_ITEMS", "there are no failed items to retry")
			return
		}
		if err := store.DB().WithContext(r.Context()).Model(&batch).Updates(map[string]any{"status": "succeeded", "updated_at": time.Now().UTC()}).Error; err != nil {
			replyError(w, 500, "IMPORT_RETRY_FAILED", err.Error())
			return
		}
		common.ReplyJSON(w, map[string]any{"batch_id": batch.ID, "retry_items": resumed})
		return
	}
	var queued *orm.AsyncJob
	err = store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		queued, err = asyncjob.EnqueueInTransaction(r.Context(), tx, asyncjob.EnqueueRequest{JobType: paperImportJobType, ResourceType: "paper_import_batch", ResourceID: batch.ID, IdempotencyKey: "paper-import-retry:" + batch.ID + ":" + uuid.NewString(), Payload: paperImportPayload{BatchID: batch.ID, UserID: userID, UserName: store.UserName(r), ItemIDs: ids}, CreateUserID: userID, CreateUserName: store.UserName(r)})
		if err != nil {
			return err
		}
		return tx.Model(&batch).Updates(map[string]any{"status": "pending", "async_job_id": queued.ID, "updated_at": time.Now().UTC()}).Error
	})
	if err != nil {
		replyError(w, 500, "IMPORT_RETRY_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"batch_id": batch.ID, "job_id": queued.ID, "status": "pending", "retry_items": len(ids) + resumed})
}

type paperImportService struct {
	acquire    func(context.Context, FulltextCandidate) (string, int64, error)
	importFile func(context.Context, doc.LocalFileImportRequest) (doc.LocalFileImportResult, error)
}

func handlePaperImportJob(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
	return (paperImportService{acquire: acquirePDF, importFile: doc.ImportLocalFile}).handleJob(ctx, job, reporter)
}

func (s paperImportService) handleJob(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (result asyncjob.Result, jobErr error) {
	var payload paperImportPayload
	if json.Unmarshal(job.PayloadJSON, &payload) != nil || payload.BatchID == "" {
		return asyncjob.Result{Permanent: true, ErrorCode: "invalid_payload"}, errors.New("invalid paper import payload")
	}
	var batch orm.PaperImportBatch
	if err := store.DB().WithContext(ctx).Where("id = ?", payload.BatchID).Take(&batch).Error; err != nil {
		return asyncjob.Result{Permanent: true, ErrorCode: "batch_not_found"}, err
	}
	finished := false
	defer func() {
		if jobErr == nil || finished {
			return
		}
		// Interrupted jobs must leave visible, retryable items even when their
		// request context has expired. Keep this final persistence attempt bounded.
		finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := failInterruptedPaperImport(finalCtx, batch.ID, result.ErrorCode, jobErr); err != nil {
			jobErr = errors.Join(jobErr, fmt.Errorf("save interrupted import failure: %w", err))
		}
	}()
	var items []orm.PaperImportItem
	itemQuery := store.DB().WithContext(ctx).Where("batch_id = ? AND status IN ?", batch.ID, []string{"pending", "failed"})
	if len(payload.ItemIDs) > 0 {
		itemQuery = itemQuery.Where("id IN ?", payload.ItemIDs)
	}
	if err := itemQuery.Order("created_at ASC").Find(&items).Error; err != nil {
		return asyncjob.Result{}, err
	}
	if err := store.DB().WithContext(ctx).Model(&batch).Updates(map[string]any{"status": "running", "updated_at": time.Now().UTC()}).Error; err != nil {
		return asyncjob.Result{ErrorCode: "import_status_failed"}, err
	}
	for index := range items {
		if result, err := s.handleItem(ctx, &batch, &items[index], payload, reporter, index, len(items)); err != nil {
			return result, err
		}
	}

	status := "succeeded"
	var totalCompleted, totalFailed, totalSkipped int64
	if err := store.DB().WithContext(ctx).Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status = 'succeeded'", batch.ID).Count(&totalCompleted).Error; err != nil {
		return asyncjob.Result{ErrorCode: "import_status_failed"}, err
	}
	if err := store.DB().WithContext(ctx).Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status = 'failed'", batch.ID).Count(&totalFailed).Error; err != nil {
		return asyncjob.Result{ErrorCode: "import_status_failed"}, err
	}
	if err := store.DB().WithContext(ctx).Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status = 'skipped'", batch.ID).Count(&totalSkipped).Error; err != nil {
		return asyncjob.Result{ErrorCode: "import_status_failed"}, err
	}
	if totalFailed > 0 {
		status = "completed_with_errors"
	}
	if totalCompleted == 0 && totalFailed > 0 {
		status = "failed"
	}
	if err := store.DB().WithContext(ctx).Model(&batch).Updates(map[string]any{"status": status, "completed_items": totalCompleted, "failed_items": totalFailed, "skipped_items": totalSkipped, "updated_at": time.Now().UTC()}).Error; err != nil {
		return asyncjob.Result{ErrorCode: "import_status_failed"}, err
	}
	finished = true
	encodedResult, _ := json.Marshal(map[string]any{"batch_id": batch.ID, "status": status, "completed": totalCompleted, "failed": totalFailed, "skipped": totalSkipped})
	if totalFailed > 0 {
		return asyncjob.Result{ResultJSON: encodedResult, Permanent: true, ErrorCode: "import_failed"}, fmt.Errorf("%d paper import items failed; inspect item errors before retrying", totalFailed)
	}
	return asyncjob.Result{ResultJSON: encodedResult}, nil
}

func finishPaperImportItem(ctx context.Context, batch *orm.PaperImportBatch, item *orm.PaperImportItem, candidate FulltextCandidate, sourceURL string, imported doc.LocalFileImportResult, importErr error) error {
	// Preserve partial success even if processing submission was canceled, so
	// a retry can continue with this exact document.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := store.DB().WithContext(persistCtx).Model(item).Updates(map[string]any{
		"document_id": imported.DocumentID, "document_task_id": imported.TaskID,
		"content_sha256": imported.ContentSHA256, "stage": "import", "updated_at": time.Now().UTC(),
	}).Error; err != nil {
		return errors.Join(importErr, err)
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(importErr, err)
	}
	if importErr = validatePaperImportResult(imported, importErr); importErr != nil {
		return markItemFailed(ctx, item, "import_failed", importErr)
	}
	_, version := NormalizeArxivID(candidate.URL)
	version = firstNonEmpty(candidate.Version, version)
	now := time.Now().UTC()
	link := orm.AcademicWorkDocument{AcademicWorkID: item.AcademicWorkID, DatasetID: batch.TargetDatasetID, DocumentID: imported.DocumentID, VersionKind: map[bool]string{true: "preprint", false: "unknown"}[candidate.SourceProvider == "arxiv"], SourceProvider: candidate.SourceProvider, SourceLocator: sourceURL, SourceVersion: version, ContentSHA256: imported.ContentSHA256, MatchMethod: "import", MatchConfidence: 1, IsPreferredVersion: true, CreatedAt: now, UpdatedAt: now}
	if err := store.DB().WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&link).Error; err != nil {
		return markItemFailed(ctx, item, "link_failed", err)
	}
	if err := bindImportedPaper(store.DB().WithContext(ctx), orm.Document{ID: imported.DocumentID, DatasetID: batch.TargetDatasetID}, orm.AcademicWork{ID: item.AcademicWorkID}); err != nil {
		return err
	}
	return store.DB().WithContext(ctx).Model(item).Updates(map[string]any{
		"status": "succeeded", "stage": "done", "error_code": "", "error_details_json": json.RawMessage(`{}`), "updated_at": now,
	}).Error
}

func failInterruptedPaperImport(ctx context.Context, batchID, code string, cause error) error {
	details, _ := json.Marshal(map[string]string{"message": cause.Error()})
	return store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status IN ?", batchID, []string{"pending", "running"}).Updates(map[string]any{
			"status": "failed", "error_code": firstNonEmpty(code, "import_interrupted"), "error_details_json": details, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		var counts []struct {
			Status string
			Total  int64
		}
		if err := tx.Model(&orm.PaperImportItem{}).Select("status, COUNT(*) AS total").Where("batch_id = ?", batchID).Group("status").Scan(&counts).Error; err != nil {
			return err
		}
		totals := map[string]int64{}
		for _, count := range counts {
			totals[count.Status] = count.Total
		}
		status := "failed"
		if totals["succeeded"] > 0 {
			status = "completed_with_errors"
		}
		return tx.Model(&orm.PaperImportBatch{}).Where("id = ?", batchID).Updates(map[string]any{
			"status":          gorm.Expr("CASE WHEN status = 'canceled' THEN status ELSE ? END", status),
			"completed_items": totals["succeeded"], "failed_items": totals["failed"], "skipped_items": totals["skipped"], "updated_at": now,
		}).Error
	})
}
func validatePaperImportResult(imported doc.LocalFileImportResult, importErr error) error {
	if importErr != nil {
		return importErr
	}
	if imported.DocumentID == "" {
		return errors.New("paper import did not return a document")
	}
	return nil
}

func findEquivalentPresentWork(ctx context.Context, workID, datasetID, unavailableDocumentID string, candidates ...FulltextCandidate) (orm.AcademicWork, orm.AcademicWorkDocument, bool) {
	var work orm.AcademicWork
	var link orm.AcademicWorkDocument
	if workID == "" || store.DB().WithContext(ctx).Where("id=?", workID).Take(&work).Error != nil {
		return work, link, false
	}
	query := store.DB().WithContext(ctx).Model(&orm.AcademicWorkDocument{}).Joins("JOIN documents d ON d.id=academic_work_documents.document_id AND d.dataset_id=academic_work_documents.dataset_id AND d.deleted_at IS NULL").Where("academic_work_documents.academic_work_id=? AND academic_work_documents.dataset_id=? AND academic_work_documents.document_id<>?", workID, datasetID, unavailableDocumentID)
	if len(candidates) > 0 {
		query = query.Where("academic_work_documents.source_version=?", candidates[0].Version)
		if candidates[0].SourceProvider != "" {
			query = query.Where("academic_work_documents.source_provider=?", candidates[0].SourceProvider)
		}
	}
	err := query.Order("academic_work_documents.is_preferred_version DESC, academic_work_documents.updated_at DESC").Take(&link).Error
	return work, link, err == nil
}

func associateReferencesWithWork(ctx context.Context, encoded json.RawMessage, workID, method string) error {
	var referenceIDs []string
	if err := json.Unmarshal(encoded, &referenceIDs); err != nil {
		return err
	}
	if len(referenceIDs) == 0 || workID == "" {
		return nil
	}
	return store.DB().WithContext(ctx).Model(&orm.AcademicReference{}).Where("id IN ?", referenceIDs).Updates(map[string]any{
		"resolved_work_id": workID, "resolution_status": "exact", "resolution_method": method,
		"resolution_confidence": 1, "updated_at": time.Now().UTC(),
	}).Error
}

func markItemFailed(ctx context.Context, item *orm.PaperImportItem, code string, err error) error {
	details, _ := json.Marshal(map[string]string{"message": err.Error()})
	return store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"status": "failed", "error_code": code, "error_details_json": details, "updated_at": time.Now().UTC()}).Error
}

func safePaperFilename(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r == '/' || r == '\\' || r == ':' || r == '\x00' {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
		if b.Len() >= 180 {
			break
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return fmt.Sprintf("paper-%d", time.Now().Unix())
	}
	return out
}

func (s paperImportService) handleItem(ctx context.Context, batch *orm.PaperImportBatch, item *orm.PaperImportItem, payload paperImportPayload, reporter asyncjob.Reporter, index int, total int) (asyncjob.Result, error) {

	if err := reporter.Heartbeat(ctx); err != nil {
		return asyncjob.Result{ErrorCode: "import_status_failed"}, err
	}
	if err := store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"status": "running", "attempt_count": item.AttemptCount + 1, "error_code": "", "error_details_json": json.RawMessage(`{}`), "updated_at": time.Now().UTC()}).Error; err != nil {
		return asyncjob.Result{ErrorCode: "import_status_failed"}, err
	}
	itemCtx := ctx
	guard, lockErr := lockArxivImport(ctx, store.DB(), "paper:"+item.AcademicWorkID)
	if lockErr != nil {
		return asyncjob.Result{}, lockErr
	}
	defer guard.release()
	ctx = guard.ctx
	var saved savedImportCandidate
	if err := json.Unmarshal(item.SelectedCandidateJSON, &saved); err != nil {
		return asyncjob.Result{Permanent: true, ErrorCode: "invalid_import_data"}, err
	}
	candidate := saved.FulltextCandidate
	if item.DocumentID != "" {
		imported, importErr := doc.ResumeLocalFileImport(ctx, doc.ResumeLocalFileImportRequest{
			DatasetID: batch.TargetDatasetID, DocumentID: item.DocumentID, TaskID: item.DocumentTaskID,
			UserID: payload.UserID, UserName: payload.UserName,
		})
		if !errors.Is(importErr, doc.ErrImportedDocumentUnavailable) {
			imported.ContentSHA256 = item.ContentSHA256
			if err := finishPaperImportItem(ctx, batch, item, candidate, candidate.URL, imported, importErr); err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			if err := reporter.SetProgress(ctx, int64(index+1), int64(total)); err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			return asyncjob.Result{}, nil
		}
	}

	if candidate.URL == "" {
		if err := store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"stage": "resolve", "updated_at": time.Now().UTC()}).Error; err != nil {
			return asyncjob.Result{ErrorCode: "import_status_failed"}, err
		}
		var referenceIDs []string
		if err := json.Unmarshal(item.ReferenceIDsJSON, &referenceIDs); err != nil {
			return asyncjob.Result{Permanent: true, ErrorCode: "invalid_import_data"}, err
		}
		if len(referenceIDs) == 0 {
			if err := markItemFailed(ctx, item, "reference_not_found", errors.New("reference metadata is missing")); err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			return asyncjob.Result{}, nil
		}
		var ref orm.AcademicReference
		if err := store.DB().WithContext(ctx).Where("id = ?", referenceIDs[0]).Take(&ref).Error; err != nil {
			if err := markItemFailed(ctx, item, "reference_not_found", err); err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			return asyncjob.Result{}, nil
		}
		// Re-resolve in the worker so external lookup never blocks the request.
		// This also repairs older rows whose DOI suffix was mistaken for an arXiv ID.
		if ref.DOINormalized != "" && !strings.Contains(strings.ToLower(ref.RawText), "arxiv") {
			ref.ArxivIDBase = ""
			if ref.ResolvedWorkID != "" {
				if err := store.DB().WithContext(ctx).Model(&orm.AcademicWork{}).Where("id = ?", ref.ResolvedWorkID).
					Updates(map[string]any{"arxiv_id_base": "", "updated_at": time.Now().UTC()}).Error; err != nil {
					return asyncjob.Result{ErrorCode: "import_status_failed"}, err
				}
			}
		}
		work, _, _, err := resolveReferenceWithExternal(ctx, store.DB().WithContext(ctx), &ref, true)
		if err != nil {
			if err := markItemFailed(ctx, item, "resolution_failed", err); err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			return asyncjob.Result{}, nil
		}
		item.AcademicWorkID = work.ID
		if err := store.DB().WithContext(ctx).Model(item).Update("academic_work_id", work.ID).Error; err != nil {
			if err := markItemFailed(ctx, item, "resolution_failed", err); err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			return asyncjob.Result{}, nil
		}
		candidates := candidatesForWork(work)
		if len(candidates) == 0 {
			if pageURL := extractReferenceURL(ref.RawText); pageURL != "" {
				if isGitHubReferenceURL(pageURL) {
					if err := store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"status": "skipped", "stage": "done", "error_code": "external_link", "error_details_json": json.RawMessage(`{"message":"GitHub code reference is available as an external link"}`), "updated_at": time.Now().UTC()}).Error; err != nil {
						return asyncjob.Result{ErrorCode: "import_status_failed"}, err
					}
					if err := reporter.SetProgress(ctx, int64(index+1), int64(total)); err != nil {
						return asyncjob.Result{ErrorCode: "import_status_failed"}, err
					}
					return asyncjob.Result{}, nil
				}
				candidates = []FulltextCandidate{{URL: pageURL, SourceType: "reference_page", SourceProvider: "web", ExpectedMIME: "text/html"}}
			} else {
				if err := markItemFailed(ctx, item, "resource_not_found", errors.New("no downloadable reference resource was found")); err != nil {
					return asyncjob.Result{ErrorCode: "import_status_failed"}, err
				}
				return asyncjob.Result{}, nil
			}
		}
		candidate = candidates[0]
		encoded, _ := json.Marshal(candidate)
		if err := store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"selected_candidate_json": encoded, "stage": "acquire", "updated_at": time.Now().UTC()}).Error; err != nil {
			return asyncjob.Result{ErrorCode: "import_status_failed"}, err
		}
	}
	if candidate.SourceProvider == "arxiv" {
		// Release the work lock before taking the canonical source lock. SQLite
		// coordination shards must never be recursively acquired.
		guard.release()
		ctx = itemCtx
		service := newArxivImportService(store.DB())
		service.acquire, service.importFile = s.acquire, s.importFile
		urls, err := canonicalPaperURLs(candidate)
		if err != nil {
			return asyncjob.Result{}, markItemFailed(ctx, item, "invalid_candidate", err)
		}
		if saved.VerifiedPaper == nil {
			saved.VerifiedPaper, err = verifiedImportPaper(ctx, store.DB(), payload.UserID, batch.TargetDatasetID, item.AcademicWorkID, candidate)
			if err != nil {
				return asyncjob.Result{}, err
			}
		}
		var identity orm.AcademicWork
		if err = store.DB().WithContext(ctx).Where("id=?", item.AcademicWorkID).Take(&identity).Error; err != nil {
			return asyncjob.Result{}, err
		}
		identifier, _ := arxivsource.Parse(urls[0])
		if identity.ArxivIDBase != "" && identity.ArxivIDBase != identifier.BaseID {
			return asyncjob.Result{}, markItemFailed(ctx, item, "invalid_candidate", errors.New("论文身份与 arXiv 附件来源不一致"))
		}
		var trusted *arxivsource.Metadata
		if saved.VerifiedPaper != nil {
			if metadata, ok := arxivMetadataFromRef(*saved.VerifiedPaper); ok && metadata.PDFURL == urls[0] {
				trusted = &metadata
			}
		}
		// Pin the provider response durably before any PDF download, including
		// legacy imports which did not carry a preview receipt.
		service.resolved = func(metadata arxivsource.Metadata) error {
			ref := paperRef(orm.AcademicWork{ID: item.AcademicWorkID})
			ref.Title, ref.Authors, ref.Year, ref.Venue, ref.DOI = metadata.Title, metadata.Authors, metadata.Year, metadata.Venue, metadata.DOI
			ref.Abstract, ref.ArxivID, ref.SourceProvider, ref.SourceVersion, ref.PDFURL, ref.PaperURL = &metadata.Abstract, metadata.BaseID, "arxiv", metadata.Version, metadata.PDFURL, metadata.AbsURL
			saved.FulltextCandidate.URL, saved.FulltextCandidate.Version = metadata.PDFURL, metadata.Version
			saved.VerifiedPaper = &ref
			item.SelectedCandidateJSON = marshal(saved)
			return store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"selected_candidate_json": item.SelectedCandidateJSON, "stage": "acquire", "updated_at": time.Now().UTC()}).Error
		}
		imported, err := service.importPaperWithMetadata(ctx, urls[0], batch.TargetDatasetID, payload.UserID, payload.UserName, trusted)
		if err != nil {
			if imported.DocumentID != "" {
				_ = store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"document_id": imported.DocumentID, "document_task_id": imported.DocumentTaskID}).Error
			}
			if e := markItemFailed(ctx, item, "download_or_processing_failed", err); e != nil {
				return asyncjob.Result{}, e
			}
			return asyncjob.Result{}, nil
		}
		candidate = saved.FulltextCandidate
		candidate.Version = imported.SourceVersion
		saved.FulltextCandidate = candidate
		encoded, _ := json.Marshal(saved)
		status := "succeeded"
		if imported.Status == "already_present" {
			status = "skipped"
		}
		if e := associateReferencesWithWork(ctx, item.ReferenceIDsJSON, imported.WorkID, "import"); e != nil {
			return asyncjob.Result{}, e
		}
		return asyncjob.Result{}, store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"academic_work_id": imported.WorkID, "document_id": imported.DocumentID, "document_task_id": imported.DocumentTaskID, "selected_candidate_json": encoded, "status": status, "stage": "done", "updated_at": time.Now().UTC()}).Error
	}
	if presentWork, presentDocument, ok := findEquivalentPresentWork(ctx, item.AcademicWorkID, batch.TargetDatasetID, item.DocumentID, candidate); ok {
		if err := associateReferencesWithWork(ctx, item.ReferenceIDsJSON, presentWork.ID, "presence"); err != nil {
			return asyncjob.Result{ErrorCode: "reference_update_failed"}, err
		}
		item.AcademicWorkID = presentWork.ID
		if err := store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"academic_work_id": presentWork.ID, "document_id": presentDocument.DocumentID, "status": "skipped", "stage": "done", "error_code": "already_present", "updated_at": time.Now().UTC()}).Error; err != nil {
			return asyncjob.Result{ErrorCode: "import_status_failed"}, err
		}
		if err := reporter.SetProgress(ctx, int64(index+1), int64(total)); err != nil {
			return asyncjob.Result{ErrorCode: "import_status_failed"}, err
		}
		return asyncjob.Result{}, nil
	}
	if err := store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"stage": "acquire", "updated_at": time.Now().UTC()}).Error; err != nil {
		return asyncjob.Result{ErrorCode: "import_status_failed"}, err
	}
	contentType, extension, sourceURL := "application/pdf", ".pdf", candidate.URL
	var path string
	var err error
	if candidate.SourceType == "reference_page" || candidate.SourceProvider == "web" {
		path, _, contentType, extension, sourceURL, err = acquireWebDocument(ctx, candidate.URL)
	} else {
		path, _, err = s.acquire(ctx, candidate)
	}
	if err != nil {
		if err := markItemFailed(ctx, item, "download_failed", err); err != nil {
			return asyncjob.Result{ErrorCode: "import_status_failed"}, err
		}
		if err := reporter.SetProgress(ctx, int64(index+1), int64(total)); err != nil {
			return asyncjob.Result{ErrorCode: "import_status_failed"}, err
		}
		return asyncjob.Result{}, nil
	}
	if err := store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"stage": "import", "updated_at": time.Now().UTC()}).Error; err != nil {
		return asyncjob.Result{ErrorCode: "import_status_failed"}, err
	}
	var work orm.AcademicWork
	if err = store.DB().WithContext(ctx).Where("id = ?", item.AcademicWorkID).Take(&work).Error; err != nil {
		_ = os.Remove(path)
		if err := markItemFailed(ctx, item, "work_not_found", err); err != nil {
			return asyncjob.Result{ErrorCode: "import_status_failed"}, err
		}
		return asyncjob.Result{}, nil
	}
	filename := strings.TrimSpace(work.CanonicalTitle)
	if filename == "" {
		filename = work.ArxivIDBase
	}
	filename = safePaperFilename(filename) + extension
	tags := []string{"paper"}
	if candidate.SourceProvider == "web" {
		tags = []string{"reference", "web"}
	}
	imported, importErr := s.importFile(ctx, doc.LocalFileImportRequest{DatasetID: batch.TargetDatasetID, ParentID: batch.TargetPID, SourcePath: path, DisplayName: filename, ContentType: contentType, DataSourceType: "ACADEMIC_PROVIDER", Tags: tags, UserID: payload.UserID, UserName: payload.UserName})
	_ = os.Remove(path)
	if err := finishPaperImportItem(ctx, batch, item, candidate, sourceURL, imported, importErr); err != nil {
		return asyncjob.Result{ErrorCode: "import_status_failed"}, err
	}
	if err := reporter.SetProgress(ctx, int64(index+1), int64(total)); err != nil {
		return asyncjob.Result{ErrorCode: "import_status_failed"}, err
	}
	return asyncjob.Result{}, nil
}

func activePaperImport(ctx context.Context, user, dataset, work, version string) (bool, error) {
	var items []orm.PaperImportItem
	err := store.DB().WithContext(ctx).Table("paper_import_items pi").Select("pi.*").Joins("JOIN paper_import_batches pb ON pb.id=pi.batch_id").Where("pi.academic_work_id=? AND pb.target_dataset_id=? AND pb.created_by=? AND pi.status IN ?", work, dataset, user, []string{"pending", "running"}).Find(&items).Error
	if err != nil {
		return false, err
	}
	for _, item := range items {
		var candidate FulltextCandidate
		if err = json.Unmarshal(item.SelectedCandidateJSON, &candidate); err != nil {
			return false, err
		}
		if version == candidate.Version {
			return true, nil
		}
	}
	return false, nil
}
