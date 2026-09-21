package doc

import (
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

const RootNodeGroup = "lazyllm_root"

type EnsureDocumentParsedRequest struct {
	UserID     string
	DatasetID  string
	DocumentID string
	Caller     DatasetCatalogCaller
}

type EnsureDocumentParsedResult struct {
	Status string `json:"status"`
	TaskID string `json:"task_id,omitempty"`
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
	if rec.lazy != nil {
		status := strings.ToUpper(strings.TrimSpace(rec.lazy.UploadStatus))
		taskStatus := strings.ToUpper(strings.TrimSpace(rec.taskStat))
		if taskStatus == "FAILED" || taskStatus == "ERROR" {
			message := "document Reader parsing failed"
			_ = s.updateParseState(r, req.DatasetID, req.DocumentID, "failed", "READER_PARSE_FAILED", message)
			return EnsureDocumentParsedResult{Status: "failed"}, &DocumentServiceError{Code: DocumentServiceUnavailable, Message: message}
		}
		switch status {
		case "SUCCESS", "SUCCEEDED":
			roots, rootsErr := s.ListDocumentChunks(r.Context(), DocumentChunksRequest{
				UserID: req.UserID, DatasetID: req.DatasetID, DocumentID: req.DocumentID,
				PageSize: 1, SegmentGroup: RootNodeGroup, Caller: req.Caller,
			})
			if rootsErr != nil || len(roots.Chunks) == 0 {
				// The parsing service can publish its terminal task status shortly
				// before the root nodes become visible. Do not expose a false
				// "parsed" state to consumers that require those nodes.
				return EnsureDocumentParsedResult{Status: "parsing"}, nil
			}
			_ = s.updateParseState(r, req.DatasetID, req.DocumentID, "succeeded", "", "")
			return EnsureDocumentParsedResult{Status: "parsed"}, nil
		case "FAILED", "ERROR":
			message := "document Reader parsing failed"
			_ = s.updateParseState(r, req.DatasetID, req.DocumentID, "failed", "READER_PARSE_FAILED", message)
			return EnsureDocumentParsedResult{Status: "failed"}, &DocumentServiceError{Code: DocumentServiceUnavailable, Message: message}
		default:
			return EnsureDocumentParsedResult{Status: "parsing"}, nil
		}
	}

	var state orm.DocumentProcessingState
	if err := s.db.WithContext(r.Context()).Where("dataset_id = ? AND document_id = ?", req.DatasetID, req.DocumentID).Take(&state).Error; err == nil && state.ParseStatus == "running" {
		var active orm.Task
		_ = s.db.WithContext(r.Context()).Where("dataset_id = ? AND doc_id = ? AND deleted_at IS NULL", req.DatasetID, req.DocumentID).Order("created_at DESC").Take(&active).Error
		return EnsureDocumentParsedResult{Status: "parsing", TaskID: active.ID}, nil
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
	err = s.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		return tx.Model(&orm.DocumentProcessingState{}).
			Where("dataset_id = ? AND document_id = ?", req.DatasetID, req.DocumentID).
			Updates(map[string]any{"parse_status": "running", "parse_error_code": "", "parse_error_message": "", "updated_at": now}).Error
	})
	if err != nil {
		return EnsureDocumentParsedResult{}, err
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
