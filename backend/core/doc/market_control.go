package doc

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"lazymind/core/acl"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/store"
)

type marketCheckpointKey struct{}
type marketRetryDispatchKey struct{}

func unconfirmedMarketAttempt(row orm.Task, ext taskExt) bool {
	return ext.MarketSubmission == "retrying" && (row.LazyllmTaskID == ext.MarketPreviousTaskID || row.LazyllmTaskID == "")
}

// The market owner persists the registered file set before any external write.
func WithMarketImportCheckpoint(ctx context.Context, checkpoint func(*MarketImportResult) error) context.Context {
	return context.WithValue(ctx, marketCheckpointKey{}, checkpoint)
}

func MarketImportCheckpoint(ctx context.Context, result *MarketImportResult) error {
	if checkpoint, ok := ctx.Value(marketCheckpointKey{}).(func(*MarketImportResult) error); ok {
		if err := checkpoint(result); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func setMarketSubmission(ctx context.Context, datasetID string, ids []string, state string) error {
	for _, id := range ids {
		var row orm.Task
		if err := store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ?", id, datasetID).Take(&row).Error; err != nil {
			return err
		}
		var ext taskExt
		if err := json.Unmarshal(row.Ext, &ext); err != nil {
			return err
		}
		ext.MarketSubmission = state
		if state == "canceled" {
			ext.TaskState = string(TaskStateCancelled)
		}
		if err := store.DB().WithContext(ctx).Model(&row).Update("ext", mustJSON(ext)).Error; err != nil {
			return err
		}
	}
	return nil
}

// Only Core-owned files that were never dispatched may be canceled locally.
// In-flight requests retain their identity and are reconciled from Algorithm.
func CancelUnsubmittedMarketFiles(ctx context.Context, datasetID string, ids []string) error {
	var rows []orm.Task
	if err := store.DB().WithContext(ctx).Where("id IN ? AND dataset_id = ?", ids, datasetID).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var ext taskExt
		if err := json.Unmarshal(row.Ext, &ext); err != nil {
			return err
		}
		if ext.MarketSubmission == "registered" && row.LazyllmTaskID == "" && NormalizeTaskStateForUI(ext.TaskState) == "WAITING" {
			if err := setMarketSubmission(ctx, datasetID, []string{row.ID}, "canceled"); err != nil {
				return err
			}
		}
	}
	return nil
}

func RequireMarketDatasetPermission(w http.ResponseWriter, r *http.Request, datasetID string) bool {
	if _, _, ok := requireDatasetPermission(r, datasetID, acl.PermissionDatasetUpload); !ok {
		replyDatasetForbidden(w)
		return false
	}
	return true
}

// CancelMarketFile uses the public document-service contract. A failed or lost
// response is reconciled, never blindly retried or converted to local success.
func CancelMarketFile(r *http.Request, file MarketTaskState) string {
	externalID := strings.TrimSpace(file.Task.LazyllmTaskID)
	if externalID == "" {
		var row readonlyorm.LazyLLMDocServiceTaskRow
		if err := store.LazyLLMDB().WithContext(r.Context()).Where("doc_id = ? AND kb_id = ?", file.Task.DocID, file.Task.DatasetID).Order("updated_at DESC").Take(&row).Error; err != nil {
			return "UNKNOWN"
		}
		externalID = row.TaskID
	}
	_ = callExternalSuspendJob(r, ExternalCancelTaskRequest{TaskID: externalID})
	// Success, conflict and transport failure all require authoritative reconciliation.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	files, err := MarketTaskStates(ctx, store.DB(), file.Task.DatasetID, []string{file.Task.ID})
	if err != nil || len(files) != 1 {
		return "UNKNOWN"
	}
	if files[0].State == "WAITING" {
		return "UNKNOWN"
	}
	return files[0].State
}

// A dispatched request can have committed remotely even when cancellation
// interrupts its response. Missing evidence stays unknown; it is not retried.
func ReconcileInterruptedMarketFiles(ctx context.Context, result *MarketImportResult) error {
	sent := make(map[string]bool, len(result.DispatchedTaskIDs))
	for _, id := range result.DispatchedTaskIDs {
		sent[id] = true
	}
	var unsent []string
	for _, id := range result.TaskIDs {
		if !sent[id] {
			unsent = append(unsent, id)
		}
	}
	if err := CancelUnsubmittedMarketFiles(ctx, result.DatasetID, unsent); err != nil {
		return err
	}
	var rows []orm.Task
	if err := store.DB().WithContext(ctx).Where("id IN ? AND dataset_id = ?", result.DispatchedTaskIDs, result.DatasetID).Find(&rows).Error; err != nil {
		return err
	}
	var dataset orm.Dataset
	if err := store.DB().WithContext(ctx).Take(&dataset, "id = ?", result.DatasetID).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var ext taskExt
		if err := json.Unmarshal(row.Ext, &ext); err != nil {
			return err
		}
		if row.LazyllmTaskID != "" || NormalizeTaskStateForUI(ext.TaskState) == "SUCCESS" {
			continue
		}
		if effectiveProcessingLevel(dataset.ProcessingLevel) == ProcessingLevelStored {
			if err := CancelUnsubmittedMarketFiles(ctx, result.DatasetID, []string{row.ID}); err != nil {
				return err
			}
			continue
		}
		var count int64
		if err := store.LazyLLMDB().WithContext(ctx).Model(&readonlyorm.LazyLLMDocServiceTaskRow{}).Where("doc_id = ? AND kb_id = ?", row.DocID, result.DatasetID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			if err := setMarketSubmission(ctx, result.DatasetID, []string{row.ID}, "submitting"); err != nil {
				return err
			}
		}
	}
	return nil
}
