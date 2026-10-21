package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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
	SourceContext     *SourceContext              `json:"source_context,omitempty"`
	SourceIDs         []string                    `json:"source_ids,omitempty"`
	NewKnowledgeBase  *doc.CreateKnowledgeRequest `json:"new_knowledge_base,omitempty"`
	EntryType         string                      `json:"entry_type"`
	TargetDatasetID   string                      `json:"target_dataset_id"`
	TargetPID         string                      `json:"target_pid,omitempty"`
	SourceDocumentIDs []string                    `json:"source_document_ids,omitempty"`
	Items             []selectedImportItem        `json:"items"`
	IdempotencyKey    string                      `json:"idempotency_key,omitempty"`
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
	if !parseJSON(r, &req) {
		replyError(w, 400, "INVALID_REQUEST", "invalid import request")
		return
	}
	if req.SourceContext != nil {
		createSourceImport(w, r, userID, req)
		return
	}
	if req.TargetDatasetID == "" || len(req.Items) == 0 || len(req.Items) > 500 {
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
		batch.AsyncJobID = queued.ID
		return tx.Model(&batch).Updates(map[string]any{"async_job_id": queued.ID, "updated_at": now}).Error
	})
	if err != nil {
		replyError(w, 500, "IMPORT_CREATE_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"batch_id": batch.ID, "job_id": batch.AsyncJobID, "status": batch.Status, "total_items": batch.TotalItems})
}

func ListImports(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("history_id") != "" || r.URL.Query().Get("task_id") != "" || r.URL.Query().Get("workflow_session_id") != "" {
		ListSourceImports(w, r)
		return
	}
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
	if batch.EntryType == "conversation_sources" {
		retrySourceImport(w, r, batch)
		return
	}
	var failed int64
	var jobID string
	noFailed := errors.New("there are no failed items to retry")
	activeBatch := errors.New("batch is already processing")
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND created_by = ?", batch.ID, userID).Take(&batch).Error; err != nil {
			return err
		}
		if err := tx.Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status = 'failed'", batch.ID).Count(&failed).Error; err != nil {
			return err
		}
		if failed == 0 {
			return noFailed
		}
		var active int64
		if err := tx.Model(&orm.AsyncJob{}).Where("id = ? AND status IN ?", batch.AsyncJobID, []string{"pending", "running"}).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return activeBatch
		}
		queued, err := asyncjob.EnqueueInTransaction(r.Context(), tx, asyncjob.EnqueueRequest{JobType: paperImportJobType, ResourceType: "paper_import_batch", ResourceID: batch.ID,
			IdempotencyKey: "paper-import-retry:" + batch.ID + ":" + uuid.NewString(), Payload: paperImportPayload{BatchID: batch.ID, UserID: userID, UserName: store.UserName(r)},
			CreateUserID: userID, CreateUserName: store.UserName(r)})
		if err != nil {
			return err
		}
		if err := tx.Model(&orm.PaperImportItem{}).Where("batch_id = ? AND status = 'failed'", batch.ID).Updates(map[string]any{"status": "pending", "error_details_json": json.RawMessage("{}"), "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		jobID = queued.ID
		return tx.Model(&batch).Updates(map[string]any{"status": "pending", "async_job_id": jobID, "updated_at": time.Now().UTC()}).Error
	})
	if errors.Is(err, noFailed) || errors.Is(err, activeBatch) {
		code := "NO_FAILED_ITEMS"
		if errors.Is(err, activeBatch) {
			code = "IMPORT_ALREADY_PROCESSING"
		}
		replyError(w, http.StatusConflict, code, err.Error())
		return
	}
	if err != nil {
		replyError(w, 500, "IMPORT_RETRY_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"batch_id": batch.ID, "job_id": jobID, "status": "pending", "retry_items": failed})
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
	return handleImportBatchJob(ctx, job, reporter, batch)
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
