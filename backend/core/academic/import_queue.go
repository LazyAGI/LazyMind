package academic

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
	"net/http"
	"os"
)

// currentImportDocuments reuses a request's processing observation and checks each
// distinct original once, even when several receipts resolve to the same PDF.
func currentImportDocuments(r *http.Request, db *gorm.DB, items []ImportQueueItem, observed operationObservation) (map[string]*ImportQueueDocument, error) {
	result := map[string]*ImportQueueDocument{}
	if len(items) == 0 {
		return result, nil
	}
	resolved := observed.Documents
	if resolved == nil {
		ids := make([]string, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.ItemID)
		}
		var rows []operationDocument
		if err := operationDocuments(db.WithContext(r.Context()), store.UserID(r), "", nil, ids).Scan(&rows).Error; err != nil {
			return nil, err
		}
		resolved = map[string]string{}
		for _, row := range rows {
			resolved[row.ID] = row.EffectiveDocumentID
		}
	}
	byDataset := map[string]map[string]bool{}
	for _, item := range items {
		id := resolved[item.ItemID]
		if id == "" {
			continue
		}
		if byDataset[item.TargetDatasetID] == nil {
			byDataset[item.TargetDatasetID] = map[string]bool{}
		}
		byDataset[item.TargetDatasetID][id] = true
	}
	current := map[string]*ImportQueueDocument{}
	for dataset, unique := range byDataset {
		ids := make([]string, 0, len(unique))
		for id := range unique {
			ids = append(ids, id)
		}
		var documents []orm.Document
		if err := db.WithContext(r.Context()).Where("dataset_id=? AND id IN ? AND deleted_at IS NULL", dataset, ids).Find(&documents).Error; err != nil {
			return nil, err
		}
		metadata := observed.Metadata
		missing := []string{}
		for _, document := range documents {
			if value, ok := metadata[document.ID]; !ok || value.DatasetID != dataset {
				missing = append(missing, document.ID)
			}
		}
		if len(missing) > 0 {
			fresh, err := processingMetadata(r, db, dataset, missing)
			if err != nil {
				return nil, err
			}
			// Keep observations owned by this request; no persisted processing state is changed.
			if metadata == nil {
				metadata = map[string]doc.DocumentMetadata{}
			}
			for id, value := range fresh {
				metadata[id] = value
			}
		}
		for _, document := range documents {
			var ext struct {
				StoredPath string `json:"stored_path"`
			}
			if err := json.Unmarshal(document.Ext, &ext); err != nil {
				return nil, err
			}
			info, err := os.Stat(ext.StoredPath)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			value, ok := metadata[document.ID]
			if !ok {
				continue
			}
			status := doc.DocumentProcessingStatus(value.ProcessingLevel, value.ProcessingState)
			if status == "not_requested" {
				status = "ready"
			}
			message := ""
			if value.ProcessingError != nil {
				status, message = "failed", value.ProcessingError.Message
			}
			current[document.ID] = &ImportQueueDocument{DocumentID: document.ID, ProcessingStatus: status, ProcessingError: message}
		}
	}
	for _, item := range items {
		result[item.ItemID] = current[resolved[item.ItemID]]
	}
	return result, nil
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
