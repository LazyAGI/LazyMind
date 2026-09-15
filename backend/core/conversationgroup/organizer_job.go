package conversationgroup

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
	"strings"
	"time"
)

func RegisterAsyncJobs() { asyncjob.Register(organizerJobType, handleOrganizerJob) }

// StartTerminalJobReconciler keeps organizer runs aligned with asyncjob even
// when a worker reaches a terminal job state before invoking this module's
// handler (for example, an older Core process that does not know the job type).
func StartTerminalJobReconciler(ctx context.Context, db *gorm.DB, interval time.Duration) <-chan struct{} {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if err := ReconcileTerminalJobs(ctx, db); err != nil && ctx.Err() == nil {
				log.Logger.Warn().Err(err).Msg("reconcile conversation organizer terminal jobs failed")
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

func ReconcileTerminalJobs(ctx context.Context, db *gorm.DB) error {
	type terminal struct {
		RunID, JobID, JobStatus, ErrorCode, ErrorMessage string
		StreamJSON                                       json.RawMessage
	}
	var rows []terminal
	if err := db.WithContext(ctx).Table("conversation_organizer_runs r").
		Select("r.id AS run_id,r.job_id,j.status AS job_status,j.error_code,j.error_message,r.stream_json").
		Joins("JOIN async_jobs j ON j.id=r.job_id").
		Where("r.status IN ? AND j.status IN ?", []string{"pending", "running", "applying"}, []string{string(asyncjob.StatusFailed), string(asyncjob.StatusCanceled)}).
		Scan(&rows).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, row := range rows {
		if !settleOrganizerStream(ctx, row.StreamJSON) {
			continue
		}
		status, stage := "failed", "failed"
		if row.JobStatus == string(asyncjob.StatusCanceled) {
			status, stage = "canceled", "canceled"
		}
		res := db.WithContext(ctx).Model(&orm.ConversationOrganizerRun{}).
			Where("id=? AND job_id=? AND status IN ?", row.RunID, row.JobID, []string{"pending", "running", "applying"}).
			Updates(map[string]any{"status": status, "stage": stage, "error_code": row.ErrorCode, "error_message": row.ErrorMessage, "stream_json": settledOrganizerStream(row.StreamJSON), "finished_at": now, "updated_at": now, "version": gorm.Expr("version + 1")})
		if res.Error != nil {
			return res.Error
		}
	}
	return nil
}

func handleOrganizerJob(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
	var payload struct {
		RunID string `json:"run_id"`
	}
	if json.Unmarshal(job.PayloadJSON, &payload) != nil || payload.RunID == "" {
		return asyncjob.Result{Permanent: true, ErrorCode: "invalid_payload"}, errors.New("invalid organizer payload")
	}
	db := store.DB()
	var run orm.ConversationOrganizerRun
	if err := db.WithContext(ctx).Where("id=? AND user_id=?", payload.RunID, job.CreateUserID).Take(&run).Error; err != nil {
		return asyncjob.Result{Permanent: true, ErrorCode: "run_not_found"}, err
	}
	if run.Status == "canceled" || run.Status == "succeeded" || run.Status == "undone" || run.Status == "confirmed" {
		raw, _ := json.Marshal(map[string]any{"run_id": run.ID, "status": run.Status})
		return asyncjob.Result{ResultJSON: raw}, nil
	}
	ctx, cancel := organizerContext(ctx, db, run, job)
	defer cancel()
	stage := "organizing"
	var checkpointStage struct {
		Stage string `json:"stage"`
	}
	if json.Unmarshal(run.CheckpointJSON, &checkpointStage) == nil && checkpointStage.Stage == "final" {
		stage = "final"
	}
	var preparation organizerPreparation
	if len(run.PreparationJSON) > 0 {
		if err := json.Unmarshal(run.PreparationJSON, &preparation); err != nil {
			return failRun(ctx, db, run, job, "invalid_snapshot", err)
		}
		if !preparation.Sealed {
			stage = "preparing"
		}
	}
	if err := ownedRunUpdate(ctx, db, run.ID, job, "pending", map[string]any{"status": "running", "stage": stage}); err != nil {
		if err == errLeaseLost {
			return asyncjob.Result{ErrorCode: "lease_lost"}, err
		}
		return asyncjob.Result{ErrorCode: "update_failed"}, err
	}
	if !settleOrganizerStream(ctx, run.StreamJSON) {
		_ = ownedRunUpdate(ctx, db, run.ID, job, "running", map[string]any{"stage": "canceling"})
		return asyncjob.Result{Permanent: true, ErrorCode: "cancellation_unconfirmed"}, errCancellationUnconfirmed
	}
	if len(run.StreamJSON) > 0 {
		run.StreamJSON = settledOrganizerStream(run.StreamJSON)
		if err := ownedRunUpdate(ctx, db, run.ID, job, "running", map[string]any{"stream_json": run.StreamJSON}); err != nil {
			return asyncjob.Result{ErrorCode: "lease_lost"}, err
		}
	}
	llmConfig, err := modelconfig.LoadLLMConfig(ctx, db, run.UserID)
	if err != nil {
		return failRun(ctx, db, run, job, "model_config", err)
	}
	if !sameOrganizerModelConfig(llmConfig, run.ModelConfigJSON) {
		return failRun(ctx, db, run, job, "model_config_changed", errors.New("conversation organizer model config changed"))
	}
	if err := prepareOrganizer(ctx, db, &run, job, llmConfig); err != nil {
		if ctx.Err() != nil {
			return asyncjob.Result{ErrorCode: "lease_lost"}, ctx.Err()
		}
		return retryOrFailRun(ctx, db, run, job, "preparation_failed", err)
	}
	var snapshot organizerSnapshot
	if err := json.Unmarshal(run.SnapshotJSON, &snapshot); err != nil {
		return failRun(ctx, db, run, job, "invalid_snapshot", err)
	}
	if len(snapshot.Conversations) == 0 {
		proposal := organizerProposal{NewGroups: []proposedNewGroup{}, ExistingGroupAssignments: []proposedAssignment{}, FreeConversationIDs: []string{}}
		raw, _ := json.Marshal(proposal)
		if err := applyProposal(ctx, db, run, job, proposal, raw); err != nil {
			return failRun(ctx, db, run, job, "apply_failed", err)
		}
		result, _ := json.Marshal(map[string]any{"run_id": run.ID, "status": "succeeded"})
		return asyncjob.Result{ResultJSON: result}, nil
	}
	for step := 0; step < 100000; step++ {
		proposal, err := runIncrementalStep(ctx, db, &run, job, snapshot, llmConfig)
		if err != nil {
			if errors.Is(err, errCancellationUnconfirmed) {
				_ = ownedRunUpdate(ctx, db, run.ID, job, "running", map[string]any{"stage": "canceling"})
				return asyncjob.Result{Permanent: true, ErrorCode: "cancellation_unconfirmed"}, err
			}
			if ctx.Err() != nil {
				return asyncjob.Result{ErrorCode: "lease_lost"}, ctx.Err()
			}
			return retryOrFailRun(ctx, db, run, job, "incremental_step_failed", err)
		}
		if reporter != nil {
			if err := reporter.SetProgress(ctx, run.ProgressCurrent, int64(len(snapshot.Conversations))); err != nil {
				return asyncjob.Result{ErrorCode: "lease_lost"}, err
			}
		}
		if proposal == nil {
			continue
		}
		raw, _ := json.Marshal(proposal)
		if err := applyProposal(ctx, db, run, job, *proposal, raw); err != nil {
			return failRun(ctx, db, run, job, "apply_failed", err)
		}
		result, _ := json.Marshal(map[string]any{"run_id": run.ID, "status": "succeeded"})
		return asyncjob.Result{ResultJSON: result}, nil
	}
	return failRun(ctx, db, run, job, "step_limit", errors.New("conversation organizer exceeded step limit"))
}

func ownedRunUpdate(ctx context.Context, db *gorm.DB, runID string, job asyncjob.Job, from string, updates map[string]any) error {
	updates["updated_at"] = time.Now().UTC()
	statuses := []string{from}
	if from == "pending" {
		statuses = []string{"pending", "running"}
	}
	res := db.WithContext(ctx).Model(&orm.ConversationOrganizerRun{}).Where("id=? AND status IN ? AND job_id=?", runID, statuses, job.ID).Where("EXISTS (SELECT 1 FROM async_jobs WHERE id=? AND status=? AND attempt_count=? AND lock_until>?)", job.ID, asyncjob.StatusRunning, job.AttemptCount, time.Now().UTC()).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return errLeaseLost
	}
	return nil
}

func rawOrNil(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}

func sanitizeModelConfig(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if normalized == "api_key" || normalized == "authorization" || strings.Contains(normalized, "secret") {
				continue
			}
			out[key] = sanitizeModelConfig(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeModelConfig(item)
		}
		return out
	default:
		return value
	}
}

func failRun(ctx context.Context, db *gorm.DB, run orm.ConversationOrganizerRun, job asyncjob.Job, code string, err error) (asyncjob.Result, error) {
	now := time.Now().UTC()
	_ = db.WithContext(ctx).Model(&orm.ConversationOrganizerRun{}).Where("id=? AND job_id=? AND status IN ?", run.ID, job.ID, []string{"pending", "running", "applying"}).Where("EXISTS (SELECT 1 FROM async_jobs WHERE id=? AND status=? AND attempt_count=? AND lock_until>?)", job.ID, asyncjob.StatusRunning, job.AttemptCount, now).Updates(map[string]any{"status": "failed", "stage": "failed", "error_code": code, "error_message": err.Error(), "finished_at": now, "updated_at": now, "version": gorm.Expr("version + 1")}).Error
	return asyncjob.Result{Permanent: true, ErrorCode: code}, err
}
func retryOrFailRun(ctx context.Context, db *gorm.DB, run orm.ConversationOrganizerRun, job asyncjob.Job, code string, err error) (asyncjob.Result, error) {
	code, retryable := organizerFailure(code, err)
	var row orm.AsyncJob
	if e := db.WithContext(ctx).Where("id=?", job.ID).Take(&row).Error; retryable && e == nil && job.AttemptCount < row.MaxAttempts {
		return asyncjob.Result{ErrorCode: code}, err
	}
	return failRun(ctx, db, run, job, code, err)
}
