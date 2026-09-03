// Package taskcenter manages TaskCenterTask records: plugin runs, background chats,
// and scheduled triggers. Each plugin session maps to one TaskCenterTask.
package taskcenter

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
	"lazymind/core/store"
)

// OnCancelHook is called by CancelTaskByID after the DB status is updated to "canceled".
// It receives the conversation_id so the caller can interrupt any active plugin session
// and notify Python to terminate the running ReAct loop.
// Register this hook at startup from the plugin package to avoid import cycles.
var OnCancelHook func(ctx context.Context, convID string)

const taskExecutionTimeoutReason = "任务执行超过2小时，未正常完成"

const (
	ArchivedReasonTaskRemove          = "task_remove"
	ArchivedReasonConversationArchive = "conversation_archive"
	ArchivedReasonConversationTrash   = "conversation_trash"
	ArchivedReasonConversationPurged  = "conversation_purged"
)

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
	return db.WithContext(ctx).Model(&orm.TaskCenterTask{}).
		Where("id = ? AND archived_at IS NULL AND status NOT IN ('canceled')", id).
		Updates(updates).Error
}

// UpdateTaskFailure persists a terminal task failure together with a user-facing reason.
func UpdateTaskFailure(ctx context.Context, db *gorm.DB, id, reason string) error {
	var task orm.TaskCenterTask
	if err := db.WithContext(ctx).Select("id", "status", "progress_json").Where("id = ?", id).First(&task).Error; err != nil {
		return err
	}
	if isTerminal(task.Status) && task.Status != "failed" {
		return nil
	}
	now := time.Now().UTC()
	return db.WithContext(ctx).Model(&orm.TaskCenterTask{}).
		Where("id = ? AND archived_at IS NULL AND status NOT IN ('succeeded','skipped','canceled')", id).
		Updates(map[string]any{"status": "failed", "progress_json": progressWithFailureReason(task.ProgressJSON, reason), "finished_at": now, "updated_at": now}).Error
}

func progressWithFailureReason(progress orm.RawJSON, reason string) orm.RawJSON {
	payload := map[string]any{}
	if strings.TrimSpace(string(progress)) != "" {
		if err := json.Unmarshal(progress, &payload); err != nil || payload == nil {
			payload = map[string]any{}
		}
	}
	if existing, ok := payload["failure_reason"].(string); ok && strings.TrimSpace(existing) != "" {
		return progress
	}
	if existing, ok := payload["error_message"].(string); ok && strings.TrimSpace(existing) != "" {
		return progress
	}
	payload["failure_reason"] = reason
	encoded, _ := json.Marshal(payload)
	return orm.RawJSON(encoded)
}

// UpdateTaskStatusBySession updates the TaskCenter record whose plugin_session_id matches.  // workflow-naming: persistence
// Used by the plugin EventLoop to sync task status when a session completes or fails.
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
		Where("plugin_session_id = ? AND archived_at IS NULL AND status NOT IN ('succeeded','failed','canceled')", sessionID). // workflow-naming: persistence
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
	case "succeeded", "failed", "skipped", "canceled":
		return true
	}
	return false
}

// IsTerminalStatus is the exported variant of isTerminal for use by other packages.
func IsTerminalStatus(status string) bool { return isTerminal(status) }

// ArchiveTasksForConversations hides task-center rows for the linked task
// conversations. Non-terminal tasks are canceled first so late status updates
// cannot make them visible or running again.
func ArchiveTasksForConversations(ctx context.Context, db *gorm.DB, userID string, conversationIDs []string, reason string, now time.Time) error {
	if len(conversationIDs) == 0 {
		return nil
	}
	updates := map[string]any{
		"archived_at": now, "archived_reason": reason, "updated_at": now,
	}
	eligible := db.WithContext(ctx).Model(&orm.TaskCenterTask{}).
		Where("user_id = ? AND conversation_id IN ?", userID, conversationIDs)
	if reason == ArchivedReasonConversationTrash {
		// Moving an already archived task conversation into trash changes the
		// lifecycle reason so a later trash restore revives its task history.
		eligible = eligible.Where("archived_at IS NULL OR archived_reason = ?", ArchivedReasonConversationArchive)
	} else {
		eligible = eligible.Where("archived_at IS NULL")
	}
	if err := eligible.
		Where("status NOT IN ('succeeded','failed','skipped','canceled')").
		Updates(map[string]any{
			"status": "canceled", "dependency_status": "canceled", "finished_at": now,
			"archived_at": now, "archived_reason": reason, "updated_at": now,
		}).Error; err != nil {
		return err
	}
	eligible = db.WithContext(ctx).Model(&orm.TaskCenterTask{}).
		Where("user_id = ? AND conversation_id IN ?", userID, conversationIDs)
	if reason == ArchivedReasonConversationTrash {
		eligible = eligible.Where("archived_at IS NULL OR archived_reason = ?", ArchivedReasonConversationArchive)
	} else {
		eligible = eligible.Where("archived_at IS NULL")
	}
	if err := eligible.
		Updates(updates).Error; err != nil {
		return err
	}
	if OnCancelHook != nil {
		for _, conversationID := range conversationIDs {
			go OnCancelHook(context.Background(), conversationID)
		}
	}
	return nil
}

// RestoreTasksForConversations only revives rows hidden by the matching
// conversation lifecycle transition. It never revives tasks removed for other
// reasons.
func RestoreTasksForConversations(ctx context.Context, db *gorm.DB, userID string, conversationIDs []string, reason string, now time.Time) error {
	if len(conversationIDs) == 0 {
		return nil
	}
	return db.WithContext(ctx).Model(&orm.TaskCenterTask{}).
		Where("user_id = ? AND conversation_id IN ? AND archived_reason = ?", userID, conversationIDs, reason).
		Updates(map[string]any{"archived_at": nil, "archived_reason": "", "updated_at": now}).Error
}

func MarkConversationPurged(ctx context.Context, db *gorm.DB, userID, conversationID string, now time.Time) error {
	return db.WithContext(ctx).Model(&orm.TaskCenterTask{}).
		Where("user_id = ? AND conversation_id = ?", userID, conversationID).
		Updates(map[string]any{
			"archived_at": now, "archived_reason": ArchivedReasonConversationPurged, "updated_at": now,
		}).Error
}

// ── response types ────────────────────────────────────────────────────────────

type stepInfo struct {
	StepID       string  `json:"step_id"`
	Title        string  `json:"title,omitempty"`
	Status       string  `json:"status"`
	CurrentPhase string  `json:"current_phase,omitempty"`
	Summary      string  `json:"summary,omitempty"`
	Artifact     *string `json:"artifact,omitempty"`
}

type taskResponse struct {
	ID                string          `json:"id"`
	UserID            string          `json:"user_id"`
	ConversationID    string          `json:"conversation_id"`
	ConversationState string          `json:"conversation_state"`
	ConversationTitle string          `json:"conversation_title,omitempty"`
	WorkflowSessionID *string         `json:"workflow_session_id,omitempty"`
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
	WaitingReason     string          `json:"waiting_reason,omitempty"`
}

func toResponse(t orm.TaskCenterTask, conversationTitle, conversationState string, scheduleName *string, steps []stepInfo) taskResponse {
	if steps == nil {
		steps = []stepInfo{}
	}
	return taskResponse{
		ID:                t.ID,
		UserID:            t.UserID,
		ConversationID:    t.ConversationID,
		ConversationState: conversationState,
		ConversationTitle: conversationTitle,
		WorkflowSessionID: t.WorkflowSessionID,
		TaskType:          t.TaskType,
		Title:             t.Title,
		Status:            t.Status,
		ScheduleID:        t.ScheduleID,
		ScheduleName:      scheduleName,
		Steps:             steps,
		ProgressJSON:      json.RawMessage(t.ProgressJSON),
		CreatedAt:         t.CreatedAt,
		UpdatedAt:         t.UpdatedAt,
		FinishedAt:        t.FinishedAt,
	}
}

// ── step loading helpers ──────────────────────────────────────────────────────

// loadStepsForWorkflowSession loads steps from plugin_session_steps for a given session.  // workflow-naming: persistence
func loadStepsForWorkflowSession(ctx context.Context, db *gorm.DB, sessionID string) []stepInfo {
	type pssRow struct {
		StepID       string `gorm:"column:step_id"`
		Title        string `gorm:"column:title"`
		Status       string `gorm:"column:status"`
		TaskID       string `gorm:"column:task_id"`
		CurrentPhase string `gorm:"column:current_phase"`
		Summary      string `gorm:"column:summary"`
	}
	var rows []pssRow
	if err := db.WithContext(ctx).
		Table("plugin_session_steps AS pss").
		Select("pss.step_id, pss.status, pss.task_id, sat.title, sat.current_phase, sat.summary").
		Joins("LEFT JOIN sub_agent_tasks AS sat ON sat.id = pss.task_id").
		Where("pss.session_id = ?", sessionID).
		Order("pss.created_at ASC").
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
			TaskID string `gorm:"column:task_id"`
			Slot   string `gorm:"column:slot"`
		}
		var arts []artRow
		_ = db.WithContext(ctx).
			Table("sub_agent_artifacts").
			Select("task_id, slot").
			Where("task_id IN ? AND seq = (SELECT MAX(seq) FROM sub_agent_artifacts sa2 WHERE sa2.task_id = sub_agent_artifacts.task_id)", taskIDs).
			Find(&arts).Error
		for _, a := range arts {
			artifactByTask[a.TaskID] = a.Slot
		}
	}
	steps := make([]stepInfo, 0, len(rows))
	for _, r := range rows {
		s := stepInfo{StepID: r.StepID, Title: r.Title, Status: r.Status, CurrentPhase: r.CurrentPhase, Summary: r.Summary}
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
		Title        string `gorm:"column:title"`
		Status       string `gorm:"column:status"`
		CurrentPhase string `gorm:"column:current_phase"`
		Summary      string `gorm:"column:summary"`
		OutputSlots  string `gorm:"column:output_slots"`
	}
	var rows []satRow
	if err := db.WithContext(ctx).
		Table("sub_agent_tasks").
		Select("title, status, current_phase, summary, output_slots").
		Where("conversation_id = ? AND agent_type <> ?", convID, "workflow_step").
		Order("seq_in_conversation ASC").
		Find(&rows).Error; err != nil {
		return nil
	}
	steps := make([]stepInfo, 0, len(rows))
	for _, r := range rows {
		s := stepInfo{StepID: r.Title, Title: r.Title, Status: r.Status, CurrentPhase: r.CurrentPhase, Summary: r.Summary}
		var keys []string
		if json.Unmarshal([]byte(r.OutputSlots), &keys) == nil && len(keys) > 0 {
			s.Artifact = &keys[0]
		}
		steps = append(steps, s)
	}
	return steps
}

// resolveTaskStatus returns the effective display status for a task.
//
// Decision tree (evaluated only when t.Status is non-terminal):
//
//  1. Workflow task (plugin_session_id set): derive from plugin_sessions.status.  // workflow-naming: persistence
//  2. No plugin: rely on the persisted task status. Chat histories are written
//     during streaming to preserve thinking progress, so their presence is not a
//     completion signal.
func resolveTaskStatus(ctx context.Context, db *gorm.DB, t orm.TaskCenterTask) string {
	if isTerminal(t.Status) {
		return t.Status
	}
	if t.Status == "waiting_inputs" {
		return "waiting_inputs"
	}
	if t.Status == "waiting" || t.Status == "pending" {
		return t.Status
	}
	if t.WorkflowSessionID != nil && *t.WorkflowSessionID != "" {
		var sess struct {
			Status string `gorm:"column:status"`
		}
		if err := db.WithContext(ctx).
			Table("plugin_sessions"). // workflow-naming: persistence
			Select("status").
			Where("id = ?", *t.WorkflowSessionID).
			First(&sess).Error; err == nil {
			switch sess.Status {
			case "active":
				return "running"
			case "waiting":
				return "waiting"
			case "completed":
				return "succeeded"
			case "failed":
				return "failed"
			}
		}
		return t.Status
	}

	if time.Since(t.CreatedAt) > 2*time.Hour {
		return "failed"
	}
	return "running"
}

func resolveTaskForResponse(ctx context.Context, db *gorm.DB, t orm.TaskCenterTask) orm.TaskCenterTask {
	if t.ArchivedAt == nil && t.Status != "canceled" {
		if session := workflowForTask(ctx, db, t); session != nil {
			t.WorkflowSessionID = &session.ID
			if status := workflowTaskStatus(session.Status); status != "" {
				t.Status = status
				t.FinishedAt = nil
				if isTerminal(status) {
					t.FinishedAt = &session.UpdatedAt
				}
				if session.UpdatedAt.After(t.UpdatedAt) {
					t.UpdatedAt = session.UpdatedAt
				}
				return t
			}
		}
	}
	storedStatus := t.Status
	t.Status = resolveTaskStatus(ctx, db, t)
	if t.Status == "failed" && !isTerminal(storedStatus) &&
		(t.WorkflowSessionID == nil || *t.WorkflowSessionID == "") &&
		time.Since(t.CreatedAt) > 2*time.Hour {
		t.ProgressJSON = progressWithFailureReason(t.ProgressJSON, taskExecutionTimeoutReason)
	}
	return t
}

func waitingDependencyReason(ctx context.Context, db *gorm.DB, t orm.TaskCenterTask) string {
	if t.Status == "failed" && t.DependencyStatus == "no_inputs" {
		return "未收集到依赖任务输出"
	}
	if t.Status != "waiting_inputs" || t.ScheduleID == nil || t.WindowStart == nil || t.WindowEnd == nil {
		return ""
	}
	type depRow struct {
		SourceScheduleID string `gorm:"column:source_schedule_id"`
		SourceName       string `gorm:"column:source_name"`
	}
	var deps []depRow
	_ = db.WithContext(ctx).Table("schedule_dependencies sd").
		Select("sd.source_schedule_id, us.name AS source_name").
		Joins("JOIN user_schedules us ON us.id = sd.source_schedule_id").
		Where("sd.target_schedule_id = ? AND sd.enabled = true", *t.ScheduleID).Find(&deps).Error
	if len(deps) == 0 {
		return ""
	}
	ready := 0
	waitingNames := make([]string, 0)
	for _, dep := range deps {
		var count int64
		db.WithContext(ctx).Table("task_center_tasks source_task").
			Joins("JOIN task_run_outputs tro ON tro.task_id = source_task.id AND tro.output_status = 'ready'").
			Where("source_task.schedule_id = ? AND source_task.archived_at IS NULL AND COALESCE(source_task.scheduled_fire_at, source_task.created_at) > ? AND COALESCE(source_task.scheduled_fire_at, source_task.created_at) <= ?", dep.SourceScheduleID, *t.WindowStart, *t.WindowEnd).
			Count(&count)
		if count > 0 {
			ready++
		} else {
			waitingNames = append(waitingNames, dep.SourceName)
		}
	}
	if len(waitingNames) == 0 {
		return "依赖已就绪，准备执行"
	}
	name := waitingNames[0]
	if len(waitingNames) > 1 {
		name += "等"
	}
	return "等待 " + name + "：" + strconv.Itoa(ready) + "/" + strconv.Itoa(len(deps))
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

	type rawRow struct {
		orm.TaskCenterTask
		ConvDisplayName   string  `gorm:"column:conv_display_name"`
		ConversationState string  `gorm:"column:conversation_state"`
		ScheduleName      *string `gorm:"column:schedule_name"`
	}

	var rows []rawRow
	dataQ := db.WithContext(r.Context()).
		Table("task_center_tasks tct").
		Select(`tct.*, c.display_name AS conv_display_name,
            CASE
                WHEN c.id IS NULL THEN 'missing'
                WHEN c.deleted_at IS NOT NULL THEN 'trash'
                WHEN c.archived_at IS NOT NULL THEN 'archived'
                ELSE 'active'
            END AS conversation_state,
            us.name AS schedule_name`).
		Joins("LEFT JOIN conversations c ON c.id = tct.conversation_id").
		Joins("LEFT JOIN user_schedules us ON us.id = tct.schedule_id").
		Joins("LEFT JOIN plugin_sessions ps ON ps.id = tct.plugin_session_id"). // workflow-naming: persistence
		Where("tct.user_id = ? AND tct.archived_at IS NULL", userID).
		Where("tct.plugin_session_id IS NULL OR ps.dismissed = false") // workflow-naming: persistence
	if taskType != "" {
		dataQ = dataQ.Where("tct.task_type = ?", taskType)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		dataQ = dataQ.Where("tct.title LIKE ? OR c.display_name LIKE ?", like, like)
	}
	// Status must be resolved from live plugin/chat state before filtering. The
	// stored task_center_tasks.status can lag behind and would otherwise exclude
	// ordinary completed tasks or put waiting plugin tasks under "running".
	if err := dataQ.Order("tct.created_at DESC").Find(&rows).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type resolvedRow struct {
		row  rawRow
		task orm.TaskCenterTask
	}
	resolved := make([]resolvedRow, 0, len(rows))
	statusCounts := map[string]int{
		"all":            0,
		"pending":        0,
		"waiting":        0,
		"waiting_inputs": 0,
		"running":        0,
		"succeeded":      0,
		"failed":         0,
		"canceled":       0,
	}
	for _, row := range rows {
		t := resolveTaskForResponse(r.Context(), db, row.TaskCenterTask)
		statusCounts["all"]++
		if _, tracked := statusCounts[t.Status]; tracked {
			statusCounts[t.Status]++
		}
		if status != "" && t.Status != status {
			continue
		}
		resolved = append(resolved, resolvedRow{row: row, task: t})
	}
	total := len(resolved)
	if offset > total {
		offset = total
	}
	end := offset + pageSize
	if end > total {
		end = total
	}

	items := make([]taskResponse, 0, end-offset)
	for _, resolvedItem := range resolved[offset:end] {
		t := resolvedItem.task
		var steps []stepInfo
		if t.WorkflowSessionID != nil && *t.WorkflowSessionID != "" {
			steps = loadStepsForWorkflowSession(r.Context(), db, *t.WorkflowSessionID)
		} else {
			steps = loadStepsForConversation(r.Context(), db, t.ConversationID)
		}
		item := toResponse(t, resolvedItem.row.ConvDisplayName, resolvedItem.row.ConversationState, resolvedItem.row.ScheduleName, steps)
		item.WaitingReason = waitingDependencyReason(r.Context(), db, resolvedItem.row.TaskCenterTask)
		items = append(items, item)
	}
	common.ReplyJSON(w, map[string]any{
		"items":         items,
		"total":         total,
		"page":          page,
		"page_size":     pageSize,
		"status_counts": statusCounts,
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
	if err := db.WithContext(r.Context()).Where("id = ? AND user_id = ? AND archived_at IS NULL", id, userID).First(&t).Error; err != nil {
		common.ReplyErr(w, "task not found", http.StatusNotFound)
		return
	}

	type convRow struct {
		DisplayName string     `gorm:"column:display_name"`
		ArchivedAt  *time.Time `gorm:"column:archived_at"`
		DeletedAt   *time.Time `gorm:"column:deleted_at"`
	}
	convTitle := ""
	conversationState := "missing"
	if t.ConversationID != "" {
		var c convRow
		if err := db.WithContext(r.Context()).
			Table("conversations").
			Select("display_name").
			Where("id = ?", t.ConversationID).
			First(&c).Error; err == nil {
			convTitle = c.DisplayName
			switch {
			case c.DeletedAt != nil:
				conversationState = "trash"
			case c.ArchivedAt != nil:
				conversationState = "archived"
			default:
				conversationState = "active"
			}
		}
	}

	t = resolveTaskForResponse(r.Context(), db, t)

	var steps []stepInfo
	if t.WorkflowSessionID != nil && *t.WorkflowSessionID != "" {
		steps = loadStepsForWorkflowSession(r.Context(), db, *t.WorkflowSessionID)
	} else {
		steps = loadStepsForConversation(r.Context(), db, t.ConversationID)
	}

	common.ReplyJSON(w, toResponse(t, convTitle, conversationState, nil, steps))
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

// RemoveTaskHandler handles POST /task-center/tasks/{task_id}:remove.
// The task and its task-conversation share the recovery lifecycle.
func RemoveTaskHandler(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(store.UserID(r))
	path := strings.TrimPrefix(r.URL.Path, "/task-center/tasks/")
	id := strings.TrimSuffix(path, ":remove")
	id = strings.Split(id, ":")[0]
	if userID == "" {
		common.ReplyErr(w, "user not found", http.StatusUnauthorized)
		return
	}

	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "db unavailable", http.StatusInternalServerError)
		return
	}
	existing, err := trashTaskRun(r.Context(), db, userID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "task not found", http.StatusNotFound)
		return
	}
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !isTerminal(existing.Status) {
		if existing.ConversationID != "" && OnCancelHook != nil {
			go OnCancelHook(r.Context(), existing.ConversationID)
		}
	}
	common.ReplyOK(w, nil)
}

func trashTaskRun(ctx context.Context, db *gorm.DB, userID, id string) (orm.TaskCenterTask, error) {
	var existing orm.TaskCenterTask
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND user_id = ? AND archived_at IS NULL", id, userID).First(&existing).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		expiresAt := now.Add(30 * 24 * time.Hour)
		result := tx.Model(&orm.Conversation{}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", existing.ConversationID, userID).
			Updates(map[string]any{
				"deleted_at": now, "trash_expires_at": expiresAt,
				"archived_at": nil, "archive_folder_id": nil, "updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return ArchiveTasksForConversations(ctx, tx, userID, []string{existing.ConversationID}, ArchivedReasonConversationTrash, now)
	})
	return existing, err
}

func archiveTaskRun(ctx context.Context, db *gorm.DB, userID, id string) (orm.TaskCenterTask, error) {
	var existing orm.TaskCenterTask
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND user_id = ? AND archived_at IS NULL", id, userID).First(&existing).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		updates := map[string]any{"archived_at": now, "archived_reason": ArchivedReasonTaskRemove, "updated_at": now}
		if !isTerminal(existing.Status) {
			updates["status"] = "canceled"
			updates["dependency_status"] = "canceled"
			updates["finished_at"] = now
		}
		result := tx.Model(&orm.TaskCenterTask{}).
			Where("id = ? AND user_id = ? AND archived_at IS NULL", id, userID).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		// Input rows are snapshots owned by this downstream run, so archive cleanup
		// removes them together with the run's visible history.
		if err := tx.Where("downstream_task_id = ?", id).Delete(&orm.TaskRunInput{}).Error; err != nil {
			return err
		}
		if existing.ScheduleID != nil && *existing.ScheduleID != "" {
			var visibleRuns int64
			if err := tx.Model(&orm.TaskCenterTask{}).
				Where("schedule_id = ? AND user_id = ? AND archived_at IS NULL", *existing.ScheduleID, userID).
				Count(&visibleRuns).Error; err != nil {
				return err
			}
			if err := tx.Model(&orm.UserSchedule{}).
				Where("id = ? AND user_id = ?", *existing.ScheduleID, userID).
				Update("run_count", visibleRuns).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return existing, err
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
		Where("schedule_id = ? AND user_id = ? AND archived_at IS NULL", scheduleID, userID).
		Count(&total)

	var rows []orm.TaskCenterTask
	if err := db.WithContext(r.Context()).
		Where("schedule_id = ? AND user_id = ? AND archived_at IS NULL", scheduleID, userID).
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
	convStates := map[string]string{}
	if len(convIDs) > 0 {
		type cRow struct {
			ID          string     `gorm:"column:id"`
			DisplayName string     `gorm:"column:display_name"`
			ArchivedAt  *time.Time `gorm:"column:archived_at"`
			DeletedAt   *time.Time `gorm:"column:deleted_at"`
		}
		var cRows []cRow
		if err := db.WithContext(r.Context()).
			Table("conversations").Select("id, display_name, archived_at, deleted_at").
			Where("id IN ?", convIDs).Find(&cRows).Error; err == nil {
			for _, c := range cRows {
				convTitles[c.ID] = c.DisplayName
				switch {
				case c.DeletedAt != nil:
					convStates[c.ID] = "trash"
				case c.ArchivedAt != nil:
					convStates[c.ID] = "archived"
				default:
					convStates[c.ID] = "active"
				}
			}
		}
	}

	schedName := sched.Name
	items := make([]taskResponse, 0, len(rows))
	for _, t := range rows {
		t = resolveTaskForResponse(r.Context(), db, t)
		var steps []stepInfo
		if t.WorkflowSessionID != nil && *t.WorkflowSessionID != "" {
			steps = loadStepsForWorkflowSession(r.Context(), db, *t.WorkflowSessionID)
		} else {
			steps = loadStepsForConversation(r.Context(), db, t.ConversationID)
		}
		sn := schedName
		conversationState := convStates[t.ConversationID]
		if conversationState == "" {
			conversationState = "missing"
		}
		item := toResponse(t, convTitles[t.ConversationID], conversationState, &sn, steps)
		item.WaitingReason = waitingDependencyReason(r.Context(), db, t)
		items = append(items, item)
	}
	common.ReplyJSON(w, map[string]any{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}
