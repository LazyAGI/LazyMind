package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"net/http"
	"os"
	"strings"
	"time"
)

// ErrImportedDocumentUnavailable permits acquisition of a replacement only
// when the previous document or its stored file no longer exists.
var ErrImportedDocumentUnavailable = errors.New("imported document file is unavailable")

type ResumeLocalFileImportRequest struct {
	DatasetID  string
	DocumentID string
	TaskID     string
	UserID     string
	UserName   string
}

// ResumeLocalFileImport retries processing of a saved import without copying
// its file or creating another document. Missing tasks are rebuilt for that
// same document; tasks already accepted by processing are left running.
func ResumeLocalFileImport(ctx context.Context, input ResumeLocalFileImportRequest) (LocalFileImportResult, error) {
	result := LocalFileImportResult{DocumentID: input.DocumentID, TaskID: input.TaskID}
	var document orm.Document
	err := store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", input.DocumentID, input.DatasetID).Take(&document).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, ErrImportedDocumentUnavailable
	}
	if err != nil {
		return result, err
	}
	var dataset orm.Dataset
	if err := store.DB().WithContext(ctx).Where("id=? AND deleted_at IS NULL", input.DatasetID).Take(&dataset).Error; err != nil {
		return result, err
	}
	var ext documentExt
	if err := json.Unmarshal(document.Ext, &ext); err != nil {
		return result, fmt.Errorf("read imported file metadata: %w", err)
	}
	info, err := os.Lstat(strings.TrimSpace(ext.StoredPath))
	if os.IsNotExist(err) || (err == nil && !info.Mode().IsRegular()) {
		return result, ErrImportedDocumentUnavailable
	}
	if err != nil {
		return result, fmt.Errorf("read imported file: %w", err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/internal/paper-import", nil)
	req.Header.Set("X-User-Id", input.UserID)
	req.Header.Set("X-User-Name", firstNonEmpty(input.UserName, input.UserID))
	tasks := store.DB().WithContext(ctx).Where("dataset_id = ? AND doc_id = ? AND task_type IN ? AND deleted_at IS NULL",
		input.DatasetID, input.DocumentID, []string{string(TaskTypeParse), string(TaskTypeParseUploaded), string(TaskTypeReparse)})
	var task orm.Task
	err = gorm.ErrRecordNotFound
	if input.TaskID != "" {
		err = tasks.Session(&gorm.Session{}).Where("id = ?", input.TaskID).Take(&task).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = tasks.Order("created_at DESC").Take(&task).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var dataset orm.Dataset
		err = store.DB().WithContext(ctx).Where("id = ? AND deleted_at IS NULL", input.DatasetID).Take(&dataset).Error
		if err == nil && effectiveProcessingLevel(dataset.ProcessingLevel) == ProcessingLevelStored {
			// An interrupted import into a storage-only library needs a receipt,
			// not an ingestion request. The public retained-file ingestion path
			// deliberately requires a higher processing level.
			now := time.Now().UTC()
			task = orm.Task{ID: newTaskID(), DocID: document.ID, DatasetID: dataset.ID,
				KbID: datasetKbIDByID(dataset.ID), AlgoID: datasetAlgoIDByID(dataset.ID),
				TaskType: string(TaskTypeParseUploaded), DisplayName: document.DisplayName, DocumentPID: document.PID,
				Ext: mustJSON(taskExt{TaskType: string(TaskTypeParseUploaded), TaskState: string(TaskStateCreating),
					DisplayName: document.DisplayName, DocumentPID: document.PID, DataSourceType: "ACADEMIC_PROVIDER",
					Files: []TaskFile{{StoredPath: ext.StoredPath, StoredName: ext.StoredName, DisplayName: document.DisplayName,
						FileSize: ext.FileSize, ContentType: ext.ContentType}}}),
				BaseModel: orm.BaseModel{CreateUserID: input.UserID, CreateUserName: firstNonEmpty(input.UserName, input.UserID), CreatedAt: now, UpdatedAt: now}}
			err = store.DB().WithContext(ctx).Create(&task).Error
		} else if err == nil {
			taskType := TaskTypeParseUploaded
			if strings.TrimSpace(document.LazyllmDocID) != "" {
				taskType = TaskTypeReparse
			}
			task, err = createTaskFromExistingDocument(req, input.DatasetID, input.UserID, firstNonEmpty(input.UserName, input.UserID),
				CreateTaskItem{Task: TaskPayload{DocumentID: input.DocumentID, DataSourceType: "ACADEMIC_PROVIDER"}}, string(taskType))
		}
	}
	if err != nil {
		return result, fmt.Errorf("recover imported document task: %w", err)
	}
	result.TaskID = task.ID
	var control taskExt
	if err := json.Unmarshal(task.Ext, &control); err != nil {
		return result, fmt.Errorf("read imported task metadata: %w", err)
	}
	if unconfirmedMarketAttempt(task, control) || (control.MarketSubmission == "submitting" && task.LazyllmTaskID == "") {
		return result, fmt.Errorf("the previous processing submission is not confirmed; reconcile the task before retrying")
	}
	// A document registration alone does not prove that this task was submitted.
	// Recovered tasks have neither a remote task ID nor an explicit task state.
	if task.LazyllmTaskID != "" || control.TaskState != "" {
		state := buildTaskResponse(req, task).TaskState
		if state == "SUCCESS" || state == "WORKING" || (state == "WAITING" && (task.LazyllmTaskID != "" || strings.EqualFold(strings.TrimSpace(control.TaskState), "WAITING"))) {
			observed, observedTask, _, observeErr := ObserveDocumentProcessingDetails(ctx, store.DB(), store.LazyLLMDB(), document)
			if observeErr != nil {
				return result, observeErr
			}
			if task.LazyllmTaskID != "" && NormalizeTaskStateForUI(observedTask) == "UNKNOWN" {
				return result, fmt.Errorf("the previous processing task state is unknown; reconcile it before retrying")
			}

			if DocumentProcessingStatus(dataset.ProcessingLevel, observed) == "failed" {
				// Current task error details override a nominal upstream success.
				state = "FAILED"
			}
		}
		if state == "SUCCESS" || state == "WORKING" || (state == "WAITING" && (task.LazyllmTaskID != "" || strings.EqualFold(strings.TrimSpace(control.TaskState), "WAITING"))) {
			result.Start = StartTaskResult{TaskID: task.ID, DocumentID: document.ID, Status: "STARTED", SubmitStatus: "ACCEPTED"}
			return result, nil
		}
	}
	start, startErr := SubmitOriginalTask(ctx, DatasetCatalogCaller{UserID: input.UserID, UserName: input.UserName, Permissions: []string{"document.write"}}, input.DatasetID, task.ID)
	result.Start = start
	if startErr != nil {
		if result.Start.Message != "" {
			startErr = fmt.Errorf("%s: %w", result.Start.Message, startErr)
		}
		return result, fmt.Errorf("document imported but processing did not start: %w", startErr)
	}
	return result, nil
}
