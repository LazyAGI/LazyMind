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
		Where("plugin_session_id = ? AND status NOT IN ('succeeded','failed','canceled')", sessionID).
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
	case "succeeded", "failed", "canceled":
		return true
	}
	return false
}

// ── API handlers ─────────────────────────────────────────────────────────────

type taskResponse struct {
	ID              string          `json:"id"`
	UserID          string          `json:"user_id"`
	ConversationID  string          `json:"conversation_id"`
	PluginSessionID *string         `json:"plugin_session_id,omitempty"`
	TaskType        string          `json:"task_type"`
	Title           *string         `json:"title,omitempty"`
	Status          string          `json:"status"`
	ScheduleID      *string         `json:"schedule_id,omitempty"`
	ProgressJSON    json.RawMessage `json:"progress,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
}

func toResponse(t orm.TaskCenterTask) taskResponse {
	return taskResponse{
		ID:              t.ID,
		UserID:          t.UserID,
		ConversationID:  t.ConversationID,
		PluginSessionID: t.PluginSessionID,
		TaskType:        t.TaskType,
		Title:           t.Title,
		Status:          t.Status,
		ScheduleID:      t.ScheduleID,
		ProgressJSON:    t.ProgressJSON,
		CreatedAt:       t.CreatedAt,
		UpdatedAt:       t.UpdatedAt,
		FinishedAt:      t.FinishedAt,
	}
}

// ListTasks handles GET /tasks
// Query params: status, task_type, page (1-based), page_size.
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
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	query := db.WithContext(r.Context()).Where("user_id = ?", userID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if taskType != "" {
		query = query.Where("task_type = ?", taskType)
	}

	var total int64
	_ = query.Model(&orm.TaskCenterTask{}).Count(&total)

	var rows []orm.TaskCenterTask
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&rows).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]taskResponse, 0, len(rows))
	for _, t := range rows {
		items = append(items, toResponse(t))
	}
	common.ReplyJSON(w, map[string]any{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// GetTaskByID handles GET /tasks/{task_id}
func GetTaskByID(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	id := strings.TrimPrefix(r.URL.Path, "/tasks/")
	id = strings.Split(id, "/")[0]
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
	common.ReplyJSON(w, toResponse(t))
}

// CancelTaskByID handles POST /tasks/{task_id}:cancel
func CancelTaskByID(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	// Path: /tasks/{task_id}:cancel
	path := strings.TrimPrefix(r.URL.Path, "/tasks/")
	id := strings.TrimSuffix(path, ":cancel")
	id = strings.Split(id, ":")[0]

	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "db unavailable", http.StatusInternalServerError)
		return
	}
	if err := CancelTask(r.Context(), db, userID, id); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, nil)
}
