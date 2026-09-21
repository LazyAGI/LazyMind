package doc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/store"
)

var ErrMarketProcessing = errors.New("knowledge base task is running, retry later")

// SnapshotMarketTaskHistory freezes terminal results before a retry, update or
// uninstall can replace/delete the files. Both lifecycle owners use this path.
func SnapshotMarketTaskHistory(ctx context.Context, db *gorm.DB, install *orm.KnowledgeMarketInstall) error {
	if install == nil {
		return nil
	}
	var cfg struct {
		TaskIDs  []string            `json:"task_ids"`
		Failures []MarketFileFailure `json:"failures"`
	}
	if err := json.Unmarshal(install.Config, &cfg); err != nil {
		return err
	}
	current := MarketParseProgress(ctx, db, install.DatasetID, cfg.TaskIDs, cfg.Failures)
	if current.Total > 0 && (current.State == "parsing" || current.State == "pending" || current.State == "unknown") {
		return ErrMarketProcessing
	}
	var jobs []orm.AsyncJob
	if err := db.WithContext(ctx).Where("create_user_id = ? AND resource_id = ? AND job_type IN ?", install.UserID, install.MarketItemID, []string{MarketInstallJobType, MarketUpdateJobType}).Order("created_at DESC, id DESC").Find(&jobs).Error; err != nil {
		return err
	}
	for index, job := range jobs {
		if job.Status == "pending" || job.Status == "running" || (job.Status == "canceled" && job.LockUntil != nil && job.LockUntil.After(time.Now())) {
			return ErrMarketProcessing
		}
		var own struct {
			DatasetID string                   `json:"dataset_id"`
			TaskIDs   []string                 `json:"task_ids"`
			Failures  []MarketFileFailure      `json:"failures"`
			Parse     *MarketParseProgressInfo `json:"parse"`
			Reason    string                   `json:"reason"`
		}
		_ = json.Unmarshal(job.ResultJSON, &own)
		if own.Parse != nil {
			continue
		}
		parse := MarketParseProgressInfo{State: "done"}
		if len(own.TaskIDs) > 0 || len(own.Failures) > 0 {
			parse = MarketParseProgress(ctx, db, own.DatasetID, own.TaskIDs, own.Failures)
		} else if index == 0 && own.Reason == "" {
			parse, own.DatasetID = current, install.DatasetID
			if parse.Total == 0 {
				parse.State = "done"
			}
		}
		if parse.Total > 0 && (parse.State == "pending" || parse.State == "parsing" || parse.State == "unknown") {
			return ErrMarketProcessing
		}
		var result map[string]any
		_ = json.Unmarshal(job.ResultJSON, &result)
		if result == nil {
			result = map[string]any{}
		}
		result["parse"], result["dataset_id"] = parse, own.DatasetID
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if err := db.WithContext(ctx).Model(&orm.AsyncJob{}).Where("id = ?", job.ID).Update("result_json", json.RawMessage(data)).Error; err != nil {
			return err
		}
	}
	return nil
}

type MarketParseProgressInfo struct {
	State    string              `json:"state"` // pending | parsing | done | partial_failed | failed
	Total    int                 `json:"total"`
	Pending  int                 `json:"pending"`
	Parsing  int                 `json:"parsing"`
	Done     int                 `json:"done"`
	Failed   int                 `json:"failed"`
	Canceled int                 `json:"canceled"`
	Unknown  int                 `json:"unknown"`
	Failures []MarketFileFailure `json:"failures"`
}

// MarketParseProgress aggregates the parse tasks recorded in the install config
// (task_ids) so the frontend can show the parsing stage of the install chain.
// The authoritative state lives in the doc-service task table
// (lazyllm_doc_service_tasks), linked via tasks.lazyllm_task_id; ext.task_state
// is only consulted as a fallback for legacy or not-yet-submitted tasks.
func MarketParseProgress(ctx context.Context, db *gorm.DB, datasetID string, taskIDs []string, failures []MarketFileFailure) MarketParseProgressInfo {
	zero := MarketParseProgressInfo{State: "pending"}
	if len(taskIDs) == 0 && len(failures) == 0 {
		return zero
	}
	files, err := MarketTaskStates(ctx, db, datasetID, taskIDs)
	if err != nil {
		return MarketParseProgressInfo{State: "unknown", Total: len(taskIDs) + len(failures), Unknown: len(taskIDs) + len(failures)}
	}
	p := MarketParseProgressInfo{Failed: len(failures), Failures: append([]MarketFileFailure{}, failures...)}
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		row := file.Task
		seen[row.ID] = true
		switch parseGroup(file.State) {
		case "pending":
			p.Pending++
		case "parsing":
			p.Parsing++
		case "done":
			p.Done++
		case "canceled":
			p.Canceled++
		case "unknown":
			p.Unknown++
		case "failed":
			p.Failed++
			p.Failures = append(p.Failures, MarketFileFailure{TaskID: row.ID, Name: row.DisplayName, Reason: file.Reason})
		}
	}
	for _, id := range taskIDs {
		if !seen[id] {
			p.Failed++
			p.Failures = append(p.Failures, MarketFileFailure{TaskID: id, Name: id, Reason: "missing_task"})
		}
	}
	p.Total = len(taskIDs) + len(failures)
	switch {
	case p.Unknown > 0:
		p.State = "unknown"
	case p.Total > 0 && p.Pending+p.Parsing > 0:
		p.State = "parsing"
	case p.Total > 0 && p.Canceled == p.Total:
		p.State = "canceled"
	case p.Canceled > 0:
		p.State = "partial_canceled"
	case p.Total > 0 && p.Failed == p.Total:
		p.State = "failed"
	case p.Failed > 0:
		p.State = "partial_failed"
	case p.Total > 0 && p.Done == p.Total:
		p.State = "done"
	case p.Total > 0:
		p.State = "parsing"
	default:
		p.State = "pending"
	}
	return p
}

// MarketTaskState is a Core file with its authoritative execution state.
type MarketTaskState struct {
	Task          orm.Task
	State, Reason string
}

func MarketTaskStates(ctx context.Context, db *gorm.DB, datasetID string, ids []string) ([]MarketTaskState, error) {
	var rows []orm.Task
	if err := db.WithContext(ctx).Where("id IN ? AND dataset_id = ? AND deleted_at IS NULL", ids, datasetID).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID, byDoc, reasons, err := loadDocServiceTaskStatuses(ctx, datasetID, rows)
	if err != nil {
		return nil, err
	}
	files := make([]MarketTaskState, 0, len(rows))
	for _, row := range rows {
		state := byID[row.LazyllmTaskID]
		if state == "" {
			state = byDoc[row.DocID]
		}
		if state == "" {
			var ext taskExt
			_ = json.Unmarshal(row.Ext, &ext)
			state = ext.TaskState
			if ext.MarketSubmission == "submitting" && row.LazyllmTaskID == "" && (state == "" || state == "WAITING" || state == "FAILED") {
				state = "UNKNOWN"
			}
			if state == "" && row.LazyllmTaskID == "" {
				state = "FAILED"
			}
			if state == "" {
				state = "UNKNOWN"
			}
		}
		var attempt taskExt
		_ = json.Unmarshal(row.Ext, &attempt)
		if unconfirmedMarketAttempt(row, attempt) {
			state = "UNKNOWN"
		}
		files = append(files, MarketTaskState{Task: row, State: NormalizeTaskStateForUI(state), Reason: parseFailureReason(row.LazyllmTaskID, row.DocID, reasons)})
	}
	return files, nil
}

// loadDocServiceTaskStatuses reads the authoritative parse statuses from the
// doc-service task table (lazyllm_doc_service_tasks), keyed by lazyllm task id
// with a doc-id fallback for tasks whose lazyllm_task_id is empty or stale. A
// lookup failure degrades to empty maps so callers fall back to ext.task_state.
func loadDocServiceTaskStatuses(ctx context.Context, datasetID string, rows []orm.Task) (byTaskID, byDocID, reasons map[string]string, err error) {
	reasons = make(map[string]string)
	byTaskID = make(map[string]string)
	byDocID = make(map[string]string)
	taskIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		if id := strings.TrimSpace(row.LazyllmTaskID); id != "" {
			taskIDs = append(taskIDs, id)
		}
	}
	if len(taskIDs) > 0 {
		var extTasks []readonlyorm.LazyLLMDocServiceTaskRow
		if err := store.LazyLLMDB().WithContext(ctx).
			Table((readonlyorm.LazyLLMDocServiceTaskRow{}).TableName()).
			Where("task_id IN ?", taskIDs).
			Find(&extTasks).Error; err != nil {
			return nil, nil, nil, err
		} else {
			for _, task := range extTasks {
				if s := strings.TrimSpace(task.Status); s != "" {
					byTaskID[task.TaskID] = s
					reasons[task.TaskID] = marketParseFailureReason(task)
				}
			}
		}
	}

	missedDocIDs := make([]string, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if _, ok := byTaskID[row.LazyllmTaskID]; ok {
			continue
		}
		docID := strings.TrimSpace(row.DocID)
		if docID == "" || seen[docID] {
			continue
		}
		seen[docID] = true
		missedDocIDs = append(missedDocIDs, docID)
	}
	if len(missedDocIDs) > 0 {
		var extDocs []readonlyorm.LazyLLMDocServiceTaskRow
		if err := store.LazyLLMDB().WithContext(ctx).
			Table((readonlyorm.LazyLLMDocServiceTaskRow{}).TableName()).
			Where("doc_id IN ? AND kb_id = ?", missedDocIDs, datasetID).
			Order("updated_at DESC").
			Find(&extDocs).Error; err != nil {
			return nil, nil, nil, err
		} else {
			for _, task := range extDocs {
				if _, ok := byDocID[task.DocID]; ok {
					continue
				}
				if s := strings.TrimSpace(task.Status); s != "" {
					byDocID[task.DocID] = s
					reasons[task.DocID] = marketParseFailureReason(task)
				}
			}
		}
	}
	return byTaskID, byDocID, reasons, nil
}

func parseFailureReason(taskID, docID string, reasons map[string]string) string {
	if reason := reasons[taskID]; reason != "" {
		return reason
	}
	if reason := reasons[docID]; reason != "" {
		return reason
	}
	return "parse_failed"
}

func marketParseFailureReason(task readonlyorm.LazyLLMDocServiceTaskRow) string {
	var message string
	if task.ErrorCode != nil {
		message += *task.ErrorCode
	}
	if task.ErrorMsg != nil {
		message += " " + *task.ErrorMsg
	}
	message = strings.ToLower(message)
	if strings.Contains(message, "throttling") || strings.Contains(message, "rate limit") || strings.Contains(message, "ratequota") {
		return "rate_limited"
	}
	return "parse_failed"
}

// parseGroup maps one task state (doc-service status or the ext.task_state
// fallback) onto a progress bucket. Doc-service statuses are normalized with
// the same helper the local-upload task panel uses so the two surfaces agree.
func parseGroup(state string) string {
	switch NormalizeTaskStateForUI(state) {
	case "WORKING":
		return "parsing"
	case "SUCCESS":
		return "done"
	case "FAILED":
		return "failed"
	case "CANCELED":
		return "canceled"
	case "UNKNOWN":
		return "unknown"
	default:
		return "pending"
	}
}
