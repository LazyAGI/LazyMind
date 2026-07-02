// Package taskcenter manages TaskCenterTask records: plugin runs, background chats,
// and scheduled triggers. Each plugin session maps to one TaskCenterTask.
package taskcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// OnCancelHook is called by CancelTaskByID after the DB status is updated to "canceled".
// It receives the conversation_id so the caller can interrupt any active plugin session
// and notify Python to terminate the running ReAct loop.
// Register this hook at startup from the plugin package to avoid import cycles.
var OnCancelHook func(ctx context.Context, convID string)

// ── DB helpers ───────────────────────────────────────────────────────────────

// CreateTask inserts a new TaskCenterTask row.
func CreateTask(ctx context.Context, db *gorm.DB, t *orm.TaskCenterTask) error {
	if t.ID == "" {
		t.ID = "tc_" + common.GenerateID()
	}
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	return db.WithContext(ctx).Create(t).Error
}

// GetTask returns a TaskCenterTask by ID, or nil if not found.
func GetTask(ctx context.Context, db *gorm.DB, id string) (*orm.TaskCenterTask, error) {
	var t orm.TaskCenterTask
	if err := db.WithContext(ctx).Where("id = ?", id).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTaskStatus updates status and optionally finished_at.
func UpdateTaskStatus(ctx context.Context, db *gorm.DB, id, status string) error {
	updates := map[string]any{
		"status":     status,
		"updated_at": time.Now().UTC(),
	}
	if isTerminal(status) {
		now := time.Now().UTC()
		updates["finished_at"] = now
	}
	return db.WithContext(ctx).Model(&orm.TaskCenterTask{}).Where("id = ?", id).Updates(updates).Error
}

// UpdateTaskStatusBySession updates the TaskCenter record whose plugin_session_id matches.
// Used by the plugin EventLoop to sync task status when a session completes or fails.
// Terminal status is unified as "completed" (not "succeeded").
func UpdateTaskStatusBySession(ctx context.Context, db *gorm.DB, sessionID, status string) error {
	updates := map[string]any{
		"status":     status,
		"updated_at": time.Now().UTC(),
	}
	if isTerminal(status) {
		now := time.Now().UTC()
		updates["finished_at"] = now
	}
	return db.WithContext(ctx).Model(&orm.TaskCenterTask{}).
		Where("plugin_session_id = ? AND status NOT IN ('completed','failed','canceled')", sessionID).
		Updates(updates).Error
}

// CancelTask marks a task as canceled if it is still pending or running.
func CancelTask(ctx context.Context, db *gorm.DB, userID, id string) error {
	return db.WithContext(ctx).Model(&orm.TaskCenterTask{}).
		Where("id = ? AND user_id = ? AND status IN ('pending','running')", id, userID).
		Updates(map[string]any{
			"status":      "canceled",
			"finished_at": time.Now().UTC(),
			"updated_at":  time.Now().UTC(),
		}).Error
}

func isTerminal(status string) bool {
	switch status {
	case "completed", "succeeded", "failed", "canceled":
		return true
	}
	return false
}

// IsTerminalStatus is the exported variant of isTerminal for use by other packages.
func IsTerminalStatus(status string) bool { return isTerminal(status) }

// ── response types ────────────────────────────────────────────────────────────

type stepInfo struct {
	StepID   string  `json:"step_id"`
	Status   string  `json:"status"`
	Artifact *string `json:"artifact,omitempty"`
}

type taskResponse struct {
	ID                string          `json:"id"`
	UserID            string          `json:"user_id"`
	ConversationID    string          `json:"conversation_id"`
	ConversationTitle string          `json:"conversation_title,omitempty"`
	PluginSessionID   *string         `json:"plugin_session_id,omitempty"`
	TaskType          string          `json:"task_type"`
	Title             *string         `json:"title,omitempty"`
	Status            string          `json:"status"`
	ScheduleID        *string         `json:"schedule_id,omitempty"`
	ScheduleName      *string         `json:"schedule_name,omitempty"`
	Steps             []stepInfo      `json:"steps"`
	ProgressJSON      json.RawMessage `json:"progress,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
	FinishedAt        *time.Time      `json:"finished_at,omitempty"`
}

func toResponse(t orm.TaskCenterTask, conversationTitle string, scheduleName *string, steps []stepInfo) taskResponse {
	if steps == nil {
		steps = []stepInfo{}
	}
	return taskResponse{
		ID:                t.ID,
		UserID:            t.UserID,
		ConversationID:    t.ConversationID,
		ConversationTitle: conversationTitle,
		PluginSessionID:   t.PluginSessionID,
		TaskType:          t.TaskType,
		Title:             t.Title,
		Status:            t.Status,
		ScheduleID:        t.ScheduleID,
		ScheduleName:      scheduleName,
		Steps:             steps,
		ProgressJSON:      t.ProgressJSON,
		CreatedAt:         t.CreatedAt,
		UpdatedAt:         t.UpdatedAt,
		FinishedAt:        t.FinishedAt,
	}
}

// ── step loading helpers ──────────────────────────────────────────────────────

// loadStepsForPluginSession loads steps from plugin_session_steps for a given session.
func loadStepsForPluginSession(ctx context.Context, db *gorm.DB, sessionID string) []stepInfo {
	type pssRow struct {
		StepID string `gorm:"column:step_id"`
		Status string `gorm:"column:status"`
		TaskID string `gorm:"column:task_id"`
	}
	var rows []pssRow
	if err := db.WithContext(ctx).
		Table("plugin_session_steps").
		Select("step_id, status, task_id").
		Where("session_id = ?", sessionID).
		Order("created_at ASC").
		Find(&rows).Error; err != nil {
		return nil
	}
	// Collect task IDs to look up latest artifact keys.
	taskIDs := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.TaskID != "" {
			taskIDs = append(taskIDs, r.TaskID)
		}
	}
	artifactByTask := map[string]string{}
	if len(taskIDs) > 0 {
		type artRow struct {
			TaskID      string `gorm:"column:task_id"`
			ArtifactKey string `gorm:"column:artifact_key"`
		}
		var arts []artRow
		_ = db.WithContext(ctx).
			Table("sub_agent_artifacts").
			Select("task_id, artifact_key").
			Where("task_id IN ? AND seq = (SELECT MAX(seq) FROM sub_agent_artifacts sa2 WHERE sa2.task_id = sub_agent_artifacts.task_id)", taskIDs).
			Find(&arts).Error
		for _, a := range arts {
			artifactByTask[a.TaskID] = a.ArtifactKey
		}
	}
	steps := make([]stepInfo, 0, len(rows))
	for _, r := range rows {
		s := stepInfo{StepID: r.StepID, Status: r.Status}
		if key, ok := artifactByTask[r.TaskID]; ok {
			s.Artifact = &key
		}
		steps = append(steps, s)
	}
	return steps
}

// loadStepsForConversation loads steps from sub_agent_tasks for a given conversation (no plugin).
func loadStepsForConversation(ctx context.Context, db *gorm.DB, convID string) []stepInfo {
	type satRow struct {
		Title              string `gorm:"column:title"`
		Status             string `gorm:"column:status"`
		OutputArtifactKeys string `gorm:"column:output_artifact_keys"`
	}
	var rows []satRow
	if err := db.WithContext(ctx).
		Table("sub_agent_tasks").
		Select("title, status, output_artifact_keys").
		Where("conversation_id = ?", convID).
		Order("seq_in_conversation ASC").
		Find(&rows).Error; err != nil {
		return nil
	}
	steps := make([]stepInfo, 0, len(rows))
	for _, r := range rows {
		s := stepInfo{StepID: r.Title, Status: r.Status}
		var keys []string
		if json.Unmarshal([]byte(r.OutputArtifactKeys), &keys) == nil && len(keys) > 0 {
			s.Artifact = &keys[0]
		}
		steps = append(steps, s)
	}
	return steps
}

// resolveTaskStatus returns the effective display status for a task by querying
// live data rather than relying on any write-time status callback.
//
// Decision tree (evaluated only when t.Status is non-terminal):
//
//  1. Plugin task (plugin_session_id set): derive from plugin_sessions.status.
//  2. No plugin: check whether chat_histories has a row for this conversation.
//     - Row exists  → SSE finished and was persisted → "completed".
//     - No row, task is older than 2 h → timed out with no output → "failed".
//     - No row, task is recent → still running → keep "running".
func resolveTaskStatus(ctx context.Context, db *gorm.DB, t orm.TaskCenterTask) string {
	if isTerminal(t.Status) {
		return t.Status
	}
	if t.PluginSessionID != nil && *t.PluginSessionID != "" {
		var sess struct {
			Status string `gorm:"column:status"`
		}
		if err := db.WithContext(ctx).
			Table("plugin_sessions").
			Select("status").
			Where("id = ?", *t.PluginSessionID).
			First(&sess).Error; err == nil {
			switch sess.Status {
			case "active":
				return "running"
			case "waiting":
				return "waiting"
			case "completed":
				return "completed"
			case "failed":
				return "failed"
			}
		}
		return t.Status
	}

	// No plugin session: use chat_histories presence as the completion signal.
	// Go writes a chat_histories row atomically at the very end of streamSingleAnswer,
	// after all SSE tokens have been consumed from Python. Its existence is therefore
	// a reliable indicator that the Python→Go SSE stream has fully completed.
	var histCount int64
	db.WithContext(ctx).
		Table("chat_histories").
		Where("conversation_id = ?", t.ConversationID).
		Count(&histCount)
	if histCount > 0 {
		return "completed"
	}
	if time.Since(t.CreatedAt) > 2*time.Hour {
		return "failed"
	}
	return "running"
}

// ── API handlers ─────────────────────────────────────────────────────────────

// ListTasks handles GET /task-center/tasks
// Query params: status, task_type, keyword, page (1-based), page_size.
func ListTasks(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "user not found", http.StatusUnauthorized)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "db unavailable", http.StatusInternalServerError)
		return
	}

	q := r.URL.Query()
	status := strings.TrimSpace(q.Get("status"))
	taskType := strings.TrimSpace(q.Get("task_type"))
	keyword := strings.TrimSpace(q.Get("keyword"))
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	query := db.WithContext(r.Context()).Where("tct.user_id = ? AND tct.archived_at IS NULL", userID)
	if status != "" {
		query = query.Where("tct.status = ?", status)
	}
	if taskType != "" {
		query = query.Where("tct.task_type = ?", taskType)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("tct.title LIKE ? OR c.display_name LIKE ?", like, like)
	}

	type rawRow struct {
		orm.TaskCenterTask
		ConvDisplayName string  `gorm:"column:conv_display_name"`
		ScheduleName    *string `gorm:"column:schedule_name"`
	}

	var total int64
	countQ := db.WithContext(r.Context()).
		Table("task_center_tasks tct").
		Joins("LEFT JOIN conversations c ON c.id = tct.conversation_id").
		Joins("LEFT JOIN user_schedules us ON us.id = tct.schedule_id").
		Where("tct.user_id = ? AND tct.archived_at IS NULL", userID)
	if status != "" {
		countQ = countQ.Where("tct.status = ?", status)
	}
	if taskType != "" {
		countQ = countQ.Where("tct.task_type = ?", taskType)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		countQ = countQ.Where("tct.title LIKE ? OR c.display_name LIKE ?", like, like)
	}
	_ = countQ.Count(&total)

	var rows []rawRow
	dataQ := db.WithContext(r.Context()).
		Table("task_center_tasks tct").
		Select("tct.*, c.display_name AS conv_display_name, us.name AS schedule_name").
		Joins("LEFT JOIN conversations c ON c.id = tct.conversation_id").
		Joins("LEFT JOIN user_schedules us ON us.id = tct.schedule_id").
		Where("tct.user_id = ? AND tct.archived_at IS NULL", userID)
	if status != "" {
		dataQ = dataQ.Where("tct.status = ?", status)
	}
	if taskType != "" {
		dataQ = dataQ.Where("tct.task_type = ?", taskType)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		dataQ = dataQ.Where("tct.title LIKE ? OR c.display_name LIKE ?", like, like)
	}
	if err := dataQ.Order("tct.created_at DESC").Offset(offset).Limit(pageSize).Find(&rows).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]taskResponse, 0, len(rows))
	for _, row := range rows {
		t := row.TaskCenterTask
		effectiveStatus := resolveTaskStatus(r.Context(), db, t)
		t.Status = effectiveStatus

		var steps []stepInfo
		if t.PluginSessionID != nil && *t.PluginSessionID != "" {
			steps = loadStepsForPluginSession(r.Context(), db, *t.PluginSessionID)
		} else {
			steps = loadStepsForConversation(r.Context(), db, t.ConversationID)
		}
		items = append(items, toResponse(t, row.ConvDisplayName, row.ScheduleName, steps))
	}
	common.ReplyJSON(w, map[string]any{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// GetTaskByID handles GET /task-center/tasks/{task_id}
func GetTaskByID(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	id := strings.TrimPrefix(r.URL.Path, "/task-center/tasks/")
	id = strings.Split(id, "/")[0]
	id = strings.Split(id, ":")[0]
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "db unavailable", http.StatusInternalServerError)
		return
	}

	var t orm.TaskCenterTask
	if err := db.WithContext(r.Context()).Where("id = ? AND user_id = ?", id, userID).First(&t).Error; err != nil {
		common.ReplyErr(w, "task not found", http.StatusNotFound)
		return
	}

	type convRow struct {
		DisplayName string `gorm:"column:display_name"`
	}
	convTitle := ""
	if t.ConversationID != "" {
		var c convRow
		if err := db.WithContext(r.Context()).
			Table("conversations").
			Select("display_name").
			Where("id = ?", t.ConversationID).
			First(&c).Error; err == nil {
			convTitle = c.DisplayName
		}
	}

	effectiveStatus := resolveTaskStatus(r.Context(), db, t)
	t.Status = effectiveStatus

	var steps []stepInfo
	if t.PluginSessionID != nil && *t.PluginSessionID != "" {
		steps = loadStepsForPluginSession(r.Context(), db, *t.PluginSessionID)
	} else {
		steps = loadStepsForConversation(r.Context(), db, t.ConversationID)
	}

	common.ReplyJSON(w, toResponse(t, convTitle, nil, steps))
}

// CancelTaskByID handles POST /task-center/tasks/{task_id}:cancel
func CancelTaskByID(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	path := strings.TrimPrefix(r.URL.Path, "/task-center/tasks/")
	id := strings.TrimSuffix(path, ":cancel")
	id = strings.Split(id, ":")[0]

	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "db unavailable", http.StatusInternalServerError)
		return
	}

	// Validate: cannot cancel a terminal task.
	var existing orm.TaskCenterTask
	if err := db.WithContext(r.Context()).Where("id = ? AND user_id = ?", id, userID).First(&existing).Error; err != nil {
		common.ReplyErr(w, "task not found", http.StatusNotFound)
		return
	}
	if isTerminal(existing.Status) {
		common.ReplyErr(w, "task already in terminal state", http.StatusBadRequest)
		return
	}

	if err := CancelTask(r.Context(), db, userID, id); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// If the task was running, notify Python to actually stop execution.
	if existing.Status == "running" && OnCancelHook != nil {
		go OnCancelHook(r.Context(), existing.ConversationID)
	}

	common.ReplyOK(w, nil)
}

// RemoveTaskHandler handles POST /task-center/tasks/{task_id}:remove
// Soft-archives the task so it no longer appears in the list. The conversation is unaffected.
func RemoveTaskHandler(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	path := strings.TrimPrefix(r.URL.Path, "/task-center/tasks/")
	id := strings.TrimSuffix(path, ":remove")
	id = strings.Split(id, ":")[0]

	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "db unavailable", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	result := db.WithContext(r.Context()).Model(&orm.TaskCenterTask{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]any{"archived_at": now, "updated_at": now})
	if result.Error != nil {
		common.ReplyErr(w, result.Error.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, nil)
}

// AddTaskHandler handles POST /task-center/tasks
// Body: { "conversation_id": "...", "title": "..." }
// Idempotent: if a record already exists for this conversation_id it returns the existing one.
func AddTaskHandler(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "user not found", http.StatusUnauthorized)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "db unavailable", http.StatusInternalServerError)
		return
	}

	var body struct {
		ConversationID string `json:"conversation_id"`
		Title          string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.ConversationID == "" {
		common.ReplyErr(w, "conversation_id required", http.StatusBadRequest)
		return
	}

	// Verify ownership.
	var convCheck struct {
		ID string `gorm:"column:id"`
	}
	if err := db.WithContext(r.Context()).
		Table("conversations").
		Select("id").
		Where("id = ? AND create_user_id = ?", body.ConversationID, userID).
		First(&convCheck).Error; err != nil {
		common.ReplyErr(w, "conversation not found", http.StatusNotFound)
		return
	}

	// Idempotency check.
	var existing orm.TaskCenterTask
	err := db.WithContext(r.Context()).
		Where("conversation_id = ? AND user_id = ?", body.ConversationID, userID).
		First(&existing).Error
	if err == nil {
		convTitle := ""
		type cRow struct {
			DisplayName string `gorm:"column:display_name"`
		}
		var cr cRow
		_ = db.WithContext(r.Context()).Table("conversations").Select("display_name").Where("id = ?", body.ConversationID).First(&cr).Error
		convTitle = cr.DisplayName
		common.ReplyJSON(w, toResponse(existing, convTitle, nil, loadStepsForConversation(r.Context(), db, body.ConversationID)))
		return
	}

	title := body.Title
	if title == "" {
		title = body.ConversationID
	}
	task := &orm.TaskCenterTask{
		UserID:         userID,
		ConversationID: body.ConversationID,
		TaskType:       "background_chat",
		Title:          &title,
		Status:         "running",
	}
	if err := CreateTask(r.Context(), db, task); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyJSON(w, toResponse(*task, title, nil, []stepInfo{}))
}

// ListScheduleTasks handles GET /task-center/schedules/{schedule_id}/tasks
func ListScheduleTasks(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "user not found", http.StatusUnauthorized)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "db unavailable", http.StatusInternalServerError)
		return
	}

	// Extract schedule_id from path: /task-center/schedules/{schedule_id}/tasks
	path := strings.TrimPrefix(r.URL.Path, "/task-center/schedules/")
	scheduleID := strings.Split(path, "/")[0]
	if scheduleID == "" {
		common.ReplyErr(w, "schedule_id required", http.StatusBadRequest)
		return
	}

	// Verify ownership of schedule.
	var sched struct {
		Name string `gorm:"column:name"`
	}
	if err := db.WithContext(r.Context()).
		Table("user_schedules").
		Select("name").
		Where("id = ? AND user_id = ?", scheduleID, userID).
		First(&sched).Error; err != nil {
		common.ReplyErr(w, "schedule not found", http.StatusNotFound)
		return
	}

	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}
	offset := (page - 1) * pageSize

	var total int64
	_ = db.WithContext(r.Context()).Model(&orm.TaskCenterTask{}).
		Where("schedule_id = ? AND user_id = ?", scheduleID, userID).
		Count(&total)

	var rows []orm.TaskCenterTask
	if err := db.WithContext(r.Context()).
		Where("schedule_id = ? AND user_id = ?", scheduleID, userID).
		Order("created_at DESC").
		Offset(offset).Limit(pageSize).
		Find(&rows).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Batch-load conversation titles.
	convIDs := make([]string, 0, len(rows))
	for _, t := range rows {
		convIDs = append(convIDs, t.ConversationID)
	}
	convTitles := map[string]string{}
	if len(convIDs) > 0 {
		type cRow struct {
			ID          string `gorm:"column:id"`
			DisplayName string `gorm:"column:display_name"`
		}
		var cRows []cRow
		if err := db.WithContext(r.Context()).
			Table("conversations").Select("id, display_name").
			Where("id IN ?", convIDs).Find(&cRows).Error; err == nil {
			for _, c := range cRows {
				convTitles[c.ID] = c.DisplayName
			}
		}
	}

	schedName := sched.Name
	items := make([]taskResponse, 0, len(rows))
	for _, t := range rows {
		effectiveStatus := resolveTaskStatus(r.Context(), db, t)
		t.Status = effectiveStatus
		var steps []stepInfo
		if t.PluginSessionID != nil && *t.PluginSessionID != "" {
			steps = loadStepsForPluginSession(r.Context(), db, *t.PluginSessionID)
		} else {
			steps = loadStepsForConversation(r.Context(), db, t.ConversationID)
		}
		sn := schedName
		items = append(items, toResponse(t, convTitles[t.ConversationID], &sn, steps))
	}
	common.ReplyJSON(w, map[string]any{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}
