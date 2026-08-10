package doc

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// MarketImportFile is one downloaded file to import into a dataset.
type MarketImportFile struct {
	LocalPath    string // absolute path of the downloaded file
	DisplayName  string // user-facing file name
	RelativePath string // path inside the package ("" when at the root)
}

// MarketImportResult summarizes a submitted market import.
type MarketImportResult struct {
	DatasetID string   `json:"dataset_id"`
	Submitted int      `json:"submitted"`
	TaskIDs   []string `json:"task_ids"`
}

// ImportMarketFiles registers document/task rows for every downloaded file and
// submits them to the parsing pipeline (parse + vectorize). It mirrors the
// multipart upload flow but sources the bytes from local files.
func ImportMarketFiles(ctx context.Context, ds *orm.Dataset, userID, userName string, files []MarketImportFile) (*MarketImportResult, error) {
	if ds == nil || len(files) == 0 {
		return nil, fmt.Errorf("dataset and files are required")
	}
	db := store.DB()
	if db == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	now := time.Now().UTC()
	taskIDs := make([]string, 0, len(files))
	for _, file := range files {
		displayName := strings.TrimSpace(file.DisplayName)
		if displayName == "" {
			displayName = filepath.Base(file.LocalPath)
		}
		documentID := newDocID()
		taskID := newTaskID()
		storedName := storedFileName(displayName, documentID)
		finalDir := buildDatasetDocFileDir(ds.TenantID, ds.ID, file.RelativePath, documentID)
		if err := os.MkdirAll(finalDir, 0o755); err != nil {
			return nil, fmt.Errorf("create dataset dir failed: %w", err)
		}
		finalPath := filepath.Join(finalDir, storedName)
		size, err := copyMarketFile(file.LocalPath, finalPath)
		if err != nil {
			return nil, fmt.Errorf("copy %s failed: %w", displayName, err)
		}
		size, err = normalizeUploadedTextFileInPlace(finalPath, displayName, size)
		if err != nil {
			return nil, fmt.Errorf("normalize %s failed: %w", displayName, err)
		}

		contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(displayName)))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		docExt := newDocumentExt(finalPath, storedName, displayName, size, contentType, file.RelativePath, nil)
		docRow := orm.Document{
			ID: documentID, DatasetID: ds.ID, DisplayName: displayName,
			DocumentType: fileDocumentTypeFromName(displayName),
			Tags:         mustJSON([]string{}), FileID: documentID,
			PDFConvertResult: docExt.ConvertStatus, Ext: mustJSON(docExt),
			BaseModel: orm.BaseModel{CreateUserID: userID, CreateUserName: userName, CreatedAt: now, UpdatedAt: now},
		}
		tExt := taskExt{
			TaskType: string(TaskTypeParseUploaded), DisplayName: displayName,
			DataSourceType: "MARKET", DocumentTags: []string{},
			Files: []TaskFile{{DisplayName: displayName, StoredName: storedName, StoredPath: finalPath, FileSize: size, RelativePath: file.RelativePath, ContentType: contentType}},
		}
		taskRow := orm.Task{
			ID: taskID, DocID: documentID, KbID: ds.ID, AlgoID: datasetAlgoIDByID(ds.ID),
			DatasetID: ds.ID, TaskType: string(TaskTypeParseUploaded),
			DisplayName: displayName, Ext: mustJSON(tExt),
			BaseModel: orm.BaseModel{CreateUserID: userID, CreateUserName: userName, CreatedAt: now, UpdatedAt: now},
		}
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&docRow).Error; err != nil {
				return err
			}
			return tx.Create(&taskRow).Error
		}); err != nil {
			return nil, fmt.Errorf("create document/task rows failed: %w", err)
		}
		recalcAffectedFolderStats(ctx, ds.ID, "")
		taskIDs = append(taskIDs, taskID)
	}

	// startTasksInternal needs a request for user context and the parsing
	// service call; a synthetic request carries the same ctx and user headers.
	r := (&http.Request{Header: make(http.Header)}).WithContext(ctx)
	r.Header.Set("X-User-Id", userID)
	r.Header.Set("X-User-Name", userName)
	results, err := startTasksInternal(r, ds.ID, taskIDs)
	if err != nil {
		return nil, fmt.Errorf("submit parse tasks failed: %w", err)
	}
	return &MarketImportResult{DatasetID: ds.ID, Submitted: len(results), TaskIDs: taskIDs}, nil
}

// copyMarketFile copies a downloaded file into the dataset doc dir.
func copyMarketFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return 0, copyErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	return n, nil
}
