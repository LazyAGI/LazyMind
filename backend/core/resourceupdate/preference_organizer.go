package resourceupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/currentmemory"
	"lazymind/core/maintenance"
)

const (
	MemoryMaintenanceLanePrefix        = "memory-maintenance:"
	MemoryReviewLanePriority           = 10
	PreferenceOrganizerLanePriority    = 100
	PreferenceOrganizerAlgorithmPrefix = "preference_organizer_"
)

type PreferenceOrganizerRequest struct {
	ForceAnalysis  bool                      `json:"force_analysis"`
	EnqueuedState  *PreferenceOrganizerState `json:"enqueued_state,omitempty"`
	ExecutionState *PreferenceOrganizerState `json:"execution_state,omitempty"`
}

func MemoryMaintenanceLaneKey(userID string) string {
	return MemoryMaintenanceLanePrefix + strings.TrimSpace(userID)
}

func PreferenceOrganizerAlgorithmTaskID(taskID string) string {
	return PreferenceOrganizerAlgorithmPrefix + strings.TrimSpace(taskID)
}

func EnqueuePreferenceOrganizer(
	ctx context.Context,
	db *gorm.DB,
	userID string,
	triggerType string,
	triggerID string,
	now time.Time,
) (orm.ResourceUpdateTask, bool, error) {
	userID = strings.TrimSpace(userID)
	if db == nil {
		return orm.ResourceUpdateTask{}, false, errors.New("db is not configured")
	}
	if userID == "" {
		return orm.ResourceUpdateTask{}, false, errors.New("user_id is required")
	}
	var out orm.ResourceUpdateTask
	created := false
	err := maintenance.UserTransaction(ctx, db, userID, func(tx *gorm.DB) error {
		maxChars, err := currentmemory.PreferenceContextMaxCharsFromEnv()
		if err != nil {
			return err
		}
		state, err := loadPreferenceOrganizerState(ctx, tx, userID, maxChars)
		if err != nil && !errors.Is(err, currentmemory.ErrNotFound) {
			return err
		}
		out, created, err = enqueuePreferenceOrganizer(ctx, tx, userID, triggerType, triggerID, now, state)
		return err
	})
	return out, created, err
}

// Caller holds maintenance.UserTransaction; do not open a nested transaction.
func enqueuePreferenceOrganizer(ctx context.Context, tx *gorm.DB, userID, triggerType, triggerID string, now time.Time, state *PreferenceOrganizerState) (orm.ResourceUpdateTask, bool, error) {
	var out orm.ResourceUpdateTask
	query := withUpdateLock(tx.Model(&orm.ResourceUpdateTask{})).
		Where("user_id = ? AND task_type = ? AND status IN ?", userID,
			orm.ResourceUpdateTaskTypeOrganizePreference,
			[]string{orm.ResourceUpdateTaskStatusPending, orm.ResourceUpdateTaskStatusRunning})
	if err := query.Order("created_at ASC").Take(&out).Error; err == nil {
		if (triggerType == "" || triggerType == orm.ResourceUpdateTriggerTypeManual) && out.Status == orm.ResourceUpdateTaskStatusPending {
			var request PreferenceOrganizerRequest
			if err := json.Unmarshal(out.RequestJSON, &request); err != nil {
				return out, false, err
			}
			request.ForceAnalysis = true
			body, err := json.Marshal(request)
			if err != nil {
				return out, false, err
			}
			out.RequestJSON = body
			return out, false, tx.Model(&out).Updates(map[string]any{"request_json": body, "updated_at": now}).Error
		}
		return out, false, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, err
	}
	requestBody, err := json.Marshal(PreferenceOrganizerRequest{
		ForceAnalysis: triggerType == "" || triggerType == orm.ResourceUpdateTriggerTypeManual,
		EnqueuedState: state,
	})
	if err != nil {
		return out, false, err
	}
	if strings.TrimSpace(triggerType) == "" {
		triggerType = orm.ResourceUpdateTriggerTypeManual
	}
	if strings.TrimSpace(triggerID) == "" {
		triggerID = fmt.Sprintf("preference-organizer:%s:%d", userID, now.UnixNano())
	}
	out = orm.ResourceUpdateTask{
		ID:           common.GenerateID(),
		TaskType:     orm.ResourceUpdateTaskTypeOrganizePreference,
		ResourceType: orm.ResourceUpdateResourceTypeUserPreference,
		UserID:       userID,
		ResourceID:   userID,
		TriggerType:  triggerType,
		TriggerID:    triggerID,
		Status:       orm.ResourceUpdateTaskStatusPending,
		RequestJSON:  requestBody,
		NextRunAt:    now,
		LaneKey:      MemoryMaintenanceLaneKey(userID),
		LanePriority: PreferenceOrganizerLanePriority,
		LaneOrderAt:  now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	create := tx.Clauses(clauseOnConflictDoNothing()).Create(&out)
	if create.Error != nil {
		return out, false, create.Error
	}
	if create.RowsAffected == 1 {
		return out, true, nil
	}
	err = query.Order("created_at ASC").Take(&out).Error
	return out, false, err
}

type preferenceOrganizerTaskResponse struct {
	TaskID             string                    `json:"task_id"`
	Status             string                    `json:"status"`
	WaitingReason      string                    `json:"waiting_reason,omitempty"`
	Result             json.RawMessage           `json:"result,omitempty"`
	ErrorCode          string                    `json:"error_code,omitempty"`
	ErrorMessage       string                    `json:"error_message,omitempty"`
	CreatedAt          time.Time                 `json:"created_at"`
	StartedAt          *time.Time                `json:"started_at,omitempty"`
	FinishedAt         *time.Time                `json:"finished_at,omitempty"`
	CurrentState       *PreferenceOrganizerState `json:"current_state,omitempty"`
	ResultValidity     string                    `json:"result_validity,omitempty"`
	InvalidationReason string                    `json:"invalidation_reason,omitempty"`
}

func SubmitPreferenceOrganizer(w http.ResponseWriter, r *http.Request) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
	task, _, err := EnqueuePreferenceOrganizer(
		r.Context(), db, userID, orm.ResourceUpdateTriggerTypeManual,
		fmt.Sprintf("manual:%s:%d", userID, now.UnixNano()), now,
	)
	if err != nil {
		common.ReplyErr(w, "create preference organizer task failed", http.StatusInternalServerError)
		return
	}
	replyPreferenceOrganizerAccepted(w, preferenceOrganizerResponseWithWaiting(r.Context(), db, task))
}

func GetPreferenceOrganizer(w http.ResponseWriter, r *http.Request) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	var task orm.ResourceUpdateTask
	err := db.WithContext(r.Context()).
		Where("id = ? AND user_id = ? AND task_type = ?", common.PathVar(r, "task_id"), userID,
			orm.ResourceUpdateTaskTypeOrganizePreference).
		Take(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "task not found", http.StatusNotFound)
		return
	}
	if err != nil {
		common.ReplyErr(w, "query preference organizer task failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, preferenceOrganizerResponseWithWaiting(r.Context(), db, task))
}

func GetLatestPreferenceOrganizer(w http.ResponseWriter, r *http.Request) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	var task orm.ResourceUpdateTask
	err := latestPreferenceOrganizer(db.WithContext(r.Context()), userID).Take(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyOK(w, (*preferenceOrganizerTaskResponse)(nil))
		return
	}
	if err != nil {
		common.ReplyErr(w, "query preference organizer task failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, preferenceOrganizerResponseWithWaiting(r.Context(), db, task))
}

func preferenceOrganizerResponseWithWaiting(ctx context.Context, db *gorm.DB, task orm.ResourceUpdateTask) preferenceOrganizerTaskResponse {
	resp := preferenceOrganizerResponse(task)
	resp.ResultValidity = "unknown"
	maxChars, err := currentmemory.PreferenceContextMaxCharsFromEnv()
	if err == nil {
		resp.CurrentState, err = loadPreferenceOrganizerState(ctx, db, task.UserID, maxChars)
	}
	if err != nil {
		resp.InvalidationReason = "state_unavailable"
	} else {
		var result preferenceOrganizerResultState
		if json.Unmarshal(task.ResultJSON, &result) != nil || result.ObservedState == nil {
			resp.InvalidationReason = "missing_snapshot"
		} else if !result.ObservedState.matches(resp.CurrentState) {
			resp.ResultValidity = "stale"
			resp.InvalidationReason = "preference_changed"
			if result.ObservedState.MaxChars != resp.CurrentState.MaxChars {
				resp.InvalidationReason = "budget_changed"
			}
		} else if task.Status == orm.ResourceUpdateTaskStatusDone && (result.FinalState == nil ||
			result.FinalState.ETag != resp.CurrentState.ETag || result.FinalState.MaxChars != resp.CurrentState.MaxChars) {
			resp.InvalidationReason = "missing_snapshot"
		} else {
			resp.ResultValidity = "current"
		}
	}
	if task.Status == orm.ResourceUpdateTaskStatusPending {
		resp.WaitingReason = "resources"
		var count int64
		if db.WithContext(ctx).Model(&orm.ResourceUpdateTask{}).Where("user_id = ? AND resource_type = ? AND task_type = ? AND status = ?", task.UserID, orm.ResourceUpdateResourceTypeMemory, orm.ResourceUpdateTaskTypeGenerateReview, orm.ResourceUpdateTaskStatusRunning).Count(&count).Error == nil && count > 0 {
			resp.WaitingReason = "memory_review"
		}
	}
	return resp
}

func preferenceOrganizerResponse(task orm.ResourceUpdateTask) preferenceOrganizerTaskResponse {
	resp := preferenceOrganizerTaskResponse{
		TaskID: task.ID, Status: task.Status, Result: task.ResultJSON,
		ErrorCode: task.ErrorCode, ErrorMessage: task.ErrorMessage,
		CreatedAt: task.CreatedAt, StartedAt: task.StartedAt, FinishedAt: task.FinishedAt,
	}
	return resp
}

func replyPreferenceOrganizerAccepted(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(common.APIResponse{Code: common.CodeOK, Message: "accepted", Data: data})
}

func (w *Worker) handlePreferenceOrganizer(ctx context.Context, task orm.ResourceUpdateTask) taskOutcome {
	var request PreferenceOrganizerRequest
	if len(task.RequestJSON) > 0 {
		if err := json.Unmarshal(task.RequestJSON, &request); err != nil {
			return permanentOutcome("invalid_request_json", err.Error())
		}
	}
	userID := strings.TrimSpace(task.UserID)
	if userID == "" {
		return permanentOutcome("missing_user_id", "user_id required")
	}
	maxChars, err := currentmemory.PreferenceContextMaxCharsFromEnv()
	if err != nil {
		return permanentOutcome("invalid_preference_budget", err.Error())
	}
	executionCtx := maintenance.WithIdentity(ctx, maintenance.Identity{TaskID: PreferenceOrganizerAlgorithmTaskID(task.ID), RunID: task.RunID})
	if err := currentmemory.NewRepository(w.db).EnsureInitialized(executionCtx, userID); err != nil {
		return retryableOutcome("preference_state_read_failed", err)
	}
	request.ExecutionState, err = loadPreferenceOrganizerState(ctx, w.db, userID, maxChars)
	if err != nil {
		return retryableOutcome("preference_state_read_failed", err)
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return permanentOutcome("invalid_request_json", err.Error())
	}
	if err := w.updateOwned(ctx, task, map[string]any{"request_json": requestJSON, "result_json": nil, "updated_at": w.clock().UTC()}); err != nil {
		return retryableOutcome("task_lease_lost", err)
	}
	llmConfig, err := w.loadLLMConfig(ctx, w.db, userID)
	if err != nil {
		return retryableOutcome("load_llm_config_failed", err)
	}
	algorithmTaskID := PreferenceOrganizerAlgorithmTaskID(task.ID)
	callCtx, cancel := context.WithTimeout(ctx, w.cfg.PreferenceOrganizerTimeout)
	defer cancel()
	resp, status, callErr := w.callers.PreferenceOrganizer(callCtx, algo.PreferenceOrganizerRequest{
		TaskID: algorithmTaskID, RunID: task.RunID, UserID: userID, LLMConfig: llmConfig, ForceAnalysis: request.ForceAnalysis, ExpectedMaxChars: maxChars,
	})
	if status == http.StatusServiceUnavailable && algo.IsMaintenanceBusy(callErr) {
		return deferredOutcome("maintenance_busy", "maintenance executor is full", 2*time.Second)
	}
	if callErr != nil {
		if status == http.StatusUnprocessableEntity {
			return permanentOutcome("preference_organizer_invalid_request", callErr.Error())
		}
		return retryableOutcome("preference_organizer_call_failed", callErr)
	}
	if status != http.StatusOK || resp == nil || strings.TrimSpace(resp.TaskID) != algorithmTaskID {
		return retryableOutcome("preference_organizer_unexpected_response", fmt.Errorf(
			"http_status=%d status=%q task_id=%q", status, func() string {
				if resp == nil {
					return ""
				}
				return resp.Status
			}(), func() string {
				if resp == nil {
					return ""
				}
				return resp.TaskID
			}()))
	}
	result := map[string]any{"outcome": resp.Outcome}
	for key, value := range resp.Result {
		result[key] = value
	}
	resultJSON, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return permanentOutcome("preference_organizer_invalid_result", marshalErr.Error())
	}
	// budget_exhausted is accepted only for compatibility with older Algorithm versions.
	if resp.Status == "success" && (resp.Outcome == "organized" ||
		resp.Outcome == "organized_with_remaining" || resp.Outcome == "no_safe_changes" ||
		resp.Outcome == "budget_exhausted") {
		return taskOutcome{Status: orm.ResourceUpdateTaskStatusDone, ResultID: algorithmTaskID, ResultJSON: resultJSON}
	}
	code := "preference_organizer_failed"
	message := "Preference Organizer failed"
	if resp.Error != nil {
		if strings.TrimSpace(resp.Error.Code) != "" {
			code = strings.TrimSpace(resp.Error.Code)
		}
		if strings.TrimSpace(resp.Error.Message) != "" {
			message = strings.TrimSpace(resp.Error.Message)
		}
	}
	out := taskOutcome{Status: orm.ResourceUpdateTaskStatusFailed, ResultID: algorithmTaskID,
		ResultJSON: resultJSON, ErrorCode: code, ErrorMessage: message, Permanent: !resp.Retryable}
	if resp.Outcome == "partial" || resp.Outcome == "stale_state" {
		out.Permanent = true
	}
	return out
}
