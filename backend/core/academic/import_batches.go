package academic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type ImportOperationItem struct {
	ImportQueueItem
	LibraryPaperID  string `json:"library_paper_id,omitempty"`
	ResultKind      string `json:"result_kind"`
	CurrentState    string `json:"current_state"`
	DocumentDeleted bool   `json:"document_deleted"`
	ResolvedLater   bool   `json:"resolved_later"`
	CanRetry        bool   `json:"can_retry"`
	taskID          string
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

// The operation outcome remains historical. Current processing and a later
// successful import affect attention/retry decisions, never the saved outcome.
func loadImportOperations(ctx context.Context, db *gorm.DB, batches []orm.PaperImportBatch, canWrite bool, documents map[string]*ImportQueueDocument) ([]ImportOperation, error) {
	result := make([]ImportOperation, 0, len(batches))
	if len(batches) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(batches))
	byID := map[string][]orm.PaperImportItem{}
	for _, batch := range batches {
		ids = append(ids, batch.ID)
	}
	var raw []orm.PaperImportItem
	if err := db.WithContext(ctx).Where("batch_id IN ?", ids).Order("created_at DESC, id DESC").Find(&raw).Error; err != nil {
		return nil, err
	}
	workIDs := []string{}
	for _, item := range raw {
		byID[item.BatchID] = append(byID[item.BatchID], item)
		if item.TitleSnapshot == "" && item.AcademicWorkID != "" {
			workIDs = append(workIDs, item.AcademicWorkID)
		}
	}
	libraryPapers := map[string]bool{}
	needsLibrary := false
	for _, item := range raw {
		if item.LibraryPaperID != "" {
			needsLibrary = true
			break
		}
	}
	if needsLibrary {
		var library orm.PaperLibrary
		err := db.WithContext(ctx).Where("dataset_id=?", batches[0].TargetDatasetID).Take(&library).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if len(library.DataJSON) > 0 {
			papers, err := importLibraryPapers(library.DataJSON)
			if err != nil {
				return nil, err
			}
			for id := range papers {
				libraryPapers[id] = true
			}
		}
	}
	titles := map[string]string{}
	if len(workIDs) > 0 {
		var works []orm.AcademicWork
		if err := db.WithContext(ctx).Select("id, canonical_title").Where("id IN ?", workIDs).Find(&works).Error; err != nil {
			return nil, err
		}
		for _, work := range works {
			titles[work.ID] = work.CanonicalTitle
		}
	}
	for _, batch := range batches {
		source := batch.SourceContextJSON
		if len(source) == 0 {
			source = json.RawMessage(`{}`)
		}
		operation := ImportOperation{ID: batch.ID, EntryType: batch.EntryType, TargetDatasetID: batch.TargetDatasetID, Source: source, CreatedAt: batch.CreatedAt, Items: []ImportOperationItem{}}
		for _, item := range byID[batch.ID] {
			var candidate FulltextCandidate
			if len(item.SelectedCandidateJSON) > 0 && json.Unmarshal(item.SelectedCandidateJSON, &candidate) != nil {
				return nil, errors.New("invalid import source")
			}
			var detail struct {
				Message string `json:"message"`
			}
			if len(item.ErrorDetailsJSON) > 0 && json.Unmarshal(item.ErrorDetailsJSON, &detail) != nil {
				return nil, errors.New("invalid import error details")
			}
			row := ImportOperationItem{ImportQueueItem: ImportQueueItem{ItemID: item.ID, BatchID: batch.ID, TargetDatasetID: batch.TargetDatasetID, WorkID: item.AcademicWorkID, Title: firstNonEmpty(item.TitleSnapshot, titles[item.AcademicWorkID], "未命名文献"), Status: item.Status, Stage: item.Stage, DocumentID: item.DocumentID, SourceProvider: candidate.SourceProvider, SourceVersion: candidate.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, ErrorCode: item.ErrorCode, ErrorMessage: detail.Message, BatchStatus: batch.Status}, LibraryPaperID: item.LibraryPaperID, ResultKind: item.ResultKind, taskID: item.DocumentTaskID}
			if !libraryPapers[row.LibraryPaperID] {
				row.LibraryPaperID = ""
			}
			if row.ResultKind == "" {
				row.ResultKind = map[string]string{"succeeded": "imported", "skipped": "already_present", "failed": "failed", "canceled": "canceled"}[item.Status]
			}
			key := string(marshal([]string{batch.TargetDatasetID, item.AcademicWorkID, candidate.SourceProvider, candidate.Version, item.DocumentID}))
			current, found := documents[key]
			if !found {
				var err error
				current, err = currentImportDocument(ctx, db, row.ImportQueueItem, batch.CreatedBy)
				if err != nil {
					return nil, err
				}
				documents[key] = current
			}
			row.CurrentDocument = current
			row.ResolvedLater = item.Status == "failed" && current != nil && current.ProcessingStatus == "ready"
			row.CurrentState = item.Status
			switch {
			case row.ResultKind == "metadata_only":
				row.CurrentState = "metadata_only"
			case item.Status == "failed":
				row.CurrentState = "failed"
				if !row.ResolvedLater && current != nil && current.ProcessingStatus != "failed" {
					row.CurrentState = "processing"
				}
			case item.Status == "pending" || item.Status == "running":
				row.CurrentState = "importing"
			case item.Status == "canceled":
				row.CurrentState = "canceled"
			case current != nil && current.ProcessingStatus == "failed":
				row.CurrentState = "processing_failed"
			case current != nil && current.ProcessingStatus != "ready":
				row.CurrentState = "processing"
			case item.DocumentID != "" && current == nil:
				row.CurrentState = "deleted"
				row.DocumentDeleted = true
			case item.Status == "skipped":
				row.CurrentState = "skipped"
			default:
				row.CurrentState = "ready"
			}
			// A queued retry is active even before the worker claims its item.
			if item.Status == "failed" && (batch.Status == "pending" || batch.Status == "running") {
				row.CurrentState = "importing"
			}
			row.CanRetry = canWrite && !row.ResolvedLater && (row.CurrentState == "failed" || row.CurrentState == "processing_failed")
			summary := &operation.Summary
			summary.Total++
			switch row.CurrentState {
			case "ready":
				summary.Ready++
			case "metadata_only":
				summary.MetadataOnly++
			case "skipped":
				summary.Skipped++
			case "importing", "processing":
				summary.Active++
			case "failed", "processing_failed":
				summary.Failed++
				if !row.ResolvedLater {
					summary.NeedsAction++
				}
			case "canceled":
				summary.Canceled++
			case "deleted":
				summary.Deleted++
			}
			if row.ResolvedLater {
				summary.ResolvedLater++
			}
			if row.CanRetry {
				summary.Retryable++
			}
			operation.Items = append(operation.Items, row)
		}
		s := operation.Summary
		switch {
		case s.Active > 0:
			operation.Status = "running"
		case s.Failed > 0:
			operation.Status = "failed"
			if s.Ready+s.Skipped+s.MetadataOnly+s.Deleted > 0 {
				operation.Status = "partial_failed"
			}
		case s.Canceled == s.Total && s.Total > 0:
			operation.Status = "canceled"
		case s.Skipped == s.Total && s.Total > 0:
			operation.Status = "skipped"
		case s.MetadataOnly == s.Total && s.Total > 0:
			operation.Status = "metadata_only"
		default:
			operation.Status = "succeeded"
		}
		result = append(result, operation)
	}
	return result, nil
}

func ListImportBatches(w http.ResponseWriter, r *http.Request) {
	dataset, canWrite, ok := paperLibraryAccess(w, r)
	if !ok {
		return
	}
	user := store.UserID(r)
	limit, cursor, err := importPage(r)
	if err != nil {
		replyError(w, 400, "INVALID_REQUEST", err.Error())
		return
	}
	query := store.DB().WithContext(r.Context()).Where("target_dataset_id=? AND created_by=?", dataset, user)
	page := query.Session(&gorm.Session{})
	if cursor != nil {
		page = page.Where("created_at < ? OR (created_at = ? AND id < ?)", cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	var batches []orm.PaperImportBatch
	if err = page.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&batches).Error; err != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	next := ""
	if len(batches) > limit {
		batches = batches[:limit]
		last := batches[len(batches)-1]
		next = nextImportCursor(last.CreatedAt, last.ID)
	}
	documents := map[string]*ImportQueueDocument{}
	operations, err := loadImportOperations(r.Context(), store.DB(), batches, canWrite, documents)
	if err != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	// Notification totals cover the complete collection, independently of paging.
	var all []orm.PaperImportBatch
	if err = query.Find(&all).Error; err != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	totals, err := loadImportOperations(r.Context(), store.DB(), all, canWrite, documents)
	if err != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	active, attention := 0, 0
	for _, operation := range totals {
		if operation.Summary.Active > 0 {
			active++
		}
		if operation.Summary.NeedsAction > 0 {
			attention++
		}
	}
	common.ReplyJSON(w, map[string]any{"items": operations, "next_cursor": next, "notification": map[string]int{"active": active, "needs_action": attention}})
}

func GetImportBatch(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var batch orm.PaperImportBatch
	if err := store.DB().WithContext(r.Context()).Where("id=? AND created_by=?", mux.Vars(r)["batch_id"], user).Take(&batch).Error; err != nil {
		replyError(w, 404, "IMPORT_NOT_FOUND", "import batch not found")
		return
	}
	copied := r.Clone(r.Context())
	q := copied.URL.Query()
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
	operations, err := loadImportOperations(r.Context(), store.DB(), []orm.PaperImportBatch{batch}, canWrite, map[string]*ImportQueueDocument{})
	if err != nil {
		replyError(w, 500, "IMPORT_QUERY_FAILED", err.Error())
		return
	}
	operation := operations[0]
	rows := make([]ImportOperationItem, 0, limit+1)
	for _, item := range operation.Items {
		if cursor != nil && (item.CreatedAt.After(cursor.CreatedAt) || item.CreatedAt.Equal(cursor.CreatedAt) && strings.Compare(item.ItemID, cursor.ID) >= 0) {
			continue
		}
		rows = append(rows, item)
		if len(rows) > limit {
			break
		}
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next = nextImportCursor(last.CreatedAt, last.ItemID)
	}
	common.ReplyJSON(w, map[string]any{"operation": operation, "items": rows, "next_cursor": next})
}
