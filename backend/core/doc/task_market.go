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

const (
	marketImportBatchSize  = 50
	marketImportBatchDelay = 200 * time.Millisecond
)

// MarketImportFile is one downloaded file to import into a dataset.
type MarketImportFile struct {
	LocalPath    string   // absolute path of the downloaded file
	DisplayName  string   // user-facing file name
	RelativePath string   // path inside the package ("" when at the root)
	Tags         []string // document tags assigned by the source adapter
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

	// startTasksInternal needs a request for user context and the parsing
	// service call; a synthetic request carries the same ctx and user headers.
	r := (&http.Request{Header: make(http.Header)}).WithContext(ctx)
	r.Header.Set("X-User-Id", userID)
	r.Header.Set("X-User-Name", userName)

	result := &MarketImportResult{DatasetID: ds.ID, TaskIDs: make([]string, 0, len(files))}
	for start := 0; start < len(files); start += marketImportBatchSize {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("market import canceled before batch %d: %w", start/marketImportBatchSize+1, err)
		}
		end := start + marketImportBatchSize
		if end > len(files) {
			end = len(files)
		}
		batchTaskIDs, submitted, err := importMarketFileBatch(ctx, db, ds, userID, userName, r, files[start:end])
		if err != nil {
			return nil, fmt.Errorf("import market files batch %d failed: %w", start/marketImportBatchSize+1, err)
		}
		result.TaskIDs = append(result.TaskIDs, batchTaskIDs...)
		result.Submitted += submitted
		if end < len(files) {
			time.Sleep(marketImportBatchDelay)
		}
	}
	return result, nil
}

// importMarketFileBatch registers one batch of documents and submits its tasks
// to the parsing pipeline before the next batch is created.
func importMarketFileBatch(ctx context.Context, db *gorm.DB, ds *orm.Dataset, userID, userName string, r *http.Request, files []MarketImportFile) ([]string, int, error) {
	now := time.Now().UTC()
	taskIDs := make([]string, 0, len(files))
	for _, file := range files {
		displayName := strings.TrimSpace(file.DisplayName)
		if displayName == "" {
			displayName = filepath.Base(file.LocalPath)
		}
		documentTags := normalizeBatchDocumentTags(file.Tags)
		documentID := newDocID()
		taskID := newTaskID()
		storedName := storedFileName(displayName, documentID)
		finalDir := buildDatasetDocFileDir(ds.TenantID, ds.ID, file.RelativePath, documentID)
		if err := os.MkdirAll(finalDir, 0o755); err != nil {
			return nil, 0, fmt.Errorf("create dataset dir failed: %w", err)
		}
		finalPath := filepath.Join(finalDir, storedName)
		size, err := copyMarketFile(file.LocalPath, finalPath)
		if err != nil {
			return nil, 0, fmt.Errorf("copy %s failed: %w", displayName, err)
		}
		size, err = normalizeUploadedTextFileInPlace(finalPath, displayName, size)
		if err != nil {
			return nil, 0, fmt.Errorf("normalize %s failed: %w", displayName, err)
		}

		contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(displayName)))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		docExt := newDocumentExt(finalPath, storedName, displayName, size, contentType, file.RelativePath, nil)
		docRow := orm.Document{
			ID: documentID, DatasetID: ds.ID, DisplayName: displayName,
			DocumentType: fileDocumentTypeFromName(displayName),
			Tags:         mustJSON(documentTags), FileID: documentID,
			PDFConvertResult: docExt.ConvertStatus, Ext: mustJSON(docExt),
			BaseModel: orm.BaseModel{CreateUserID: userID, CreateUserName: userName, CreatedAt: now, UpdatedAt: now},
		}
		tExt := taskExt{
			TaskType: string(TaskTypeParseUploaded), DisplayName: displayName,
			DataSourceType: "MARKET", DocumentTags: documentTags,
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
			return nil, 0, fmt.Errorf("create document/task rows failed: %w", err)
		}
		recalcAffectedFolderStats(ctx, ds.ID, "")
		taskIDs = append(taskIDs, taskID)
	}

	results, err := startTasksInternal(r, ds.ID, taskIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("submit parse tasks failed: %w", err)
	}
	return taskIDs, len(results), nil
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
