package academic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

type ImportOperationItem struct {
	ImportQueueItem
	LibraryPaperID   string          `json:"library_paper_id,omitempty"`
	ResultKind       string          `json:"result_kind"`
	CurrentState     string          `json:"current_state"`
	DocumentDeleted  bool            `json:"document_deleted"`
	ResolvedLater    bool            `json:"resolved_later"`
	CanRetry         bool            `json:"can_retry"`
	UploadRequestKey string          `json:"upload_request_key,omitempty"`
	PendingPaper     json.RawMessage `json:"pending_paper,omitempty"`
	ContentSHA256    string          `json:"content_sha256,omitempty"`
	taskID           string
}

type ImportOperationSummary struct {
	Total         int `json:"total"`
	Ready         int `json:"ready"`
	MetadataOnly  int `json:"metadata_only"`
	Skipped       int `json:"skipped"`
	Active        int `json:"active"`
	Failed        int `json:"failed"`
	Canceled      int `json:"canceled"`
	Deleted       int `json:"deleted"`
	ResolvedLater int `json:"resolved_later"`
	Retryable     int `json:"retryable"`
	NeedsAction   int `json:"needs_action"`
}

type ImportOperation struct {
	ID              string                 `json:"id"`
	EntryType       string                 `json:"entry_type"`
	TargetDatasetID string                 `json:"target_dataset_id"`
	Source          json.RawMessage        `json:"source"`
	Status          string                 `json:"status"`
	CreatedAt       time.Time              `json:"created_at"`
	Summary         ImportOperationSummary `json:"summary"`
	Items           []ImportOperationItem  `json:"-"`
}

type importCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func importPage(r *http.Request) (int, *importCursor, error) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			return 0, nil, errors.New("limit must be between 1 and 100")
		}
		limit = n
	}
	var cursor *importCursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		data, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			return 0, nil, errors.New("invalid cursor")
		}
		cursor = &importCursor{}
		if json.Unmarshal(data, cursor) != nil || cursor.ID == "" || cursor.CreatedAt.IsZero() {
			return 0, nil, errors.New("invalid cursor")
		}
	}
	return limit, cursor, nil
}
func nextImportCursor(created time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString(marshal(importCursor{created, id}))
}

func operationDocuments(db *gorm.DB, user, dataset string, batchIDs, itemIDs []string) *gorm.DB {
	provider, version := "json_extract(i.selected_candidate_json,'$.source_provider')", "json_extract(i.selected_candidate_json,'$.version')"
	if db.Dialector.Name() == "postgres" {
		provider = "(CAST(i.selected_candidate_json AS jsonb)->>'source_provider')"
		version = "(CAST(i.selected_candidate_json AS jsonb)->>'version')"
	}
	fallback := "(SELECT l.document_id FROM academic_work_documents l WHERE l.academic_work_id=i.academic_work_id AND l.dataset_id=b.target_dataset_id AND COALESCE(" + provider + ",'')<>'' AND COALESCE(" + version + ",'')<>'' AND l.source_provider=" + provider + " AND l.source_version=" + version + " ORDER BY l.is_preferred_version DESC,l.updated_at DESC LIMIT 1)"
	source := db.Table("paper_import_items i").Select("i.id,i.batch_id,i.status,i.stage,i.result_kind,b.status AS batch_status,b.target_dataset_id,COALESCE(NULLIF(i.document_id,''),"+fallback+") AS effective_document_id").Joins("JOIN paper_import_batches b ON b.id=i.batch_id").Where("b.created_by=?", user)
	if dataset != "" {
		source = source.Where("b.target_dataset_id=?", dataset)
	}
	if batchIDs != nil {
		source = source.Where("b.id IN ?", batchIDs)
	}
	if itemIDs != nil {
		source = source.Where("i.id IN ?", itemIDs)
	}
	return source
}

type operationProcessing struct {
	DocumentID string `json:"document_id"`
	State      string `json:"processing_state"`
}

// Observe distinct active documents, not every historical import receipt.
// Terminal stages need no external query unless a task was created or resumed.
func observeOperationProcessing(r *http.Request, db *gorm.DB, dataset string, batchIDs []string) ([]operationProcessing, error) {
	var ids []string
	source := operationDocuments(db, store.UserID(r), dataset, batchIDs, nil)
	err := db.Table("(?) x", source).Select("DISTINCT d.id").
		Joins("JOIN documents d ON d.id=x.effective_document_id AND d.dataset_id=x.target_dataset_id AND d.deleted_at IS NULL").
		Joins("JOIN datasets ds ON ds.id=d.dataset_id").
		Joins("LEFT JOIN document_processing_states p ON p.document_id=d.id AND p.dataset_id=d.dataset_id").
		Where(`ds.processing_level <> 'stored' AND (p.document_id IS NULL OR p.parse_status NOT IN ('succeeded','failed') OR (ds.processing_level IN ('chunked','indexed') AND p.chunk_status NOT IN ('succeeded','failed')) OR (ds.processing_level='indexed' AND p.index_status NOT IN ('succeeded','failed')) OR EXISTS (SELECT 1 FROM tasks t WHERE t.dataset_id=d.dataset_id AND t.doc_id=d.id AND t.deleted_at IS NULL AND (t.created_at>p.updated_at OR t.updated_at>p.updated_at)))`).
		Pluck("d.id", &ids).Error
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	metadata, err := processingMetadata(r, db, dataset, ids)
	if err != nil {
		return nil, err
	}
	result := make([]operationProcessing, 0, len(metadata))
	for id, current := range metadata {
		status := doc.DocumentProcessingStatus(current.ProcessingLevel, current.ProcessingState)
		if status == "not_requested" {
			status = "ready"
		}
		if status != "ready" && status != "failed" {
			status = "processing"
		}
		result = append(result, operationProcessing{DocumentID: id, State: status})
	}
	return result, nil
}

// Counts stay in SQL; only active task observations cross the read-only database boundary.
func operationStates(db *gorm.DB, user, dataset string, batchIDs, itemIDs []string, observed []operationProcessing) *gorm.DB {
	source := operationDocuments(db, user, dataset, batchIDs, itemIDs)
	state := "CASE WHEN ds.processing_level='stored' THEN 'ready' WHEN p.parse_status='failed' OR (ds.processing_level IN ('chunked','indexed') AND p.chunk_status='failed') OR (ds.processing_level='indexed' AND p.index_status='failed') THEN 'failed' WHEN p.document_id IS NULL THEN 'ready' WHEN p.parse_status IN ('succeeded','success','ready') AND (ds.processing_level NOT IN ('chunked','indexed') OR p.chunk_status IN ('succeeded','success','ready')) AND (ds.processing_level<>'indexed' OR p.index_status IN ('succeeded','success','ready')) THEN 'ready' ELSE 'processing' END"
	joined := db.Table("(?) x", source).Joins("LEFT JOIN documents d ON d.id=x.effective_document_id AND d.dataset_id=x.target_dataset_id AND d.deleted_at IS NULL").Joins("LEFT JOIN datasets ds ON ds.id=x.target_dataset_id").Joins("LEFT JOIN document_processing_states p ON p.document_id=d.id AND p.dataset_id=d.dataset_id")
	if len(observed) > 0 {
		var projection *gorm.DB
		if db.Dialector.Name() == "postgres" {
			projection = db.Table("jsonb_to_recordset(CAST(? AS jsonb)) AS observed(document_id text,processing_state text)", string(marshal(observed))).Select("document_id,processing_state")
		} else {
			// Materialize once so SQLite can index the document join instead of
			// scanning the JSON array for each historical receipt.
			projection = db.Raw(`WITH observed AS MATERIALIZED (
 SELECT json_extract(value,'$.document_id') AS document_id,
        json_extract(value,'$.processing_state') AS processing_state FROM json_each(?)
) SELECT * FROM observed`, string(marshal(observed)))
		}
		joined = joined.Joins("LEFT JOIN (?) observed ON observed.document_id=d.id", projection)
		state = "COALESCE(observed.processing_state," + state + ")"
	}
	joined = joined.Select("x.*,CASE WHEN d.id IS NOT NULL THEN 1 ELSE 0 END AS document_available," + state + " AS processing_state")
	return db.Table("(?) state", joined).Select(`state.id,state.batch_id,
 CASE WHEN state.status='failed' AND state.document_available=1 AND state.processing_state='ready' THEN 1 ELSE 0 END AS resolved_later,
 CASE WHEN state.stage='awaiting_association' THEN 'awaiting_association'
 WHEN state.result_kind='metadata_only' THEN 'metadata_only'
 WHEN state.status='failed' AND state.batch_status IN ('pending','running') THEN 'importing'
 WHEN state.status='failed' AND state.document_available=1 AND state.processing_state='processing' THEN 'processing'
 WHEN state.status='failed' THEN 'failed'
 WHEN state.status IN ('pending','running') AND state.batch_status='canceled' THEN 'canceled'
 WHEN state.status IN ('pending','running') THEN 'importing'
 WHEN state.status='canceled' THEN 'canceled'
 WHEN state.document_available=1 AND state.processing_state='failed' THEN 'processing_failed'
 WHEN state.document_available=1 AND state.processing_state='processing' THEN 'processing'
 WHEN COALESCE(state.effective_document_id,'')<>'' AND state.document_available=0 THEN 'deleted'
 WHEN state.status='skipped' THEN 'skipped' ELSE 'ready' END AS current_state`)
}
func operationSummaryQuery(db *gorm.DB, user, dataset string, batchIDs []string, canWrite bool, observed []operationProcessing) *gorm.DB {
	retry := 0
	if canWrite {
		retry = 1
	}
	return db.Table("(?) s", operationStates(db, user, dataset, batchIDs, nil, observed)).Select(`s.batch_id,COUNT(*) AS total,
 SUM(CASE WHEN current_state='ready' THEN 1 ELSE 0 END) AS ready,
 SUM(CASE WHEN current_state='metadata_only' THEN 1 ELSE 0 END) AS metadata_only,
 SUM(CASE WHEN current_state='skipped' THEN 1 ELSE 0 END) AS skipped,
 SUM(CASE WHEN current_state IN ('importing','processing') THEN 1 ELSE 0 END) AS active,
 SUM(CASE WHEN current_state IN ('failed','processing_failed') THEN 1 ELSE 0 END) AS failed,
 SUM(CASE WHEN current_state='canceled' THEN 1 ELSE 0 END) AS canceled,
 SUM(CASE WHEN current_state='deleted' THEN 1 ELSE 0 END) AS deleted,
 SUM(resolved_later) AS resolved_later,
 SUM(CASE WHEN resolved_later=0 AND current_state IN ('failed','processing_failed') THEN ? ELSE 0 END) AS retryable,
 SUM(CASE WHEN current_state='awaiting_association' OR (resolved_later=0 AND current_state IN ('failed','processing_failed')) THEN 1 ELSE 0 END) AS needs_action`, retry).Group("s.batch_id")
}
func operationStatus(s ImportOperationSummary) string {
	switch {
	case s.NeedsAction > 0 && s.Failed == 0 && s.Active == 0:
		return "awaiting_association"
	case s.Active > 0:
		return "running"
	case s.Failed > 0:
		if s.Ready+s.Skipped+s.MetadataOnly+s.Deleted > 0 {
			return "partial_failed"
		}
		return "failed"
	case s.Total > 0 && s.Canceled == s.Total:
		return "canceled"
	case s.Total > 0 && s.Skipped == s.Total:
		return "skipped"
	case s.Total > 0 && s.MetadataOnly == s.Total:
		return "metadata_only"
	default:
		return "succeeded"
	}
}
func loadOperationSummaries(ctx context.Context, db *gorm.DB, user, dataset string, batches []orm.PaperImportBatch, canWrite bool, observed []operationProcessing) ([]ImportOperation, error) {
	result := []ImportOperation{}
	if len(batches) == 0 {
		return result, nil
	}
	ids := []string{}
	for _, b := range batches {
		ids = append(ids, b.ID)
	}
	var rows []struct {
		BatchID string
		ImportOperationSummary
	}
	if err := operationSummaryQuery(db.WithContext(ctx), user, dataset, ids, canWrite, observed).Scan(&rows).Error; err != nil {
		return nil, err
	}
	summaries := map[string]ImportOperationSummary{}
	for _, row := range rows {
		summaries[row.BatchID] = row.ImportOperationSummary
	}
	for _, b := range batches {
		source := b.SourceContextJSON
		if len(source) == 0 {
			source = marshal(map[string]any{})
		}
		s := summaries[b.ID]
		result = append(result, ImportOperation{ID: b.ID, EntryType: b.EntryType, TargetDatasetID: b.TargetDatasetID, Source: source, Status: operationStatus(s), CreatedAt: b.CreatedAt, Summary: s, Items: []ImportOperationItem{}})
	}
	return result, nil
}
func ListImportOperations(w http.ResponseWriter, r *http.Request) {
	dataset, canWrite, ok := paperLibraryAccess(w, r)
	if !ok {
		return
	}
	limit, cursor, err := importPage(r)
	if err != nil {
		replyError(w, 400, "INVALID_REQUEST", err.Error())
		return
	}
	db := store.DB().WithContext(r.Context())
	query := db.Where("target_dataset_id=? AND created_by=?", dataset, store.UserID(r))
	if cursor != nil {
		query = query.Where("created_at < ? OR (created_at=? AND id<?)", cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	var batches []orm.PaperImportBatch
	if err = query.Order("created_at DESC,id DESC").Limit(limit + 1).Find(&batches).Error; err != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	next := ""
	if len(batches) > limit {
		batches = batches[:limit]
		last := batches[len(batches)-1]
		next = nextImportCursor(last.CreatedAt, last.ID)
	}
	observed, err := observeOperationProcessing(r, db, dataset, nil)
	if err != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	operations, err := loadOperationSummaries(r.Context(), db, store.UserID(r), dataset, batches, canWrite, observed)
	if err != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	var totals struct {
		Active      int `json:"active"`
		NeedsAction int `json:"needs_action"`
	}
	err = db.Table("(?) totals", operationSummaryQuery(db, store.UserID(r), dataset, nil, canWrite, observed)).Select("COALESCE(SUM(CASE WHEN active>0 THEN 1 ELSE 0 END),0) AS active,COALESCE(SUM(CASE WHEN needs_action>0 THEN 1 ELSE 0 END),0) AS needs_action").Scan(&totals).Error
	if err != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"items": operations, "next_cursor": next, "notification": totals})
}
func GetImportOperation(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var batch orm.PaperImportBatch
	db := store.DB().WithContext(r.Context())
	if err := db.Where("id=? AND created_by=?", mux.Vars(r)["batch_id"], user).Take(&batch).Error; err != nil {
		replyError(w, 404, "IMPORT_NOT_FOUND", "import batch not found")
		return
	}
	copied := r.Clone(r.Context())
	u := *r.URL
	copied.URL = &u
	q := u.Query()
	q.Set("target_dataset_id", batch.TargetDatasetID)
	copied.URL.RawQuery = q.Encode()
	_, canWrite, ok := paperLibraryAccess(w, copied)
	if !ok {
		return
	}
	limit, cursor, err := importPage(r)
	if err != nil {
		replyError(w, 400, "INVALID_REQUEST", err.Error())
		return
	}
	query := db.Where("batch_id=?", batch.ID)
	if cursor != nil {
		query = query.Where("created_at<? OR (created_at=? AND id<?)", cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	var raw []orm.PaperImportItem
	if err = query.Order("created_at DESC,id DESC").Limit(limit + 1).Find(&raw).Error; err != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	next := ""
	if len(raw) > limit {
		raw = raw[:limit]
		last := raw[len(raw)-1]
		next = nextImportCursor(last.CreatedAt, last.ID)
	}
	observed, err := observeOperationProcessing(r, db, batch.TargetDatasetID, []string{batch.ID})
	if err != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	rows, err := loadOperationPage(r.Context(), db, batch, raw, canWrite, observed)
	if err != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	operations, err := loadOperationSummaries(r.Context(), db, user, batch.TargetDatasetID, []orm.PaperImportBatch{batch}, canWrite, observed)
	if err != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"batch": batch, "operation": operations[0], "items": rows, "next_cursor": next})
}
func loadOperationPage(ctx context.Context, db *gorm.DB, batch orm.PaperImportBatch, raw []orm.PaperImportItem, canWrite bool, observed []operationProcessing) ([]ImportOperationItem, error) {
	result := []ImportOperationItem{}
	if len(raw) == 0 {
		return result, nil
	}
	ids, workIDs, paperIDs := []string{}, []string{}, []string{}
	for _, i := range raw {
		ids = append(ids, i.ID)
		workIDs = append(workIDs, i.AcademicWorkID)
		if i.LibraryPaperID != "" {
			paperIDs = append(paperIDs, i.LibraryPaperID)
		}
	}
	var works []orm.AcademicWork
	if err := db.WithContext(ctx).Where("id IN ?", workIDs).Find(&works).Error; err != nil {
		return nil, err
	}
	titles := map[string]string{}
	for _, work := range works {
		titles[work.ID] = work.CanonicalTitle
	}
	var entries []orm.PaperLibraryEntry
	if len(paperIDs) > 0 {
		if err := db.Where("dataset_id=? AND id IN ? AND removed_at IS NULL", batch.TargetDatasetID, paperIDs).Find(&entries).Error; err != nil {
			return nil, err
		}
	}
	present := map[string]bool{}
	for _, e := range entries {
		present[e.ID] = true
	}
	for _, item := range raw {
		var candidate FulltextCandidate
		var details struct {
			Message string `json:"message"`
		}
		if len(item.SelectedCandidateJSON) > 0 {
			if err := json.Unmarshal(item.SelectedCandidateJSON, &candidate); err != nil {
				return nil, err
			}
		}
		if len(item.ErrorDetailsJSON) > 0 {
			if err := json.Unmarshal(item.ErrorDetailsJSON, &details); err != nil {
				return nil, err
			}
		}
		row := ImportOperationItem{ImportQueueItem: ImportQueueItem{ItemID: item.ID, BatchID: batch.ID, WorkID: item.AcademicWorkID, Title: firstNonEmpty(item.TitleSnapshot, titles[item.AcademicWorkID], "未命名文献"), Status: item.Status, Stage: item.Stage, DocumentID: item.DocumentID, TargetDatasetID: batch.TargetDatasetID, SourceProvider: candidate.SourceProvider, SourceVersion: candidate.Version, ErrorCode: item.ErrorCode, ErrorMessage: details.Message, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, BatchStatus: batch.Status}, LibraryPaperID: item.LibraryPaperID, ResultKind: item.ResultKind, taskID: item.DocumentTaskID}
		if !present[row.LibraryPaperID] {
			row.LibraryPaperID = ""
		}
		if row.ResultKind == "" {
			row.ResultKind = map[string]string{"succeeded": "imported", "skipped": "already_present", "failed": "failed", "canceled": "canceled"}[item.Status]
		}
		current, err := currentImportDocument(ctx, db, row.ImportQueueItem, batch.CreatedBy)
		if err != nil {
			return nil, err
		}
		row.CurrentDocument = current
		if item.Stage == "awaiting_association" {
			row.ContentSHA256 = item.ContentSHA256
			row.UploadRequestKey = batch.IdempotencyKey
			var source struct {
				Paper json.RawMessage `json:"paper"`
			}
			if err = json.Unmarshal(batch.SourceContextJSON, &source); err != nil {
				return nil, err
			}
			row.PendingPaper = source.Paper
		}
		result = append(result, row)
	}
	var states []struct {
		ID            string
		CurrentState  string
		ResolvedLater int
	}
	if err := operationStates(db.WithContext(ctx), batch.CreatedBy, batch.TargetDatasetID, []string{batch.ID}, ids, observed).Scan(&states).Error; err != nil {
		return nil, err
	}
	byID := map[string]int{}
	for i := range result {
		byID[result[i].ItemID] = i
	}
	for _, s := range states {
		row := &result[byID[s.ID]]
		row.CurrentState = s.CurrentState
		row.ResolvedLater = s.ResolvedLater > 0
		if row.DocumentID != "" && row.CurrentDocument == nil && row.CurrentState != "importing" && row.CurrentState != "awaiting_association" && row.CurrentState != "metadata_only" && row.Status != "failed" && row.Status != "canceled" {
			row.CurrentState = "deleted"
		}
		row.DocumentDeleted = row.CurrentState == "deleted"
		row.CanRetry = canWrite && !row.ResolvedLater && (row.CurrentState == "failed" || row.CurrentState == "processing_failed")
	}
	return result, nil
}
