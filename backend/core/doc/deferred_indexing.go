package doc

import (
	"context"
	"net/http"
	"strings"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/modelprovider"
	"lazymind/core/settings"
	"lazymind/core/store"
	"lazymind/core/systemdeps"
)

// StartDeferredIndexing upgrades native artifacts only when both RAG and the
// owner's embedding model are ready. SQLite stages survive process restarts.
func StartDeferredIndexing(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reconcileDeferredIndexing(ctx)
			}
		}
	}()
	return done
}

func reconcileDeferredIndexing(ctx context.Context) {
	reconcileDeferredIndexingWithReady(ctx, func(ctx context.Context, user string) (bool, error) {
		return modelprovider.IsModelReady(ctx, store.DB(), user, "embed_main")
	})
}

func reconcileDeferredIndexingWithReady(ctx context.Context, isReady func(context.Context, string) (bool, error)) {
	if !systemdeps.PythonComponentActive("rag") {
		return
	}
	db := store.DB().WithContext(ctx)
	var states []orm.DocumentProcessingState
	if db.Where("parse_artifact_ref <> '' AND index_status <> ?", "succeeded").
		Order("updated_at ASC").Find(&states).Error != nil {
		return
	}
	readiness := make(map[string]bool)
	for _, state := range states {
		if ctx.Err() != nil {
			return
		}
		var dataset orm.Dataset
		if db.Where("id = ? AND deleted_at IS NULL", state.DatasetID).Take(&dataset).Error != nil ||
			effectiveProcessingLevel(dataset.ProcessingLevel) != ProcessingLevelIndexed {
			continue
		}
		var document orm.Document
		if db.Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", state.DocumentID, state.DatasetID).
			Take(&document).Error != nil {
			continue
		}
		r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/internal/deferred-indexing", nil)
		r.Header.Set("X-User-Id", dataset.CreateUserID)
		syncProcessingTaskStates(r, dataset.ID)
		db.Where("dataset_id = ? AND document_id = ?", dataset.ID, document.ID).Take(&state)
		if state.IndexStatus == "succeeded" || state.IndexStatus == "running" {
			continue
		}
		// Failed/abandoned submissions back off; successful remote submissions are
		// joined by syncProcessingTaskStates rather than submitted repeatedly.
		if state.IndexStatus != "pending" && time.Since(state.UpdatedAt) < 5*time.Minute {
			continue
		}
		controls, err := settings.LoadFeatureControls(ctx, db, dataset.CreateUserID)
		if err != nil || !controls.DocumentParsingEnabled {
			continue
		}
		ready, checked := readiness[dataset.CreateUserID]
		if !checked {
			ready, err = isReady(ctx, dataset.CreateUserID)
			readiness[dataset.CreateUserID] = ready && err == nil
		}
		if !readiness[dataset.CreateUserID] {
			continue
		}
		claimed := db.Model(&orm.DocumentProcessingState{}).
			Where("dataset_id = ? AND document_id = ? AND updated_at = ? AND index_status = ?",
				dataset.ID, document.ID, state.UpdatedAt, state.IndexStatus).
			Updates(map[string]any{"index_status": "processing", "updated_at": time.Now().UTC()})
		if claimed.Error != nil || claimed.RowsAffected != 1 {
			continue
		}
		err = ensureDatasetRAG(ctx, &dataset)
		if err == nil {
			err = db.Model(&orm.Dataset{}).Where("id = ?", dataset.ID).Update("ext", dataset.Ext).Error
		}
		if err == nil {
			taskType := string(TaskTypeParseUploaded)
			if strings.TrimSpace(document.LazyllmDocID) != "" {
				taskType = string(TaskTypeReparse)
			}
			task, createErr := createTaskFromExistingDocument(r, dataset.ID, dataset.CreateUserID, "",
				CreateTaskItem{Task: TaskPayload{DocumentID: document.ID}}, taskType)
			err = createErr
			if err == nil {
				_, err = startTasksInternal(r, dataset.ID, []string{task.ID})
			}
		}
		if err != nil {
			db.Model(&orm.DocumentProcessingState{}).Where("dataset_id = ? AND document_id = ?", dataset.ID, document.ID).
				Updates(map[string]any{"index_status": "failed", "index_error_message": err.Error(), "updated_at": time.Now().UTC()})
		}
	}
}
