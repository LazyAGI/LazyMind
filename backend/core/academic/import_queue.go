package academic

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
	"os"
)

func currentImportDocument(ctx context.Context, db *gorm.DB, item ImportQueueItem, user string) (*ImportQueueDocument, error) {
	id := item.DocumentID
	if id == "" {
		if item.WorkID == "" || item.SourceProvider == "" || item.SourceVersion == "" {
			return nil, nil
		}
		var link orm.AcademicWorkDocument
		err := db.WithContext(ctx).Where("academic_work_id=? AND dataset_id=? AND source_provider=? AND source_version=?", item.WorkID, item.TargetDatasetID, item.SourceProvider, item.SourceVersion).Order("is_preferred_version DESC, updated_at DESC").Take(&link).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		id = link.DocumentID
	}
	var d orm.Document
	err := db.WithContext(ctx).Where("id=? AND dataset_id=? AND deleted_at IS NULL", id, item.TargetDatasetID).Take(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ext struct {
		StoredPath string `json:"stored_path"`
	}
	if err = json.Unmarshal(d.Ext, &ext); err != nil {
		return nil, err
	}
	stat, err := os.Stat(ext.StoredPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, nil
	}
	status, message, err := paperProcessingStatus(ctx, db, d)
	if err != nil {
		return nil, err
	}
	return &ImportQueueDocument{DocumentID: id, ProcessingStatus: status, ProcessingError: message}, nil
}
func paperProcessingStatus(ctx context.Context, db *gorm.DB, d orm.Document) (string, string, error) {
	var ds orm.Dataset
	if err := db.WithContext(ctx).Where("id=? AND deleted_at IS NULL", d.DatasetID).Take(&ds).Error; err != nil {
		return "", "", err
	}
	if ds.ProcessingLevel == doc.ProcessingLevelStored {
		return "ready", "", nil
	}
	state, _, diagnostic, err := doc.ObserveDocumentProcessingDetails(ctx, db, store.LazyLLMDB(), d)
	if err != nil {
		return "", "", err
	}
	status := doc.DocumentProcessingStatus(ds.ProcessingLevel, state)
	if diagnostic != nil {
		return "failed", diagnostic.Message, nil
	}
	return status, "", nil
}
