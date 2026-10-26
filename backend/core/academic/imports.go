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

	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
)

const paperImportJobType = "academic.paper_import"

type selectedImportItem struct {
	Paper        *paperlibrary.Paper `json:"paper,omitempty"`
	DocumentID   string              `json:"document_id,omitempty"`
	Revision     int64               `json:"revision,omitempty"`
	Confirmed    bool                `json:"confirmed,omitempty"`
	WorkID       string              `json:"work_id"`
	ReferenceIDs []string            `json:"reference_ids,omitempty"`
	Candidate    FulltextCandidate   `json:"candidate"`
}

type createImportRequest struct {
	FolderID          string                      `json:"folder_id,omitempty"`
	Folders           []paperlibrary.Folder       `json:"folders,omitempty"`
	Format            string                      `json:"format,omitempty"`
	FileName          string                      `json:"file_name,omitempty"`
	PreviewToken      string                      `json:"preview_token,omitempty"`
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
	if !parseJSON(r, &req) {
		replyError(w, 400, "INVALID_REQUEST", "invalid request")
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
	if !doc.HasDatasetPermission(r, req.TargetDatasetID, acl.PermissionDatasetUpload) {
		replyError(w, http.StatusForbidden, "TARGET_DATASET_FORBIDDEN", "target knowledge base is not writable")
		return
	}
	if req.EntryType == "bibliography" || req.EntryType == "pdf_upload" || req.EntryType == "paper_link" && len(req.Items) == 1 && req.Items[0].Candidate.URL == "" {
		createLibraryImport(w, r, userID, req)
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
	policySnapshot := map[string]any{"version_policy": "skip_exact", "folder_id": req.FolderID}
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
	if r.URL.Query().Get("view") == "operations" {
		ListImportOperations(w, r)
		return
	}
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
			document, err = currentImportDocument(r.Context(), store.DB(), record.ImportQueueItem, userID)
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
	if r.URL.Query().Get("view") == "operations" {
		GetImportOperation(w, r)
		return
	}
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
	if batch.TargetDatasetID != "" && !doc.HasDatasetPermission(r, batch.TargetDatasetID, acl.PermissionDatasetUpload) {
		replyError(w, 403, "TARGET_DATASET_FORBIDDEN", "target knowledge base is not writable")
		return
	}

	if batch.EntryType == "conversation_sources" || batch.EntryType == "paper_link" || batch.EntryType == "paper_expansion" || batch.EntryType == "pdf_upload" {
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

func findEquivalentPresentWork(ctx context.Context, workID, datasetID string, version ...string) (orm.AcademicWork, orm.AcademicWorkDocument, error) {
	var source orm.AcademicWork
	if workID == "" {
		return orm.AcademicWork{}, orm.AcademicWorkDocument{}, gorm.ErrRecordNotFound
	}
	if err := store.DB().WithContext(ctx).Where("id = ?", workID).Take(&source).Error; err != nil {
		return orm.AcademicWork{}, orm.AcademicWorkDocument{}, err
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
	query := activeSourceDocuments(store.DB().WithContext(ctx), datasetID).
		Select("aw.id AS work_id, awd.document_id").
		Joins("JOIN academic_work_documents awd ON awd.document_id = d.id AND awd.dataset_id = d.dataset_id").
		Joins("JOIN academic_works aw ON aw.id = awd.academic_work_id").
		Where("awd.dataset_id = ?", datasetID).
		Where("("+strings.Join(conditions, " OR ")+")", args...).
		Order("CASE WHEN aw.id = '" + strings.ReplaceAll(source.ID, "'", "''") + "' THEN 0 ELSE 1 END, awd.is_preferred_version DESC, awd.updated_at DESC").
		Limit(1)
	if len(version) > 0 && version[0] != "" {
		query = query.Where("awd.source_version = ?", version[0])
		if source.ArxivIDBase != "" {
			query = query.Where("aw.arxiv_id_base=?", source.ArxivIDBase)
		}
	}
	query = query.Scan(&row)
	if query.Error != nil {
		return orm.AcademicWork{}, orm.AcademicWorkDocument{}, query.Error
	}
	if row.WorkID == "" || row.DocumentID == "" {
		return orm.AcademicWork{}, orm.AcademicWorkDocument{}, gorm.ErrRecordNotFound
	}
	var work orm.AcademicWork
	var document orm.AcademicWorkDocument
	if err := store.DB().WithContext(ctx).Where("id = ?", row.WorkID).Take(&work).Error; err != nil {
		return orm.AcademicWork{}, orm.AcademicWorkDocument{}, err
	}
	if err := store.DB().WithContext(ctx).Where("academic_work_id = ? AND dataset_id = ? AND document_id = ?", row.WorkID, datasetID, row.DocumentID).Take(&document).Error; err != nil {
		return orm.AcademicWork{}, orm.AcademicWorkDocument{}, err
	}
	return work, document, nil
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
