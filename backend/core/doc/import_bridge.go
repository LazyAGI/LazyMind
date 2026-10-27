package doc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// LocalFileImportRequest is only for trusted Core workers. Public clients must
// transfer bytes through uploads; their filesystem paths never reach this API.
type LocalFileImportRequest struct {
	// DocumentID is a stable identity supplied by a trusted, locked importer.
	DocumentID                                                                string
	DatasetID, ParentID, SourcePath, DisplayName, ContentType, DataSourceType string
	Tags                                                                      []string
	UserID, UserName                                                          string
}
type LocalFileImportResult struct {
	DocumentID, TaskID, ContentSHA256 string
	Start                             StartTaskResult
}

func ImportLocalFile(ctx context.Context, input LocalFileImportRequest) (LocalFileImportResult, error) {
	if strings.TrimSpace(input.DatasetID) == "" || strings.TrimSpace(input.UserID) == "" || strings.TrimSpace(input.SourcePath) == "" {
		return LocalFileImportResult{}, fmt.Errorf("dataset_id, source_path, and user_id are required")
	}
	var dataset orm.Dataset
	if err := store.DB().WithContext(ctx).Where("id = ? AND deleted_at IS NULL", input.DatasetID).Take(&dataset).Error; err != nil {
		return LocalFileImportResult{}, err
	}
	file, err := os.Open(input.SourcePath)
	if err != nil {
		return LocalFileImportResult{}, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		return LocalFileImportResult{}, fmt.Errorf("source file is not regular")
	}
	svc, err := NewOriginalService(OriginalServiceDeps{DB: store.DB()})
	if err != nil {
		return LocalFileImportResult{}, err
	}
	caller := DatasetCatalogCaller{UserID: input.UserID, UserName: input.UserName, TenantID: dataset.TenantID, Permissions: []string{"document.write"}}
	ref, err := svc.AcquireOriginal(ctx, caller, AcquireOriginalRequest{Filename: firstNonEmpty(input.DisplayName, filepath.Base(input.SourcePath)), ContentType: input.ContentType, Reader: file})
	if err != nil {
		return LocalFileImportResult{}, err
	}
	bound, err := svc.ImportOriginal(ctx, caller, ImportOriginalRequest{DocumentID: input.DocumentID, FileRef: ref.FileRef, DatasetID: input.DatasetID, ParentID: input.ParentID, Tags: input.Tags, DataSourceType: input.DataSourceType})
	if err != nil {
		return LocalFileImportResult{}, err
	}
	result := LocalFileImportResult{DocumentID: bound.Document.ID, TaskID: bound.Task.ID, ContentSHA256: bound.ContentSHA256, Start: bound.Start}
	if bound.SubmissionStatus == "failed" {
		return result, fmt.Errorf("document imported but processing did not start: %s", bound.SubmissionError)
	}
	return result, nil
}
