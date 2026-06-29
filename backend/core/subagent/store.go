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
	TaskID             string
	ConversationID     string
	TriggerHistoryID   string
	AgentType          string
	Title              string
	Objective          string
	Mode               string
	Params             json.RawMessage
	InputArtifactKeys  json.RawMessage
	OutputArtifactKeys json.RawMessage
	WorkspacePath      string
	CreateUserID       string
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
			ID:                 in.TaskID,
			ConversationID:     in.ConversationID,
			TriggerHistoryID:   in.TriggerHistoryID,
			SeqInConversation:  maxSeq + 1,
			AgentType:          in.AgentType,
			Title:              in.Title,
			Objective:          in.Objective,
			Params:             normalizeJSON(in.Params, "{}"),
			Mode:               in.Mode,
			Status:             StatusPending,
			ProgressPct:        0,
			LastHeartbeat:      now,
			WorkspacePath:      in.WorkspacePath,
			InputArtifactKeys:  normalizeJSON(in.InputArtifactKeys, "[]"),
			OutputArtifactKeys: normalizeJSON(in.OutputArtifactKeys, "[]"),
			CreateUserID:       in.CreateUserID,
			CreatedAt:          now,
			UpdatedAt:          now,
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

// UpdateFinalStatus marks a terminal status (succeeded/failed/interrupted/canceled) with optional summary.
// "failed" is never allowed to overwrite an already-terminal interrupted or succeeded status: a race
// between StopActivePluginSession (which writes interrupted) and the SSE-EOF handler (which calls
// routeError → UpdateFinalStatus with failed) would otherwise silently downgrade interrupted → failed,
// breaking the checkpoint-resume path and causing the frontend to display "failed" after a user stop.
func UpdateFinalStatus(ctx context.Context, db *gorm.DB, taskID, status, summary string) error {
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
	q := db.WithContext(ctx).Model(&orm.SubAgentTask{}).Where("id = ?", taskID)
	if status == StatusFailed {
		// Do not downgrade a terminal interrupted/succeeded status to failed.
		q = q.Where("status NOT IN ?", []string{StatusInterrupted, StatusSucceeded})
	}
	return q.Updates(updates).Error
}

// SaveArtifact appends one artifact row for a task.
func SaveArtifact(ctx context.Context, db *gorm.DB, taskID, key, contentType string, value json.RawMessage, seq int) error {
	now := time.Now().UTC()
	row := &orm.SubAgentArtifact{
		ID:          "saa_" + common.GenerateID(),
		TaskID:      taskID,
		ArtifactKey: key,
		ContentType: contentType,
		Value:       normalizeJSON(value, "{}"),
		Seq:         seq,
		CreatedAt:   now,
	}
	return db.WithContext(ctx).Create(row).Error
}

// LoadArtifacts returns artifacts for a task ordered by (artifact_key, seq).
func LoadArtifacts(ctx context.Context, db *gorm.DB, taskID string) ([]orm.SubAgentArtifact, error) {
	var rows []orm.SubAgentArtifact
	if err := db.WithContext(ctx).Where("task_id = ?", taskID).
		Order("artifact_key ASC, seq ASC").Find(&rows).Error; err != nil {
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
