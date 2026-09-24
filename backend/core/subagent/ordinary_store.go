package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/taskdisplay"
)

var ErrStaleExecution = errors.New("stale task execution")
var ErrPublicEventConflict = errors.New("public event conflict")

type publicStepRecord struct {
	EventID     string                        `json:"event_id"`
	ExecutionID string                        `json:"execution_id"`
	Step        taskdisplay.PublicProcessStep `json:"step"`
}

// beginDisplayExecution runs under the same row lock as launch/resume. Historical
// records stay intact; their execution IDs cannot contaminate the new attempt.
func beginDisplayExecution(ctx context.Context, db *gorm.DB, taskID, executionID string) error {
	result := db.WithContext(ctx).Model(&orm.SubAgentTask{}).Where("id = ? AND status IN ?", taskID, []string{StatusPending, StatusRunning}).Updates(map[string]any{
		"execution_id": executionID, "display_revision": gorm.Expr("display_revision + 1"), "started_at": nil, "finished_at": nil, "sources": orm.RawJSON(`[]`),
		"progress_pct": 0,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTaskTerminal
	}
	return nil
}

func PersistPublicProcessStep(ctx context.Context, db *gorm.DB, taskID, executionID, eventID string, step taskdisplay.PublicProcessStep) (bool, error) {
	if executionID == "" || eventID == "" || len(eventID) > 128 || taskdisplay.ValidateProcessStep(step) != nil {
		return false, taskdisplay.ErrInvalidProcessStep
	}
	accepted := false
	err := taskTransaction(ctx, db, func(tx *gorm.DB) error {
		// UPDATE acquires a portable write lock before reading the current generation.
		if err := tx.Model(&orm.SubAgentTask{}).Where("id = ?", taskID).UpdateColumn("display_revision", gorm.Expr("display_revision")).Error; err != nil {
			return err
		}
		task, err := GetTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if task.ExecutionID != executionID {
			return ErrStaleExecution
		}
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(taskID+"/"+executionID+"/"+eventID)).String()
		var existing orm.SubAgentStep
		if err := tx.Where("id = ?", id).First(&existing).Error; err == nil {
			var record publicStepRecord
			if json.Unmarshal(existing.Content, &record) != nil || !reflect.DeepEqual(record.Step, step) {
				return ErrPublicEventConflict
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if isTerminal(task.Status) {
			return ErrTaskTerminal
		}
		var rows []orm.SubAgentStep
		if err := tx.Where("task_id = ? AND execution_id = ? AND role = ?", taskID, executionID, "process_step").Order("seq ASC").Find(&rows).Error; err != nil {
			return err
		}
		objects := map[string]taskdisplay.PublicProcessStep{}
		for _, row := range rows {
			var record publicStepRecord
			if json.Unmarshal(row.Content, &record) == nil {
				objects[record.Step.StepID] = record.Step
			}
		}
		if previous, ok := objects[step.StepID]; ok {
			if step.Revision <= previous.Revision {
				return ErrPublicEventConflict
			}
			if err := taskdisplay.ValidateTransition(previous, step); err != nil {
				return err
			}
		} else if len(objects) >= taskdisplay.MaxProcessSteps {
			return taskdisplay.ErrInvalidProcessStep
		}
		var maxSeq int
		if err := tx.Model(&orm.SubAgentStep{}).Where("task_id = ?", taskID).Select("COALESCE(MAX(seq), -1)").Scan(&maxSeq).Error; err != nil {
			return err
		}
		content, _ := json.Marshal(publicStepRecord{EventID: eventID, ExecutionID: executionID, Step: step})
		if err := tx.Create(&orm.SubAgentStep{ID: id, TaskID: taskID, ExecutionID: executionID, Seq: maxSeq + 1, Role: "process_step", Content: content, CreatedAt: time.Now().UTC()}).Error; err != nil {
			return err
		}
		if err := tx.Model(task).Updates(map[string]any{"display_revision": gorm.Expr("display_revision + 1"), "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		if err := recordOrdinaryWorkflowChange(ctx, tx, task); err != nil {
			return err
		}
		accepted = true
		return nil
	})
	return accepted, err
}

// routeExecutionEvent fences every locally streamed event, including sources and
// artifacts. Both workspace and unbound runs use the persisted execution ID.
func routeExecutionEvent(ctx context.Context, db *gorm.DB, taskID, executionID string, ev TaskEvent, persist func(*gorm.DB, TaskEvent) (bool, error)) (bool, error) {
	accepted := false
	err := withWorkspaceRunUpdate(ctx, db, taskID, func(tx *gorm.DB) error {
		task, err := GetTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if task.ExecutionID != executionID {
			return ErrStaleExecution
		}
		ev.ExecutionID = executionID
		if role, content := remoteStepContent(ev); role != "" {
			if err := AppendRemoteStep(ctx, tx, taskID, role, content); err != nil {
				return fmt.Errorf("persist task step: %w", err)
			}
		}
		accepted, err = persist(tx, ev)
		return err
	})
	observeTaskEventResult(ctx, accepted, err)
	return accepted, err
}

// recordOrdinaryWorkflowChange shares the workflow's durable cursor without
// exposing process contents to its lifecycle event stream.
func recordOrdinaryWorkflowChange(ctx context.Context, db *gorm.DB, task *orm.SubAgentTask) error {
	if task.AgentType != "workflow_step" {
		return nil
	}
	if !db.Migrator().HasTable(&orm.WorkflowEvent{}) || !db.Migrator().HasTable(&orm.WorkflowSessionStep{}) {
		return nil
	}
	var step orm.WorkflowSessionStep
	if err := db.WithContext(ctx).Where("task_id = ?", task.ID).First(&step).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	var session orm.WorkflowSession
	if err := db.WithContext(ctx).Where("id = ?", step.SessionID).First(&session).Error; err != nil {
		return err
	}
	return db.WithContext(ctx).Create(&orm.WorkflowEvent{SessionID: session.ID, OwnerUserID: session.CreateUserID, ContractVersion: "workflow.v1", EventType: "ordinary.task_changed", EntityID: step.ID, StateVersion: session.StateVersion, PayloadJSON: json.RawMessage(`{}`), CreatedAt: time.Now().UTC()}).Error
}

// taskTransaction respects an existing execution/lease transaction. Beginning a
// second SQLite IMMEDIATE transaction would escape its lock or deadlock the gate.
func taskTransaction(ctx context.Context, db *gorm.DB, fn func(*gorm.DB) error) error {
	if _, ok := db.Statement.ConnPool.(gorm.TxCommitter); ok {
		return fn(db.WithContext(ctx))
	}
	return common.ImmediateTransactionWithSQLiteBusyRetry(ctx, db, fn)
}

// ingestRemoteTaskEvent repeats lease validation while holding the attempt row
// lock, then locks the task before persisting. Authorization before body decoding
// alone cannot fence a lease replaced while a request is in flight.
func ingestRemoteTaskEvent(ctx context.Context, db *gorm.DB, event TaskEvent, lease string) (bool, error) {
	accepted := false
	err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		locked := tx.Model(&orm.WorkflowSessionStep{}).Where("task_id = ? AND lease_token = ?", event.TaskID, lease).
			Where("(lease_expires_at >= ? AND status IN ?) OR status IN ?", time.Now().UTC(), []string{"claimed", "running"}, []string{"succeeded", "failed", "cancelled", "interrupted"}).
			UpdateColumn("lease_token", gorm.Expr("lease_token"))
		if locked.Error != nil {
			return locked.Error
		}
		if locked.RowsAffected != 1 {
			return ErrStaleExecution
		}
		if err := tx.Model(&orm.SubAgentTask{}).Where("id = ?", event.TaskID).UpdateColumn("display_revision", gorm.Expr("display_revision")).Error; err != nil {
			return err
		}
		task, err := GetTask(ctx, tx, event.TaskID)
		if err != nil {
			return err
		}
		if event.ExecutionID != "" && event.ExecutionID != task.ExecutionID {
			return ErrStaleExecution
		}
		event.ExecutionID = task.ExecutionID
		if role, content := remoteStepContent(event); role != "" {
			if err := AppendRemoteStep(ctx, tx, event.TaskID, role, content); err != nil {
				return err
			}
		}
		accepted, err = persistTaskEvent(ctx, tx, event)
		return err
	})
	observeTaskEventResult(ctx, accepted, err)
	return accepted, err
}

func observeTaskEventResult(ctx context.Context, accepted bool, err error) {
	if err != nil {
		taskdisplay.Observe(ctx, taskdisplay.EventRejected, 0, 1)
	} else if !accepted {
		taskdisplay.Observe(ctx, taskdisplay.EventDuplicate, 0, 1)
	}
}
