package resourceupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
)

func (w *Worker) handleSkillGenerate(ctx context.Context, task orm.ResourceUpdateTask) taskOutcome {
	request, outcome := w.freezeSkillRequest(ctx, task)
	if outcome.Status != "" {
		return outcome
	}

	modelConfigs, err := w.loadLLMConfig(ctx, w.db, request.UserID)
	if err != nil {
		return retryableOutcome("load_model_configs_failed", err)
	}
	resourceUpdateInfo(logEventSkillReviewCallStart).
		Str("task_id", task.ID).
		Str("user_id", request.UserID).
		Str("requestid", request.RequestID).
		Str("start_time", request.StartTime).
		Str("end_time", request.EndTime).
		Int("qualified_session_count", request.QualifiedSessionCount).
		Int("quantity_threshold", request.QuantityThreshold).
		Int("user_turn_count", request.UserTurnCount).
		Int("tool_call_count", request.ToolCallCount).
		Msg(logEventSkillReviewCallStart)
	resp, status, err := w.callers.Skill(ctx, algo.SkillReviewRequest{
		RequestID:    request.RequestID,
		UserID:       request.UserID,
		StartTime:    request.StartTime,
		EndTime:      request.EndTime,
		MinUserTurns: w.cfg.MinUserTurns,
		MinToolTurns: w.cfg.MinToolTurns,
		SkillBaseDir: defaultSkillBaseDir,
		FSBaseURL:    common.CoreSelfEndpoint(),
		ModelConfigs: modelConfigs,
	})
	if err != nil {
		resourceUpdateWarn(logEventSkillReviewCallFailed, err).
			Str("task_id", task.ID).
			Str("user_id", request.UserID).
			Str("requestid", request.RequestID).
			Str("start_time", request.StartTime).
			Str("end_time", request.EndTime).
			Int("http_status", status).
			Str("reason", "request_failed").
			Msg(logEventSkillReviewCallFailed)
		return retryableOutcome("skill_review_call_failed", fmt.Errorf("http_status=%d: %w", status, err))
	}
	if status != 200 || resp == nil || resp.Code != 0 || !skillReviewResponseStatusAccepted(resp.Data.Status) || resp.Data.RequestID != request.RequestID {
		resourceUpdateWarn(logEventSkillReviewCallFailed, nil).
			Str("task_id", task.ID).
			Str("user_id", request.UserID).
			Str("requestid", request.RequestID).
			Str("response_requestid", safeSkillRequestID(resp)).
			Int("http_status", status).
			Int("response_code", safeSkillCode(resp)).
			Str("response_status", safeSkillStatus(resp)).
			Str("reason", "unexpected_response").
			Msg(logEventSkillReviewCallFailed)
		return retryableOutcome("skill_review_unexpected_response", fmt.Errorf("http_status=%d code=%d status=%q requestid=%q", status, safeSkillCode(resp), safeSkillStatus(resp), safeSkillRequestID(resp)))
	}
	resourceUpdateInfo(logEventSkillReviewAccepted).
		Str("task_id", task.ID).
		Str("algorithm_task_id", safeSkillTaskID(resp)).
		Str("user_id", request.UserID).
		Str("requestid", request.RequestID).
		Int("http_status", status).
		Msg(logEventSkillReviewAccepted)
	return taskOutcome{Status: orm.ResourceUpdateTaskStatusDone}
}

func (w *Worker) freezeSkillRequest(ctx context.Context, task orm.ResourceUpdateTask) (skillGenerateRequestJSON, taskOutcome) {
	var request skillGenerateRequestJSON
	if len(task.RequestJSON) > 0 {
		if err := json.Unmarshal(task.RequestJSON, &request); err != nil {
			return request, permanentOutcome("invalid_request_json", err.Error())
		}
	}
	userID := strings.TrimSpace(request.UserID)
	if userID == "" {
		userID = strings.TrimSpace(task.UserID)
	}
	if userID == "" {
		return request, permanentOutcome("missing_user_id", "user_id required")
	}

	if request.WindowFrozen {
		start, err := parseTaskTime(request.StartTime)
		if err != nil {
			return request, permanentOutcome("invalid_start_time", err.Error())
		}
		end, err := parseTaskTime(request.EndTime)
		if err != nil {
			return request, permanentOutcome("invalid_end_time", err.Error())
		}
		if !end.After(start) || strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.UserID) == "" {
			resourceUpdateWarn(logEventSkillReviewPreflight, nil).
				Str("task_id", task.ID).
				Str("user_id", request.UserID).
				Str("requestid", request.RequestID).
				Str("reason", "invalid_frozen_window").
				Msg(logEventSkillReviewPreflight)
			return request, permanentOutcome("invalid_frozen_window", "frozen request requires requestid/user_id/start_time/end_time")
		}
		quantityThreshold := request.QuantityThreshold
		if quantityThreshold <= 0 {
			quantityThreshold = w.stageFor(0).QuantityThreshold
		}
		if request.QualifiedSessionCount < quantityThreshold {
			resourceUpdateInfo(logEventSkillReviewPreflight).
				Str("task_id", task.ID).
				Str("user_id", request.UserID).
				Str("requestid", request.RequestID).
				Str("reason", "frozen_threshold_not_reached").
				Int("qualified_session_count", request.QualifiedSessionCount).
				Int("quantity_threshold", quantityThreshold).
				Int("user_turn_count", request.UserTurnCount).
				Int("tool_call_count", request.ToolCallCount).
				Int("min_user_turns", w.cfg.MinUserTurns).
				Int("min_tool_turns", w.cfg.MinToolTurns).
				Msg(logEventSkillReviewPreflight)
			return request, taskOutcome{Status: orm.ResourceUpdateTaskStatusSkipped, ErrorCode: "skill_review_history_threshold_not_reached"}
		}
		resourceUpdateInfo(logEventSkillReviewReused).
			Str("task_id", task.ID).
			Str("user_id", request.UserID).
			Str("requestid", request.RequestID).
			Str("start_time", request.StartTime).
			Str("end_time", request.EndTime).
			Msg(logEventSkillReviewReused)
		return request, taskOutcome{}
	}

	now := w.clock().UTC()
	var frozen skillGenerateRequestJSON
	thresholdReached := false
	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state orm.SkillReviewSchedulerState
		if err := withUpdateLock(tx).
			Where("user_id = ?", userID).
			Take(&state).Error; err != nil {
			return err
		}
		if strings.TrimSpace(state.ActiveTaskID) != task.ID {
			return errSkillActiveTaskMismatch
		}
		if state.LastAcceptedAt != nil && now.Before(state.LastAcceptedAt.Add(w.cfg.MinInterval)) {
			return errSkillTooFrequent
		}
		start := state.LastWindowEnd
		if start.IsZero() {
			start = now
		}
		end := now
		if !end.After(start) {
			return errSkillInvalidWindow
		}
		if end.Sub(start) > w.cfg.MaxWindow {
			return errSkillWindowTooOld
		}
		stage := w.stageFor(state.StageIndex)
		stats, err := CountSkillReviewHistoryStats(ctx, tx, userID, start, end, w.cfg.MinUserTurns, w.cfg.MinToolTurns)
		if err != nil {
			return err
		}
		stats.QuantityThreshold = stage.QuantityThreshold
		request.UserID = userID
		request.StartTime = formatTaskTime(start)
		request.EndTime = formatTaskTime(end)
		request.UserTurnCount = stats.UserTurnCount
		request.ToolCallCount = stats.ToolCallCount
		request.QualifiedSessionCount = stats.QualifiedSessionCount
		request.QuantityThreshold = stage.QuantityThreshold
		request.StartPreflightAt = formatTaskTime(now)
		if !now.Before(state.NextRunAt) {
			request.StartTriggerReason = "stale"
		} else {
			request.StartTriggerReason = "quantity"
		}
		request.WindowFrozen = true
		if strings.TrimSpace(request.RequestID) == "" {
			request.RequestID = common.GenerateID()
		}
		body, err := json.Marshal(request)
		if err != nil {
			return err
		}
		if err := tx.Model(&orm.ResourceUpdateTask{}).
			Where("id = ? AND status = ? AND locked_by = ?", task.ID, orm.ResourceUpdateTaskStatusRunning, w.workerID).
			Updates(map[string]any{"request_json": body, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&orm.SkillReviewSchedulerState{}).
			Where("user_id = ?", userID).
			Updates(map[string]any{"last_preflight_check_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		frozen = request
		thresholdReached = stats.QualifiedSessionCount >= stage.QuantityThreshold
		return nil
	})
	if err != nil {
		resourceUpdateWarn(logEventSkillReviewPreflight, err).
			Str("task_id", task.ID).
			Str("user_id", userID).
			Str("reason", skillPreflightReason(err)).
			Msg(logEventSkillReviewPreflight)
		switch err {
		case errSkillActiveTaskMismatch:
			return request, taskOutcome{Status: orm.ResourceUpdateTaskStatusSkipped, ErrorCode: "skill_review_active_task_mismatch", ErrorMessage: err.Error()}
		case errSkillTooFrequent:
			return request, taskOutcome{Status: orm.ResourceUpdateTaskStatusSkipped, ErrorCode: "skill_review_too_frequent", ErrorMessage: err.Error()}
		case errSkillInvalidWindow:
			return request, permanentOutcome("skill_review_invalid_window", err.Error())
		case errSkillWindowTooOld:
			return request, permanentOutcome("skill_review_window_too_old", err.Error())
		case errSkillThresholdNotReached:
			return request, taskOutcome{Status: orm.ResourceUpdateTaskStatusSkipped, ErrorCode: "skill_review_history_threshold_not_reached", ErrorMessage: err.Error()}
		default:
			return request, retryableOutcome("skill_preflight_failed", err)
		}
	}
	if !thresholdReached {
		resourceUpdateInfo(logEventSkillReviewPreflight).
			Str("task_id", task.ID).
			Str("user_id", frozen.UserID).
			Str("requestid", frozen.RequestID).
			Str("reason", "start_threshold_not_reached").
			Int("qualified_session_count", frozen.QualifiedSessionCount).
			Int("quantity_threshold", frozen.QuantityThreshold).
			Int("user_turn_count", frozen.UserTurnCount).
			Int("tool_call_count", frozen.ToolCallCount).
			Int("min_user_turns", w.cfg.MinUserTurns).
			Int("min_tool_turns", w.cfg.MinToolTurns).
			Msg(logEventSkillReviewPreflight)
		return frozen, taskOutcome{Status: orm.ResourceUpdateTaskStatusSkipped, ErrorCode: "skill_review_history_threshold_not_reached", ErrorMessage: errSkillThresholdNotReached.Error()}
	}
	resourceUpdateInfo(logEventSkillReviewFrozen).
		Str("task_id", task.ID).
		Str("user_id", frozen.UserID).
		Str("requestid", frozen.RequestID).
		Str("start_time", frozen.StartTime).
		Str("end_time", frozen.EndTime).
		Int("qualified_session_count", frozen.QualifiedSessionCount).
		Int("quantity_threshold", frozen.QuantityThreshold).
		Int("user_turn_count", frozen.UserTurnCount).
		Int("tool_call_count", frozen.ToolCallCount).
		Str("trigger_reason", frozen.StartTriggerReason).
		Msg(logEventSkillReviewFrozen)
	return frozen, taskOutcome{}
}

func (w *Worker) handleMemoryGenerate(ctx context.Context, task orm.ResourceUpdateTask) taskOutcome {
	var request memoryGenerateRequestJSON
	if len(task.RequestJSON) == 0 {
		return permanentOutcome("missing_request_json", "request_json required")
	}
	if err := json.Unmarshal(task.RequestJSON, &request); err != nil {
		return permanentOutcome("invalid_request_json", err.Error())
	}
	userID := strings.TrimSpace(task.UserID)
	if userID == "" {
		return permanentOutcome("missing_user_id", "user_id required")
	}
	sessionID := strings.TrimSpace(request.SessionID)
	if sessionID == "" {
		return permanentOutcome("missing_session_id", "session_id required")
	}
	memoryContent, userContent := memoryReviewContentsFromRequest(request)
	llmConfig, err := w.loadLLMConfig(ctx, w.db, userID)
	if err != nil {
		return retryableOutcome("load_llm_config_failed", err)
	}
	resourceUpdateInfo(logEventMemoryReviewCallStart).
		Str("task_id", task.ID).
		Str("user_id", userID).
		Str("resource_type", task.ResourceType).
		Str("resource_id", task.ResourceID).
		Str("session_id", sessionID).
		Msg(logEventMemoryReviewCallStart)
	resp, status, err := w.callers.Memory(ctx, algo.MemoryReviewRequest{
		UserID:    userID,
		History:   decodeHistory(request.History),
		Memory:    memoryContent,
		User:      userContent,
		LLMConfig: llmConfig,
	})
	if err != nil {
		resourceUpdateWarn(logEventMemoryReviewCallFailed, err).
			Str("task_id", task.ID).
			Str("user_id", userID).
			Str("resource_type", task.ResourceType).
			Str("session_id", sessionID).
			Int("http_status", status).
			Str("reason", "request_failed").
			Msg(logEventMemoryReviewCallFailed)
		return retryableOutcome("memory_review_call_failed", fmt.Errorf("http_status=%d: %w", status, err))
	}
	if status != 200 || resp == nil || strings.TrimSpace(resp.Status) != "success" {
		resourceUpdateWarn(logEventMemoryReviewCallFailed, nil).
			Str("task_id", task.ID).
			Str("user_id", userID).
			Str("resource_type", task.ResourceType).
			Str("session_id", sessionID).
			Int("http_status", status).
			Str("response_status", safeMemoryStatus(resp)).
			Str("reason", "unexpected_response").
			Msg(logEventMemoryReviewCallFailed)
		return retryableOutcome("memory_review_unexpected_response", fmt.Errorf("http_status=%d status=%q", status, safeMemoryStatus(resp)))
	}
	resourceUpdateInfo(logEventMemoryReviewCallDone).
		Str("task_id", task.ID).
		Str("user_id", userID).
		Str("resource_type", task.ResourceType).
		Str("session_id", sessionID).
		Int("http_status", status).
		Msg(logEventMemoryReviewCallDone)
	return taskOutcome{Status: orm.ResourceUpdateTaskStatusDone}
}

func memoryReviewContentsFromRequest(request memoryGenerateRequestJSON) (string, string) {
	memoryContent := request.Memory
	userContent := request.User
	switch strings.TrimSpace(request.Target) {
	case orm.ResourceUpdateResourceTypeMemory:
		if memoryContent == "" {
			memoryContent = request.CurrentContent
		}
	case orm.ResourceUpdateResourceTypeUserPreference:
		if userContent == "" {
			userContent = request.CurrentContent
		}
	}
	return memoryContent, userContent
}

func decodeHistory(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return string(raw)
	}
	return out
}

var (
	errSkillActiveTaskMismatch  = errors.New("skill scheduler active_task_id does not point to current task")
	errSkillTooFrequent         = errors.New("skill review is too frequent")
	errSkillInvalidWindow       = errors.New("skill review window is invalid")
	errSkillWindowTooOld        = errors.New("skill review window exceeds max window")
	errSkillThresholdNotReached = errors.New("skill review history threshold not reached")
)

func safeSkillCode(resp *algo.SkillReviewResponse) int {
	if resp == nil {
		return 0
	}
	return resp.Code
}

func safeSkillStatus(resp *algo.SkillReviewResponse) string {
	if resp == nil {
		return ""
	}
	return resp.Data.Status
}

func safeSkillRequestID(resp *algo.SkillReviewResponse) string {
	if resp == nil {
		return ""
	}
	return resp.Data.RequestID
}

func safeSkillTaskID(resp *algo.SkillReviewResponse) string {
	if resp == nil {
		return ""
	}
	return resp.Data.TaskID
}

func skillReviewResponseStatusAccepted(status string) bool {
	switch strings.TrimSpace(status) {
	case "running", "completed":
		return true
	default:
		return false
	}
}

func safeMemoryStatus(resp *algo.MemoryReviewResponse) string {
	if resp == nil {
		return ""
	}
	return resp.Status
}

func (w *Worker) stageFor(index int) Stage {
	if len(w.cfg.Stages) == 0 {
		return DefaultConfig().Stages[0]
	}
	if index < 0 {
		index = 0
	}
	if index >= len(w.cfg.Stages) {
		index = len(w.cfg.Stages) - 1
	}
	stage := w.cfg.Stages[index]
	if stage.Window <= 0 {
		stage.Window = w.cfg.MaxWindow
	}
	if stage.Interval <= 0 {
		stage.Interval = w.cfg.MinInterval
	}
	if stage.QuantityThreshold <= 0 {
		defaultStages := DefaultConfig().Stages
		if index >= 0 && index < len(defaultStages) {
			stage.QuantityThreshold = defaultStages[index].QuantityThreshold
		} else {
			stage.QuantityThreshold = defaultStages[len(defaultStages)-1].QuantityThreshold
		}
	}
	return stage
}

func skillPreflightReason(err error) string {
	switch {
	case errors.Is(err, errSkillActiveTaskMismatch):
		return "active_task_mismatch"
	case errors.Is(err, errSkillTooFrequent):
		return "min_interval_not_reached"
	case errors.Is(err, errSkillInvalidWindow):
		return "invalid_window"
	case errors.Is(err, errSkillWindowTooOld):
		return "window_too_old"
	case errors.Is(err, errSkillThresholdNotReached):
		return "history_threshold_not_reached"
	default:
		return "preflight_failed"
	}
}
