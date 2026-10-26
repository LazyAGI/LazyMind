package academic

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func currentImportDocument(ctx context.Context, db *gorm.DB, item ImportQueueItem) (*ImportQueueDocument, error) {
	var links []orm.AcademicWorkDocument
	if item.DocumentID != "" {
		var document orm.Document
		err := db.WithContext(ctx).Where("id=? AND dataset_id=? AND deleted_at IS NULL", item.DocumentID, item.TargetDatasetID).Take(&document).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			ext, _, available, err := readArxivDocument(document)
			if err != nil {
				return nil, err
			}
			if available {
				_, status, detail, err := newArxivImportService(db).processingStatus(ctx, document, ext.Marker)
				if err != nil {
					return nil, err
				}
				return &ImportQueueDocument{DocumentID: document.ID, ProcessingStatus: status, ProcessingError: detail}, nil
			}
		}
		// A historical document binding must not silently jump to a replacement.
		return nil, nil
	}
	if item.WorkID == "" {
		return nil, nil
	}
	query := db.WithContext(ctx).Where("academic_work_id=? AND dataset_id=?", item.WorkID, item.TargetDatasetID)
	if item.SourceProvider != "" && item.SourceVersion != "" {
		query = query.Where("source_provider=? AND source_version=?", item.SourceProvider, item.SourceVersion)
	} else if item.DocumentID != "" {
		query = query.Where("document_id=?", item.DocumentID)
	} else {
		return nil, nil
	}
	if err := query.Order("is_preferred_version DESC, updated_at DESC").Find(&links).Error; err != nil {
		return nil, err
	}
	service := newArxivImportService(db)
	for _, link := range links {
		var document orm.Document
		if err := db.WithContext(ctx).Where("id=? AND dataset_id=? AND deleted_at IS NULL", link.DocumentID, item.TargetDatasetID).Take(&document).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		} else if err != nil {
			return nil, err
		}
		ext, _, available, err := readArxivDocument(document)
		if err != nil {
			return nil, err
		}
		if !available {
			continue
		}
		_, status, detail, err := service.processingStatus(ctx, document, ext.Marker)
		if err != nil {
			return nil, err
		}
		return &ImportQueueDocument{DocumentID: document.ID, ProcessingStatus: status, ProcessingError: detail}, nil
	}
	return nil, nil
}
