package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
	"lazymind/core/settings"
	"lazymind/core/store"
	"net/http"
	"strings"
	"time"
)

func SubmitReparseTask(ctx context.Context, caller DatasetCatalogCaller, datasetID, taskID string) (StartTaskResult, error) {
	controls, err := settings.LoadFeatureControls(ctx, store.DB(), caller.UserID)
	if err != nil {
		return StartTaskResult{}, err
	}
	if !controls.DocumentParsingEnabled {
		return StartTaskResult{}, resourceErr("UNAVAILABLE", "document parsing is paused in settings")
	}
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://internal/", nil)
	r.Header.Set("X-User-Id", caller.UserID)
	r.Header.Set("X-Tenant-Id", caller.TenantID)
	var ds orm.Dataset
	if err = store.DB().WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).Take(&ds).Error; err != nil {
		return StartTaskResult{}, err
	}
	level := effectiveProcessingLevel(ds.ProcessingLevel)
	if level == ProcessingLevelStored {
		level = ProcessingLevelParsed
	}
	var task orm.Task
	if err = store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ?", taskID, datasetID).Take(&task).Error; err != nil {
		return StartTaskResult{}, err
	}
	var row orm.Document
	if err = store.DB().WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", task.DocID, datasetID).Take(&row).Error; err != nil {
		return StartTaskResult{}, err
	}
	if row.LazyllmDocID == "" {
		results, e := startParseTasksAtLevel(ctx, caller.UserID, datasetID, []string{taskID}, level)
		if len(results) > 0 {
			return results[0], e
		}
		return StartTaskResult{}, e
	}
	results, err := startReparseTasksInternalAtLevel(r, datasetID, []string{taskID}, level)
	if len(results) > 0 {
		return results[0], err
	}
	return StartTaskResult{}, err
}

func summarizeResourceResult(result *ResourceOperationResult) {
	result.Summary = ResourceOperationSummary{Total: len(result.Items)}
	result.ProcessingSummary = nil
	for i := range result.Items {
		item := &result.Items[i]
		if result.Action == "knowledge.document.upload" && item.Error != nil && item.ProcessingError == nil {
			stage, recovery := "import", "修正文件或服务异常后，只重新处理明确失败的文件。"
			if item.ResourceRef.DocumentID != "" {
				stage, recovery = "submission", "原件已保存；查询任务状态后按失败原因重试解析，不要重复上传。"
			}
			switch item.Error.Code {
			case "UNSUPPORTED_FORMAT":
				recovery = "转换为受支持的文件格式后，仅重新导入该失败项。"
			case "FILE_NOT_FOUND", "FILE_NOT_ACCESSIBLE":
				recovery = "重新指定当前用户可访问的文件后，仅重新导入该失败项。"
			case "UNCONFIRMED":
				recovery = "先核实回执及目标位置，不要重复上传。"
			}
			item.ProcessingError = &ProcessingDiagnostic{Stage: stage, Code: item.Error.Code, Message: SafeProcessingMessage(item.Error.Message), TaskID: item.TaskID, Recovery: recovery}
		}
		if item.ResourceRef.DocumentID != "" && item.ProcessingStatus != "" {
			if result.ProcessingSummary == nil {
				result.ProcessingSummary = map[string]int{}
			}
			result.ProcessingSummary[item.ProcessingStatus]++
		}
		switch item.ResourceStatus {
		case "failed":
			result.Summary.Failed++
		case "unconfirmed":
			result.Summary.Unconfirmed++
		case "skipped", "not_attempted":
			result.Summary.Skipped++
		default:
			result.Summary.Succeeded++
		}
	}
	result.Status = "succeeded"
	if result.Summary.Failed > 0 || result.Summary.Unconfirmed > 0 {
		result.Status = "partial"
		if result.Summary.Succeeded == 0 {
			result.Status = "failed"
		}
	}
}

// Reparse creates tasks and the execution receipt in one transaction. Dataset
// version locking serializes competing explicit retries; submission happens once after commit.
func (s *ResourceService) Reparse(ctx context.Context, op ResourceOperationContext, req ReparseDocumentsRequest) (ResourceOperationResult, error) {
	const action = "knowledge.document.reparse"
	if len(req.DocumentIDs) == 0 || len(req.DocumentIDs) > 20 || req.KnowledgeBaseID == "" {
		return ResourceOperationResult{}, resourceErr("INVALID_ARGUMENT", "select a library and one to twenty documents")
	}
	if saved, found, err := s.ReplayOperation(ctx, op, action, req); found || err != nil {
		return saved, err
	}
	if _, err := s.dataset(ctx, s.db, op, req.KnowledgeBaseID, acl.PermissionDatasetWrite); err != nil {
		return ResourceOperationResult{}, err
	}
	snapshots := map[string]processingSnapshot{}
	for _, id := range req.DocumentIDs {
		if strings.TrimSpace(id) == "" {
			return ResourceOperationResult{}, resourceErr("INVALID_ARGUMENT", "document ID required")
		}
		snap, err := loadProcessingSnapshot(ctx, s.db, s.lazyDB, req.KnowledgeBaseID, id)
		if err != nil {
			return ResourceOperationResult{}, err
		}
		snapshots[id] = snap
	}
	created := map[string]orm.Task{}
	result, err := s.mutate(ctx, op, action, req, func(tx *gorm.DB) (ResourceOperationResult, error) {
		var out ResourceOperationResult
		ds, err := s.dataset(ctx, tx, op, req.KnowledgeBaseID, acl.PermissionDatasetWrite)
		if err != nil {
			return out, err
		}
		if err = lockResourceDataset(tx, ds); err != nil {
			return out, err
		}
		seen := map[string]bool{}
		for i, id := range req.DocumentIDs {
			item := ResourceOperationItem{InputIndex: &i, ResourceRef: ResourceRef{Kind: "document", KnowledgeBaseID: ds.ID, DocumentID: id}, Name: id, ResourceStatus: "failed"}
			row, e := resourceDocument(tx, item.ResourceRef)
			if e != nil {
				var resource *ResourceError
				if !errors.As(e, &resource) {
					return out, e
				}
				item.Error = resource
				if resource.Code == "INVALID_ARGUMENT" {
					item.Error = resourceErr("INVALID_ARGUMENT", "folder cannot be reparsed")
				}
				out.Items = append(out.Items, item)
				continue
			}
			item.Name = row.DisplayName
			item.ParentID = row.PID
			if isFolderLikeDocument(row) {
				item.Error = resourceErr("INVALID_ARGUMENT", "folder cannot be reparsed")
				out.Items = append(out.Items, item)
				continue
			}
			if seen[id] {
				item.ResourceStatus = "skipped"
				out.Items = append(out.Items, item)
				continue
			}
			seen[id] = true
			snap := snapshots[id]
			state, raw := snap.observe(row)
			var latest orm.Task
			e = tx.Where("dataset_id = ? AND doc_id = ? AND deleted_at IS NULL", ds.ID, id).Order("created_at DESC, id DESC").Take(&latest).Error
			if e != nil && e != gorm.ErrRecordNotFound {
				return out, e
			}
			item.TaskID = latest.ID
			if len(snap.documents) == 0 || snap.documents[0].ResourceVersion != row.ResourceVersion {
				return out, resourceErr("CONFLICT", "document changed; query before retrying")
			}
			if latest.ID != snap.tasks[id].ID {
				// Another retry won while this request was observing the old failure.
				// Reuse its task; do not publish an unobserved external state.
				item.ResourceStatus = "skipped"
				item.ProcessingStatus = "unknown"
				var latestExt taskExt
				_ = json.Unmarshal(latest.Ext, &latestExt)
				if latest.LazyllmTaskID == "" && (NormalizeTaskStateForUI(latestExt.TaskState) == "WORKING" || NormalizeTaskStateForUI(latestExt.TaskState) == "WAITING") {
					item.ProcessingStatus = "pending"
				}
				out.Items = append(out.Items, item)
				continue
			}
			if state.ParseStatus == "running" || state.ChunkStatus == "running" || state.IndexStatus == "running" {
				item.ResourceStatus = "skipped"
				item.ProcessingStatus = "running"
				out.Items = append(out.Items, item)
				continue
			}
			if state.ParseStatus != "failed" && state.ChunkStatus != "failed" && state.IndexStatus != "failed" && NormalizeTaskStateForUI(raw) != "FAILED" {
				item.ResourceStatus = "skipped"
				item.ProcessingStatus = "unknown"
				if state.ParseStatus == "succeeded" {
					item.ProcessingStatus = "ready"
				}
				out.Items = append(out.Items, item)
				continue
			}
			task := existingDocumentTask(row, ds.ID, ds.KbID, parseDatasetAlgo(ds.Ext).AlgoID, op.Caller.UserID, op.Caller.UserName, newTaskID(), CreateTaskItem{}, string(TaskTypeReparse))
			var ext taskExt
			_ = json.Unmarshal(task.Ext, &ext)
			ext.TaskState = string(TaskStateCreating)
			ext.ProcessingRevision = ds.ProcessingRevision
			ext.ProcessingLevel = effectiveProcessingLevel(ds.ProcessingLevel)
			if ext.ProcessingLevel == ProcessingLevelStored {
				ext.ProcessingLevel = ProcessingLevelParsed
			}
			task.Ext = mustJSON(ext)
			if e = tx.Create(&task).Error; e != nil {
				return out, e
			}
			if e = tx.Model(&orm.DocumentProcessingState{}).Where("dataset_id = ? AND document_id = ?", ds.ID, id).Updates(map[string]any{"parse_status": "running", "chunk_status": "pending", "index_status": "pending", "parse_error_code": "", "parse_error_message": "", "chunk_error_code": "", "chunk_error_message": "", "index_error_code": "", "index_error_message": "", "updated_at": time.Now().UTC()}).Error; e != nil {
				return out, e
			}
			created[id] = task
			item.ResourceStatus = "updated"
			item.ProcessingStatus = "pending"
			item.TaskID = task.ID
			item.Error = nil
			out.Items = append(out.Items, item)
		}
		return out, nil
	})
	if err != nil {
		return result, err
	}
	for i := range result.Items {
		item := &result.Items[i]
		task, ok := created[item.ResourceRef.DocumentID]
		if !ok || item.ResourceStatus != "updated" {
			continue
		}
		submitted, e := s.submitReparse(ctx, op.Caller, req.KnowledgeBaseID, task.ID)
		item.ProcessingStatus = "submitted"
		if e != nil || submitted.Status != "STARTED" {
			msg := submitted.Message
			if e != nil {
				msg = e.Error()
			}
			if msg == "" {
				msg = "解析任务提交失败"
			}
			item.ProcessingStatus = "failed"
			item.ResourceStatus = "failed"
			item.Error = resourceErr("PROCESSING_FAILED", SafeProcessingMessage(msg))
			item.ProcessingError = &ProcessingDiagnostic{Stage: "submission", Code: "PROCESSING_FAILED", Message: item.Error.Message, TaskID: task.ID, Recovery: "检查解析服务后重新发起解析。"}
			var ext taskExt
			_ = json.Unmarshal(task.Ext, &ext)
			ext.TaskState = string(TaskStateFailed)
			ext.ErrorMessage = item.Error.Message
			if e = s.db.WithContext(ctx).Model(&orm.Task{}).Where("id = ? AND lazyllm_task_id = ?", task.ID, "").Update("ext", mustJSON(ext)).Error; e != nil {
				item.Error = resourceErr("UNCONFIRMED", fmt.Sprintf("submission state could not be saved for task %s", task.ID))
			}
		}
	}
	summarizeResourceResult(&result)
	s.updateReceiptResult(ctx, op, result)
	return result, nil
}
