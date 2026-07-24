package resourceupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/skillv2/taskguard"
)

const manualSkillReviewQuantityThreshold = 1

func buildManualSkillReviewSummary(ctx context.Context, db *gorm.DB, userID string, cfg Config, now time.Time) (skillReviewSummaryResponse, error) {
	cfg = normalizeConfig(cfg)
	userID = strings.TrimSpace(userID)
	now = now.UTC()
	state, found, err := settleManualSkillReviewSummaryState(ctx, db, userID, cfg, now)
	if err != nil {
		return skillReviewSummaryResponse{}, err
	}
	if !found {
		state = orm.SkillReviewSchedulerState{LastWindowEnd: now.Add(-cfg.MaxWindow)}
	}
	start, end := manualSkillReviewWindowFromState(state, cfg, now)
	stats, err := CountSkillReviewHistoryStats(ctx, db, userID, start, end, cfg.MinUserTurns, cfg.MinToolTurns)
	if err != nil {
		return skillReviewSummaryResponse{}, err
	}
	task, runningRequestID, err := findRunningSkillReviewTask(ctx, db, userID)
	if err != nil {
		return skillReviewSummaryResponse{}, err
	}
	return manualSkillReviewSummaryFromStats(stats, cfg, start, end, task, runningRequestID), nil
}

func settleManualSkillReviewSummaryState(ctx context.Context, db *gorm.DB, userID string, cfg Config, now time.Time) (orm.SkillReviewSchedulerState, bool, error) {
	var state orm.SkillReviewSchedulerState
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := withUpdateLock(tx).WithContext(ctx).
			Where("user_id = ?", userID).
			Take(&state).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if strings.TrimSpace(state.ActiveTaskID) == "" {
			return nil
		}
		err = releaseManualSkillReviewActiveTask(ctx, tx, &state, cfg, now)
		if errors.Is(err, errReviewConflict) {
			return nil
		}
		return err
	})
	if err != nil {
		return orm.SkillReviewSchedulerState{}, false, err
	}
	if strings.TrimSpace(state.UserID) == "" {
		return orm.SkillReviewSchedulerState{}, false, nil
	}
	return state, true, nil
}

func createManualSkillReviewTask(ctx context.Context, db *gorm.DB, userID string, cfg Config, now time.Time) (orm.ResourceUpdateTask, skillReviewSummaryResponse, error) {
	cfg = normalizeConfig(cfg)
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return orm.ResourceUpdateTask{}, skillReviewSummaryResponse{}, errReviewInvalid
	}
	now = now.UTC()

	var outTask orm.ResourceUpdateTask
	var outSummary skillReviewSummaryResponse
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		decision, err := taskguard.EvaluateSkillOperation(ctx, tx, nil, taskguard.SkillOperationRequest{
			UserID:        userID,
			Operation:     taskguard.TriggerSkillReview,
			TriggerSource: "manual",
		})
		if err != nil {
			return err
		}
		if !decision.Allowed {
			return fmt.Errorf("%w: %s", errReviewConflict, decision.Message)
		}
		if task, _, err := findRunningSkillReviewTask(ctx, withUpdateLock(tx), userID); err != nil {
			return err
		} else if strings.TrimSpace(task.ID) != "" {
			return fmt.Errorf("%w: skill review task is already running", errReviewConflict)
		}

		state, err := lockOrCreateManualSkillReviewState(ctx, tx, userID, cfg, now)
		if err != nil {
			return err
		}
		if err := releaseManualSkillReviewActiveTask(ctx, tx, &state, cfg, now); err != nil {
			return err
		}

		start, end := manualSkillReviewWindowFromState(state, cfg, now)
		if !end.After(start) {
			return fmt.Errorf("%w: skill review window is invalid", errReviewInvalid)
		}
		stats, err := CountSkillReviewHistoryStats(ctx, tx, userID, start, end, cfg.MinUserTurns, cfg.MinToolTurns)
		if err != nil {
			return err
		}
		if stats.QualifiedSessionCount < manualSkillReviewQuantityThreshold {
			return fmt.Errorf("%w: no depositable skill review conversations", errReviewInvalid)
		}

		task, requestID, err := newManualSkillGenerateTask(userID, stats, start, end, now)
		if err != nil {
			return err
		}
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		if err := tx.Model(&orm.SkillReviewSchedulerState{}).
			Where("user_id = ?", userID).
			Updates(map[string]any{
				"active_task_id":          task.ID,
				"last_quantity_check_at":  now,
				"last_preflight_check_at": now,
				"locked_by":               "",
				"locked_until":            nil,
				"last_error_code":         "",
				"last_error_message":      "",
				"updated_at":              now,
			}).Error; err != nil {
			return err
		}
		outTask = task
		outSummary = manualSkillReviewSummaryFromStats(stats, cfg, start, end, task, requestID)
		return nil
	})
	return outTask, outSummary, err
}

func lockOrCreateManualSkillReviewState(ctx context.Context, tx *gorm.DB, userID string, cfg Config, now time.Time) (orm.SkillReviewSchedulerState, error) {
	var state orm.SkillReviewSchedulerState
	err := withUpdateLock(tx).WithContext(ctx).
		Where("user_id = ?", userID).
		Take(&state).Error
	if err == nil {
		return state, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return orm.SkillReviewSchedulerState{}, err
	}
	firstStage := NewScheduler(tx, cfg, "manual-skill-review").stageFor(0)
	state = orm.SkillReviewSchedulerState{
		UserID:        userID,
		LastWindowEnd: now.Add(-cfg.MaxWindow),
		NextRunAt:     now.Add(firstStage.Interval),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := tx.WithContext(ctx).Create(&state).Error; err != nil {
		return orm.SkillReviewSchedulerState{}, err
	}
	return state, nil
}

func releaseManualSkillReviewActiveTask(ctx context.Context, tx *gorm.DB, state *orm.SkillReviewSchedulerState, cfg Config, now time.Time) error {
	activeTaskID := strings.TrimSpace(state.ActiveTaskID)
	if activeTaskID == "" {
		return nil
	}

	var task orm.ResourceUpdateTask
	err := withUpdateLock(tx).WithContext(ctx).Where("id = ?", activeTaskID).Take(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		state.ActiveTaskID = ""
		return tx.Model(&orm.SkillReviewSchedulerState{}).
			Where("user_id = ?", state.UserID).
			Updates(map[string]any{
				"active_task_id":     "",
				"last_error_code":    "active_task_not_found",
				"last_error_message": activeTaskID,
				"locked_by":          "",
				"locked_until":       nil,
				"updated_at":         now,
			}).Error
	}
	if err != nil {
		return err
	}

	switch task.Status {
	case orm.ResourceUpdateTaskStatusPending, orm.ResourceUpdateTaskStatusRunning:
		return fmt.Errorf("%w: skill review task is already running", errReviewConflict)
	case orm.ResourceUpdateTaskStatusDone:
		return NewScheduler(tx, cfg, "manual-skill-review").settleSuccessfulTask(tx, state, task, now, true)
	case orm.ResourceUpdateTaskStatusSkipped, orm.ResourceUpdateTaskStatusFailed:
		state.ActiveTaskID = ""
		return tx.Model(&orm.SkillReviewSchedulerState{}).
			Where("user_id = ?", state.UserID).
			Updates(map[string]any{
				"active_task_id":     "",
				"last_error_code":    task.ErrorCode,
				"last_error_message": task.ErrorMessage,
				"locked_by":          "",
				"locked_until":       nil,
				"updated_at":         now,
			}).Error
	default:
		return fmt.Errorf("%w: skill review task is awaiting scheduler settlement", errReviewConflict)
	}
}

func manualSkillReviewWindowFromState(state orm.SkillReviewSchedulerState, cfg Config, now time.Time) (time.Time, time.Time) {
	end := now.UTC()
	start := state.LastWindowEnd.UTC()
	if start.IsZero() || start.Before(end.Add(-cfg.MaxWindow)) {
		start = end.Add(-cfg.MaxWindow)
	}
	if start.After(end) {
		start = end
	}
	return start, end
}

func manualSkillReviewSummaryFromStats(stats HistoryStats, cfg Config, start, end time.Time, runningTask orm.ResourceUpdateTask, runningRequestID string) skillReviewSummaryResponse {
	summary := skillReviewSummaryResponse{
		QualifiedSessionCount: stats.QualifiedSessionCount,
		UserTurnCount:         stats.UserTurnCount,
		ToolCallCount:         stats.ToolCallCount,
		MinUserTurns:          cfg.MinUserTurns,
		MinToolTurns:          cfg.MinToolTurns,
		QuantityThreshold:     manualSkillReviewQuantityThreshold,
		WindowStart:           start,
		WindowEnd:             end,
		RunningRequestID:      runningRequestID,
	}
	if strings.TrimSpace(runningTask.ID) != "" {
		task := taskToResponse(runningTask)
		summary.RunningTask = &task
	}
	return summary
}

func findRunningSkillReviewTask(ctx context.Context, db *gorm.DB, userID string) (orm.ResourceUpdateTask, string, error) {
	var task orm.ResourceUpdateTask
	err := db.WithContext(ctx).
		Where(
			"user_id = ? AND task_type = ? AND resource_type = ? AND status IN ?",
			strings.TrimSpace(userID),
			orm.ResourceUpdateTaskTypeGenerateReview,
			orm.ResourceUpdateResourceTypeSkill,
			[]string{orm.ResourceUpdateTaskStatusPending, orm.ResourceUpdateTaskStatusRunning},
		).
		Order("created_at DESC").
		Take(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return orm.ResourceUpdateTask{}, "", nil
	}
	if err != nil {
		return orm.ResourceUpdateTask{}, "", err
	}
	return task, skillTaskRequestID(task), nil
}

func newManualSkillGenerateTask(userID string, stats HistoryStats, start, end, now time.Time) (orm.ResourceUpdateTask, string, error) {
	requestID := newSkillReviewRequestID()
	request := skillGenerateRequestJSON{
		RequestID:                      requestID,
		UserID:                         strings.TrimSpace(userID),
		TriggerReason:                  "manual",
		CandidateUserTurnCount:         stats.UserTurnCount,
		CandidateToolCallCount:         stats.ToolCallCount,
		CandidateQualifiedSessionCount: stats.QualifiedSessionCount,
		QuantityThreshold:              manualSkillReviewQuantityThreshold,
		SchedulerPreflightAt:           formatTaskTime(now),
		StartTime:                      formatTaskTime(start),
		EndTime:                        formatTaskTime(end),
		UserTurnCount:                  stats.UserTurnCount,
		ToolCallCount:                  stats.ToolCallCount,
		QualifiedSessionCount:          stats.QualifiedSessionCount,
		StartPreflightAt:               formatTaskTime(now),
		StartTriggerReason:             "manual",
		SessionIDs:                     stats.QualifiedSessionIDs,
		WindowFrozen:                   true,
	}
	body, err := json.Marshal(request)
	if err != nil {
		return orm.ResourceUpdateTask{}, "", err
	}
	return orm.ResourceUpdateTask{
		ID:           common.GenerateID(),
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeSkill,
		UserID:       strings.TrimSpace(userID),
		ResourceID:   "",
		TriggerType:  orm.ResourceUpdateTriggerTypeManual,
		TriggerID:    fmt.Sprintf("skill_review_manual:%s:%s", strings.TrimSpace(userID), requestID),
		Status:       orm.ResourceUpdateTaskStatusPending,
		RequestJSON:  body,
		NextRunAt:    now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, requestID, nil
}

func skillTaskRequestID(task orm.ResourceUpdateTask) string {
	var request skillGenerateRequestJSON
	if len(task.RequestJSON) == 0 || json.Unmarshal(task.RequestJSON, &request) != nil {
		return ""
	}
	return strings.TrimSpace(request.RequestID)
}
