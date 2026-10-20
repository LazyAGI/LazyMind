package doc

import (
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"strings"
)

// validateTaskPublication fences an external response against current Core
// ownership. A stale parser response must never recreate or publish a moved source.
func validateTaskPublication(tx *gorm.DB, datasetID string, task orm.Task, document orm.Document) error {
	var dataset orm.Dataset
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND deleted_at IS NULL", datasetID).Take(&dataset).Error; err != nil {
		return fmt.Errorf("dataset no longer accepts task publication: %w", err)
	}
	var current orm.Document
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", document.ID, datasetID).Take(&current).Error; err != nil {
		return fmt.Errorf("document ownership changed before publication: %w", err)
	}
	if current.ResourceVersion != document.ResourceVersion {
		return fmt.Errorf("document changed before task publication")
	}
	var activeTask orm.Task
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND dataset_id = ? AND doc_id = ? AND deleted_at IS NULL", task.ID, datasetID, document.ID).Take(&activeTask).Error; err != nil {
		return fmt.Errorf("task no longer owns this document: %w", err)
	}
	return nil
}

// This write must run after the publication transaction rolls back. Returning
// its error prevents callers from silently losing the durable rejection.
func rejectExternalPublications(db *gorm.DB, datasetID string, externalIDs []string) error {
	rows := make([]orm.RejectedDocumentPublication, 0, len(externalIDs))
	seen := map[string]bool{}
	for _, id := range externalIDs {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			rows = append(rows, orm.RejectedDocumentPublication{DatasetID: datasetID, ExternalDocID: id})
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}
