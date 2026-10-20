package doc

import (
	"context"
	"encoding/json"
	"fmt"

	"lazymind/core/common/orm"
	"lazymind/core/settings"
	"lazymind/core/store"
)

// SubmitOriginalTask is the context-only entry into the existing parser pipeline.
// Resource creation has already committed, and failures never discard its IDs.
func SubmitOriginalTask(ctx context.Context, caller DatasetCatalogCaller, datasetID, taskID string) (StartTaskResult, error) {
	var dataset orm.Dataset
	if err := store.DB().WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).Take(&dataset).Error; err != nil {
		return StartTaskResult{}, err
	}
	var task orm.Task
	if err := store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskID, datasetID).Take(&task).Error; err != nil {
		return StartTaskResult{}, err
	}
	result := StartTaskResult{TaskID: task.ID, DocumentID: task.DocID, DisplayName: task.DisplayName, Status: "FAILED", SubmitStatus: "REJECTED"}
	if effectiveProcessingLevel(dataset.ProcessingLevel) == ProcessingLevelStored {
		var ext taskExt
		if err := json.Unmarshal(task.Ext, &ext); err != nil {
			return result, err
		}
		ext.TaskState = string(TaskStateSucceeded)
		if err := store.DB().WithContext(ctx).Model(&orm.Task{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskID, datasetID).Update("ext", mustJSON(ext)).Error; err != nil {
			return result, err
		}
		result.Status = "STARTED"
		result.SubmitStatus = "ACCEPTED"
		result.Message = "document stored without parsing"
		return result, nil
	}
	controls, err := settings.LoadFeatureControls(ctx, store.DB(), caller.UserID)
	if err != nil {
		return result, err
	}
	if !controls.DocumentParsingEnabled {
		return result, fmt.Errorf("document parsing is paused in settings")
	}
	results, err := startParseTasksAtLevel(ctx, caller.UserID, datasetID, []string{taskID}, "")
	if len(results) > 0 {
		return results[0], err
	}
	return result, err
}
