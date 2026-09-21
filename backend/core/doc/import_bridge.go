package doc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// LocalFileImportRequest is the service-level counterpart of browser upload.
// It lets trusted Core workers import an already validated local file while
// retaining the same document/task/processing-level behavior as normal upload.
type LocalFileImportRequest struct {
	DatasetID      string
	ParentID       string
	SourcePath     string
	DisplayName    string
	ContentType    string
	DataSourceType string
	Tags           []string
	UserID         string
	UserName       string
}

type LocalFileImportResult struct {
	DocumentID    string
	TaskID        string
	ContentSHA256 string
	Start         StartTaskResult
}

func ImportLocalFile(ctx context.Context, input LocalFileImportRequest) (LocalFileImportResult, error) {
	input.DatasetID = strings.TrimSpace(input.DatasetID)
	input.SourcePath = strings.TrimSpace(input.SourcePath)
	input.UserID = strings.TrimSpace(input.UserID)
	if input.DatasetID == "" || input.SourcePath == "" || input.UserID == "" {
		return LocalFileImportResult{}, fmt.Errorf("dataset_id, source_path, and user_id are required")
	}
	var dataset orm.Dataset
	if err := store.DB().WithContext(ctx).Where("id = ? AND deleted_at IS NULL", input.DatasetID).Take(&dataset).Error; err != nil {
		return LocalFileImportResult{}, fmt.Errorf("target dataset not found")
	}
	source, err := os.Open(input.SourcePath)
	if err != nil {
		return LocalFileImportResult{}, err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return LocalFileImportResult{}, fmt.Errorf("source file is not regular")
	}

	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = filepath.Base(input.SourcePath)
	}
	uploadID := newUploadID()
	finalDir := buildDatasetDocFileDir(dataset.TenantID, dataset.ID, "", uploadID)
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		return LocalFileImportResult{}, err
	}
	storedName := storedFileName(displayName, uploadID)
	finalPath := filepath.Join(finalDir, storedName)
	target, err := os.OpenFile(finalPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		_ = os.RemoveAll(finalDir)
		return LocalFileImportResult{}, err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(target, hasher), io.LimitReader(source, 128<<20))
	closeErr := target.Close()
	if copyErr != nil || closeErr != nil || written != info.Size() {
		_ = os.RemoveAll(finalDir)
		return LocalFileImportResult{}, fmt.Errorf("copy import file failed")
	}
	hash := hex.EncodeToString(hasher.Sum(nil))
	now := time.Now().UTC()
	userName := firstNonEmpty(strings.TrimSpace(input.UserName), input.UserID)
	ext := uploadedFileExt{StoredPath: finalPath, StoredName: storedName, OriginalFilename: displayName, FileSize: written,
		ContentType: strings.TrimSpace(input.ContentType), DocumentPID: strings.TrimSpace(input.ParentID), DocumentTags: append([]string(nil), input.Tags...)}
	uploaded := orm.UploadedFile{UploadFileID: uploadID, ContentHash: hash, DatasetID: dataset.ID, TenantID: dataset.TenantID,
		Status: UploadedFileStateUploaded, Ext: mustJSON(ext), BaseModel: orm.BaseModel{CreateUserID: input.UserID, CreateUserName: userName, CreatedAt: now, UpdatedAt: now}}
	if err := store.DB().WithContext(ctx).Create(&uploaded).Error; err != nil {
		_ = os.RemoveAll(finalDir)
		return LocalFileImportResult{}, err
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/internal/paper-import", nil)
	req.Header.Set("X-User-Id", input.UserID)
	req.Header.Set("X-User-Name", userName)
	tasks, err := createTaskFromUploadedFile(req, dataset.ID, input.UserID, userName, CreateTaskItem{UploadFileID: uploadID,
		Task: TaskPayload{TaskType: TaskTypeParseUploaded, DocumentPID: input.ParentID, DisplayName: displayName,
			DataSourceType: firstNonEmpty(strings.TrimSpace(input.DataSourceType), "ACADEMIC_PROVIDER"), DocumentTags: input.Tags}}, string(TaskTypeParseUploaded))
	if err != nil || len(tasks) == 0 {
		return LocalFileImportResult{}, fmt.Errorf("create import task failed: %w", err)
	}
	starts, startErr := startTasksInternal(req, dataset.ID, []string{tasks[0].ID})
	result := LocalFileImportResult{DocumentID: tasks[0].DocID, TaskID: tasks[0].ID, ContentSHA256: hash}
	if len(starts) > 0 {
		result.Start = starts[0]
	}
	if startErr != nil && effectiveProcessingLevel(dataset.ProcessingLevel) != ProcessingLevelStored {
		return result, fmt.Errorf("document imported but processing did not start: %w", startErr)
	}
	return result, nil
}
