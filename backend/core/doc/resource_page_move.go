package doc

import (
	"encoding/json"
	"net/http"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// The page keeps its task records, but execution uses the same transactional
// resource operation and receipt as MCP instead of the legacy transfer writer.
func startResourceMoveTasks(r *http.Request, datasetID string, taskIDs []string) ([]StartTaskResult, error) {
	service, base, err := resourcePageService(r)
	if err != nil {
		return nil, err
	}
	results := make([]StartTaskResult, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		result := StartTaskResult{TaskID: taskID, Status: "FAILED", SubmitStatus: "REJECTED"}
		var task orm.Task
		if err := store.DB().WithContext(r.Context()).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", taskID, datasetID).Take(&task).Error; err != nil {
			result.Message = "task not found"
			results = append(results, result)
			continue
		}
		op := base
		op.OperationKey = "page-move:" + task.ID
		var ext taskExt
		_ = json.Unmarshal(task.Ext, &ext)
		// Replay must precede looking up a source removed by an earlier attempt.
		var request MoveResourcesRequest
		var stored map[string]json.RawMessage
		_ = json.Unmarshal(task.Ext, &stored)
		if json.Unmarshal(stored["resource_move_request"], &request) != nil || len(request.Sources) == 0 {
			var row orm.Document
			if err := store.DB().WithContext(r.Context()).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", task.DocID, datasetID).Take(&row).Error; err != nil {
				result.Message = "source document not found"
				results = append(results, result)
				continue
			}
			destination := ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: firstNonEmpty(task.TargetDatasetID, datasetID)}
			if task.TargetPID != "" {
				destination.Kind, destination.FolderID = "folder", task.TargetPID
			}
			request = MoveResourcesRequest{Sources: []ResourceRef{resourceRef(row)}, Destination: destination}
			if stored == nil {
				stored = map[string]json.RawMessage{}
			}
			stored["resource_move_request"], _ = json.Marshal(request)
			if err := store.DB().WithContext(r.Context()).Model(&orm.Task{}).Where("id = ?", task.ID).Update("ext", mustJSON(stored)).Error; err != nil {
				return results, err
			}
		}
		out, moveErr := service.Move(r.Context(), op, request)
		if moveErr != nil {
			result.Message = moveErr.Error()
			ext.TaskState = string(TaskStateFailed)
		} else if out.Status == "succeeded" && len(out.Items) > 0 {
			result.Status, result.SubmitStatus, result.Message = "STARTED", "SUBMITTED", "moved"
			result.DocumentID = out.Items[0].ResourceRef.ID()
			ext.TaskState = string(TaskStateSucceeded)
		} else {
			result.Message = "move outcome requires verification"
			if len(out.Items) > 0 && out.Items[0].Error != nil {
				result.Message = out.Items[0].Error.Message
			}
			ext.TaskState = string(TaskStateFailed)
		}
		// Preserve the replay input alongside the task projection.
		projected := map[string]json.RawMessage{}
		_ = json.Unmarshal(mustJSON(ext), &projected)
		projected["resource_move_request"] = stored["resource_move_request"]
		projected["resource_operation_result"], _ = json.Marshal(out)
		if err := store.DB().WithContext(r.Context()).Model(&orm.Task{}).Where("id = ?", task.ID).Updates(map[string]any{"ext": mustJSON(projected), "updated_at": time.Now().UTC()}).Error; err != nil {
			result.Message += "; task status could not be saved"
		}
		results = append(results, result)
	}
	return results, nil
}
