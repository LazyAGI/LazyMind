package resourceupdate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/resourcechange"
	skilldiff "lazymind/core/skillv2/diff"
	"lazymind/core/store"
)

type taskResponse struct {
	ID             string     `json:"id"`
	TaskType       string     `json:"task_type"`
	ResourceType   string     `json:"resource_type"`
	UserID         string     `json:"user_id"`
	ResourceID     string     `json:"resource_id"`
	TriggerType    string     `json:"trigger_type"`
	TriggerID      string     `json:"trigger_id"`
	Status         string     `json:"status"`
	ReviewResultID string     `json:"review_result_id,omitempty"`
	ResultID       string     `json:"result_id,omitempty"`
	ErrorCode      string     `json:"error_code,omitempty"`
	ErrorMessage   string     `json:"error_message,omitempty"`
	AttemptCount   int        `json:"attempt_count"`
	NextRunAt      time.Time  `json:"next_run_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

type skillReviewResultResponse struct {
	ID             string                        `json:"id"`
	SkillName      string                        `json:"skill_name"`
	Type           string                        `json:"type"`
	ReviewStatus   string                        `json:"review_status"`
	UserID         string                        `json:"userid"`
	RequestID      string                        `json:"requestid"`
	SkillContent   string                        `json:"skill_content,omitempty"`
	CurrentContent string                        `json:"current_content,omitempty"`
	Diff           string                        `json:"diff,omitempty"`
	DiffEntryLines []reviewDiffEntryLineResponse `json:"diffEntryLines,omitempty"`
	Summary        string                        `json:"summary"`
	Time           time.Time                     `json:"time"`
}

type reviewDiffEntryLineResponse struct {
	Type                    string `json:"type"`
	Text                    string `json:"text"`
	HTML                    string `json:"html,omitempty"`
	OldLine                 int    `json:"oldLine,omitempty"`
	NewLine                 int    `json:"newLine,omitempty"`
	DisplayNoNewLineWarning bool   `json:"displayNoNewLineWarning,omitempty"`
}

type skillReviewSummaryResponse struct {
	QualifiedSessionCount int           `json:"qualified_session_count"`
	UserTurnCount         int           `json:"user_turn_count"`
	ToolCallCount         int           `json:"tool_call_count"`
	MinUserTurns          int           `json:"min_user_turns"`
	MinToolTurns          int           `json:"min_tool_turns"`
	QuantityThreshold     int           `json:"quantity_threshold"`
	WindowStart           time.Time     `json:"window_start"`
	WindowEnd             time.Time     `json:"window_end"`
	RunningTask           *taskResponse `json:"running_task,omitempty"`
	RunningRequestID      string        `json:"running_requestid,omitempty"`
}

type skillReviewRunResponse struct {
	Task      taskResponse               `json:"task"`
	Summary   skillReviewSummaryResponse `json:"summary"`
	RequestID string                     `json:"requestid"`
}

type skillReviewTaskStatusResponse struct {
	Task        taskResponse `json:"task"`
	RequestID   string       `json:"requestid"`
	Status      string       `json:"status"`
	RunStatus   string       `json:"run_status,omitempty"`
	ResultCount int64        `json:"result_count"`
}

type skillReviewTaskListResponse struct {
	Items    []skillReviewTaskStatusResponse `json:"items"`
	Page     int                             `json:"page"`
	PageSize int                             `json:"page_size"`
	Total    int64                           `json:"total"`
}

type skillReviewStatsRow struct {
	ID         string `gorm:"column:id"`
	RequestID  string `gorm:"column:requestid"`
	UserID     string `gorm:"column:userid"`
	Status     string `gorm:"column:status"`
	StartedAt  string `gorm:"column:started_at"`
	DurationMS int    `gorm:"column:duration_ms"`
	Summary    string `gorm:"column:summary"`
}

type skillReviewStatsResponse struct {
	ID           string          `json:"id"`
	RequestID    string          `json:"requestid"`
	UserID       string          `json:"userid"`
	Status       string          `json:"status"`
	StartedAt    string          `json:"started_at"`
	DurationMS   int             `json:"duration_ms"`
	SkillCount   int64           `json:"skill_count"`
	CreatedCount int64           `json:"created_count"`
	UpdatedCount int64           `json:"updated_count"`
	SkippedCount int64           `json:"skipped_count"`
	FailedCount  int64           `json:"failed_count"`
	Summary      json.RawMessage `json:"summary"`
}

type memoryReviewResultResponse struct {
	ID             string          `json:"id"`
	UserID         string          `json:"user_id"`
	Target         string          `json:"target"`
	SessionID      string          `json:"session_id"`
	SourceContent  string          `json:"source_content"`
	Content        string          `json:"content"`
	CurrentContent string          `json:"current_content,omitempty"`
	Diff           string          `json:"diff,omitempty"`
	Operations     json.RawMessage `json:"operations,omitempty"`
	State          string          `json:"state"`
	ReviewStatus   string          `json:"review_status"`
	Time           time.Time       `json:"time"`
}

func ListTasks(w http.ResponseWriter, r *http.Request) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	page := parsePositiveQueryInt(r.URL.Query().Get("page"), 1, 0)
	pageSize := parsePositiveQueryInt(r.URL.Query().Get("page_size"), 20, 100)
	query := db.WithContext(r.Context()).Model(&orm.ResourceUpdateTask{}).Where("user_id = ?", userID)
	if status := strings.TrimSpace(r.URL.Query().Get("status")); status != "" {
		query = query.Where("status = ?", status)
	}
	if resourceType := strings.TrimSpace(r.URL.Query().Get("resource_type")); resourceType != "" {
		query = query.Where("resource_type = ?", resourceType)
	}
	if taskType := strings.TrimSpace(r.URL.Query().Get("task_type")); taskType != "" {
		query = query.Where("task_type = ?", taskType)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.ReplyErr(w, "query tasks failed", http.StatusInternalServerError)
		return
	}
	var rows []orm.ResourceUpdateTask
	if err := query.Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&rows).Error; err != nil {
		common.ReplyErr(w, "query tasks failed", http.StatusInternalServerError)
		return
	}
	items := make([]taskResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, taskToResponse(row))
	}
	common.ReplyOK(w, map[string]any{"items": items, "page": page, "page_size": pageSize, "total": total})
}

func GetTask(w http.ResponseWriter, r *http.Request) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	taskID := common.PathVar(r, "task_id")
	if strings.TrimSpace(taskID) == "" {
		common.ReplyErr(w, "missing task_id", http.StatusBadRequest)
		return
	}
	var row orm.ResourceUpdateTask
	if err := db.WithContext(r.Context()).Where("id = ? AND user_id = ?", taskID, userID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "task not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query task failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, taskToResponse(row))
}

func GetSkillReviewSummary(w http.ResponseWriter, r *http.Request) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	summary, err := buildManualSkillReviewSummary(r.Context(), db, userID, DefaultConfig(), time.Now().UTC())
	if err != nil {
		common.ReplyErr(w, "query skill review summary failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, summary)
}

func RunSkillReview(w http.ResponseWriter, r *http.Request) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	task, summary, err := createManualSkillReviewTask(r.Context(), db, userID, DefaultConfig(), time.Now().UTC())
	if err != nil {
		switch {
		case errors.Is(err, errReviewConflict), errors.Is(err, gorm.ErrDuplicatedKey):
			common.ReplyErr(w, err.Error(), http.StatusConflict)
		case errors.Is(err, errReviewInvalid):
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		default:
			common.ReplyErr(w, "run skill review failed", http.StatusInternalServerError)
		}
		return
	}
	common.ReplyOK(w, skillReviewRunResponse{
		Task:      taskToResponse(task),
		Summary:   summary,
		RequestID: summary.RunningRequestID,
	})
}

func ListSkillReviewTasks(w http.ResponseWriter, r *http.Request) {
	listSkillTasks(w, r, orm.ResourceUpdateTaskTypeGenerateReview, "skill review task")
}

func ListSkillOrganizeTasks(w http.ResponseWriter, r *http.Request) {
	listSkillTasks(w, r, orm.ResourceUpdateTaskTypeOrganizeSkill, "skill organize task")
}

func listSkillTasks(w http.ResponseWriter, r *http.Request, taskType, errorLabel string) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	page := parsePositiveQueryInt(r.URL.Query().Get("page"), 1, 0)
	pageSize := parsePositiveQueryInt(r.URL.Query().Get("page_size"), 20, 1000)
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	requestID := strings.TrimSpace(r.URL.Query().Get("requestid"))
	resp, err := buildSkillTaskList(r.Context(), db, userID, taskType, status, requestID, page, pageSize)
	if err != nil {
		mapReviewError(w, err, errorLabel)
		return
	}
	common.ReplyOK(w, resp)
}

func buildSkillReviewTaskList(ctx context.Context, db *gorm.DB, userID, status, requestID string, page, pageSize int) (skillReviewTaskListResponse, error) {
	return buildSkillTaskList(ctx, db, userID, orm.ResourceUpdateTaskTypeGenerateReview, status, requestID, page, pageSize)
}

func buildSkillOrganizeTaskList(ctx context.Context, db *gorm.DB, userID, status, requestID string, page, pageSize int) (skillReviewTaskListResponse, error) {
	return buildSkillTaskList(ctx, db, userID, orm.ResourceUpdateTaskTypeOrganizeSkill, status, requestID, page, pageSize)
}

func buildSkillTaskList(ctx context.Context, db *gorm.DB, userID, taskType, status, requestID string, page, pageSize int) (skillReviewTaskListResponse, error) {
	var tasks []orm.ResourceUpdateTask
	if err := db.WithContext(ctx).
		Where(
			"user_id = ? AND task_type = ? AND resource_type = ? AND trigger_type = ?",
			strings.TrimSpace(userID),
			taskType,
			orm.ResourceUpdateResourceTypeSkill,
			orm.ResourceUpdateTriggerTypeManual,
		).
		Order("created_at DESC").
		Find(&tasks).Error; err != nil {
		return skillReviewTaskListResponse{}, err
	}

	items := make([]skillReviewTaskStatusResponse, 0, len(tasks))
	for _, task := range tasks {
		item, err := buildSkillReviewTaskStatus(ctx, db, userID, task)
		if err != nil {
			return skillReviewTaskListResponse{}, err
		}
		if requestID != "" && item.RequestID != requestID {
			continue
		}
		if status == "" ||
			item.Status == status ||
			(status == orm.ResourceUpdateTaskStatusRunning &&
				(item.Status == orm.ResourceUpdateTaskStatusPending || item.Status == orm.ResourceUpdateTaskStatusRunning)) {
			items = append(items, item)
		}
	}

	total := len(items)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	return skillReviewTaskListResponse{
		Items:    items[start:end],
		Page:     page,
		PageSize: pageSize,
		Total:    int64(total),
	}, nil
}

func buildSkillReviewTaskStatus(ctx context.Context, db *gorm.DB, userID string, task orm.ResourceUpdateTask) (skillReviewTaskStatusResponse, error) {
	requestID := skillTaskRequestID(task)
	if strings.TrimSpace(requestID) == "" {
		return skillReviewTaskStatusResponse{}, errReviewInvalid
	}

	resp := skillReviewTaskStatusResponse{
		Task:      taskToResponse(task),
		RequestID: requestID,
		Status:    task.Status,
	}
	stats, found, err := findSkillReviewTaskStats(ctx, db, userID, task, requestID)
	if err != nil {
		return skillReviewTaskStatusResponse{}, err
	}
	if !found {
		if task.Status == orm.ResourceUpdateTaskStatusDone {
			resp.Status = orm.ResourceUpdateTaskStatusRunning
		}
		return resp, nil
	}

	resp.RunStatus = stats.Status
	resp.Status = skillReviewTaskStatusFromRunStats(stats.Status)
	resp.ResultCount = skillReviewStatsToResponse(stats).SkillCount
	return resp, nil
}

func findSkillReviewTaskStats(ctx context.Context, db *gorm.DB, userID string, task orm.ResourceUpdateTask, requestID string) (skillReviewStatsRow, bool, error) {
	query := db.WithContext(ctx).
		Table("skill_review_stats").
		Select("id, requestid, userid, status, started_at, duration_ms, summary").
		Where("userid = ?", strings.TrimSpace(userID))
	if resultID := strings.TrimSpace(task.ResultID); resultID != "" {
		var row skillReviewStatsRow
		err := query.Where("id = ?", resultID).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return skillReviewStatsRow{}, false, nil
		}
		if err != nil {
			return skillReviewStatsRow{}, false, err
		}
		return row, true, nil
	}

	var rows []skillReviewStatsRow
	if err := query.
		Where("requestid = ?", strings.TrimSpace(requestID)).
		Order("started_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return skillReviewStatsRow{}, false, err
	}
	if len(rows) == 0 {
		return skillReviewStatsRow{}, false, nil
	}
	for _, row := range rows {
		if strings.TrimSpace(row.Status) == orm.SkillReviewStatsStatusCompleted {
			return row, true, nil
		}
	}
	for _, row := range rows {
		if strings.TrimSpace(row.Status) == orm.SkillReviewStatsStatusSkipped {
			return row, true, nil
		}
	}
	return rows[len(rows)-1], true, nil
}

func skillReviewTaskStatusFromRunStats(runStatus string) string {
	switch strings.TrimSpace(runStatus) {
	case orm.SkillReviewStatsStatusCompleted:
		return orm.ResourceUpdateTaskStatusDone
	case orm.SkillReviewStatsStatusFailed:
		return orm.ResourceUpdateTaskStatusFailed
	case orm.SkillReviewStatsStatusSkipped:
		return orm.ResourceUpdateTaskStatusSkipped
	default:
		return orm.ResourceUpdateTaskStatusRunning
	}
}

type skillReviewStatsCounts struct {
	SkillCount   int64
	CreatedCount int64
	UpdatedCount int64
	SkippedCount int64
	FailedCount  int64
}

func skillReviewStatsToResponse(row skillReviewStatsRow) skillReviewStatsResponse {
	summary, counts := parseSkillReviewStatsSummary(row.Summary)
	return skillReviewStatsResponse{
		ID:           strings.TrimSpace(row.ID),
		RequestID:    strings.TrimSpace(row.RequestID),
		UserID:       strings.TrimSpace(row.UserID),
		Status:       strings.TrimSpace(row.Status),
		StartedAt:    strings.TrimSpace(row.StartedAt),
		DurationMS:   row.DurationMS,
		SkillCount:   counts.SkillCount,
		CreatedCount: counts.CreatedCount,
		UpdatedCount: counts.UpdatedCount,
		SkippedCount: counts.SkippedCount,
		FailedCount:  counts.FailedCount,
		Summary:      summary,
	}
}

func parseSkillReviewStatsSummary(raw string) (json.RawMessage, skillReviewStatsCounts) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return json.RawMessage(`{}`), skillReviewStatsCounts{}
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil || value == nil {
		return json.RawMessage(`{}`), skillReviewStatsCounts{}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		encoded = []byte(`{}`)
	}
	summary, _ := value.(map[string]any)
	return json.RawMessage(encoded), skillReviewStatsCountsFromSummary(summary)
}

func skillReviewStatsCountsFromSummary(summary map[string]any) skillReviewStatsCounts {
	counts := skillReviewStatsCounts{
		SkillCount:   summaryInt64(summary, "skill_count", "skills_count", "result_count", "results_count", "total_count", "total"),
		CreatedCount: summaryInt64(summary, "created_count", "create_count", "new_count", "new_skill_count"),
		UpdatedCount: summaryInt64(summary, "updated_count", "update_count", "modified_count", "patch_count", "patched_count"),
		SkippedCount: summaryInt64(summary, "skipped_count", "skip_count"),
		FailedCount:  summaryInt64(summary, "failed_count", "fail_count", "error_count"),
	}
	if counts.CreatedCount == 0 {
		counts.CreatedCount = summaryArrayLen(summary, "created_skills", "new_skills", "create_skills")
	}
	if counts.UpdatedCount == 0 {
		counts.UpdatedCount = summaryArrayLen(summary, "updated_skills", "modified_skills", "patch_skills")
	}
	if counts.SkippedCount == 0 {
		counts.SkippedCount = summaryArrayLen(summary, "skipped_skills")
	}
	if counts.FailedCount == 0 {
		counts.FailedCount = summaryArrayLen(summary, "failed_skills")
	}
	if apply := summaryObject(summary, "apply"); apply != nil {
		if counts.SkillCount == 0 {
			counts.SkillCount = summaryInt64(apply, "output_count")
		}
		if counts.CreatedCount == 0 && counts.UpdatedCount == 0 {
			for _, item := range summaryArray(apply, "applied") {
				entry, _ := item.(map[string]any)
				switch strings.TrimSpace(summaryString(entry, "type")) {
				case "new":
					counts.CreatedCount++
				case "patch":
					counts.UpdatedCount++
				}
			}
		}
	}
	if nested := summaryObject(summary, "counts"); nested != nil && counts.SkillCount == 0 {
		counts.SkillCount = summaryInt64(nested, "skill_count", "result_count", "resolution", "candidate", "draft")
	}
	if counts.SkillCount == 0 {
		counts.SkillCount = counts.CreatedCount + counts.UpdatedCount + counts.SkippedCount + counts.FailedCount
	}
	if counts.SkillCount == 0 {
		counts.SkillCount = summaryArrayLen(summary, "skills", "items", "results", "review_results", "skill_results")
	}
	return counts
}

func summaryObject(summary map[string]any, key string) map[string]any {
	if summary == nil {
		return nil
	}
	value, _ := summary[key].(map[string]any)
	return value
}

func summaryArray(summary map[string]any, key string) []any {
	if summary == nil {
		return nil
	}
	value, _ := summary[key].([]any)
	return value
}

func summaryString(summary map[string]any, key string) string {
	if summary == nil {
		return ""
	}
	value, _ := summary[key].(string)
	return value
}

func summaryInt64(summary map[string]any, keys ...string) int64 {
	if summary == nil {
		return 0
	}
	for _, key := range keys {
		value, ok := summary[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case json.Number:
			if n, err := typed.Int64(); err == nil {
				return n
			}
		case float64:
			return int64(typed)
		case int:
			return int64(typed)
		case int64:
			return typed
		case string:
			if n, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}

func summaryArrayLen(summary map[string]any, keys ...string) int64 {
	if summary == nil {
		return 0
	}
	for _, key := range keys {
		value, ok := summary[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case []any:
			return int64(len(typed))
		case []map[string]any:
			return int64(len(typed))
		}
	}
	return 0
}

func AcceptSkillReviewResult(w http.ResponseWriter, r *http.Request) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	userName := strings.TrimSpace(store.UserName(r))
	resultID := common.PathVar(r, "review_result_id")
	row, err := acceptSkillReviewResult(r.Context(), db, userID, userName, resultID)
	if err != nil {
		mapReviewError(w, err, "accept skill review result")
		return
	}
	resourceUpdateInfo(logEventReviewAccepted).
		Str("resource_type", orm.ResourceUpdateResourceTypeSkill).
		Str("review_result_id", row.ID).
		Str("user_id", row.UserID).
		Str("review_type", row.Type).
		Msg(logEventReviewAccepted)
	common.ReplyOK(w, skillResultToResponse(row))
}

func RejectSkillReviewResult(w http.ResponseWriter, r *http.Request) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	resultID := common.PathVar(r, "review_result_id")
	row, err := rejectSkillReviewResult(r.Context(), db, userID, resultID)
	if err != nil {
		mapReviewError(w, err, "reject skill review result")
		return
	}
	resourceUpdateInfo(logEventReviewRejected).
		Str("resource_type", orm.ResourceUpdateResourceTypeSkill).
		Str("review_result_id", row.ID).
		Str("user_id", row.UserID).
		Str("review_type", row.Type).
		Msg(logEventReviewRejected)
	common.ReplyOK(w, skillResultToResponse(row))
}

func ListMemoryReviewResults(w http.ResponseWriter, r *http.Request) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	page := parsePositiveQueryInt(r.URL.Query().Get("page"), 1, 0)
	pageSize := parsePositiveQueryInt(r.URL.Query().Get("page_size"), 20, 100)
	query := memoryResultSelect(db.WithContext(r.Context())).Where("user_id = ?", userID)
	if status := strings.TrimSpace(r.URL.Query().Get("review_status")); status != "" {
		query = query.Where("review_status = ?", status)
	}
	if target := strings.TrimSpace(r.URL.Query().Get("target")); target != "" {
		query = query.Where("target = ?", target)
	}
	var rows []MemoryReviewResult
	if err := query.Order("time DESC, id DESC").
		Find(&rows).Error; err != nil {
		common.ReplyErr(w, "query memory review results failed", http.StatusInternalServerError)
		return
	}
	mappedRows := make([]MemoryReviewResult, 0, len(rows))
	for _, row := range rows {
		if !memoryReviewResultMapped(r.Context(), db, row) {
			continue
		}
		mappedRows = append(mappedRows, row)
	}
	total := len(mappedRows)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	items := make([]memoryReviewResultResponse, 0, end-start)
	for _, row := range mappedRows[start:end] {
		items = append(items, memoryResultToResponse(row))
	}
	common.ReplyOK(w, map[string]any{"items": items, "page": page, "page_size": pageSize, "total": total})
}

func GetMemoryReviewResult(w http.ResponseWriter, r *http.Request) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	resultID := common.PathVar(r, "review_result_id")
	var row MemoryReviewResult
	err := memoryResultSelect(db.WithContext(r.Context())).Where("id = ? AND user_id = ?", resultID, userID).Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "memory review result not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query memory review result failed", http.StatusInternalServerError)
		return
	}
	if !memoryReviewResultMapped(r.Context(), db, row) {
		common.ReplyErr(w, "memory review result not found", http.StatusNotFound)
		return
	}
	resp, err := memoryResultDetailResponse(r.Context(), db, row)
	if err != nil {
		mapReviewError(w, err, "query memory review result")
		return
	}
	common.ReplyOK(w, resp)
}

func AcceptMemoryReviewResult(w http.ResponseWriter, r *http.Request) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	resultID := common.PathVar(r, "review_result_id")
	row, err := acceptMemoryReviewResult(r.Context(), db, userID, resultID)
	if err != nil {
		mapReviewError(w, err, "accept memory review result")
		return
	}
	resourceUpdateInfo(logEventReviewAccepted).
		Str("resource_type", normalizeReviewTarget(row.Target)).
		Str("review_result_id", row.ID).
		Str("user_id", row.UserID).
		Str("target", row.Target).
		Msg(logEventReviewAccepted)
	common.ReplyOK(w, memoryResultToResponse(row))
}

func RejectMemoryReviewResult(w http.ResponseWriter, r *http.Request) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	resultID := common.PathVar(r, "review_result_id")
	row, err := rejectMemoryReviewResult(r.Context(), db, userID, resultID)
	if err != nil {
		mapReviewError(w, err, "reject memory review result")
		return
	}
	resourceUpdateInfo(logEventReviewRejected).
		Str("resource_type", normalizeReviewTarget(row.Target)).
		Str("review_result_id", row.ID).
		Str("user_id", row.UserID).
		Str("target", row.Target).
		Msg(logEventReviewRejected)
	common.ReplyOK(w, memoryResultToResponse(row))
}

func acceptSkillReviewResult(ctx context.Context, db *gorm.DB, userID, userName, resultID string) (SkillReviewResult, error) {
	var out SkillReviewResult
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := lockSkillReviewResultForUser(ctx, tx, resultID, userID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(row.ReviewStatus) != reviewStatusPending {
			return errReviewConflict
		}
		switch strings.TrimSpace(row.Type) {
		case skillReviewTypePatch:
			v2Resource, err := mapSkillPatchResultToV2Resource(ctx, withUpdateLock(tx), row)
			if err == nil {
				if err := applySkillV2PatchResult(ctx, tx, row, v2Resource); err != nil {
					return err
				}
				break
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return errReviewNotFound
		case skillReviewTypeNew:
			if _, err := createSkillV2FromNewResult(ctx, tx, row, userName); err != nil {
				return err
			}
			if err := updateSkillReviewStatus(ctx, tx, row.ID, reviewStatusAccepted); err != nil {
				return err
			}
		default:
			return errReviewInvalid
		}
		row.ReviewStatus = reviewStatusAccepted
		out = row
		return nil
	})
	return out, err
}

func AcceptSkillReviewResultByID(ctx context.Context, db *gorm.DB, userID, userName, resultID string) (SkillReviewResult, error) {
	return acceptSkillReviewResult(ctx, db, userID, userName, resultID)
}

func rejectSkillReviewResult(ctx context.Context, db *gorm.DB, userID, resultID string) (SkillReviewResult, error) {
	var out SkillReviewResult
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := lockSkillReviewResultForUser(ctx, tx, resultID, userID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(row.ReviewStatus) != reviewStatusPending {
			return errReviewConflict
		}
		if err := updateSkillReviewStatus(ctx, tx, row.ID, reviewStatusRejected); err != nil {
			return err
		}
		row.ReviewStatus = reviewStatusRejected
		out = row
		return nil
	})
	return out, err
}

func RejectSkillReviewResultByID(ctx context.Context, db *gorm.DB, userID, resultID string) (SkillReviewResult, error) {
	return rejectSkillReviewResult(ctx, db, userID, resultID)
}

func acceptMemoryReviewResult(ctx context.Context, db *gorm.DB, userID, resultID string) (MemoryReviewResult, error) {
	now := time.Now().UTC()
	var out MemoryReviewResult
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := lockMemoryReviewResultForUser(ctx, tx, resultID, userID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(row.ReviewStatus) != reviewStatusPending {
			return errReviewConflict
		}
		if strings.TrimSpace(row.State) != memoryReviewStateSuccess {
			return errReviewInvalid
		}
		switch normalizeReviewTarget(row.Target) {
		case orm.ResourceUpdateResourceTypeMemory:
			resource, err := mapMemoryReviewResultToPersonalResource(withUpdateLock(tx).WithContext(ctx), orm.ResourceUpdateResourceTypeMemory, row)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errReviewNotFound
				}
				return err
			}
			if err := applyPersonalResourceReviewResult(ctx, tx, orm.ResourceUpdateResourceTypeMemory, row, resource, now, false, resourcechange.Source{
				ChangeSource:  resourcechange.ChangeSourceReviewAccept,
				SourceRefType: resourcechange.SourceRefTypeMemoryReview,
				SourceRefID:   row.ID,
				ChangedAt:     now,
			}); err != nil {
				return err
			}
		case orm.ResourceUpdateResourceTypeUserPreference:
			resource, err := mapMemoryReviewResultToPersonalResource(withUpdateLock(tx).WithContext(ctx), orm.ResourceUpdateResourceTypeUserPreference, row)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errReviewNotFound
				}
				return err
			}
			if err := applyPersonalResourceReviewResult(ctx, tx, orm.ResourceUpdateResourceTypeUserPreference, row, resource, now, false, resourcechange.Source{
				ChangeSource:  resourcechange.ChangeSourceReviewAccept,
				SourceRefType: resourcechange.SourceRefTypeMemoryReview,
				SourceRefID:   row.ID,
				ChangedAt:     now,
			}); err != nil {
				return err
			}
		default:
			return errReviewInvalid
		}
		row.ReviewStatus = reviewStatusAccepted
		out = row
		return nil
	})
	return out, err
}

func AcceptMemoryReviewResultByID(ctx context.Context, db *gorm.DB, userID, resultID string) (MemoryReviewResult, error) {
	return acceptMemoryReviewResult(ctx, db, userID, resultID)
}

func rejectMemoryReviewResult(ctx context.Context, db *gorm.DB, userID, resultID string) (MemoryReviewResult, error) {
	var out MemoryReviewResult
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := lockMemoryReviewResultForUser(ctx, tx, resultID, userID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(row.ReviewStatus) != reviewStatusPending {
			return errReviewConflict
		}
		if err := updateMemoryReviewStatus(ctx, tx, row.ID, reviewStatusRejected); err != nil {
			return err
		}
		row.ReviewStatus = reviewStatusRejected
		out = row
		return nil
	})
	return out, err
}

func RejectMemoryReviewResultByID(ctx context.Context, db *gorm.DB, userID, resultID string) (MemoryReviewResult, error) {
	return rejectMemoryReviewResult(ctx, db, userID, resultID)
}

func LatestPendingSkillPatchReviewResult(ctx context.Context, db *gorm.DB, userID, skillName string) (SkillReviewResult, error) {
	var rows []SkillReviewResult
	err := skillResultSelect(db.WithContext(ctx)).
		Where("userid = ? AND type = ? AND review_status = ? AND skill_name = ?",
			strings.TrimSpace(userID),
			skillReviewTypePatch,
			reviewStatusPending,
			strings.TrimSpace(skillName),
		).
		Order("time DESC, id DESC").
		Find(&rows).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return SkillReviewResult{}, errReviewNotFound
	}
	if err != nil {
		return SkillReviewResult{}, err
	}
	for _, row := range rows {
		if _, err := mapSkillPatchResultToV2Resource(ctx, db, row); err == nil {
			return row, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return SkillReviewResult{}, err
		}
	}
	return SkillReviewResult{}, errReviewNotFound
}

func LatestPendingMemoryReviewResult(ctx context.Context, db *gorm.DB, userID, target string) (MemoryReviewResult, error) {
	var rows []MemoryReviewResult
	target = normalizeReviewTarget(target)
	err := memoryResultSelect(db.WithContext(ctx)).
		Where("user_id = ? AND target = ? AND state = ? AND review_status = ?",
			strings.TrimSpace(userID),
			target,
			memoryReviewStateSuccess,
			reviewStatusPending,
		).
		Order("time DESC, id DESC").
		Find(&rows).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MemoryReviewResult{}, errReviewNotFound
	}
	if err != nil {
		return MemoryReviewResult{}, err
	}
	for _, row := range rows {
		var err error
		switch target {
		case orm.ResourceUpdateResourceTypeMemory:
			_, err = mapMemoryReviewResultToPersonalResource(db.WithContext(ctx), orm.ResourceUpdateResourceTypeMemory, row)
		case orm.ResourceUpdateResourceTypeUserPreference:
			_, err = mapMemoryReviewResultToPersonalResource(db.WithContext(ctx), orm.ResourceUpdateResourceTypeUserPreference, row)
		default:
			return MemoryReviewResult{}, errReviewInvalid
		}
		if err == nil {
			return row, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return MemoryReviewResult{}, err
		}
	}
	return MemoryReviewResult{}, errReviewNotFound
}

func ReplyReviewError(w http.ResponseWriter, err error, fallback string) {
	mapReviewError(w, err, fallback)
}

func IsReviewNotFound(err error) bool {
	return errors.Is(err, errReviewNotFound) || errors.Is(err, gorm.ErrRecordNotFound)
}

func requestDBAndUser(w http.ResponseWriter, r *http.Request) (*gorm.DB, string, bool) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return nil, "", false
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return nil, "", false
	}
	return db, userID, true
}

func taskToResponse(row orm.ResourceUpdateTask) taskResponse {
	return taskResponse{
		ID:             row.ID,
		TaskType:       row.TaskType,
		ResourceType:   row.ResourceType,
		UserID:         row.UserID,
		ResourceID:     row.ResourceID,
		TriggerType:    row.TriggerType,
		TriggerID:      row.TriggerID,
		Status:         row.Status,
		ReviewResultID: row.ReviewResultID,
		ResultID:       row.ResultID,
		ErrorCode:      row.ErrorCode,
		ErrorMessage:   row.ErrorMessage,
		AttemptCount:   row.AttemptCount,
		NextRunAt:      row.NextRunAt,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		StartedAt:      row.StartedAt,
		FinishedAt:     row.FinishedAt,
	}
}

func skillResultToResponse(row SkillReviewResult) skillReviewResultResponse {
	return skillReviewResultResponse{
		ID:             row.ID,
		SkillName:      row.SkillName,
		Type:           row.Type,
		ReviewStatus:   row.ReviewStatus,
		UserID:         row.UserID,
		RequestID:      row.RequestID,
		SkillContent:   row.SkillContent,
		CurrentContent: "",
		Summary:        row.Summary,
		Time:           row.Time,
	}
}

func skillResultDetailResponse(ctx context.Context, db *gorm.DB, row SkillReviewResult) (skillReviewResultResponse, error) {
	resp := skillResultToResponse(row)
	if strings.TrimSpace(row.Type) != skillReviewTypePatch {
		if strings.TrimSpace(row.SkillContent) != "" {
			diff, err := evolution.BuildContentDiff("", row.SkillContent)
			if err != nil {
				return skillReviewResultResponse{}, err
			}
			resp.Diff = diff
			resp.DiffEntryLines = buildReviewDiffEntryLines(ctx, "", row.SkillContent)
		}
		return resp, nil
	}
	if current, ok, err := skillV2CurrentContent(ctx, db, row.UserID, row.SkillName); err != nil {
		return skillReviewResultResponse{}, err
	} else if ok {
		resp.CurrentContent = current
		diff, err := evolution.BuildContentDiff(current, row.SkillContent)
		if err != nil {
			return skillReviewResultResponse{}, err
		}
		resp.Diff = diff
		resp.DiffEntryLines = buildReviewDiffEntryLines(ctx, current, row.SkillContent)
		return resp, nil
	}
	return skillReviewResultResponse{}, errReviewNotFound
}

func buildReviewDiffEntryLines(ctx context.Context, currentContent, draftContent string) []reviewDiffEntryLineResponse {
	oldFS := reviewSingleFileFS{content: currentContent, exists: strings.TrimSpace(currentContent) != ""}
	newFS := reviewSingleFileFS{content: draftContent, exists: strings.TrimSpace(draftContent) != ""}
	diff, err := skilldiff.NewService(skilldiff.ServiceDeps{}).CompareFile(ctx, oldFS, newFS, skilldiff.DiffOptions{Path: "SKILL.md"})
	if err != nil {
		return nil
	}
	out := make([]reviewDiffEntryLineResponse, 0, len(diff.DiffEntryLines))
	for _, line := range diff.DiffEntryLines {
		out = append(out, reviewDiffEntryLineResponse{
			Type:                    line.Type,
			Text:                    line.Text,
			HTML:                    line.HTML,
			OldLine:                 line.OldLine,
			NewLine:                 line.NewLine,
			DisplayNoNewLineWarning: line.DisplayNoNewLineWarning,
		})
	}
	return out
}

type reviewSingleFileFS struct {
	content string
	exists  bool
}

func (fs reviewSingleFileFS) ListAll(context.Context) ([]skilldiff.EntryInfo, error) {
	if !fs.exists {
		return nil, nil
	}
	return []skilldiff.EntryInfo{{
		Path:     "SKILL.md",
		Type:     "file",
		BlobHash: evolution.HashContent(fs.content),
		Binary:   false,
		FileType: "markdown",
		Size:     int64(len([]byte(fs.content))),
	}}, nil
}

func (fs reviewSingleFileFS) ReadFile(context.Context, string) ([]byte, error) {
	return []byte(fs.content), nil
}

func memoryResultToResponse(row MemoryReviewResult) memoryReviewResultResponse {
	return memoryReviewResultResponse{
		ID:             row.ID,
		UserID:         row.UserID,
		Target:         row.Target,
		SessionID:      row.SessionID,
		SourceContent:  row.SourceContent,
		Content:        row.Content,
		CurrentContent: "",
		Diff:           "",
		Operations:     row.Operations,
		State:          row.State,
		ReviewStatus:   row.ReviewStatus,
		Time:           row.Time,
	}
}

func memoryResultDetailResponse(ctx context.Context, db *gorm.DB, row MemoryReviewResult) (memoryReviewResultResponse, error) {
	resp := memoryResultToResponse(row)
	var currentContent string
	switch normalizeReviewTarget(row.Target) {
	case orm.ResourceUpdateResourceTypeMemory:
		resource, err := mapMemoryReviewResultToPersonalResource(db.WithContext(ctx), orm.ResourceUpdateResourceTypeMemory, row)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return memoryReviewResultResponse{}, errReviewNotFound
			}
			return memoryReviewResultResponse{}, err
		}
		content, _, err := personalResourceHeadContent(ctx, db, resource)
		if err != nil {
			return memoryReviewResultResponse{}, err
		}
		currentContent = content
	case orm.ResourceUpdateResourceTypeUserPreference:
		resource, err := mapMemoryReviewResultToPersonalResource(db.WithContext(ctx), orm.ResourceUpdateResourceTypeUserPreference, row)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return memoryReviewResultResponse{}, errReviewNotFound
			}
			return memoryReviewResultResponse{}, err
		}
		content, _, err := personalResourceHeadContent(ctx, db, resource)
		if err != nil {
			return memoryReviewResultResponse{}, err
		}
		currentContent = content
	default:
		return memoryReviewResultResponse{}, errReviewInvalid
	}
	resp.CurrentContent = currentContent
	diff, err := evolution.BuildContentDiff(currentContent, row.Content)
	if err != nil {
		return memoryReviewResultResponse{}, err
	}
	resp.Diff = diff
	return resp, nil
}

func memoryReviewResultMapped(ctx context.Context, db *gorm.DB, row MemoryReviewResult) bool {
	var err error
	switch normalizeReviewTarget(row.Target) {
	case orm.ResourceUpdateResourceTypeMemory:
		_, err = mapMemoryReviewResultToPersonalResource(db.WithContext(ctx), orm.ResourceUpdateResourceTypeMemory, row)
	case orm.ResourceUpdateResourceTypeUserPreference:
		_, err = mapMemoryReviewResultToPersonalResource(db.WithContext(ctx), orm.ResourceUpdateResourceTypeUserPreference, row)
	default:
		return false
	}
	return err == nil
}

func lockSkillReviewResultForUser(ctx context.Context, tx *gorm.DB, id, userID string) (SkillReviewResult, error) {
	var row SkillReviewResult
	err := skillResultSelect(withUpdateLock(tx).WithContext(ctx)).
		Where("id = ? AND userid = ?", strings.TrimSpace(id), strings.TrimSpace(userID)).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return SkillReviewResult{}, errReviewNotFound
	}
	return row, err
}

func lockMemoryReviewResultForUser(ctx context.Context, tx *gorm.DB, id, userID string) (MemoryReviewResult, error) {
	var row MemoryReviewResult
	err := memoryResultSelect(withUpdateLock(tx).WithContext(ctx)).
		Where("id = ? AND user_id = ?", strings.TrimSpace(id), strings.TrimSpace(userID)).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MemoryReviewResult{}, errReviewNotFound
	}
	return row, err
}

func updateSkillReviewStatus(ctx context.Context, tx *gorm.DB, id, status string) error {
	result := tx.WithContext(ctx).
		Table("skill_review_results").
		Where("id = ? AND review_status = ?", id, reviewStatusPending).
		Update("review_status", status)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errReviewConflict
	}
	return nil
}

func updateMemoryReviewStatus(ctx context.Context, tx *gorm.DB, id, status string) error {
	result := tx.WithContext(ctx).
		Table("memory_review").
		Where("id = ? AND review_status = ?", id, reviewStatusPending).
		Update("review_status", status)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errReviewConflict
	}
	return nil
}
