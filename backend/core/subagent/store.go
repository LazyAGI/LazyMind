// Package subagent persists and routes SubAgent task lifecycle (tasks / steps / artifacts).
// DB is the authoritative source; Redis only accelerates live Task SSE streaming.
package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

// Status values for a SubAgent task.
const (
	StatusPending     = "pending"
	StatusRunning     = "running"
	StatusSucceeded   = "succeeded"
	StatusFailed      = "failed"
	StatusInterrupted = "interrupted"
	StatusCanceled    = "canceled"
)

// CreateTaskInput carries the fields needed to create a task record.
// seq_in_conversation is allocated inside the transaction, not provided by the caller.
type CreateTaskInput struct {
	TaskID           string
	ConversationID   string
	TriggerHistoryID string
	AgentType        string
	Title            string
	Objective        string
	Mode             string
	Params           json.RawMessage
	InputSlots       json.RawMessage
	OutputSlots      json.RawMessage
	WorkspacePath    string
	CreateUserID     string
}

// lockConversationSeq serializes seq_in_conversation allocation per conversation.
// PostgreSQL forbids FOR UPDATE together with aggregate functions (MAX), so we take a
// transaction-scoped advisory lock keyed by conversation_id instead. The lock is released
// automatically when the transaction commits or rolls back. Other dialects (e.g. SQLite in
// tests) run serially, so this is a no-op there; uq_sat_conv_seq remains the final safeguard.
func lockConversationSeq(tx *gorm.DB, conversationID string) error {
	if tx.Dialector.Name() == "postgres" {
		return tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", conversationID).Error
	}
	return nil
}

func normalizeJSON(raw json.RawMessage, fallback string) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(fallback)
	}
	return raw
}

// CreateTask inserts a task, allocating seq_in_conversation atomically within a transaction.
func CreateTask(ctx context.Context, db *gorm.DB, in CreateTaskInput) (*orm.SubAgentTask, error) {
	now := time.Now().UTC()
	var task *orm.SubAgentTask
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockConversationSeq(tx, in.ConversationID); err != nil {
			return err
		}
		var maxSeq int
		row := tx.Model(&orm.SubAgentTask{}).
			Select("COALESCE(MAX(seq_in_conversation), 0)").
			Where("conversation_id = ?", in.ConversationID)
		if err := row.Scan(&maxSeq).Error; err != nil {
			return err
		}
		t := &orm.SubAgentTask{
			ID:                in.TaskID,
			ConversationID:    in.ConversationID,
			TriggerHistoryID:  in.TriggerHistoryID,
			SeqInConversation: maxSeq + 1,
			AgentType:         in.AgentType,
			Title:             in.Title,
			Objective:         in.Objective,
			Params:            normalizeJSON(in.Params, "{}"),
			Mode:              in.Mode,
			Status:            StatusPending,
			ProgressPct:       0,
			LastHeartbeat:     now,
			WorkspacePath:     in.WorkspacePath,
			InputSlots:        normalizeJSON(in.InputSlots, "[]"),
			OutputSlots:       normalizeJSON(in.OutputSlots, "[]"),
			CreateUserID:      in.CreateUserID,
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		if err := tx.Create(t).Error; err != nil {
			return err
		}
		task = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return task, nil
}

// GetTask loads a single task by id.
func GetTask(ctx context.Context, db *gorm.DB, taskID string) (*orm.SubAgentTask, error) {
	var t orm.SubAgentTask
	if err := db.WithContext(ctx).Where("id = ?", taskID).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTasksByConversation returns tasks of a conversation ordered by creation sequence.
func ListTasksByConversation(ctx context.Context, db *gorm.DB, convID string) ([]orm.SubAgentTask, error) {
	var tasks []orm.SubAgentTask
	if err := db.WithContext(ctx).Where("conversation_id = ?", convID).
		Order("seq_in_conversation ASC").Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

// ListTasksByConversationForUser returns tasks only when they belong to the
// requesting user. Public Task Center APIs must use this ownership-scoped form.
func ListTasksByConversationForUser(
	ctx context.Context, db *gorm.DB, convID, userID string,
) ([]orm.SubAgentTask, error) {
	var tasks []orm.SubAgentTask
	if err := db.WithContext(ctx).
		Where("conversation_id = ? AND create_user_id = ?", convID, userID).
		Order("seq_in_conversation ASC").Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

// ConversationArtifactRecord combines an artifact with the task metadata needed
// by conversation-level download views.
type ConversationArtifactRecord struct {
	ArtifactID       string          `gorm:"column:artifact_id"`
	TaskID           string          `gorm:"column:task_id"`
	TriggerHistoryID string          `gorm:"column:trigger_history_id"`
	WorkspacePath    string          `gorm:"column:workspace_path"`
	Slot             string          `gorm:"column:slot"`
	ContentType      string          `gorm:"column:content_type"`
	Value            json.RawMessage `gorm:"column:value"`
	Seq              int             `gorm:"column:seq"`
	Caption          *string         `gorm:"column:caption"`
	CreatedAt        time.Time       `gorm:"column:created_at"`
}

// ListArtifactsByConversationForUser loads visible artifacts and their task
// metadata in one query, avoiding one artifact query per SubAgent task.
func ListArtifactsByConversationForUser(
	ctx context.Context, db *gorm.DB, convID, userID string,
) ([]ConversationArtifactRecord, error) {
	var records []ConversationArtifactRecord
	err := db.WithContext(ctx).
		Table("sub_agent_artifacts AS artifact").
		Select(`artifact.id AS artifact_id, artifact.task_id, task.trigger_history_id,
			task.workspace_path, artifact.slot, artifact.content_type, artifact.value,
			artifact.seq, artifact.caption, artifact.created_at`).
		Joins("JOIN sub_agent_tasks AS task ON task.id = artifact.task_id").
		Where("task.conversation_id = ? AND task.create_user_id = ? AND artifact.hidden = ?", convID, userID, false).
		Order("artifact.created_at ASC, artifact.id ASC").
		Scan(&records).Error
	return records, err
}

// UpdateStatus transitions a task to running and refreshes heartbeat.
func UpdateStatus(ctx context.Context, db *gorm.DB, taskID, status string) error {
	now := time.Now().UTC()
	return db.WithContext(ctx).Model(&orm.SubAgentTask{}).Where("id = ?", taskID).
		Updates(map[string]any{
			"status":         status,
			"last_heartbeat": now,
			"updated_at":     now,
		}).Error
}

// AcceptTaskStart applies a runner's task_start event only while the task is
// still launchable. Explicit resume uses UpdateStatus above; a late start event
// must never revive a task that Stop already made terminal.
func AcceptTaskStart(ctx context.Context, db *gorm.DB, taskID string) (bool, error) {
	now := time.Now().UTC()
	result := db.WithContext(ctx).Model(&orm.SubAgentTask{}).
		Where("id = ? AND status IN ?", taskID, []string{StatusPending, StatusRunning}).
		Updates(map[string]any{
			"status":         StatusRunning,
			"last_heartbeat": now,
			"updated_at":     now,
		})
	return result.RowsAffected > 0, result.Error
}

// UpdateProgress writes progress percentage / phase / eta and refreshes heartbeat.
func UpdateProgress(ctx context.Context, db *gorm.DB, taskID string, pct int, phase string, estimatedSec int) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"progress_pct":   pct,
		"current_phase":  phase,
		"last_heartbeat": now,
		"updated_at":     now,
	}
	if estimatedSec > 0 {
		updates["estimated_sec"] = estimatedSec
	}
	return db.WithContext(ctx).Model(&orm.SubAgentTask{}).Where("id = ?", taskID).Updates(updates).Error
}

// UpdateFinalStatus marks a terminal status with optional summary. Terminal state
// is first-writer-wins so late runner frames cannot overwrite an explicit stop.
func UpdateFinalStatus(ctx context.Context, db *gorm.DB, taskID, status, summary string) error {
	_, err := AcceptFinalStatus(ctx, db, taskID, status, summary)
	return err
}

// AcceptFinalStatus makes terminal task state first-writer-wins. This prevents
// a delayed succeeded/error frame from overwriting an explicit user stop.
func AcceptFinalStatus(
	ctx context.Context,
	db *gorm.DB,
	taskID, status, summary string,
) (bool, error) {
	now := time.Now().UTC()
	updates := map[string]any{
		"status":         status,
		"summary":        summary,
		"last_heartbeat": now,
		"updated_at":     now,
	}
	if status == StatusSucceeded {
		updates["progress_pct"] = 100
	}
	terminal := []string{StatusSucceeded, StatusFailed, StatusInterrupted, StatusCanceled}
	result := db.WithContext(ctx).Model(&orm.SubAgentTask{}).
		Where("id = ? AND (status NOT IN ? OR status = ?)", taskID, terminal, status).
		Updates(updates)
	return result.RowsAffected > 0, result.Error
}

// SaveArtifact appends one artifact row for a task.
func SaveArtifact(ctx context.Context, db *gorm.DB, taskID, key, contentType string, value json.RawMessage, seq int) error {
	now := time.Now().UTC()
	row := &orm.SubAgentArtifact{
		ID:          "saa_" + common.GenerateID(),
		TaskID:      taskID,
		Slot:        key,
		ContentType: contentType,
		Value:       normalizeJSON(value, "{}"),
		Seq:         seq,
		CreatedAt:   now,
	}
	return db.WithContext(ctx).Create(row).Error
}

// LoadArtifacts returns artifacts for a task ordered by (slot, seq).
func LoadArtifacts(ctx context.Context, db *gorm.DB, taskID string) ([]orm.SubAgentArtifact, error) {
	var rows []orm.SubAgentArtifact
	if err := db.WithContext(ctx).Where("task_id = ?", taskID).
		Order("slot ASC, seq ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// CountSteps returns the number of persisted ReAct steps for a task.
func CountSteps(ctx context.Context, db *gorm.DB, taskID string) (int64, error) {
	var n int64
	if err := db.WithContext(ctx).Model(&orm.SubAgentStep{}).Where("task_id = ?", taskID).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// CountByConversation returns how many tasks exist for a conversation (drives has_subagents).
func CountByConversation(ctx context.Context, db *gorm.DB, convID string) (int64, error) {
	var n int64
	if err := db.WithContext(ctx).Model(&orm.SubAgentTask{}).Where("conversation_id = ?", convID).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// MarkInterrupted flags running tasks whose heartbeat is older than maxAge as interrupted.
func MarkInterrupted(ctx context.Context, db *gorm.DB, maxAge time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-maxAge)
	res := db.WithContext(ctx).Model(&orm.SubAgentTask{}).
		Where("status = ? AND last_heartbeat < ?", StatusRunning, cutoff).
		Updates(map[string]any{"status": StatusInterrupted, "updated_at": time.Now().UTC()})
	return res.RowsAffected, res.Error
}

// IsNotFound reports whether the error is a gorm record-not-found error.
func IsNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}

// LoadSteps returns all steps for a task ordered by seq ascending.
func LoadSteps(ctx context.Context, db *gorm.DB, taskID string) ([]orm.SubAgentStep, error) {
	var steps []orm.SubAgentStep
	err := db.WithContext(ctx).
		Where("task_id = ?", taskID).
		Order("seq asc").
		Find(&steps).Error
	return steps, err
}
