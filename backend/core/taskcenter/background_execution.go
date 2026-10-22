package taskcenter

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
)

const backgroundExecutionKey = "_background_execution"

// BackgroundExecutionPart is a server-owned execution identity. Terminal evidence
// is content-free and write-once, independent of the fallible notice projection.
type BackgroundExecutionPart struct {
	HistoryID   string     `json:"history_id"`
	RunID       string     `json:"run_id"`
	Status      string     `json:"status,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}
type backgroundExecution struct {
	Version int                       `json:"version"`
	Parts   []BackgroundExecutionPart `json:"parts"`
}

func readBackgroundExecution(progress orm.RawJSON) (backgroundExecution, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(progress, &fields) != nil {
		return backgroundExecution{}, false
	}
	raw, present := fields[backgroundExecutionKey]
	var execution backgroundExecution
	_ = json.Unmarshal(raw, &execution)
	return execution, present
}
func withBackgroundExecution(progress orm.RawJSON, execution backgroundExecution) orm.RawJSON {
	fields := map[string]json.RawMessage{}
	_ = json.Unmarshal(progress, &fields)
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	fields[backgroundExecutionKey], _ = json.Marshal(execution)
	encoded, _ := json.Marshal(fields)
	return encoded
}

// NewBackgroundExecutionProgress marks even interrupted, unbound admissions as
// scoped. Such rows must never enter the legacy history guessing path.
func NewBackgroundExecutionProgress(progress orm.RawJSON) orm.RawJSON {
	return withBackgroundExecution(progress, backgroundExecution{Version: 1, Parts: []BackgroundExecutionPart{}})
}

func plainBackgroundQuery(db *gorm.DB, conversationID string) *gorm.DB {
	return db.Model(&orm.TaskCenterTask{}).Where("conversation_id = ? AND task_type = 'background_chat' AND archived_at IS NULL", conversationID).
		Where("status NOT IN ?", terminalTaskStatuses).Where("plugin_session_id IS NULL OR plugin_session_id = ''") // workflow-naming: persistence
}
func backgroundOwner(ctx context.Context, db *gorm.DB, conversationID string) (string, error) {
	var conversation orm.Conversation
	err := db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Select("create_user_id").Where("id = ? AND deleted_at IS NULL", conversationID).Take(&conversation).Error
	return conversation.CreateUserID, err
}

// BindBackgroundExecution only accepts the internal task ID passed by the chat
// admission path, and checks it against the server's conversation owner.
func BindBackgroundExecution(ctx context.Context, db *gorm.DB, taskID, conversationID string, parts []BackgroundExecutionPart) error {
	if taskID == "" {
		return nil
	}
	if len(parts) < 1 || len(parts) > 2 {
		return errors.New("invalid background execution parts")
	}
	for i, part := range parts {
		if part.HistoryID == "" || part.RunID == "" || part.Status != "" || part.CompletedAt != nil {
			return errors.New("invalid background execution identity")
		}
		if i > 0 && (part.HistoryID == parts[0].HistoryID || part.RunID == parts[0].RunID) {
			return errors.New("duplicate background execution part")
		}
	}
	return notificationTx(ctx, db, func(tx *gorm.DB) error {
		owner, err := backgroundOwner(ctx, tx, conversationID)
		if err != nil {
			return err
		}
		var task orm.TaskCenterTask
		// Replay may bind a terminal task to its existing identity, but never change it.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ? AND conversation_id = ? AND task_type = 'background_chat' AND archived_at IS NULL", taskID, owner, conversationID).
			Where("plugin_session_id IS NULL OR plugin_session_id = ''").Take(&task).Error; err != nil { // workflow-naming: persistence
			return err
		}
		execution, scoped := readBackgroundExecution(task.ProgressJSON)
		if !scoped || execution.Version != 1 {
			return errors.New("background task is not scoped")
		}
		if len(execution.Parts) > 0 {
			if len(execution.Parts) != len(parts) {
				return errors.New("background execution rebinding rejected")
			}
			for i := range parts {
				if execution.Parts[i].HistoryID != parts[i].HistoryID || execution.Parts[i].RunID != parts[i].RunID {
					return errors.New("background execution rebinding rejected")
				}
			}
			return nil
		}
		if isTerminal(task.Status) {
			return errors.New("background task is terminal")
		}
		execution.Parts = parts
		return tx.Model(&task).Update("progress_json", withBackgroundExecution(task.ProgressJSON, execution)).Error
	})
}

func backgroundAggregate(execution backgroundExecution) (string, time.Time) {
	if execution.Version != 1 || len(execution.Parts) < 1 || len(execution.Parts) > 2 {
		return "", time.Time{}
	}
	status, at := "succeeded", time.Time{}
	for _, part := range execution.Parts {
		if part.HistoryID == "" || part.RunID == "" || part.CompletedAt == nil || part.CompletedAt.IsZero() {
			return "", time.Time{}
		}
		switch part.Status {
		case "failed":
			status = "failed"
		case "canceled":
			if status != "failed" {
				status = "canceled"
			}
		case "succeeded":
		default:
			return "", time.Time{}
		}
		if part.CompletedAt.After(at) {
			at = *part.CompletedAt
		}
	}
	return status, at
}

// CaptureBackgroundExecution must be called inside the authoritative history
// writer checkpoint. It reads the durable owner/run, never a callback payload.
func CaptureBackgroundExecution(ctx context.Context, db *gorm.DB, conversationID, historyID, runID string) error {
	if historyID == "" || runID == "" {
		return nil
	}
	capture := func(tx *gorm.DB) error {
		owner, err := backgroundOwner(ctx, tx, conversationID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		var tasks []orm.TaskCenterTask
		if err := plainBackgroundQuery(tx.WithContext(ctx), conversationID).Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", owner).Order("id").Find(&tasks).Error; err != nil {
			return err
		}
		for _, task := range tasks {
			execution, scoped := readBackgroundExecution(task.ProgressJSON)
			if !scoped || execution.Version != 1 || len(execution.Parts) < 1 || len(execution.Parts) > 2 {
				continue
			}
			status, at, err := exactBackgroundHistory(ctx, tx, conversationID, historyID, runID, len(execution.Parts) == 2)
			if err != nil {
				return err
			}
			if status == "" {
				continue
			}
			for i := range execution.Parts {
				part := &execution.Parts[i]
				if part.HistoryID != historyID || part.RunID != runID || part.Status != "" || part.CompletedAt != nil {
					continue
				}
				if at.Before(task.CreatedAt) {
					continue
				}
				part.Status, part.CompletedAt = status, &at
				if err := tx.Model(&task).Update("progress_json", withBackgroundExecution(task.ProgressJSON, execution)).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}
	return capture(db.WithContext(ctx))
}

func exactBackgroundHistory(ctx context.Context, db *gorm.DB, conversationID, historyID, runID string, dualPart bool) (string, time.Time, error) {
	// A dual primary may also exist in ChatHistory during regeneration or after
	// selection. Its two-part binding makes MultiAnswersChatHistory authoritative.
	if dualPart {
		var h orm.MultiAnswersChatHistory
		err := db.WithContext(ctx).Where("id = ? AND conversation_id = ? AND run_id = ?", historyID, conversationID, runID).Take(&h).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", time.Time{}, nil
		}
		if err != nil {
			return "", time.Time{}, err
		}
		return ordinaryTerminalStatus(h.RunStatus), NotificationHistoryCompletedAt(h.CreateTime, h.UpdateTime, h.Ext), nil
	}
	var h orm.ChatHistory
	err := db.WithContext(ctx).Where("id = ? AND conversation_id = ? AND run_id = ?", historyID, conversationID, runID).Take(&h).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, err
	}
	return ordinaryTerminalStatus(h.RunStatus), NotificationHistoryCompletedAt(h.CreateTime, h.UpdateTime, h.Ext), nil
}

// FinalizeBackgroundExecution projects only tasks expecting this exact identity.
// Checkpointing is committed first so a rejected notice cannot erase completion.
func FinalizeBackgroundExecution(ctx context.Context, db *gorm.DB, conversationID, historyID, runID string) error {
	if err := checkpointBackgroundExecution(ctx, db, conversationID, historyID, runID); err != nil {
		return err
	}
	return reconcileBackgroundTasks(ctx, db, conversationID, "", historyID, runID)
}

func reconcileBackgroundTasks(ctx context.Context, db *gorm.DB, conversationID, taskID, historyID, runID string) error {
	var tasks []orm.TaskCenterTask
	query := plainBackgroundQuery(db.WithContext(ctx), conversationID)
	if taskID != "" {
		query = query.Where("id = ?", taskID)
	}
	if err := query.Find(&tasks).Error; err != nil {
		return err
	}
	for _, task := range tasks {
		execution, scoped := readBackgroundExecution(task.ProgressJSON)
		if scoped && historyID != "" {
			matches := false
			for _, part := range execution.Parts {
				if part.HistoryID == historyID && part.RunID == runID {
					matches = true
				}
			}
			if !matches {
				continue
			}
		}
		status, at, err := ordinaryHistoryCompletion(ctx, db, task)
		if err != nil {
			return err
		}
		if status != "" {
			if err := notificationTx(ctx, db, func(tx *gorm.DB) error {
				// Recheck every lifecycle boundary at the write, including a
				// workflow linked after the completion evidence was read.
				result := plainBackgroundQuery(tx.WithContext(ctx), conversationID).Where("id = ? AND user_id = ?", task.ID, task.UserID).
					Updates(map[string]any{"status": status, "finished_at": at, "updated_at": time.Now().UTC()})
				if result.Error != nil || result.RowsAffected == 0 {
					return result.Error
				}
				return persistTaskNotifications(ctx, tx, task.ID)
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkpointBackgroundExecution(ctx context.Context, db *gorm.DB, conversationID, historyID, runID string) error {
	return notificationTx(ctx, db, func(tx *gorm.DB) error { return CaptureBackgroundExecution(ctx, tx, conversationID, historyID, runID) })
}
