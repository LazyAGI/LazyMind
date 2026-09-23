package workflow

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/store"
)

const externalWorkflowTaskJobType = "workflow.external_task"

// Recover pre-existing tasks and report exhausted worker failures after restarts.
func StartExternalWorkflowTaskRecovery(ctx context.Context, db *gorm.DB) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			if err := recoverExternalWorkflowTasks(ctx, db); err != nil && ctx.Err() == nil {
				log.Logger.Warn().Err(err).Msg("external workflow task recovery failed")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func recoverExternalWorkflowTasks(ctx context.Context, db *gorm.DB) error {
	var tasks []orm.ExternalAgentWorkflowTask
	if err := db.WithContext(ctx).Where("status IN ?", []string{externalTaskStatusQueued, externalTaskStatusConverting, externalTaskStatusRunning}).Find(&tasks).Error; err != nil {
		return err
	}
	for _, task := range tasks {
		var latest []orm.AsyncJob
		if err := db.WithContext(ctx).Where("job_type=? AND resource_id=?", externalWorkflowTaskJobType, task.ID).Order("created_at DESC").Limit(1).Find(&latest).Error; err != nil {
			return err
		}
		tick := "start"
		if len(latest) > 0 {
			job := latest[0]
			if job.Status == string(asyncjob.StatusPending) || job.Status == string(asyncjob.StatusRunning) {
				continue
			}
			if job.Status == string(asyncjob.StatusFailed) || job.Status == string(asyncjob.StatusCanceled) {
				updated := failExternalTask(db.WithContext(ctx), task, "TASK_WORKER_FAILED", "后台任务未能完成。", "请进入 LazyMind 查看执行记录；确认已有步骤结果后再决定是否重新发起任务。")
				if updated.ErrorCode == "TASK_STATE_WRITE_FAILED" {
					return fmt.Errorf("persist external task: %s", updated.ErrorMessage)
				}
				continue
			}
			tick = job.ID
		}
		if _, err := asyncjob.Enqueue(ctx, db, externalTaskJobRequest(task, tick)); err != nil {
			return err
		}
	}
	// A handoff never resumes steps automatically. Observe completion performed
	// in LazyMind so a later result query can still return the user's outputs.
	var completedHandoffs []orm.ExternalAgentWorkflowTask
	if err := db.WithContext(ctx).Where("status=? AND session_id IN (?)", externalTaskStatusWaitingUserAction,
		db.Model(&orm.WorkflowSession{}).Select("id").Where("status=?", "completed")).Find(&completedHandoffs).Error; err != nil {
		return err
	}
	for _, task := range completedHandoffs {
		updated := completeExternalTask(ctx, db.WithContext(ctx), task)
		if updated.ErrorCode == "TASK_STATE_WRITE_FAILED" {
			return fmt.Errorf("collect handed-off task: %s", updated.ErrorMessage)
		}
	}
	return nil
}

func RegisterExternalWorkflowTaskJob() {
	asyncjob.Register(externalWorkflowTaskJobType, handleExternalWorkflowTaskJob)
}

func externalTaskJobRequest(task orm.ExternalAgentWorkflowTask, tick string) asyncjob.EnqueueRequest {
	return asyncjob.EnqueueRequest{
		JobType: externalWorkflowTaskJobType, ResourceType: "external_workflow_task", ResourceID: task.ID,
		IdempotencyKey: task.ID + ":" + tick, CreateUserID: task.OwnerUserID, MaxAttempts: 3,
	}
}

// Each tick releases the worker before waiting for generation or execution.
// Scheduling a successor with the current job ID makes retries idempotent.
func handleExternalWorkflowTaskJob(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
	db := store.DB().WithContext(ctx)
	var task orm.ExternalAgentWorkflowTask
	if err := db.WithContext(ctx).Where("id=? AND owner_user_id=?", job.ResourceID, job.CreateUserID).First(&task).Error; err != nil {
		return asyncjob.Result{}, err
	}
	task = reconcileExternalAgentWorkflowTask(ctx, db, task)
	if ctx.Err() != nil {
		return asyncjob.Result{}, ctx.Err()
	}
	if task.ErrorCode == "TASK_STATE_WRITE_FAILED" {
		return asyncjob.Result{}, fmt.Errorf("persist external task: %s", task.ErrorMessage)
	}
	if !externalTaskTerminal(task.Status) && task.Status != externalTaskStatusWaitingUserAction {
		next := externalTaskJobRequest(task, job.ID)
		next.RunAt = time.Now().UTC().Add(3 * time.Second)
		if _, err := asyncjob.Enqueue(ctx, db, next); err != nil {
			return asyncjob.Result{}, err
		}
	}
	return asyncjob.Result{ResultJSON: mustJSON(map[string]any{"task_id": task.ID, "status": task.Status})}, nil
}
