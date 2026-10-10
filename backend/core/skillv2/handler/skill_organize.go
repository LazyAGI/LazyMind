package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/chat"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/modelconfig"
	"lazymind/core/skillv2/taskguard"
)

const (
	minSkillOrganizeSkills        = 2
	maxSkillOrganizeSkills        = 20
	skillOrganizeBaseDir          = "skills"
	skillOrganizeInternalCategory = "internal"
	skillOrganizeIDPrefix         = "org_"
)

var (
	skillOrganizeCaller          = algo.OrganizeSkill
	skillOrganizeLoadModelConfig = modelconfig.LoadLLMConfig
	skillOrganizeResolveChatLLM  = chat.LoadDefaultChatLLMConfig
)

type skillOrganizeSubmitRequest struct {
	Mode        string   `json:"mode"`
	RequestID   string   `json:"requestid"`
	Skills      []string `json:"skills"`
	ArtifactDir string   `json:"artifact_dir,omitempty"`
}

type skillOrganizeSubmitResponse struct {
	Status    string `json:"status"`
	RequestID string `json:"requestid"`
	TaskID    string `json:"taskid"`
}

func SubmitSkillOrganize(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req skillOrganizeSubmitRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	normalized, err := normalizeSkillOrganizeRequest(req)
	if err != nil {
		replyError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if normalized.Mode == "deep" {
		normalized.Skills = filterInternalSkillOrganizePaths(normalized.Skills)
	}
	selectedSkills, skillIDs, err := resolveSkillOrganizeSelection(r.Context(), db, userID, normalized.Skills, normalized.Mode)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	if len(selectedSkills) < minSkillOrganizeSkills {
		code, reason := "skill_organize_insufficient_skills", "at least two editable skills are required"
		if normalized.Mode == "deep" {
			code, reason = "skill_organize_insufficient_internal_skills", "at least two internal skills are required"
			if len(selectedSkills) == 0 {
				code, reason = "skill_organize_no_internal_skills", "no internal skills selected"
			}
		}
		common.ReplyErrWithData(w, reason, map[string]any{"code": code}, http.StatusBadRequest)
		return
	}
	normalized.Skills = selectedSkills
	decision, err := taskguard.EvaluateSkillOperation(r.Context(), db, nil, taskguard.SkillOperationRequest{
		UserID:        userID,
		SkillIDs:      skillIDs,
		Operation:     taskguard.TriggerSkillOrganize,
		TriggerSource: "manual",
	})
	if err != nil {
		replyTaskGuardUnavailable(w, decision)
		return
	}
	if !decision.Allowed {
		replyTaskGuardBlocked(w, decision)
		return
	}
	reservation, err := createSkillOrganizeReservation(r.Context(), db, userID, normalized.RequestID)
	if err != nil {
		latest, guardErr := taskguard.EvaluateSkillOperation(r.Context(), db, nil, taskguard.SkillOperationRequest{
			UserID:        userID,
			Operation:     taskguard.TriggerSkillOrganize,
			TriggerSource: "manual",
		})
		if guardErr != nil {
			replyTaskGuardUnavailable(w, latest)
			return
		}
		if !latest.Allowed {
			replyTaskGuardBlocked(w, latest)
			return
		}
		replyServiceError(w, err)
		return
	}

	resp, status, err := submitSkillOrganize(r.Context(), db, userID, normalized)
	accepted := err == nil && status == http.StatusOK && resp != nil && resp.Code == 0 && skillOrganizeResponseStatusAccepted(resp.Data.Status) &&
		resp.Data.RequestID == normalized.RequestID && strings.TrimSpace(resp.Data.TaskID) != ""
	if accepted {
		if noteErr := noteSkillOrganizeAccepted(r.Context(), db, reservation.ID, strings.TrimSpace(resp.Data.TaskID), normalized, skillIDs); noteErr != nil {
			replyError(w, "update skill organize reservation failed", http.StatusInternalServerError)
			return
		}
	} else {
		reservationErr := err
		if reservationErr == nil {
			reservationErr = fmt.Errorf("skill organize returned unexpected response")
		}
		if finishErr := finishSkillOrganizeReservation(r.Context(), db, reservation.ID, orm.ResourceUpdateTaskStatusFailed, "", reservationErr); finishErr != nil {
			replyError(w, "update skill organize reservation failed", http.StatusInternalServerError)
			return
		}
	}
	if err != nil {
		replyError(w, fmt.Sprintf("skill organize call failed: %v", err), http.StatusBadGateway)
		return
	}
	if !accepted {
		replyError(w, "skill organize returned unexpected response", http.StatusBadGateway)
		return
	}

	common.ReplyOK(w, skillOrganizeSubmitResponse{
		Status:    resp.Data.Status,
		RequestID: resp.Data.RequestID,
		TaskID:    resp.Data.TaskID,
	})
}

func createSkillOrganizeReservation(ctx context.Context, db *gorm.DB, userID, requestID string) (orm.ResourceUpdateTask, error) {
	now := time.Now().UTC()
	requestJSON, err := json.Marshal(map[string]string{"requestid": strings.TrimSpace(requestID)})
	if err != nil {
		return orm.ResourceUpdateTask{}, err
	}
	lockedUntil := now.Add(5 * time.Minute)
	task := orm.ResourceUpdateTask{
		ID:           common.GenerateID(),
		TaskType:     orm.ResourceUpdateTaskTypeOrganizeSkill,
		ResourceType: orm.ResourceUpdateResourceTypeSkill,
		UserID:       strings.TrimSpace(userID),
		TriggerType:  orm.ResourceUpdateTriggerTypeManual,
		TriggerID:    "skill_organize:" + strings.TrimSpace(userID) + ":" + strings.TrimSpace(requestID),
		Status:       orm.ResourceUpdateTaskStatusRunning,
		RequestJSON:  requestJSON,
		ResultJSON:   json.RawMessage(`{"organize_rollback_version":1}`),
		NextRunAt:    now,
		LockedBy:     "skill-organize-admission",
		LockedUntil:  &lockedUntil,
		StartedAt:    &now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return task, db.WithContext(ctx).Create(&task).Error
}

func noteSkillOrganizeAccepted(ctx context.Context, db *gorm.DB, taskID, resultID string, req skillOrganizeSubmitRequest, skillIDs []string) error {
	requestJSON, err := json.Marshal(map[string]any{
		"requestid": strings.TrimSpace(req.RequestID),
		"mode":      strings.TrimSpace(req.Mode),
		"skills":    req.Skills,
		"skill_ids": append([]string(nil), skillIDs...),
	})
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Model(&orm.ResourceUpdateTask{}).Where("id = ? AND status = ?", taskID, orm.ResourceUpdateTaskStatusRunning).Updates(map[string]any{
		"result_id":    strings.TrimSpace(resultID),
		"request_json": requestJSON,
		"updated_at":   time.Now().UTC(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return taskguard.ErrOrganizeNotRunning
	}
	return nil
}

func finishSkillOrganizeReservation(ctx context.Context, db *gorm.DB, taskID, status, resultID string, taskErr error) error {
	now := time.Now().UTC()
	errorMessage := ""
	errorCode := ""
	if taskErr != nil {
		errorMessage = taskErr.Error()
	}
	if status == orm.ResourceUpdateTaskStatusFailed {
		errorCode = "skill_organize_call_failed"
	}
	return db.WithContext(ctx).Model(&orm.ResourceUpdateTask{}).Where("id = ? AND status = ?", taskID, orm.ResourceUpdateTaskStatusRunning).Updates(map[string]any{
		"status":        status,
		"result_id":     strings.TrimSpace(resultID),
		"error_code":    errorCode,
		"error_message": errorMessage,
		"locked_by":     "",
		"locked_until":  nil,
		"finished_at":   now,
		"updated_at":    now,
	}).Error
}

func skillOrganizeResponseStatusAccepted(status string) bool {
	switch strings.TrimSpace(status) {
	case "pending", "running", "completed":
		return true
	default:
		return false
	}
}

func filterInternalSkillOrganizePaths(skillPaths []string) []string {
	internal := make([]string, 0, len(skillPaths))
	for _, skillPath := range skillPaths {
		parts := strings.Split(skillPath, "/")
		if len(parts) == 3 && parts[1] == skillOrganizeInternalCategory {
			internal = append(internal, skillPath)
		}
	}
	return internal
}

func resolveSkillOrganizeSelection(ctx context.Context, db *gorm.DB, userID string, skillPaths []string, mode string) ([]string, []string, error) {
	relativeRoots := make([]string, 0, len(skillPaths))
	for _, skillPath := range skillPaths {
		relativeRoots = append(relativeRoots, strings.TrimPrefix(skillPath, skillOrganizeBaseDir+"/"))
	}
	type skillOrganizeRow struct {
		ID                    string `gorm:"column:id"`
		Category              string `gorm:"column:category"`
		RelativeRoot          string `gorm:"column:relative_root"`
		OriginBuiltinSkillUID string `gorm:"column:origin_builtin_skill_uid"`
	}
	var rows []skillOrganizeRow
	if err := db.WithContext(ctx).Table("skills").
		Select("id, category, relative_root, origin_builtin_skill_uid").
		Where("owner_user_id = ? AND deleted_at IS NULL AND relative_root IN ?", strings.TrimSpace(userID), relativeRoots).
		Where("NOT EXISTS (SELECT 1 FROM skill_market_items AS market_items WHERE market_items.source_skill_id = skills.id)").
		Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	byRoot := make(map[string]skillOrganizeRow, len(rows))
	for _, row := range rows {
		byRoot[row.RelativeRoot] = row
	}
	internalSkills := make([]string, 0, len(relativeRoots))
	ids := make([]string, 0, len(relativeRoots))
	for _, relativeRoot := range relativeRoots {
		row, ok := byRoot[relativeRoot]
		if !ok {
			return nil, nil, gorm.ErrRecordNotFound
		}
		if mode == "deep" && (row.Category != skillOrganizeInternalCategory || strings.TrimSpace(row.OriginBuiltinSkillUID) != "") {
			continue
		}
		internalSkills = append(internalSkills, skillOrganizeBaseDir+"/"+relativeRoot)
		ids = append(ids, row.ID)
	}
	return internalSkills, ids, nil
}

func applySkillOrganizeLLM(ctx context.Context, db *gorm.DB, userID string, modelConfigs map[string]any) map[string]any {
	if modelConfigs == nil {
		modelConfigs = map[string]any{}
	}
	var chatLLM map[string]any
	if !modelconfig.HasRuntimeSource(modelConfigs["evo_llm"]) {
		resolved, err := skillOrganizeResolveChatLLM(ctx, db, userID)
		if err == nil {
			chatLLM = resolved
		}
	}
	return modelconfig.ApplyEvolutionOrFallbackLLM(modelConfigs, chatLLM)
}

func submitSkillOrganize(ctx context.Context, db *gorm.DB, userID string, req skillOrganizeSubmitRequest) (*algo.SkillOrganizeResponse, int, error) {
	modelConfigs, err := skillOrganizeLoadModelConfig(ctx, db, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("load model configs: %w", err)
	}
	modelConfigs = applySkillOrganizeLLM(ctx, db, userID, modelConfigs)
	log.Logger.Info().
		Str("user_id", userID).
		Str("requestid", req.RequestID).
		Str("model_configs", modelconfig.SummarizeLLMConfigForLog(modelConfigs)).
		Msg("skill organize model configs")
	algorithmSkills := make([]string, len(req.Skills))
	for i, skillPath := range req.Skills {
		algorithmSkills[i] = strings.TrimPrefix(skillPath, skillOrganizeBaseDir+"/")
	}
	return skillOrganizeCaller(ctx, algo.SkillOrganizeRequest{
		Mode:         req.Mode,
		RequestID:    req.RequestID,
		UserID:       userID,
		Skills:       algorithmSkills,
		ArtifactDir:  req.ArtifactDir,
		ModelConfigs: modelConfigs,
	})
}

func normalizeSkillOrganizeRequest(req skillOrganizeSubmitRequest) (skillOrganizeSubmitRequest, error) {
	req.Mode = strings.TrimSpace(req.Mode)
	if req.Mode == "" {
		req.Mode = "light"
	}
	if req.Mode != "light" && req.Mode != "deep" {
		return req, fmt.Errorf("mode must be light or deep")
	}
	req.RequestID = strings.TrimSpace(req.RequestID)
	if req.RequestID == "" {
		return req, fmt.Errorf("requestid is required")
	}
	if !strings.HasPrefix(req.RequestID, skillOrganizeIDPrefix) {
		req.RequestID = skillOrganizeIDPrefix + req.RequestID
	}
	req.ArtifactDir = strings.TrimSpace(req.ArtifactDir)
	if len(req.Skills) == 0 {
		return req, fmt.Errorf("skills is required")
	}
	if len(req.Skills) > maxSkillOrganizeSkills {
		return req, fmt.Errorf("skills must not exceed %d items", maxSkillOrganizeSkills)
	}

	seen := make(map[string]struct{}, len(req.Skills))
	normalized := make([]string, 0, len(req.Skills))
	for _, raw := range req.Skills {
		skillPath, err := normalizeSkillOrganizePath(raw)
		if err != nil {
			return req, err
		}
		if _, ok := seen[skillPath]; ok {
			return req, fmt.Errorf("duplicate skill path: %s", skillPath)
		}
		seen[skillPath] = struct{}{}
		normalized = append(normalized, skillPath)
	}
	req.Skills = normalized
	return req, nil
}

func normalizeSkillOrganizePath(raw string) (string, error) {
	value := strings.Trim(strings.TrimSpace(raw), "/")
	if value == "" {
		return "", fmt.Errorf("skill path is required")
	}
	if strings.Contains(value, `\`) || strings.Contains(value, "//") {
		return "", fmt.Errorf("invalid skill path: %s", raw)
	}
	cleaned := path.Clean(value)
	if cleaned != value || cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", fmt.Errorf("invalid skill path: %s", raw)
	}
	parts := strings.Split(cleaned, "/")
	if len(parts) != 3 || parts[0] != skillOrganizeBaseDir || parts[1] == "" || parts[2] == "" {
		return "", fmt.Errorf("skill path must be skills/<category>/<skill_name>")
	}
	return cleaned, nil
}

func CancelSkillOrganize(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	requestID := strings.TrimSpace(r.URL.Query().Get("requestid"))
	if requestID == "" {
		var body struct {
			RequestID string `json:"requestid"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		requestID = strings.TrimSpace(body.RequestID)
	}
	if requestID == "" {
		replyError(w, "requestid is required", http.StatusBadRequest)
		return
	}
	err := cancelSkillOrganizeReservation(r.Context(), db, userID, requestID)
	if errors.Is(err, taskguard.ErrOrganizeNotRunning) || errors.Is(err, gorm.ErrRecordNotFound) {
		replyError(w, "skill organize task is not running", http.StatusConflict)
		return
	}
	if err != nil {
		replyError(w, "skill organize cancel failed", http.StatusInternalServerError)
		return
	}
	// Persistence is authoritative, including when another algorithm process
	// owns the model call. Notification only shortens cooperative cancellation.
	ctx, cancel := context.WithTimeout(r.Context(), 250*time.Millisecond)
	defer cancel()
	_, _, _ = algo.CancelSkillOrganize(ctx, algo.SkillOrganizeCancelRequest{
		RequestID: requestID,
		UserID:    userID,
	})
	var task orm.ResourceUpdateTask
	if err := db.WithContext(r.Context()).Where("user_id = ? AND trigger_id = ? AND task_type = ?", userID, "skill_organize:"+userID+":"+requestID, orm.ResourceUpdateTaskTypeOrganizeSkill).Take(&task).Error; err != nil {
		replyError(w, "skill organize cancellation status unavailable", http.StatusInternalServerError)
		return
	}
	var result struct {
		Details map[string]any `json:"cancellation_details"`
	}
	_ = json.Unmarshal(task.ResultJSON, &result)
	pending, _ := result.Details["pending_review"].(bool)
	common.ReplyOK(w, map[string]any{"status": "cancelled", "requestid": requestID, "pending_review": pending, "cancellation_details": result.Details})
}

func cancelSkillOrganizeReservation(ctx context.Context, db *gorm.DB, userID, requestID string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&orm.ResourceUpdateTask{}).Where("user_id = ? AND task_type = ? AND trigger_id = ?", userID, orm.ResourceUpdateTaskTypeOrganizeSkill, "skill_organize:"+userID+":"+requestID)
		if err := query.UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
			return err
		}
		var task orm.ResourceUpdateTask
		if err := query.Take(&task).Error; err != nil {
			return err
		}
		if task.Status == orm.ResourceUpdateTaskStatusSkipped && task.ErrorCode == "skill_organize_cancelled" {
			return nil
		}
		if task.Status != orm.ResourceUpdateTaskStatusRunning {
			return taskguard.ErrOrganizeNotRunning
		}
		statsQuery := tx.Model(&orm.SkillReviewStats{}).Where("userid = ? AND requestid = ?", userID, requestID)
		if err := statsQuery.UpdateColumn("duration_ms", gorm.Expr("duration_ms")).Error; err != nil {
			return err
		}
		var stats []orm.SkillReviewStats
		if err := statsQuery.Find(&stats).Error; err != nil {
			return err
		}
		for _, row := range stats {
			if row.Status == "completed" || row.Status == "failed" || row.Status == "skipped" {
				return taskguard.ErrOrganizeNotRunning
			}
		}
		details, err := taskguard.PrepareOrganizeCancellation(ctx, tx, task)
		if err != nil {
			return err
		}
		message := "Skill organize was cancelled."
		if details != nil {
			message += " Changes were retained for review because rollback was incomplete; affected skills are disabled and trashed sources remain recoverable."
		}
		now := time.Now().UTC()
		if len(stats) == 0 {
			id := task.ResultID
			if id == "" {
				id = task.ID
			}
			stats = []orm.SkillReviewStats{{ID: id, RequestID: requestID, UserID: userID, StartedAt: now.Format(time.RFC3339Nano)}}
		}
		for _, row := range stats {
			summary := map[string]any{}
			_ = json.Unmarshal([]byte(row.Summary), &summary)
			if summary == nil {
				summary = map[string]any{}
			}
			summary["status"] = "cancelled"
			summary["error_code"] = "skill_organize_cancelled"
			summary["error_category"] = "cancelled"
			summary["error"] = message
			if details != nil {
				summary["cancellation_details"] = details
				summary["pending_review"] = true
			}
			encoded, err := json.Marshal(summary)
			if err != nil {
				return err
			}
			row.Status, row.Summary = "cancelled", string(encoded)
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
		}
		// Only drafts still owned by this run are disposable after full rollback.
		taskIDs := []string{requestID}
		if task.ResultID != "" {
			taskIDs = append(taskIDs, task.ResultID)
		}
		ids := tx.Table("skill_drafts AS d").Select("d.skill_id").Joins("JOIN skills AS s ON s.id = d.skill_id").Where("s.owner_user_id = ? AND d.task_id IN ?", userID, taskIDs)
		updates := map[string]any{"task_id": "", "conversation_id": nil, "version": gorm.Expr("version + 1"), "updated_at": now}
		if details == nil {
			if err := tx.Exec("DELETE FROM skill_draft_entries WHERE skill_id IN (?)", ids).Error; err != nil {
				return err
			}
			updates["draft_updated_at"] = nil
		} else {
			// Do not expose a renamed package's old published identity to runtime
			// discovery while its coherent replacement draft awaits review.
			var retained []orm.SkillV2Skill
			if err := tx.Where("id IN (?)", ids).Find(&retained).Error; err != nil {
				return err
			}
			details["retained_skills"] = retained
			if err := tx.Model(&orm.SkillV2Skill{}).Where("id IN (?)", ids).Updates(map[string]any{"is_enabled": false, "call_mode": "manual"}).Error; err != nil {
				return err
			}
			updates["draft_status"], updates["draft_updated_at"] = "pending_confirm", now
			result := map[string]any{}
			if err := json.Unmarshal(task.ResultJSON, &result); err != nil {
				result = map[string]any{"original_result": string(task.ResultJSON)}
			}
			if result == nil {
				result = map[string]any{}
			}
			result["cancellation_details"] = details
			encoded, err := json.Marshal(result)
			if err != nil {
				return err
			}
			if err := tx.Model(&orm.ResourceUpdateTask{}).Where("id = ?", task.ID).UpdateColumn("result_json", json.RawMessage(encoded)).Error; err != nil {
				return err
			}
		}
		if err := tx.Table("skill_drafts").Where("skill_id IN (?)", ids).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Model(&orm.ResourceUpdateTask{}).Where("id = ?", task.ID).Updates(map[string]any{"status": orm.ResourceUpdateTaskStatusSkipped, "error_code": "skill_organize_cancelled", "error_message": message, "locked_by": "", "locked_until": nil, "finished_at": now, "updated_at": now}).Error
	})
}
