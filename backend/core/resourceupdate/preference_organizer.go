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
	"lazymind/core/maintenance"
)

const (
	MemoryMaintenanceLanePrefix        = "memory-maintenance:"
	MemoryReviewLanePriority           = 10
	PreferenceOrganizerLanePriority    = 100
	PreferenceOrganizerAlgorithmPrefix = "preference_organizer_"
)

type PreferenceOrganizerRequest struct {
	ForceAnalysis bool `json:"force_analysis"`
}

func DefaultPreferenceOrganizerRequest() PreferenceOrganizerRequest {
	return PreferenceOrganizerRequest{}
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
		query := withUpdateLock(tx.Model(&orm.ResourceUpdateTask{})).
			Where("user_id = ? AND task_type = ? AND status IN ?", userID,
				orm.ResourceUpdateTaskTypeOrganizePreference,
				[]string{orm.ResourceUpdateTaskStatusPending, orm.ResourceUpdateTaskStatusRunning})
		if err := query.Order("created_at ASC").Take(&out).Error; err == nil {
			if (triggerType == "" || triggerType == orm.ResourceUpdateTriggerTypeManual) && out.Status == orm.ResourceUpdateTaskStatusPending {
				body, _ := json.Marshal(PreferenceOrganizerRequest{ForceAnalysis: true})
				out.RequestJSON = body
				return tx.Model(&out).Updates(map[string]any{"request_json": body, "updated_at": now}).Error
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		requestBody, err := json.Marshal(PreferenceOrganizerRequest{ForceAnalysis: triggerType == "" || triggerType == orm.ResourceUpdateTriggerTypeManual})
		if err != nil {
			return err
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
			return create.Error
		}
		if create.RowsAffected == 1 {
			created = true
			return nil
		}
		return tx.Where("user_id = ? AND task_type = ? AND status IN ?", userID,
			orm.ResourceUpdateTaskTypeOrganizePreference,
			[]string{orm.ResourceUpdateTaskStatusPending, orm.ResourceUpdateTaskStatusRunning}).
			Order("created_at ASC").Take(&out).Error
	})
	return out, created, err
}

type preferenceOrganizerTaskResponse struct {
	TaskID        string          `json:"task_id"`
	Status        string          `json:"status"`
	WaitingReason string          `json:"waiting_reason,omitempty"`
	CurrentPass   int             `json:"current_pass,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
	ErrorCode     string          `json:"error_code,omitempty"`
	ErrorMessage  string          `json:"error_message,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	StartedAt     *time.Time      `json:"started_at,omitempty"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
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
	err := db.WithContext(r.Context()).Where("user_id = ? AND task_type = ?", userID, orm.ResourceUpdateTaskTypeOrganizePreference).
		Order("CASE WHEN status IN ('pending', 'running') THEN 0 ELSE 1 END ASC").Order("created_at DESC, id DESC").Take(&task).Error
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
	if len(task.ResultJSON) > 0 {
		var state struct {
			CurrentPass int `json:"current_pass"`
		}
		if json.Unmarshal(task.ResultJSON, &state) == nil {
			resp.CurrentPass = state.CurrentPass
		}
	}
	return resp
}

func replyPreferenceOrganizerAccepted(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(common.APIResponse{Code: common.CodeOK, Message: "accepted", Data: data})
}

func (w *Worker) handlePreferenceOrganizer(ctx context.Context, task orm.ResourceUpdateTask) taskOutcome {
	request := DefaultPreferenceOrganizerRequest()
	if len(task.RequestJSON) > 0 {
		if err := json.Unmarshal(task.RequestJSON, &request); err != nil {
			return permanentOutcome("invalid_request_json", err.Error())
		}
	}
	userID := strings.TrimSpace(task.UserID)
	if userID == "" {
		return permanentOutcome("missing_user_id", "user_id required")
	}
	llmConfig, err := w.loadLLMConfig(ctx, w.db, userID)
	if err != nil {
		return retryableOutcome("load_llm_config_failed", err)
	}
	progress, _ := json.Marshal(map[string]any{"current_pass": 1})
	if err := w.updateOwned(ctx, task, map[string]any{"result_json": progress, "updated_at": w.clock().UTC()}); err != nil {
		return retryableOutcome("task_lease_lost", err)
	}
	algorithmTaskID := PreferenceOrganizerAlgorithmTaskID(task.ID)
	callCtx, cancel := context.WithTimeout(ctx, w.cfg.PreferenceOrganizerTimeout)
	defer cancel()
	resp, status, callErr := w.callers.PreferenceOrganizer(callCtx, algo.PreferenceOrganizerRequest{
		TaskID: algorithmTaskID, RunID: task.RunID, UserID: userID, LLMConfig: llmConfig, ForceAnalysis: request.ForceAnalysis,
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
