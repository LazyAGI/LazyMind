package doc

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
)

const RootNodeGroup = "lazyllm_root"

const (
	// Keep the synchronous wait below the default 30-second Core client and
	// gateway timeouts. A longer parse keeps running on the shared task and a
	// subsequent read joins it instead of creating duplicate work.
	documentParseWaitTimeout  = 25 * time.Second
	documentParsePollInterval = time.Second
)

type EnsureDocumentParsedRequest struct {
	UserID     string
	DatasetID  string
	DocumentID string
	Caller     DatasetCatalogCaller
}

type EnsureDocumentParsedResult struct {
	Error  *ProcessingDiagnostic `json:"error,omitempty"`
	Status string                `json:"status"`
	TaskID string                `json:"task_id,omitempty"`
}

func isRetryableParseObservation(uploadStatus, taskStatus string) bool {
	uploadStatus = strings.ToUpper(strings.TrimSpace(uploadStatus))
	if uploadStatus == "SUCCESS" || uploadStatus == "SUCCEEDED" {
		return false
	}
	if uploadStatus == "FAILED" || uploadStatus == "ERROR" {
		return true
	}
	taskStatus = strings.ToUpper(strings.TrimSpace(taskStatus))
	return taskStatus == "FAILED" || taskStatus == "ERROR"
}

// EnsureDocumentParsedAndWait owns the complete on-demand parse lifecycle for
// synchronous readers. Concurrent callers join the same task through
// EnsureDocumentParsed's database latch and wait here for its terminal state.
func (s *DocumentService) EnsureDocumentParsedAndWait(r *http.Request, req EnsureDocumentParsedRequest) error {
	return waitForDocumentParsed(r.Context(), documentParseWaitTimeout, documentParsePollInterval, func() (EnsureDocumentParsedResult, error) {
		return s.EnsureDocumentParsed(r, req)
	})
}

func waitForDocumentParsed(
	ctx context.Context,
	timeout time.Duration,
	pollInterval time.Duration,
	ensure func() (EnsureDocumentParsedResult, error),
) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		result, err := ensure()
		if err != nil {
			return err
		}
		switch strings.ToLower(strings.TrimSpace(result.Status)) {
		case "parsed":
			return nil
		case "failed":
			return &DocumentServiceError{Code: DocumentServiceUnavailable, Message: "document Reader parsing failed"}
		}

		poll := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			poll.Stop()
			return ctx.Err()
		case <-deadline.C:
			poll.Stop()
			return &DocumentServiceError{Code: DocumentServiceUnavailable, Message: "document parsing is still running"}
		case <-poll.C:
		}
	}
}

// EnsureDocumentParsed is the single on-demand entry point for features that
// consume parsed document roots. It requests processing_level=parsed without
// changing the dataset's default level; Reader selection remains owned by the
// document parsing pipeline (configured OCR Reader, then its built-in fallback).
func (s *DocumentService) EnsureDocumentParsed(r *http.Request, req EnsureDocumentParsedRequest) (EnsureDocumentParsedResult, error) {
	rec, err := s.loadRecord(r.Context(), req.UserID, req.DatasetID, req.DocumentID, req.Caller)
	if err != nil {
		return EnsureDocumentParsedResult{}, err
	}
	// Native reader artifacts remain usable while deferred indexing runs or fails.
	if artifact, err := readBaseArtifact(r.Context(), rec); err != nil {
		return EnsureDocumentParsedResult{}, err
	} else if artifact != nil {
		return EnsureDocumentParsedResult{Status: "parsed"}, nil
	}
	if rec.lazy != nil {
		status := strings.ToUpper(strings.TrimSpace(rec.lazy.UploadStatus))
		if status == "SUCCESS" || status == "SUCCEEDED" {
			// Check current service artifacts before historical Core task states:
			// a retry can succeed while an older task still says FAILED or WORKING.
			roots, rootsErr := s.ListDocumentChunks(r.Context(), DocumentChunksRequest{
				UserID: req.UserID, DatasetID: req.DatasetID, DocumentID: req.DocumentID,
				PageSize: 1, SegmentGroup: RootNodeGroup, Caller: req.Caller,
			})
			if rootsErr != nil || len(roots.Chunks) == 0 {
				// UploadStatus is not updated by DOC_REPARSE. Missing roots may
				// mean a failed current task, not merely delayed visibility.
				return s.parseResultWithoutRoots(r.Context(), rec)
			}
			_ = s.updateParseState(r, req.DatasetID, req.DocumentID, "succeeded", "", "")
			return EnsureDocumentParsedResult{Status: "parsed"}, nil
		}
	}
	if rec.processing.ParseStatus == "failed" || NormalizeTaskStateForUI(rec.taskStat) == "FAILED" {
		return failedDocumentParseResult(rec.diagnostic), nil
	}
	if rec.processing.ParseStatus == "running" {
		var active orm.Task
		_ = s.db.WithContext(r.Context()).Where("dataset_id = ? AND doc_id = ? AND deleted_at IS NULL", req.DatasetID, req.DocumentID).Order("created_at DESC, id DESC").Take(&active).Error
		return EnsureDocumentParsedResult{Status: "parsing", TaskID: active.ID}, nil
	}
	if rec.lazy != nil {
		status := strings.ToUpper(strings.TrimSpace(rec.lazy.UploadStatus))
		taskStatus := strings.ToUpper(strings.TrimSpace(rec.taskStat))
		switch status {
		case "FAILED", "ERROR":
			return EnsureDocumentParsedResult{Status: "failed", Error: rec.diagnostic}, nil
		default:
			if isRetryableParseObservation(status, taskStatus) {
				return EnsureDocumentParsedResult{Status: "failed", Error: rec.diagnostic}, nil
			}
			if rec.processing.ParseStatus == "running" {
				return EnsureDocumentParsedResult{Status: "parsing"}, nil
			}
			return EnsureDocumentParsedResult{Status: "unknown"}, nil
		}
	}

	now := time.Now().UTC()
	taskID := newTaskID()
	filename := firstNonEmpty(rec.row.DisplayName, rec.ext.OriginalFilename, req.DocumentID)
	taskMetadata := taskExt{
		TaskType:       string(TaskTypeParse),
		DisplayName:    filename,
		DataSourceType: "LOCAL_FILE",
		Files: []TaskFile{{DisplayName: filename, StoredName: rec.ext.StoredName, StoredPath: rec.ext.StoredPath,
			ParseStoredPath: rec.ext.ParseStoredPath, FileSize: rec.ext.FileSize, RelativePath: rec.ext.RelativePath, ContentType: rec.ext.ContentType}},
		TaskState: string(TaskStateCreating),
	}
	task := orm.Task{ID: taskID, DocID: req.DocumentID, KbID: rec.dataset.KbID, AlgoID: parseDatasetAlgo(rec.dataset.Ext).AlgoID,
		DatasetID: req.DatasetID, TaskType: string(TaskTypeParse), DisplayName: filename, Ext: mustJSON(taskMetadata),
		BaseModel: orm.BaseModel{CreateUserID: req.UserID, CreateUserName: rec.row.CreateUserName, CreatedAt: now, UpdatedAt: now}}
	claimed := false
	err = s.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		// The conditional update is the document-level parsing latch. Concurrent
		// chats can all call this method, but only one transaction changes the
		// state to running and creates a task; every loser joins that task below.
		claim := tx.Model(&orm.DocumentProcessingState{}).
			Where("dataset_id = ? AND document_id = ? AND parse_status <> ?", req.DatasetID, req.DocumentID, "running").
			Updates(map[string]any{"parse_status": "running", "parse_error_code": "", "parse_error_message": "", "updated_at": now})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			return nil
		}
		claimed = true
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return EnsureDocumentParsedResult{}, err
	}
	if !claimed {
		var active orm.Task
		_ = s.db.WithContext(r.Context()).Where(
			"dataset_id = ? AND doc_id = ? AND task_type = ? AND deleted_at IS NULL",
			req.DatasetID, req.DocumentID, string(TaskTypeParse),
		).Order("created_at DESC").Take(&active).Error
		return EnsureDocumentParsedResult{Status: "parsing", TaskID: active.ID}, nil
	}
	results, startErr := startParseTasksInternalAtLevel(r, req.DatasetID, []string{taskID}, ProcessingLevelParsed)
	if startErr != nil || len(results) == 0 || results[0].Status != "STARTED" {
		message := "submit document Reader parsing failed"
		if startErr != nil {
			message = startErr.Error()
		}
		_ = s.updateParseState(r, req.DatasetID, req.DocumentID, "failed", "READER_SUBMIT_FAILED", message)
		return EnsureDocumentParsedResult{Status: "failed", TaskID: taskID}, &DocumentServiceError{Code: DocumentServiceUnavailable, Message: message, Err: startErr}
	}
	return EnsureDocumentParsedResult{Status: "parsing", TaskID: taskID}, nil
}

func failedDocumentParseResult(diagnostic *ProcessingDiagnostic) EnsureDocumentParsedResult {
	if diagnostic == nil {
		diagnostic = &ProcessingDiagnostic{Stage: "parse", Code: "PROCESSING_FAILED", Message: SafeProcessingMessage(""), Recovery: "请明确发起重新解析。"}
	}
	return EnsureDocumentParsedResult{Status: "failed", TaskID: diagnostic.TaskID, Error: diagnostic}
}

// Roots are authoritative when available. Otherwise consult the parser's
// current task, which can supersede a historical failed Core task after a
// service-side retry. Without that evidence, preserve the observed failure.
func (s *DocumentService) parseResultWithoutRoots(ctx context.Context, rec documentServiceRecord) (EnsureDocumentParsedResult, error) {
	fallback := EnsureDocumentParsedResult{Status: "parsing"}
	if rec.processing.ParseStatus == "failed" || NormalizeTaskStateForUI(rec.taskStat) == "FAILED" {
		fallback = failedDocumentParseResult(rec.diagnostic)
	}
	if s.lazyDB == nil || rec.dataset.KbID == "" {
		return fallback, nil
	}
	queryError := func(err error) (EnsureDocumentParsedResult, error) {
		return EnsureDocumentParsedResult{}, &DocumentServiceError{Code: DocumentServiceUnavailable, Message: "query current document parsing task failed", Err: err}
	}
	var current readonlyorm.LazyLLMDocParseStateRow
	err := s.lazyDB.WithContext(ctx).Where("doc_id = ? AND kb_id = ?", rec.row.LazyllmDocID, rec.dataset.KbID).Take(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fallback, nil
	}
	if err != nil {
		return queryError(err)
	}
	if strings.TrimSpace(current.CurrentTaskID) == "" {
		return fallback, nil
	}
	var latest orm.Task
	err = s.db.WithContext(ctx).Where("dataset_id = ? AND doc_id = ? AND deleted_at IS NULL", rec.row.DatasetID, rec.row.ID).
		Order("created_at DESC, id DESC").Take(&latest).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return queryError(err)
	}
	taskID := current.CurrentTaskID
	if latest.ID != "" && latest.LazyllmTaskID == current.CurrentTaskID {
		taskID = latest.ID
	} else if latest.ID != "" {
		// A new Core submission can fail before creating a parser task. Do
		// not let the previous parser snapshot mask that newer local failure.
		var remote readonlyorm.LazyLLMDocServiceTaskRow
		err = s.lazyDB.WithContext(ctx).Where("task_id = ? AND doc_id = ? AND kb_id = ?", current.CurrentTaskID, rec.row.LazyllmDocID, rec.dataset.KbID).
			Order("created_at DESC, id DESC").Take(&remote).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fallback, nil
		}
		if err != nil {
			return queryError(err)
		}
		if !remote.CreatedAt.After(latest.CreatedAt) {
			return fallback, nil
		}
	}
	switch NormalizeTaskStateForUI(strings.ToUpper(strings.TrimSpace(current.Status))) {
	case "FAILED", "ERROR", "CANCELED", "CANCELLED":
		code, message := "PROCESSING_FAILED", ""
		if current.LastErrorCode != nil && *current.LastErrorCode != "" {
			code = *current.LastErrorCode
		}
		if current.LastErrorMsg != nil {
			message = *current.LastErrorMsg
		}
		return failedDocumentParseResult(&ProcessingDiagnostic{Stage: "parse", Code: code, Message: SafeProcessingMessage(message), TaskID: taskID, Recovery: "请明确发起重新解析。"}), nil
	case "SUCCESS", "WORKING", "WAITING":
		// A successful task may publish its status before roots are visible.
		return EnsureDocumentParsedResult{Status: "parsing", TaskID: taskID}, nil
	default:
		return EnsureDocumentParsedResult{Status: "unknown", TaskID: taskID}, nil
	}
}

func (s *DocumentService) updateParseState(r *http.Request, datasetID, documentID, status, code, message string) error {
	updates := map[string]any{"parse_status": status, "parse_error_code": code, "parse_error_message": message, "updated_at": time.Now().UTC()}
	result := s.db.WithContext(r.Context()).Model(&orm.DocumentProcessingState{}).
		Where("dataset_id = ? AND document_id = ?", datasetID, documentID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		state := newDocumentProcessingState(datasetID, documentID, time.Now().UTC())
		state.ParseStatus, state.ParseErrorCode, state.ParseErrorMessage = status, code, message
		return s.db.WithContext(r.Context()).Create(&state).Error
	}
	return nil
}
