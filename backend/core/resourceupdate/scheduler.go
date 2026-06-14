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
)

type Scheduler struct {
	db       *gorm.DB
	cfg      Config
	workerID string
	clock    clockFunc
}

func NewScheduler(db *gorm.DB, cfg Config, workerID string) *Scheduler {
	cfg = normalizeConfig(cfg)
	if strings.TrimSpace(workerID) == "" {
		workerID = defaultWorkerID("resourceupdate-scheduler")
	}
	return &Scheduler{
		db:       db,
		cfg:      cfg,
		workerID: workerID,
		clock:    time.Now,
	}
}

func (s *Scheduler) RunOnce(ctx context.Context) (SchedulerTickResult, error) {
	var result SchedulerTickResult
	if s == nil || s.db == nil {
		return result, errors.New("resource update scheduler db is nil")
	}
	now := s.clock().UTC()
	seeded, err := s.seedStates(ctx, now)
	if err != nil {
		return result, err
	}
	result.SeededStates = seeded

	var states []orm.SkillReviewSchedulerState
	if err := s.db.WithContext(ctx).
		Where("(locked_until IS NULL OR locked_until < ?) AND (next_run_at <= ? OR last_quantity_check_at IS NULL OR last_quantity_check_at <= ? OR TRIM(COALESCE(active_task_id, '')) <> '')", now, now, now.Add(-s.cfg.QuantityCheckInterval)).
		Order("next_run_at ASC, last_quantity_check_at ASC").
		Limit(s.cfg.SchedulerBatchSize).
		Find(&states).Error; err != nil {
		return result, err
	}
	for _, state := range states {
		claimed, err := s.claimState(ctx, state.UserID, now)
		if err != nil {
			return result, err
		}
		if !claimed {
			continue
		}
		result.ClaimedStates++
		created, skipped, err := s.processClaimedState(ctx, state.UserID, now)
		if err != nil {
			return result, err
		}
		if created {
			result.CreatedTasks++
		}
		if skipped {
			result.SkippedStates++
		}
	}
	return result, nil
}

func (s *Scheduler) seedStates(ctx context.Context, now time.Time) (int, error) {
	var userIDs []string
	if err := s.db.WithContext(ctx).
		Table("conversations").
		Distinct("create_user_id").
		Where("TRIM(COALESCE(create_user_id, '')) <> '' AND deleted_at IS NULL").
		Pluck("create_user_id", &userIDs).Error; err != nil {
		return 0, err
	}
	if len(userIDs) == 0 {
		return 0, nil
	}
	firstStage := s.stageFor(0)
	states := make([]orm.SkillReviewSchedulerState, 0, len(userIDs))
	for _, userID := range userIDs {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		states = append(states, orm.SkillReviewSchedulerState{
			UserID:        userID,
			LastWindowEnd: now,
			NextRunAt:     now.Add(firstStage.Interval),
			CreatedAt:     now,
			UpdatedAt:     now,
		})
	}
	if len(states) == 0 {
		return 0, nil
	}
	tx := s.db.WithContext(ctx).Clauses(clauseOnConflictDoNothing()).Create(&states)
	if tx.Error == nil && tx.RowsAffected > 0 {
		resourceUpdateInfo(logEventSchedulerSeeded).
			Int64("seeded", tx.RowsAffected).
			Msg(logEventSchedulerSeeded)
	}
	return int(tx.RowsAffected), tx.Error
}

func (s *Scheduler) claimState(ctx context.Context, userID string, now time.Time) (bool, error) {
	lockUntil := now.Add(s.cfg.SchedulerLockTTL)
	tx := s.db.WithContext(ctx).
		Model(&orm.SkillReviewSchedulerState{}).
		Where("user_id = ? AND (locked_until IS NULL OR locked_until < ?) AND (next_run_at <= ? OR last_quantity_check_at IS NULL OR last_quantity_check_at <= ? OR TRIM(COALESCE(active_task_id, '')) <> '')", userID, now, now, now.Add(-s.cfg.QuantityCheckInterval)).
		Updates(map[string]any{
			"locked_by":    s.workerID,
			"locked_until": lockUntil,
			"updated_at":   now,
		})
	return tx.RowsAffected == 1, tx.Error
}

func (s *Scheduler) processClaimedState(ctx context.Context, userID string, now time.Time) (bool, bool, error) {
	var created bool
	var skipped bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state orm.SkillReviewSchedulerState
		if err := withUpdateLock(tx).
			Where("user_id = ?", userID).
			Take(&state).Error; err != nil {
			return err
		}
		if state.LockedBy != s.workerID {
			return nil
		}

		settled, active, err := s.settleActiveTask(ctx, tx, &state, now)
		if err != nil {
			return err
		}
		if active {
			resourceUpdateInfo(logEventSchedulerActive).
				Str("user_id", state.UserID).
				Str("active_task_id", state.ActiveTaskID).
				Msg(logEventSchedulerActive)
			return s.releaseStateLock(tx, state.UserID, now)
		}
		if settled {
			return nil
		}

		staleDue := !now.Before(state.NextRunAt)
		quantityDue := state.LastQuantityCheckAt == nil || !now.Before(state.LastQuantityCheckAt.Add(s.cfg.QuantityCheckInterval))
		if !staleDue && !quantityDue {
			resourceUpdateInfo(logEventSchedulerNotDue).
				Str("user_id", state.UserID).
				Time("next_run_at", state.NextRunAt).
				Time("last_quantity_check_at", timeOrZero(state.LastQuantityCheckAt)).
				Time("next_quantity_check_at", timeOrZero(addTimePtr(state.LastQuantityCheckAt, s.cfg.QuantityCheckInterval))).
				Msg(logEventSchedulerNotDue)
			return s.releaseStateLock(tx, state.UserID, now)
		}
		if state.LastAcceptedAt != nil && now.Before(state.LastAcceptedAt.Add(s.cfg.MinInterval)) {
			resourceUpdateInfo(logEventSchedulerMinInterval).
				Str("user_id", state.UserID).
				Time("last_accepted_at", *state.LastAcceptedAt).
				Time("next_allowed_at", state.LastAcceptedAt.Add(s.cfg.MinInterval)).
				Dur("min_interval", s.cfg.MinInterval).
				Msg(logEventSchedulerMinInterval)
			return s.updateState(tx, state.UserID, map[string]any{
				"last_quantity_check_at": now,
				"locked_by":              "",
				"locked_until":           nil,
				"updated_at":             now,
			})
		}

		start, end := s.nextWindow(&state, now)
		if !end.After(start) {
			resourceUpdateInfo(logEventSchedulerSkipped).
				Str("user_id", state.UserID).
				Str("reason", "skill_review_invalid_window").
				Time("start_time", start).
				Time("end_time", end).
				Msg(logEventSchedulerSkipped)
			return s.releaseStateLock(tx, state.UserID, now)
		}
		if end.Sub(start) > s.cfg.MaxWindow {
			skipped = true
			resourceUpdateInfo(logEventSchedulerSkipped).
				Str("user_id", state.UserID).
				Str("reason", "skill_review_window_too_old").
				Time("start_time", start).
				Time("end_time", end).
				Dur("window", end.Sub(start)).
				Msg(logEventSchedulerSkipped)
			return s.updateState(tx, state.UserID, map[string]any{
				"last_preflight_check_at": now,
				"last_error_code":         "skill_review_window_too_old",
				"last_error_message":      "skill review window exceeds max window",
				"locked_by":               "",
				"locked_until":            nil,
				"updated_at":              now,
			})
		}
		stats, err := CountSkillReviewHistoryStats(ctx, tx, state.UserID, start, end)
		if err != nil {
			return err
		}
		if stats.UserTurnCount < s.cfg.MinUserTurns || stats.ToolCallCount < s.cfg.MinToolTurns {
			skipped = true
			resourceUpdateInfo(logEventSchedulerSkipped).
				Str("user_id", state.UserID).
				Str("reason", "skill_review_history_threshold_not_reached").
				Int("user_turn_count", stats.UserTurnCount).
				Int("tool_call_count", stats.ToolCallCount).
				Int("min_user_turns", s.cfg.MinUserTurns).
				Int("min_tool_turns", s.cfg.MinToolTurns).
				Time("start_time", start).
				Time("end_time", end).
				Msg(logEventSchedulerSkipped)
			return s.updateState(tx, state.UserID, map[string]any{
				"last_quantity_check_at":  now,
				"last_preflight_check_at": now,
				"locked_by":               "",
				"locked_until":            nil,
				"updated_at":              now,
			})
		}

		reason := "quantity"
		if staleDue {
			reason = "stale"
		}
		task, err := newSkillGenerateTask(state.UserID, reason, stats, now)
		if err != nil {
			return err
		}
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		created = true
		resourceUpdateInfo(logEventSchedulerTaskCreated).
			Str("task_id", task.ID).
			Str("user_id", task.UserID).
			Str("trigger_reason", reason).
			Str("trigger_id", task.TriggerID).
			Int("candidate_user_turn_count", stats.UserTurnCount).
			Int("candidate_tool_call_count", stats.ToolCallCount).
			Time("candidate_start_time", start).
			Time("candidate_end_time", end).
			Msg(logEventSchedulerTaskCreated)
		return s.updateState(tx, state.UserID, map[string]any{
			"active_task_id":          task.ID,
			"last_quantity_check_at":  now,
			"last_preflight_check_at": now,
			"locked_by":               "",
			"locked_until":            nil,
			"last_error_code":         "",
			"last_error_message":      "",
			"updated_at":              now,
		})
	})
	return created, skipped, err
}

func (s *Scheduler) settleActiveTask(ctx context.Context, tx *gorm.DB, state *orm.SkillReviewSchedulerState, now time.Time) (bool, bool, error) {
	activeTaskID := strings.TrimSpace(state.ActiveTaskID)
	if activeTaskID == "" {
		return false, false, nil
	}
	var task orm.ResourceUpdateTask
	err := tx.WithContext(ctx).Where("id = ?", activeTaskID).Take(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		resourceUpdateWarn(logEventSchedulerSettled, err).
			Str("user_id", state.UserID).
			Str("active_task_id", activeTaskID).
			Str("reason", "active_task_not_found").
			Msg(logEventSchedulerSettled)
		state.ActiveTaskID = ""
		return true, false, s.updateState(tx, state.UserID, map[string]any{
			"active_task_id":     "",
			"next_run_at":        now.Add(s.cfg.SchedulerRetryDelay),
			"last_error_code":    "active_task_not_found",
			"last_error_message": activeTaskID,
			"locked_by":          "",
			"locked_until":       nil,
			"updated_at":         now,
		})
	}
	if err != nil {
		return false, false, err
	}
	switch task.Status {
	case orm.ResourceUpdateTaskStatusPending, orm.ResourceUpdateTaskStatusRunning:
		return false, true, nil
	case orm.ResourceUpdateTaskStatusDone:
		resourceUpdateInfo(logEventSchedulerSettled).
			Str("user_id", state.UserID).
			Str("active_task_id", activeTaskID).
			Str("task_status", task.Status).
			Str("reason", "accepted").
			Msg(logEventSchedulerSettled)
		return true, false, s.settleSuccessfulTask(tx, state, task, now, true)
	case orm.ResourceUpdateTaskStatusSkipped:
		resourceUpdateInfo(logEventSchedulerSettled).
			Str("user_id", state.UserID).
			Str("active_task_id", activeTaskID).
			Str("task_status", task.Status).
			Str("reason", task.ErrorCode).
			Msg(logEventSchedulerSettled)
		state.ActiveTaskID = ""
		return true, false, s.updateState(tx, state.UserID, map[string]any{
			"active_task_id":     "",
			"last_error_code":    task.ErrorCode,
			"last_error_message": task.ErrorMessage,
			"locked_by":          "",
			"locked_until":       nil,
			"updated_at":         now,
		})
	case orm.ResourceUpdateTaskStatusFailed:
		if isFrozenSkillTask(task.RequestJSON) {
			resourceUpdateWarn(logEventSchedulerSettled, nil).
				Str("user_id", state.UserID).
				Str("active_task_id", activeTaskID).
				Str("task_status", task.Status).
				Str("reason", "frozen_task_failed_blocks_window").
				Str("error_code", task.ErrorCode).
				Msg(logEventSchedulerSettled)
			return true, false, s.updateState(tx, state.UserID, map[string]any{
				"last_error_code":    task.ErrorCode,
				"last_error_message": task.ErrorMessage,
				"locked_by":          "",
				"locked_until":       nil,
				"updated_at":         now,
			})
		}
		resourceUpdateWarn(logEventSchedulerSettled, nil).
			Str("user_id", state.UserID).
			Str("active_task_id", activeTaskID).
			Str("task_status", task.Status).
			Str("reason", "unfrozen_task_failed_retry_later").
			Str("error_code", task.ErrorCode).
			Time("next_run_at", now.Add(s.cfg.SchedulerRetryDelay)).
			Msg(logEventSchedulerSettled)
		state.ActiveTaskID = ""
		return true, false, s.updateState(tx, state.UserID, map[string]any{
			"active_task_id":     "",
			"next_run_at":        now.Add(s.cfg.SchedulerRetryDelay),
			"last_error_code":    task.ErrorCode,
			"last_error_message": task.ErrorMessage,
			"locked_by":          "",
			"locked_until":       nil,
			"updated_at":         now,
		})
	default:
		return false, true, nil
	}
}

func (s *Scheduler) settleSuccessfulTask(tx *gorm.DB, state *orm.SkillReviewSchedulerState, task orm.ResourceUpdateTask, now time.Time, countedSuccess bool) error {
	windowEnd := frozenTaskEnd(task.RequestJSON)
	if windowEnd.IsZero() {
		windowEnd = now
	}
	stageIndex := state.StageIndex
	stageSuccessCount := state.StageSuccessCount
	totalSuccessCount := state.TotalSuccessCount
	if countedSuccess {
		stageSuccessCount++
		totalSuccessCount++
		stage := s.stageFor(stageIndex)
		if stage.Successes > 0 && stageSuccessCount >= stage.Successes && stageIndex+1 < len(s.cfg.Stages) {
			stageIndex++
			stageSuccessCount = 0
		}
	}
	next := windowEnd.Add(s.stageFor(stageIndex).Interval)
	lastAcceptedAt := now
	state.ActiveTaskID = ""
	state.StageIndex = stageIndex
	state.StageSuccessCount = stageSuccessCount
	state.TotalSuccessCount = totalSuccessCount
	state.LastWindowEnd = windowEnd
	state.NextRunAt = next
	return s.updateState(tx, state.UserID, map[string]any{
		"last_window_end":     windowEnd,
		"next_run_at":         next,
		"stage_index":         stageIndex,
		"stage_success_count": stageSuccessCount,
		"total_success_count": totalSuccessCount,
		"last_accepted_at":    &lastAcceptedAt,
		"active_task_id":      "",
		"locked_by":           "",
		"locked_until":        nil,
		"last_error_code":     "",
		"last_error_message":  "",
		"updated_at":          now,
	})
}

func (s *Scheduler) releaseStateLock(tx *gorm.DB, userID string, now time.Time) error {
	return s.updateState(tx, userID, map[string]any{
		"locked_by":    "",
		"locked_until": nil,
		"updated_at":   now,
	})
}

func (s *Scheduler) updateState(tx *gorm.DB, userID string, updates map[string]any) error {
	return tx.Model(&orm.SkillReviewSchedulerState{}).Where("user_id = ?", userID).Updates(updates).Error
}

func (s *Scheduler) nextWindow(state *orm.SkillReviewSchedulerState, now time.Time) (time.Time, time.Time) {
	start := state.LastWindowEnd
	if start.IsZero() {
		start = now
	}
	return start.UTC(), now.UTC()
}

func (s *Scheduler) stageFor(index int) Stage {
	if len(s.cfg.Stages) == 0 {
		return DefaultConfig().Stages[0]
	}
	if index < 0 {
		index = 0
	}
	if index >= len(s.cfg.Stages) {
		index = len(s.cfg.Stages) - 1
	}
	stage := s.cfg.Stages[index]
	if stage.Window <= 0 {
		stage.Window = s.cfg.MaxWindow
	}
	if stage.Interval <= 0 {
		stage.Interval = s.cfg.MinInterval
	}
	return stage
}

func newSkillGenerateTask(userID, triggerReason string, stats HistoryStats, now time.Time) (orm.ResourceUpdateTask, error) {
	requestID := common.GenerateID()
	request := skillGenerateRequestJSON{
		RequestID:              requestID,
		UserID:                 strings.TrimSpace(userID),
		TriggerReason:          triggerReason,
		CandidateUserTurnCount: stats.UserTurnCount,
		CandidateToolCallCount: stats.ToolCallCount,
		SchedulerPreflightAt:   formatTaskTime(now),
	}
	body, err := json.Marshal(request)
	if err != nil {
		return orm.ResourceUpdateTask{}, err
	}
	return orm.ResourceUpdateTask{
		ID:           common.GenerateID(),
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeSkill,
		UserID:       strings.TrimSpace(userID),
		ResourceID:   "",
		TriggerType:  orm.ResourceUpdateTriggerTypeScheduled,
		TriggerID:    fmt.Sprintf("skill_review:%s:%s", strings.TrimSpace(userID), requestID),
		Status:       orm.ResourceUpdateTaskStatusPending,
		RequestJSON:  body,
		NextRunAt:    now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func frozenTaskEnd(raw json.RawMessage) time.Time {
	var request skillGenerateRequestJSON
	if len(raw) == 0 || json.Unmarshal(raw, &request) != nil {
		return time.Time{}
	}
	t, _ := parseTaskTime(request.EndTime)
	return t
}

func isFrozenSkillTask(raw json.RawMessage) bool {
	var request skillGenerateRequestJSON
	return len(raw) > 0 && json.Unmarshal(raw, &request) == nil && request.WindowFrozen
}

func formatTaskTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTaskTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, strings.TrimSpace(s))
}

func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func addTimePtr(t *time.Time, d time.Duration) *time.Time {
	if t == nil {
		return nil
	}
	next := t.Add(d)
	return &next
}
