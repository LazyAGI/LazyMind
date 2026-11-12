package doc

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type ProcessingTaskStage struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}
type ProcessingTaskItem struct {
	ID           string                `json:"id"`
	DatasetID    string                `json:"dataset_id"`
	DatasetName  string                `json:"dataset_name"`
	DocumentID   string                `json:"document_id"`
	DocumentName string                `json:"document_name"`
	Level        string                `json:"level"`
	Status       string                `json:"status"`
	Stages       []ProcessingTaskStage `json:"stages"`
	Error        string                `json:"error,omitempty"`
	CreatedAt    time.Time             `json:"created_at"`
}
type ProcessingTasksResponse struct {
	UnfinishedCount int                  `json:"unfinished_count"`
	Items           []ProcessingTaskItem `json:"items"`
	Total           int                  `json:"total"`
	Page            int                  `json:"page"`
	PageSize        int                  `json:"page_size"`
	Counts          map[string]int       `json:"counts"`
}

// ListProcessingTasks presents the latest document attempt, not every historical
// retry. Read access is checked before loading names, states or error details.
// Reads never reconcile/write state and remain available without RAG installed.
func ListProcessingTasks(w http.ResponseWriter, r *http.Request) {
	user := store.UserID(r)
	if user == "" {
		common.ReplyErr(w, "missing X-User-Id", 401)
		return
	}
	db := store.DB().WithContext(r.Context())
	var datasets []orm.Dataset
	if err := db.Where("deleted_at IS NULL").Find(&datasets).Error; err != nil {
		common.ReplyErr(w, "query libraries failed", 500)
		return
	}
	items := []ProcessingTaskItem{}
	readable := make([]orm.Dataset, 0, len(datasets))
	for _, dataset := range datasets {
		if canAccessDataset(&dataset, user, acl.PermRead) {
			readable = append(readable, dataset)
		}
	}
	// One scan-source authorization request per poll, not one per library.
	readable = filterDatasetsByScanSourceAccess(r, readable, acl.PermissionDatasetRead)
	for _, dataset := range readable {
		var tasks []orm.Task
		err := db.Where("tasks.dataset_id = ? AND tasks.deleted_at IS NULL", dataset.ID).
			Where("task_type IN ?", []string{string(TaskTypeParse), string(TaskTypeParseUploaded), string(TaskTypeReparse)}).
			Where("EXISTS (SELECT 1 FROM documents d WHERE d.id=tasks.doc_id AND d.dataset_id=tasks.dataset_id AND d.deleted_at IS NULL AND (d.document_type IS NULL OR d.document_type <> 'FOLDER'))").
			Where(`NOT EXISTS (SELECT 1 FROM tasks newer WHERE newer.dataset_id=tasks.dataset_id AND newer.doc_id=tasks.doc_id AND newer.deleted_at IS NULL AND (newer.created_at>tasks.created_at OR (newer.created_at=tasks.created_at AND newer.id>tasks.id)))`).Find(&tasks).Error
		if err != nil {
			common.ReplyErr(w, "query processing tasks failed", 500)
			return
		}
		if len(tasks) == 0 {
			continue
		}
		ids := make([]string, 0, len(tasks))
		for _, task := range tasks {
			ids = append(ids, task.DocID)
		}
		snapshot, err := loadProcessingSnapshot(r.Context(), db, store.LazyLLMDB(), dataset.ID, "", ids)
		if err != nil {
			common.ReplyErr(w, "processing status temporarily unavailable", 503)
			return
		}
		for _, document := range snapshot.documents {
			task, ok := snapshot.tasks[document.ID]
			if !ok {
				continue
			}
			var ext taskExt
			_ = json.Unmarshal(task.Ext, &ext)
			if ext.ProcessingLevel == ProcessingLevelStored {
				continue
			}
			state, raw := snapshot.observe(document)
			// Original-only paper upload receipts are not parsing attempts.
			if dataset.Scene == "academic_papers" && task.TaskType == string(TaskTypeParseUploaded) && task.LazyllmTaskID == "" && ext.ProcessingLevel == "" && ext.TaskState == string(TaskStateSucceeded) && raw == "" {
				continue
			}
			// Clearing vectors changes current artifacts, not the completed attempt.
			if raw == "" {
				raw = ext.TaskState
				if task.LazyllmTaskID != "" {
					raw = snapshot.external[[2]string{task.LazyllmTaskID, document.LazyllmDocID}]
				}
			}
			level := ext.ProcessingLevel
			if level != ProcessingLevelParsed && level != ProcessingLevelChunked && level != ProcessingLevelIndexed {
				level = ProcessingLevelParsed
			}
			item := ProcessingTaskItem{ID: task.ID, DatasetID: dataset.ID, DatasetName: dataset.DisplayName, DocumentID: document.ID, DocumentName: document.DisplayName, Level: level, Status: processingTaskStatus(raw), CreatedAt: task.CreatedAt}
			for i, stage := range []struct{ name, status string }{{"parse", state.ParseStatus}, {"chunk", state.ChunkStatus}, {"index", state.IndexStatus}} {
				requested := i == 0 || (i == 1 && level != ProcessingLevelParsed) || (i == 2 && level == ProcessingLevelIndexed)
				status := stage.status
				// observe() can infer all requested stages from one aggregate
				// worker failure. That is not evidence that each stage ran.
				if status == "failed" && (item.Status == "failed" || item.Status == "canceled") {
					knownParseFailure := level == ProcessingLevelParsed && i == 0 && item.Status == "failed"
					knownIndexFailure := i == 2 && state.IndexArtifactRef == "core-task:"+task.ID && item.Status == "failed"
					if !knownParseFailure && !knownIndexFailure {
						status = "unknown"
					}
				}
				if stage.name == "index" && state.EmbeddingVersion == embeddingVersionReembedManual && status == "pending" {
					status = "cleared"
				}
				if !requested {
					status = "not_requested"
				} else if status == "running" {
					status = "submitted"
				} else if status == "" {
					status = "unknown"
				}
				item.Stages = append(item.Stages, ProcessingTaskStage{Name: stage.name, Status: status})
			}
			if item.Status == "failed" {
				detail := snapshot.diagnostics[[2]string{task.LazyllmTaskID, document.LazyllmDocID}]
				if task.LazyllmTaskID == "" {
					detail[1] = ext.ErrorMessage
				}
				if d := processingDiagnostic(state, task, "FAILED", detail[0], detail[1]); d != nil {
					item.Error = d.Message
				}
			}
			items = append(items, item)
		}
	}
	result := filterProcessingTasks(items, r.URL.Query().Get("status"), r.URL.Query().Get("level"), r.URL.Query().Get("q"), r.URL.Query().Get("page"))
	for _, item := range items {
		if item.Status == "pending" || item.Status == "running" {
			result.UnfinishedCount++
		}
	}
	common.ReplyJSON(w, result)
}
func processingTaskStatus(raw string) string {
	switch NormalizeTaskStateForUI(raw) {
	case "WAITING":
		return "pending"
	case "WORKING":
		return "running"
	case "SUCCESS":
		return "succeeded"
	case "FAILED":
		return "failed"
	case "CANCELED", "CANCELLED":
		return "canceled"
	default:
		return "unknown"
	}
}
func filterProcessingTasks(items []ProcessingTaskItem, status, level, keyword, pageRaw string) ProcessingTasksResponse {
	result := ProcessingTasksResponse{Items: []ProcessingTaskItem{}, Counts: map[string]int{"active": 0, "failed": 0, "succeeded": 0, "canceled": 0, "unknown": 0}, PageSize: 20}
	page, _ := strconv.Atoi(pageRaw)
	if page < 1 {
		page = 1
	}
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	filtered := []ProcessingTaskItem{}
	for _, item := range items {
		if level != "" && level != item.Level {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(item.DocumentName+" "+item.DatasetName), keyword) {
			continue
		}
		bucket := item.Status
		if bucket == "running" || bucket == "pending" {
			bucket = "active"
		}
		result.Counts[bucket]++
		if status == "" || status == "all" || status == bucket {
			filtered = append(filtered, item)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].ID > filtered[j].ID
		}
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	result.Total = len(filtered)
	maxPage := max(1, (len(filtered)+result.PageSize-1)/result.PageSize)
	if page > maxPage {
		page = maxPage
	}
	result.Page = page
	start := (page - 1) * result.PageSize
	end := min(start+result.PageSize, len(filtered))
	result.Items = filtered[start:end]
	return result
}
