package handler

import (
	"context"
	"encoding/json"
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
	reservationStatus := orm.ResourceUpdateTaskStatusFailed
	algorithmTaskID := ""
	if accepted {
		reservationStatus = orm.ResourceUpdateTaskStatusDone
		algorithmTaskID = strings.TrimSpace(resp.Data.TaskID)
	}
	reservationErr := err
	if !accepted && reservationErr == nil {
		reservationErr = fmt.Errorf("skill organize returned unexpected response")
	}
	if finishErr := finishSkillOrganizeReservation(r.Context(), db, reservation.ID, reservationStatus, algorithmTaskID, reservationErr); finishErr != nil {
		replyError(w, "update skill organize reservation failed", http.StatusInternalServerError)
		return
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
		NextRunAt:    now,
		LockedBy:     "skill-organize-admission",
		LockedUntil:  &lockedUntil,
		StartedAt:    &now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return task, db.WithContext(ctx).Create(&task).Error
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
	return db.WithContext(ctx).Model(&orm.ResourceUpdateTask{}).Where("id = ?", taskID).Updates(map[string]any{
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
