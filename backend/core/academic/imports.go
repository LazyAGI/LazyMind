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
				continue
			}
			work, _, _, err := resolveReferenceWithExternal(r.Context(), store.DB().WithContext(r.Context()), &ref, false)
			if err != nil {
				continue
			}
			input.WorkID = work.ID
		}
		if input.WorkID == "" || seen[input.WorkID] {
			continue
		}
		seen[input.WorkID] = true
		var count int64
		if store.DB().WithContext(r.Context()).Model(&orm.AcademicWork{}).Where("id = ?", input.WorkID).Count(&count).Error != nil || count == 0 {
			replyError(w, 422, "WORK_NOT_FOUND", "one or more works are unresolved")
			return
		}
		var active int64
		_ = store.DB().WithContext(r.Context()).Table("paper_import_items pi").
			Joins("JOIN paper_import_batches pb ON pb.id = pi.batch_id").
			Where("pi.academic_work_id = ? AND pb.target_dataset_id = ? AND pb.created_by = ? AND pi.status IN ?", input.WorkID, req.TargetDatasetID, userID, []string{"pending", "running"}).
			Count(&active).Error
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
	common.ReplyJSON(w, map[string]any{"batch_id": batch.ID, "job_id": job.ID, "status": batch.Status, "total_items": batch.TotalItems})
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
			CASE WHEN pi.status = 'failed' AND EXISTS (
				SELECT 1 FROM academic_works aw2 JOIN academic_work_documents awd2 ON awd2.academic_work_id = aw2.id
				WHERE awd2.dataset_id = pb.target_dataset_id AND (
					(aw.doi_normalized <> '' AND aw2.doi_normalized = aw.doi_normalized) OR
					(aw.arxiv_id_base <> '' AND aw2.arxiv_id_base = aw.arxiv_id_base) OR
					(aw.normalized_title <> '' AND aw2.normalized_title = aw.normalized_title AND aw2.publication_year = aw.publication_year)
				)
			) THEN 'skipped' ELSE pi.status END AS status,
			pi.stage, pi.document_id,
			CASE WHEN pi.status = 'failed' AND EXISTS (
				SELECT 1 FROM academic_works aw2 JOIN academic_work_documents awd2 ON awd2.academic_work_id = aw2.id
				WHERE awd2.dataset_id = pb.target_dataset_id AND (
					(aw.doi_normalized <> '' AND aw2.doi_normalized = aw.doi_normalized) OR
					(aw.arxiv_id_base <> '' AND aw2.arxiv_id_base = aw.arxiv_id_base) OR
					(aw.normalized_title <> '' AND aw2.normalized_title = aw.normalized_title AND aw2.publication_year = aw.publication_year)
				)
			) THEN 'already_present' ELSE pi.error_code END AS error_code,
			CASE WHEN pi.status = 'failed' AND EXISTS (
				SELECT 1 FROM academic_works aw2 JOIN academic_work_documents awd2 ON awd2.academic_work_id = aw2.id
				WHERE awd2.dataset_id = pb.target_dataset_id AND (
					(aw.doi_normalized <> '' AND aw2.doi_normalized = aw.doi_normalized) OR
					(aw.arxiv_id_base <> '' AND aw2.arxiv_id_base = aw.arxiv_id_base) OR
					(aw.normalized_title <> '' AND aw2.normalized_title = aw.normalized_title AND aw2.publication_year = aw.publication_year)
				)
			) THEN '已由后续任务下载并关联' ELSE COALESCE(pi.error_details_json::jsonb->>'message', '') END AS error_message,
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
	var rows []ImportQueueItem
	if err := query.Order("pi.created_at DESC").Limit(200).Scan(&rows).Error; err != nil {
		replyError(w, http.StatusInternalServerError, "IMPORT_QUERY_FAILED", err.Error())
		return
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
		_, _ = asyncjob.CancelResourceJobs(r.Context(), store.DB(), paperImportJobType, "paper_import_batch", batch.ID, "user canceled paper import")
	}
	_ = store.DB().WithContext(r.Context()).Model(&batch).Updates(map[string]any{"status": "canceled", "updated_at": time.Now().UTC()}).Error
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
	_ = store.DB().WithContext(r.Context()).Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status = 'failed'", batchID).Count(&failed).Error
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
	_ = store.DB().WithContext(r.Context()).Model(&batch).Updates(map[string]any{"status": "pending", "async_job_id": queued.ID, "updated_at": time.Now().UTC()}).Error
	common.ReplyJSON(w, map[string]any{"batch_id": batch.ID, "job_id": queued.ID, "status": "pending", "retry_items": failed})
}

func handlePaperImportJob(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
	var payload paperImportPayload
	if json.Unmarshal(job.PayloadJSON, &payload) != nil || payload.BatchID == "" {
		return asyncjob.Result{Permanent: true, ErrorCode: "invalid_payload"}, errors.New("invalid paper import payload")
	}
	var batch orm.PaperImportBatch
	if err := store.DB().WithContext(ctx).Where("id = ?", payload.BatchID).Take(&batch).Error; err != nil {
		return asyncjob.Result{Permanent: true, ErrorCode: "batch_not_found"}, err
	}
	var items []orm.PaperImportItem
	if err := store.DB().WithContext(ctx).Where("batch_id = ? AND status IN ?", batch.ID, []string{"pending", "failed"}).Order("created_at ASC").Find(&items).Error; err != nil {
		return asyncjob.Result{}, err
	}
	_ = store.DB().WithContext(ctx).Model(&batch).Updates(map[string]any{"status": "running", "updated_at": time.Now().UTC()}).Error
	completed, failed, skipped := 0, 0, 0
	for index := range items {
		item := &items[index]
		_ = reporter.Heartbeat(ctx)
		_ = store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"status": "running", "attempt_count": item.AttemptCount + 1, "error_code": "", "updated_at": time.Now().UTC()}).Error
		var candidate FulltextCandidate
		_ = json.Unmarshal(item.SelectedCandidateJSON, &candidate)
		if presentWork, presentDocument, ok := findEquivalentPresentWork(ctx, item.AcademicWorkID, batch.TargetDatasetID); ok {
			associateReferencesWithWork(ctx, item.ReferenceIDsJSON, presentWork.ID, "presence")
			item.AcademicWorkID = presentWork.ID
			skipped++
			_ = store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"academic_work_id": presentWork.ID, "document_id": presentDocument.DocumentID, "status": "skipped", "stage": "done", "error_code": "already_present", "updated_at": time.Now().UTC()}).Error
			_ = reporter.SetProgress(ctx, int64(index+1), int64(len(items)))
			continue
		}
		if candidate.URL == "" {
			_ = store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"stage": "resolve", "updated_at": time.Now().UTC()}).Error
			var referenceIDs []string
			_ = json.Unmarshal(item.ReferenceIDsJSON, &referenceIDs)
			if len(referenceIDs) == 0 {
				failed++
				markItemFailed(ctx, item, "reference_not_found", errors.New("reference metadata is missing"))
				continue
			}
			var ref orm.AcademicReference
			if err := store.DB().WithContext(ctx).Where("id = ?", referenceIDs[0]).Take(&ref).Error; err != nil {
				failed++
				markItemFailed(ctx, item, "reference_not_found", err)
				continue
			}
			// Re-resolve in the worker so external lookup never blocks the request.
			// This also repairs older rows whose DOI suffix was mistaken for an arXiv ID.
			if ref.DOINormalized != "" && !strings.Contains(strings.ToLower(ref.RawText), "arxiv") {
				ref.ArxivIDBase = ""
				if ref.ResolvedWorkID != "" {
					_ = store.DB().WithContext(ctx).Model(&orm.AcademicWork{}).Where("id = ?", ref.ResolvedWorkID).
						Updates(map[string]any{"arxiv_id_base": "", "updated_at": time.Now().UTC()}).Error
				}
			}
			work, _, _, err := resolveReferenceWithExternal(ctx, store.DB().WithContext(ctx), &ref, true)
			if err != nil {
				failed++
				markItemFailed(ctx, item, "resolution_failed", err)
				continue
			}
			item.AcademicWorkID = work.ID
			if err := store.DB().WithContext(ctx).Model(item).Update("academic_work_id", work.ID).Error; err != nil {
				failed++
				markItemFailed(ctx, item, "resolution_failed", err)
				continue
			}
			candidates := candidatesForWork(work)
			if len(candidates) == 0 {
				if pageURL := extractReferenceURL(ref.RawText); pageURL != "" {
					if isGitHubReferenceURL(pageURL) {
						skipped++
						_ = store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"status": "skipped", "stage": "done", "error_code": "external_link", "error_details_json": json.RawMessage(`{"message":"GitHub code reference is available as an external link"}`), "updated_at": time.Now().UTC()}).Error
						_ = reporter.SetProgress(ctx, int64(index+1), int64(len(items)))
						continue
					}
					candidates = []FulltextCandidate{{URL: pageURL, SourceType: "reference_page", SourceProvider: "web", ExpectedMIME: "text/html"}}
				} else {
					failed++
					markItemFailed(ctx, item, "resource_not_found", errors.New("no downloadable reference resource was found"))
					continue
				}
			}
			candidate = candidates[0]
			encoded, _ := json.Marshal(candidate)
			_ = store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"selected_candidate_json": encoded, "stage": "acquire", "updated_at": time.Now().UTC()}).Error
		}
		if presentWork, presentDocument, ok := findEquivalentPresentWork(ctx, item.AcademicWorkID, batch.TargetDatasetID); ok {
			associateReferencesWithWork(ctx, item.ReferenceIDsJSON, presentWork.ID, "presence")
			item.AcademicWorkID = presentWork.ID
			skipped++
			_ = store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"academic_work_id": presentWork.ID, "document_id": presentDocument.DocumentID, "status": "skipped", "stage": "done", "error_code": "already_present", "updated_at": time.Now().UTC()}).Error
			_ = reporter.SetProgress(ctx, int64(index+1), int64(len(items)))
			continue
		}
		_ = store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"stage": "acquire", "updated_at": time.Now().UTC()}).Error
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
			markItemFailed(ctx, item, "download_failed", err)
			_ = reporter.SetProgress(ctx, int64(index+1), int64(len(items)))
			continue
		}
		_ = store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"stage": "import", "updated_at": time.Now().UTC()}).Error
		var work orm.AcademicWork
		if err = store.DB().WithContext(ctx).Where("id = ?", item.AcademicWorkID).Take(&work).Error; err != nil {
			_ = os.Remove(path)
			failed++
			markItemFailed(ctx, item, "work_not_found", err)
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
		if importErr != nil && imported.DocumentID == "" {
			failed++
			markItemFailed(ctx, item, "import_failed", importErr)
			_ = reporter.SetProgress(ctx, int64(index+1), int64(len(items)))
			continue
		}
		_, version := NormalizeArxivID(candidate.URL)
		now := time.Now().UTC()
		link := orm.AcademicWorkDocument{AcademicWorkID: item.AcademicWorkID, DatasetID: batch.TargetDatasetID, DocumentID: imported.DocumentID, VersionKind: map[bool]string{true: "preprint", false: "unknown"}[candidate.SourceProvider == "arxiv"], SourceProvider: candidate.SourceProvider, SourceLocator: sourceURL, SourceVersion: version, ContentSHA256: imported.ContentSHA256, MatchMethod: "import", MatchConfidence: 1, IsPreferredVersion: true, CreatedAt: now, UpdatedAt: now}
		if err := store.DB().WithContext(ctx).Create(&link).Error; err != nil {
			failed++
			markItemFailed(ctx, item, "link_failed", err)
			continue
		}
		completed++
		updates := map[string]any{"status": "succeeded", "stage": "done", "document_id": imported.DocumentID, "document_task_id": imported.TaskID, "content_sha256": imported.ContentSHA256, "updated_at": now}
		if importErr != nil {
			updates["error_code"] = "processing_start_failed"
			details, _ := json.Marshal(map[string]string{"message": importErr.Error()})
			updates["error_details_json"] = details
		}
		_ = store.DB().WithContext(ctx).Model(item).Updates(updates).Error
		_ = reporter.SetProgress(ctx, int64(index+1), int64(len(items)))
	}
	status := "succeeded"
	var totalCompleted, totalFailed, totalSkipped int64
	_ = store.DB().WithContext(ctx).Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status = 'succeeded'", batch.ID).Count(&totalCompleted).Error
	_ = store.DB().WithContext(ctx).Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status = 'failed'", batch.ID).Count(&totalFailed).Error
	_ = store.DB().WithContext(ctx).Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status = 'skipped'", batch.ID).Count(&totalSkipped).Error
	if totalFailed > 0 {
		status = "completed_with_errors"
	}
	if totalCompleted == 0 && totalFailed > 0 {
		status = "failed"
	}
	_ = store.DB().WithContext(ctx).Model(&batch).Updates(map[string]any{"status": status, "completed_items": totalCompleted, "failed_items": totalFailed, "skipped_items": totalSkipped, "updated_at": time.Now().UTC()}).Error
	result, _ := json.Marshal(map[string]any{"batch_id": batch.ID, "status": status, "completed": totalCompleted, "failed": totalFailed, "skipped": totalSkipped})
	return asyncjob.Result{ResultJSON: result, Permanent: false, ErrorCode: map[bool]string{true: "partial_failure", false: ""}[failed > 0]}, nil
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

func associateReferencesWithWork(ctx context.Context, encoded json.RawMessage, workID, method string) {
	var referenceIDs []string
	_ = json.Unmarshal(encoded, &referenceIDs)
	if len(referenceIDs) == 0 || workID == "" {
		return
	}
	_ = store.DB().WithContext(ctx).Model(&orm.AcademicReference{}).Where("id IN ?", referenceIDs).Updates(map[string]any{
		"resolved_work_id": workID, "resolution_status": "exact", "resolution_method": method,
		"resolution_confidence": 1, "updated_at": time.Now().UTC(),
	}).Error
}

func markItemFailed(ctx context.Context, item *orm.PaperImportItem, code string, err error) {
	details, _ := json.Marshal(map[string]string{"message": err.Error()})
	_ = store.DB().WithContext(ctx).Model(item).Updates(map[string]any{"status": "failed", "error_code": code, "error_details_json": details, "updated_at": time.Now().UTC()}).Error
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
