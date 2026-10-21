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
	EntryType         string               `json:"entry_type"`
	TargetDatasetID   string               `json:"target_dataset_id"`
	TargetPID         string               `json:"target_pid,omitempty"`
	SourceDocumentIDs []string             `json:"source_document_ids,omitempty"`
	Items             []selectedImportItem `json:"items"`
	IdempotencyKey    string               `json:"idempotency_key,omitempty"`
}

type paperImportPayload struct {
	BatchID  string `json:"batch_id"`
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
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
	if !canWriteDataset(r.Context(), req.TargetDatasetID, userID) {
		replyError(w, http.StatusForbidden, "TARGET_DATASET_FORBIDDEN", "target knowledge base is not writable")
		return
	}
	entryType := strings.TrimSpace(req.EntryType)
	if entryType == "" {
		entryType = "single_reference"
	}
	if entryType != "single_reference" && entryType != "paper_references" && entryType != "collection_references" {
		replyError(w, 400, "INVALID_ENTRY_TYPE", "invalid entry_type")
		return
	}
	now := time.Now().UTC()
	batchID := uuid.NewString()
	sources, _ := json.Marshal(req.SourceDocumentIDs)
	policy, _ := json.Marshal(map[string]any{"version_policy": "skip_exact"})
	batch := orm.PaperImportBatch{ID: batchID, EntryType: entryType, TargetDatasetID: req.TargetDatasetID, TargetPID: req.TargetPID, SourceDocumentIDsJSON: sources, PolicySnapshotJSON: policy, Status: "pending", TotalItems: len(req.Items), IdempotencyKey: strings.TrimSpace(req.IdempotencyKey), CreatedBy: userID, CreatedAt: now, UpdatedAt: now}
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
		if seen[input.WorkID] {
			continue
		}
		seen[input.WorkID] = true
		var count int64
		if store.DB().WithContext(r.Context()).Model(&orm.AcademicWork{}).Where("id = ?", input.WorkID).Count(&count).Error != nil || count == 0 {
			replyError(w, 422, "WORK_NOT_FOUND", "one or more works are unresolved")
			return
		}
		var active int64
		if err := store.DB().WithContext(r.Context()).Table("paper_import_items pi").
			Joins("JOIN paper_import_batches pb ON pb.id = pi.batch_id").
			Where("pi.academic_work_id = ? AND pb.target_dataset_id = ? AND pb.created_by = ? AND pi.status IN ?", input.WorkID, req.TargetDatasetID, userID, []string{"pending", "running"}).
			Count(&active).Error; err != nil {
			replyError(w, http.StatusInternalServerError, "IMPORT_QUERY_FAILED", err.Error())
			return
		}
		if active > 0 {
			alreadyQueued++
			continue
		}
		refs, _ := json.Marshal(input.ReferenceIDs)
		candidate, _ := json.Marshal(input.Candidate)
		stage := "acquire"
		if input.Candidate.URL == "" {
			stage = "resolve"
		}
		items = append(items, orm.PaperImportItem{ID: uuid.NewString(), BatchID: batchID, AcademicWorkID: input.WorkID, ReferenceIDsJSON: refs, PresenceSnapshotJSON: json.RawMessage("{}"), SelectedCandidateJSON: candidate, Stage: stage, Status: "pending", ErrorDetailsJSON: json.RawMessage("{}"), CreatedAt: now, UpdatedAt: now})
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
			pi.created_at, pi.updated_at`).
		Joins("JOIN paper_import_batches pb ON pb.id = pi.batch_id").
		Joins("JOIN academic_works aw ON aw.id = pi.academic_work_id").
		Where("pb.created_by = ?", userID)
	if datasetID != "" {
		query = query.Where("pb.target_dataset_id = ?", datasetID)
	}
	if sourceDocumentID != "" {
		sourceIDs, _ := json.Marshal([]string{sourceDocumentID})
		query = query.Where("pb.source_document_ids_json::jsonb @> ?::jsonb", string(sourceIDs))
	}
	var records []struct {
		ImportQueueItem
		ErrorDetailsJSON json.RawMessage
	}
	if err := query.Order("pi.created_at DESC").Limit(200).Scan(&records).Error; err != nil {
		replyError(w, http.StatusInternalServerError, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	rows := make([]ImportQueueItem, 0, len(records))
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
	var failed int64
	if err := store.DB().WithContext(r.Context()).Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status = 'failed'", batchID).Count(&failed).Error; err != nil {
		replyError(w, http.StatusInternalServerError, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	if failed == 0 {
		replyError(w, http.StatusConflict, "NO_FAILED_ITEMS", "there are no failed items to retry")
		return
	}
	queued, err := asyncjob.Enqueue(r.Context(), store.DB(), asyncjob.EnqueueRequest{JobType: paperImportJobType, ResourceType: "paper_import_batch", ResourceID: batch.ID,
		IdempotencyKey: "paper-import-retry:" + batch.ID + ":" + uuid.NewString(), Payload: paperImportPayload{BatchID: batch.ID, UserID: userID, UserName: store.UserName(r)},
		CreateUserID: userID, CreateUserName: store.UserName(r)})
	if err != nil {
		replyError(w, 500, "IMPORT_RETRY_FAILED", err.Error())
		return
	}
	if err := store.DB().WithContext(r.Context()).Model(&batch).Updates(map[string]any{"status": "pending", "async_job_id": queued.ID, "updated_at": time.Now().UTC()}).Error; err != nil {
		replyError(w, http.StatusInternalServerError, "IMPORT_RETRY_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"batch_id": batch.ID, "job_id": queued.ID, "status": "pending", "retry_items": failed})
}

func handlePaperImportJob(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (result asyncjob.Result, jobErr error) {
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
	if err := store.DB().WithContext(ctx).Where("batch_id = ? AND status IN ?", batch.ID, []string{"pending", "failed"}).Order("created_at ASC").Find(&items).Error; err != nil {
		return asyncjob.Result{}, err
	}
	if err := store.DB().WithContext(ctx).Model(&batch).Updates(map[string]any{"status": "running", "updated_at": time.Now().UTC()}).Error; err != nil {
		return asyncjob.Result{ErrorCode: "import_status_failed"}, err
	}
	completed, failed, skipped := 0, 0, 0
	for index := range items {
		item := &items[index]
		if err := reporter.Heartbeat(ctx); err != nil {
			return asyncjob.Result{ErrorCode: "import_status_failed"}, err
		}
		if err := store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"status": "running", "attempt_count": item.AttemptCount + 1, "error_code": "", "updated_at": time.Now().UTC()}).Error; err != nil {
			return asyncjob.Result{ErrorCode: "import_status_failed"}, err
		}
		var candidate FulltextCandidate
		if err := json.Unmarshal(item.SelectedCandidateJSON, &candidate); err != nil {
			return asyncjob.Result{Permanent: true, ErrorCode: "invalid_import_data"}, err
		}
		if presentWork, presentDocument, ok := findEquivalentPresentWork(ctx, item.AcademicWorkID, batch.TargetDatasetID); ok {
			if err := associateReferencesWithWork(ctx, item.ReferenceIDsJSON, presentWork.ID, "presence"); err != nil {
				return asyncjob.Result{ErrorCode: "reference_update_failed"}, err
			}
			item.AcademicWorkID = presentWork.ID
			skipped++
			if err := store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"academic_work_id": presentWork.ID, "document_id": presentDocument.DocumentID, "status": "skipped", "stage": "done", "error_code": "already_present", "updated_at": time.Now().UTC()}).Error; err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			if err := reporter.SetProgress(ctx, int64(index+1), int64(len(items))); err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			continue
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
				failed++
				if err := markItemFailed(ctx, item, "reference_not_found", errors.New("reference metadata is missing")); err != nil {
					return asyncjob.Result{ErrorCode: "import_status_failed"}, err
				}
				continue
			}
			var ref orm.AcademicReference
			if err := store.DB().WithContext(ctx).Where("id = ?", referenceIDs[0]).Take(&ref).Error; err != nil {
				failed++
				if err := markItemFailed(ctx, item, "reference_not_found", err); err != nil {
					return asyncjob.Result{ErrorCode: "import_status_failed"}, err
				}
				continue
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
				failed++
				if err := markItemFailed(ctx, item, "resolution_failed", err); err != nil {
					return asyncjob.Result{ErrorCode: "import_status_failed"}, err
				}
				continue
			}
			item.AcademicWorkID = work.ID
			if err := store.DB().WithContext(ctx).Model(item).Update("academic_work_id", work.ID).Error; err != nil {
				failed++
				if err := markItemFailed(ctx, item, "resolution_failed", err); err != nil {
					return asyncjob.Result{ErrorCode: "import_status_failed"}, err
				}
				continue
			}
			candidates := candidatesForWork(work)
			if len(candidates) == 0 {
				if pageURL := extractReferenceURL(ref.RawText); pageURL != "" {
					if isGitHubReferenceURL(pageURL) {
						skipped++
						if err := store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"status": "skipped", "stage": "done", "error_code": "external_link", "error_details_json": json.RawMessage(`{"message":"GitHub code reference is available as an external link"}`), "updated_at": time.Now().UTC()}).Error; err != nil {
							return asyncjob.Result{ErrorCode: "import_status_failed"}, err
						}
						if err := reporter.SetProgress(ctx, int64(index+1), int64(len(items))); err != nil {
							return asyncjob.Result{ErrorCode: "import_status_failed"}, err
						}
						continue
					}
					candidates = []FulltextCandidate{{URL: pageURL, SourceType: "reference_page", SourceProvider: "web", ExpectedMIME: "text/html"}}
				} else {
					failed++
					if err := markItemFailed(ctx, item, "resource_not_found", errors.New("no downloadable reference resource was found")); err != nil {
						return asyncjob.Result{ErrorCode: "import_status_failed"}, err
					}
					continue
				}
			}
			candidate = candidates[0]
			encoded, _ := json.Marshal(candidate)
			if err := store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"selected_candidate_json": encoded, "stage": "acquire", "updated_at": time.Now().UTC()}).Error; err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
		}
		if presentWork, presentDocument, ok := findEquivalentPresentWork(ctx, item.AcademicWorkID, batch.TargetDatasetID); ok {
			if err := associateReferencesWithWork(ctx, item.ReferenceIDsJSON, presentWork.ID, "presence"); err != nil {
				return asyncjob.Result{ErrorCode: "reference_update_failed"}, err
			}
			item.AcademicWorkID = presentWork.ID
			skipped++
			if err := store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"academic_work_id": presentWork.ID, "document_id": presentDocument.DocumentID, "status": "skipped", "stage": "done", "error_code": "already_present", "updated_at": time.Now().UTC()}).Error; err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			if err := reporter.SetProgress(ctx, int64(index+1), int64(len(items))); err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			continue
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
			path, _, err = acquirePDF(ctx, candidate)
		}
		if err != nil {
			failed++
			if err := markItemFailed(ctx, item, "download_failed", err); err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			if err := reporter.SetProgress(ctx, int64(index+1), int64(len(items))); err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			continue
		}
		if err := store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"stage": "import", "updated_at": time.Now().UTC()}).Error; err != nil {
			return asyncjob.Result{ErrorCode: "import_status_failed"}, err
		}
		var work orm.AcademicWork
		if err = store.DB().WithContext(ctx).Where("id = ?", item.AcademicWorkID).Take(&work).Error; err != nil {
			_ = os.Remove(path)
			failed++
			if err := markItemFailed(ctx, item, "work_not_found", err); err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			continue
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
		imported, importErr := doc.ImportLocalFile(ctx, doc.LocalFileImportRequest{DatasetID: batch.TargetDatasetID, ParentID: batch.TargetPID, SourcePath: path, DisplayName: filename, ContentType: contentType, DataSourceType: "ACADEMIC_PROVIDER", Tags: tags, UserID: payload.UserID, UserName: payload.UserName})
		_ = os.Remove(path)
		if importErr = validatePaperImportResult(imported, importErr); importErr != nil {
			failed++
			if err := markItemFailed(ctx, item, "import_failed", importErr); err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			if err := store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"document_id": imported.DocumentID, "document_task_id": imported.TaskID, "content_sha256": imported.ContentSHA256}).Error; err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, errors.Join(importErr, err)
			}
			if err := reporter.SetProgress(ctx, int64(index+1), int64(len(items))); err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			continue
		}
		_, version := NormalizeArxivID(candidate.URL)
		now := time.Now().UTC()
		link := orm.AcademicWorkDocument{AcademicWorkID: item.AcademicWorkID, DatasetID: batch.TargetDatasetID, DocumentID: imported.DocumentID, VersionKind: map[bool]string{true: "preprint", false: "unknown"}[candidate.SourceProvider == "arxiv"], SourceProvider: candidate.SourceProvider, SourceLocator: sourceURL, SourceVersion: version, ContentSHA256: imported.ContentSHA256, MatchMethod: "import", MatchConfidence: 1, IsPreferredVersion: true, CreatedAt: now, UpdatedAt: now}
		if err := store.DB().WithContext(ctx).Create(&link).Error; err != nil {
			failed++
			if err := markItemFailed(ctx, item, "link_failed", err); err != nil {
				return asyncjob.Result{ErrorCode: "import_status_failed"}, err
			}
			continue
		}
		completed++
		updates := map[string]any{"status": "succeeded", "stage": "done", "document_id": imported.DocumentID, "document_task_id": imported.TaskID, "content_sha256": imported.ContentSHA256, "updated_at": now}

		if err := store.DB().WithContext(ctx).Model(item).Updates(updates).Error; err != nil {
			return asyncjob.Result{ErrorCode: "import_status_failed"}, err
		}
		if err := reporter.SetProgress(ctx, int64(index+1), int64(len(items))); err != nil {
			return asyncjob.Result{ErrorCode: "import_status_failed"}, err
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

func findEquivalentPresentWork(ctx context.Context, workID, datasetID string) (orm.AcademicWork, orm.AcademicWorkDocument, bool) {
	var source orm.AcademicWork
	if workID == "" || store.DB().WithContext(ctx).Where("id = ?", workID).Take(&source).Error != nil {
		return orm.AcademicWork{}, orm.AcademicWorkDocument{}, false
	}
	conditions := []string{"aw.id = ?"}
	args := []any{source.ID}
	if source.DOINormalized != "" {
		conditions, args = append(conditions, "aw.doi_normalized = ?"), append(args, source.DOINormalized)
	}
	if source.ArxivIDBase != "" {
		conditions, args = append(conditions, "aw.arxiv_id_base = ?"), append(args, source.ArxivIDBase)
	}
	if source.NormalizedTitle != "" {
		conditions, args = append(conditions, "(aw.normalized_title = ? AND aw.publication_year = ?)"), append(args, source.NormalizedTitle, source.PublicationYear)
	}
	var row struct {
		WorkID     string
		DocumentID string
	}
	query := store.DB().WithContext(ctx).Table("academic_works aw").
		Select("aw.id AS work_id, awd.document_id").
		Joins("JOIN academic_work_documents awd ON awd.academic_work_id = aw.id").
		Where("awd.dataset_id = ?", datasetID).
		Where("("+strings.Join(conditions, " OR ")+")", args...).
		Order("CASE WHEN aw.id = '" + strings.ReplaceAll(source.ID, "'", "''") + "' THEN 0 ELSE 1 END, awd.is_preferred_version DESC, awd.updated_at DESC").
		Limit(1).Scan(&row)
	if query.Error != nil || row.WorkID == "" || row.DocumentID == "" {
		return orm.AcademicWork{}, orm.AcademicWorkDocument{}, false
	}
	var work orm.AcademicWork
	var document orm.AcademicWorkDocument
	if store.DB().WithContext(ctx).Where("id = ?", row.WorkID).Take(&work).Error != nil ||
		store.DB().WithContext(ctx).Where("academic_work_id = ? AND dataset_id = ? AND document_id = ?", row.WorkID, datasetID, row.DocumentID).Take(&document).Error != nil {
		return orm.AcademicWork{}, orm.AcademicWorkDocument{}, false
	}
	return work, document, true
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
