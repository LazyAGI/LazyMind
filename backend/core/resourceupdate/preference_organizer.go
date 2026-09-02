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
)

const (
	MemoryMaintenanceLanePrefix        = "memory-maintenance:"
	MemoryReviewLanePriority           = 10
	PreferenceOrganizerLanePriority    = 100
	PreferenceOrganizerAlgorithmPrefix = "preference_organizer_"
)

type PreferenceOrganizerRequest struct {
	TargetItems         int `json:"target_items"`
	MinItems            int `json:"min_items"`
	HardMinItems        int `json:"hard_min_items"`
	MaxItems            int `json:"max_items"`
	TargetPromptPercent int `json:"target_prompt_percent"`
	MaxChanges          int `json:"max_changes"`
	MaxPasses           int `json:"max_passes"`
	MaxRoundsPerPass    int `json:"max_rounds_per_pass"`
}

type PreferenceOrganizingError struct {
	TaskID string
}

func (e *PreferenceOrganizingError) Error() string {
	return "preference_organizing: Preference Organizer is running; this preference write was not saved"
}

func DefaultPreferenceOrganizerRequest() PreferenceOrganizerRequest {
	return PreferenceOrganizerRequest{
		TargetItems:         30,
		MinItems:            20,
		HardMinItems:        15,
		MaxItems:            40,
		TargetPromptPercent: 40,
		MaxChanges:          50,
		MaxPasses:           2,
		MaxRoundsPerPass:    60,
	}
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
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := withUpdateLock(tx.Model(&orm.ResourceUpdateTask{})).
			Where("user_id = ? AND task_type = ? AND status IN ?", userID,
				orm.ResourceUpdateTaskTypeOrganizePreference,
				[]string{orm.ResourceUpdateTaskStatusPending, orm.ResourceUpdateTaskStatusRunning})
		if err := query.Order("created_at ASC").Take(&out).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		requestBody, err := json.Marshal(DefaultPreferenceOrganizerRequest())
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

func RunningPreferenceOrganizer(
	ctx context.Context,
	db *gorm.DB,
	userID string,
) (orm.ResourceUpdateTask, bool, error) {
	if db == nil || !db.Migrator().HasTable(&orm.ResourceUpdateTask{}) {
		return orm.ResourceUpdateTask{}, false, nil
	}
	var task orm.ResourceUpdateTask
	err := db.WithContext(ctx).
		Where("user_id = ? AND task_type = ? AND status = ?", strings.TrimSpace(userID),
			orm.ResourceUpdateTaskTypeOrganizePreference, orm.ResourceUpdateTaskStatusRunning).
		Take(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return orm.ResourceUpdateTask{}, false, nil
	}
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "no such table") {
		return orm.ResourceUpdateTask{}, false, nil
	}
	return task, err == nil, err
}

func AuthorizePreferenceMutation(
	ctx context.Context,
	db *gorm.DB,
	userID string,
	algorithmTaskID string,
) error {
	task, running, err := RunningPreferenceOrganizer(ctx, db, userID)
	if err != nil || !running {
		return err
	}
	if strings.TrimSpace(algorithmTaskID) == PreferenceOrganizerAlgorithmTaskID(task.ID) {
		return nil
	}
	return &PreferenceOrganizingError{TaskID: task.ID}
}

type preferenceOrganizerTaskResponse struct {
	TaskID       string          `json:"task_id"`
	Status       string          `json:"status"`
	CurrentPass  int             `json:"current_pass,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	StartedAt    *time.Time      `json:"started_at,omitempty"`
	FinishedAt   *time.Time      `json:"finished_at,omitempty"`
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
	replyPreferenceOrganizerAccepted(w, preferenceOrganizerResponse(task))
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
	common.ReplyOK(w, preferenceOrganizerResponse(task))
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
	progress, _ := json.Marshal(map[string]any{"current_pass": 1, "outcome": "running"})
	if err := w.db.WithContext(ctx).Model(&orm.ResourceUpdateTask{}).
		Where("id = ? AND status = ? AND locked_by = ?", task.ID, orm.ResourceUpdateTaskStatusRunning, w.workerID).
		Updates(map[string]any{"result_json": progress, "updated_at": w.clock().UTC()}).Error; err != nil {
		return retryableOutcome("persist_preference_organizer_progress_failed", err)
	}

	algorithmTaskID := PreferenceOrganizerAlgorithmTaskID(task.ID)
	var resp *algo.PreferenceOrganizerResponse
	var status int
	callOutcome := w.withTaskLeaseHeartbeat(ctx, task, func(callCtx context.Context) taskOutcome {
		callCtx, cancel := context.WithTimeout(callCtx, w.cfg.PreferenceOrganizerTimeout)
		defer cancel()
		var callErr error
		resp, status, callErr = w.callers.PreferenceOrganizer(callCtx, algo.PreferenceOrganizerRequest{
			TaskID: algorithmTaskID, UserID: userID, LLMConfig: llmConfig,
			TargetItems: request.TargetItems, MinItems: request.MinItems,
			HardMinItems: request.HardMinItems, MaxItems: request.MaxItems,
			TargetPromptPercent: request.TargetPromptPercent, MaxChanges: request.MaxChanges,
			MaxPasses: request.MaxPasses, MaxRoundsPerPass: request.MaxRoundsPerPass,
		})
		if callErr != nil {
			if status == http.StatusUnprocessableEntity {
				return permanentOutcome("preference_organizer_invalid_request", callErr.Error())
			}
			return retryableOutcome("preference_organizer_call_failed", callErr)
		}
		return taskOutcome{Status: orm.ResourceUpdateTaskStatusDone}
	})
	if callOutcome.Status != orm.ResourceUpdateTaskStatusDone {
		return callOutcome
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

// withTaskLeaseHeartbeat keeps one claimed task, and therefore its lane, owned
// while a long synchronous downstream call is in progress.
func (w *Worker) withTaskLeaseHeartbeat(
	ctx context.Context,
	task orm.ResourceUpdateTask,
	call func(context.Context) taskOutcome,
) taskOutcome {
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	interval := w.cfg.WorkerLockTTL / 3
	if interval < time.Second {
		interval = time.Second
	}
	done := make(chan struct{})
	leaseErrors := make(chan error, 1)
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-callCtx.Done():
				return
			case <-ticker.C:
				now := w.clock().UTC()
				update := w.db.WithContext(callCtx).Model(&orm.ResourceUpdateTask{}).
					Where("id = ? AND status = ? AND locked_by = ?", task.ID,
						orm.ResourceUpdateTaskStatusRunning, w.workerID).
					Updates(map[string]any{
						"locked_until": now.Add(w.cfg.WorkerLockTTL),
						"updated_at":   now,
					})
				if update.Error != nil || update.RowsAffected != 1 {
					err := update.Error
					if err == nil {
						err = errors.New("preference organizer task lease was lost")
					}
					leaseErrors <- err
					cancel()
					return
				}
			}
		}
	}()
	outcome := call(callCtx)
	cancel()
	<-done
	select {
	case err := <-leaseErrors:
		if outcome.Status != orm.ResourceUpdateTaskStatusDone {
			return retryableOutcome("preference_organizer_lease_lost", err)
		}
	default:
	}
	return outcome
}
