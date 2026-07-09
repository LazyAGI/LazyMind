package plugin

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/skill"
	"lazymind/core/store"
)

// uuidPattern matches a standard UUID v4 string (8-4-4-4-12 hex digits with hyphens).
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// pluginIDPattern extracts the `id:` field from plugin.yaml.
// Matches bare or single/double-quoted values on a line of its own.
var pluginIDPattern = regexp.MustCompile(`(?m)^id:\s*["']?([^"'\n]+?)["']?\s*$`)

// extractPluginID returns the plugin id from a plugin.yaml string, or "" if not found.
func extractPluginID(yamlContent string) string {
	if m := pluginIDPattern.FindStringSubmatch(yamlContent); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// isBuiltinPluginID returns true when id does not look like a UUID.
// Built-in plugin IDs are human-readable strings (e.g. "image-plugin"),
// while user draft IDs are always UUID v4 strings generated on creation.
// This check is a first-line defence; the DB query's WHERE created_by=userID
// would reject any mistaken match anyway, but returning 403 explicitly avoids
// confusing "not found" responses and makes the intent clear.
func isBuiltinPluginID(id string) bool {
	return !uuidPattern.MatchString(strings.ToLower(id))
}

// draftResponse is the JSON shape returned for a single PluginDraft.
type draftResponse struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Content            string `json:"content"`
	PluginYAMLContent  string `json:"plugin_yaml_content"`
	StateYAMLContent   string `json:"state_yaml_content"`
	StateLayoutContent string `json:"state_layout_content"`
	ScenarioContent    string `json:"scenario_content"`
	ScriptsContent     string `json:"scripts_content"`
	DesignBriefContent string `json:"design_brief_content"`
	GenerateStatus     string `json:"generate_status"`
	GenerateError      string `json:"generate_error"`
	GenerateWarning    string `json:"generate_warning"`
	Version            int    `json:"version"`
	CreatedBy          string `json:"created_by"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
	SourceType         string `json:"source_type"`
	SourceSkillID      string `json:"source_skill_id"`
	SourceSkillName    string `json:"source_skill_name"`
}

func toDraftResponse(d orm.PluginDraft) draftResponse {
	return draftResponse{
		ID:                 d.ID,
		Name:               d.Name,
		Content:            d.Content,
		PluginYAMLContent:  d.PluginYAMLContent,
		StateYAMLContent:   d.StateYAMLContent,
		StateLayoutContent: d.StateLayoutContent,
		ScenarioContent:    d.ScenarioContent,
		ScriptsContent:     d.ScriptsContent,
		DesignBriefContent: d.DesignBriefContent,
		GenerateStatus:     d.GenerateStatus,
		GenerateError:      d.GenerateError,
		GenerateWarning:    d.GenerateWarning,
		Version:            d.Version,
		CreatedBy:          d.CreatedBy,
		CreatedAt:          d.CreatedAt.Format(time.RFC3339),
		UpdatedAt:          d.UpdatedAt.Format(time.RFC3339),
		SourceType:         d.SourceType,
		SourceSkillID:      d.SourceSkillID,
		SourceSkillName:    d.SourceSkillName,
	}
}

// ListPluginDrafts handles GET /plugin-drafts
// Returns the drafts owned by the current user, paginated.
func ListPluginDrafts(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	pageStr := r.URL.Query().Get("page")
	pageSizeStr := r.URL.Query().Get("page_size")
	page := 1
	pageSize := 20
	if n, err := strconv.Atoi(pageStr); err == nil && n > 0 {
		page = n
	}
	if n, err := strconv.Atoi(pageSizeStr); err == nil && n > 0 && n <= 100 {
		pageSize = n
	}
	offset := (page - 1) * pageSize

	db := store.DB()
	var total int64
	if err := db.Model(&orm.PluginDraft{}).Where("created_by = ?", userID).Count(&total).Error; err != nil {
		common.ReplyErr(w, "query failed", http.StatusInternalServerError)
		return
	}

	var drafts []orm.PluginDraft
	if err := db.Where("created_by = ?", userID).
		Order("updated_at DESC").
		Limit(pageSize).
		Offset(offset).
		Find(&drafts).Error; err != nil {
		common.ReplyErr(w, "query failed", http.StatusInternalServerError)
		return
	}

	records := make([]draftResponse, 0, len(drafts))
	for _, d := range drafts {
		records = append(records, toDraftResponse(d))
	}

	common.ReplyOK(w, map[string]any{
		"records": records,
		"total":   total,
	})
}

// CreatePluginDraft handles POST /plugin-drafts
// Body: { "name": "...", "content": "...", "source_type": "blank|ai|skill" }
func CreatePluginDraft(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Name       string `json:"name"`
		Content    string `json:"content"`
		SourceType string `json:"source_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		common.ReplyErr(w, "name is required", http.StatusBadRequest)
		return
	}
	// Validate source_type; default to blank for unknown values.
	sourceType := body.SourceType
	if sourceType != "ai" && sourceType != "skill" && sourceType != "blank" {
		sourceType = ""
	}

	draft := orm.PluginDraft{
		ID:         uuid.New().String(),
		Name:       body.Name,
		Content:    body.Content,
		SourceType: sourceType,
		CreatedBy:  userID,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}

	if err := store.DB().Create(&draft).Error; err != nil {
		common.ReplyErr(w, "create failed", http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, toDraftResponse(draft))
}

// GetPluginDraft handles GET /plugin-drafts/{draft_id}
func GetPluginDraft(w http.ResponseWriter, r *http.Request) {
	draftID := common.PathVar(r, "draft_id")
	userID := common.UserID(r)
	if draftID == "" {
		common.ReplyErr(w, "draft_id required", http.StatusBadRequest)
		return
	}

	var draft orm.PluginDraft
	if err := store.DB().Where("id = ? AND created_by = ?", draftID, userID).First(&draft).Error; err != nil {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}

	common.ReplyOK(w, toDraftResponse(draft))
}

// SavePluginDraft handles POST /plugin-drafts/{draft_id}:save
//
//	Body: {
//	  "content": "...",
//	  "plugin_yaml_content": "...",
//	  "state_yaml_content": "...",
//	  "state_layout_content": "...",   // no version check, last-write-wins
//	  "scenario_content": "...",
//	  "scripts_content": "...",
//	  "version": 3                      // required when sending plugin_yaml_content or state_yaml_content
//	}
//
// Returns 409 Conflict when version is stale (another write already incremented it).
func SavePluginDraft(w http.ResponseWriter, r *http.Request) {
	draftID := common.PathVar(r, "draft_id")
	userID := common.UserID(r)
	if draftID == "" {
		common.ReplyErr(w, "draft_id required", http.StatusBadRequest)
		return
	}
	if isBuiltinPluginID(draftID) {
		common.ReplyErr(w, "built-in plugins cannot be modified", http.StatusForbidden)
		return
	}

	var body struct {
		Content            *string `json:"content"`
		PluginYAMLContent  *string `json:"plugin_yaml_content"`
		StateYAMLContent   *string `json:"state_yaml_content"`
		StateLayoutContent *string `json:"state_layout_content"`
		ScenarioContent    *string `json:"scenario_content"`
		ScriptsContent     *string `json:"scripts_content"`
		// Version is the caller's last-known version. Required when writing
		// plugin_yaml_content or state_yaml_content; ignored otherwise.
		Version *int `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}

	db := store.DB()
	var draft orm.PluginDraft
	if err := db.Where("id = ? AND created_by = ?", draftID, userID).First(&draft).Error; err != nil {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}

	// Reject saves while an AI repair is in progress to prevent overwriting in-flight changes.
	if draft.GenerateStatus == generateStatusRepairing {
		common.ReplyErr(w, "repair in progress, please wait", http.StatusConflict)
		return
	}

	// --- Optimistic-lock check for versioned fields ---
	needsVersionCheck := body.PluginYAMLContent != nil || body.StateYAMLContent != nil
	if needsVersionCheck && body.Version != nil {
		if *body.Version != draft.Version {
			common.ReplyErrWithData(w, "conflict", toDraftResponse(draft), http.StatusConflict)
			return
		}
	}

	updates := map[string]any{"updated_at": time.Now().UTC()}
	if body.Content != nil {
		updates["content"] = *body.Content
	}
	if body.PluginYAMLContent != nil {
		updates["plugin_yaml_content"] = *body.PluginYAMLContent
		// Keep plugin_id in sync so the per-user unique index can enforce deduplication.
		updates["plugin_id"] = extractPluginID(*body.PluginYAMLContent)
	}
	if body.StateYAMLContent != nil {
		updates["state_yaml_content"] = *body.StateYAMLContent
	}
	if body.StateLayoutContent != nil {
		updates["state_layout_content"] = *body.StateLayoutContent
	}
	if body.ScenarioContent != nil {
		updates["scenario_content"] = *body.ScenarioContent
	}
	if body.ScriptsContent != nil {
		updates["scripts_content"] = *body.ScriptsContent
	}
	if needsVersionCheck && body.Version != nil {
		updates["version"] = draft.Version + 1
	}

	if err := db.Model(&draft).Updates(updates).Error; err != nil {
		if strings.Contains(err.Error(), "idx_plugin_drafts_user_plugin_id") ||
			strings.Contains(err.Error(), "unique") && strings.Contains(err.Error(), "plugin_id") {
			common.ReplyErr(w, "plugin id already exists for this user", http.StatusConflict)
			return
		}
		common.ReplyErr(w, "save failed", http.StatusInternalServerError)
		return
	}
	// Reload to return the authoritative post-save state.
	if err := db.Where("id = ?", draftID).First(&draft).Error; err != nil {
		common.ReplyErr(w, "reload failed", http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, toDraftResponse(draft))
}

// DeletePluginDraft handles DELETE /plugin-drafts/{draft_id}
func DeletePluginDraft(w http.ResponseWriter, r *http.Request) {
	draftID := common.PathVar(r, "draft_id")
	userID := common.UserID(r)
	if draftID == "" {
		common.ReplyErr(w, "draft_id required", http.StatusBadRequest)
		return
	}
	if isBuiltinPluginID(draftID) {
		common.ReplyErr(w, "built-in plugins cannot be modified", http.StatusForbidden)
		return
	}

	result := store.DB().Where("id = ? AND created_by = ?", draftID, userID).Delete(&orm.PluginDraft{})
	if result.Error != nil {
		common.ReplyErr(w, "delete failed", http.StatusInternalServerError)
		return
	}
	if result.RowsAffected == 0 {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}

	common.ReplyOK(w, nil)
}

// AIGeneratePluginDraft handles POST /plugin-drafts/{draft_id}:ai-generate
// Body: { "description": "..." } or { "skill_id": "..." } (mutually exclusive)
// Sets generate_status to "generating" and enqueues an async job.
// Returns immediately with the current draft (generate_status == "generating").
func AIGeneratePluginDraft(w http.ResponseWriter, r *http.Request) {
	draftID := common.PathVar(r, "draft_id")
	userID := common.UserID(r)
	if draftID == "" {
		common.ReplyErr(w, "draft_id required", http.StatusBadRequest)
		return
	}
	if isBuiltinPluginID(draftID) {
		common.ReplyErr(w, "built-in plugins cannot be modified", http.StatusForbidden)
		return
	}

	var body struct {
		Description string `json:"description"`
		SkillID     string `json:"skill_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	body.Description = strings.TrimSpace(body.Description)
	body.SkillID = strings.TrimSpace(body.SkillID)
	if body.Description == "" && body.SkillID == "" {
		common.ReplyErr(w, "description or skill_id is required", http.StatusBadRequest)
		return
	}

	db := store.DB()
	var draft orm.PluginDraft
	if err := db.Where("id = ? AND created_by = ?", draftID, userID).First(&draft).Error; err != nil {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}

	skillContent := ""
	skillName := ""
	if body.SkillID != "" {
		if skill.IsBuiltinSkillID(body.SkillID) {
			content, sname, ok, loadErr := skill.GetBuiltinSkillContent(body.SkillID)
			if loadErr != nil {
				common.ReplyErr(w, "skill not found", http.StatusInternalServerError)
				return
			}
			if !ok || content == "" {
				common.ReplyErr(w, "skill not found", http.StatusNotFound)
				return
			}
			skillContent = content
			skillName = sname
		} else {
			var skillRow struct {
				Content   string
				SkillName string
			}
			if err := db.Raw("SELECT content, skill_name FROM skill_resources WHERE id = ?", body.SkillID).Scan(&skillRow).Error; err != nil {
				common.ReplyErr(w, "skill not found", http.StatusInternalServerError)
				return
			}
			if skillRow.Content == "" {
				common.ReplyErr(w, "skill not found", http.StatusNotFound)
				return
			}
			skillContent = skillRow.Content
			skillName = skillRow.SkillName
		}
	}

	sourceUpdates := map[string]any{
		"generate_status": generateStatusGenerating,
		"updated_at":      time.Now().UTC(),
	}
	// Set source_type on first generation (don't overwrite if already set by CreatePluginDraft).
	if draft.SourceType == "" {
		if body.SkillID != "" {
			sourceUpdates["source_type"] = "skill"
		} else {
			sourceUpdates["source_type"] = "ai"
		}
	}
	if body.SkillID != "" && draft.SourceSkillID == "" {
		sourceUpdates["source_skill_id"] = body.SkillID
		sourceUpdates["source_skill_name"] = skillName
	}

	if err := db.Model(&draft).Updates(sourceUpdates).Error; err != nil {
		common.ReplyErr(w, "update failed", http.StatusInternalServerError)
		return
	}
	draft.GenerateStatus = generateStatusGenerating
	if st, ok := sourceUpdates["source_type"].(string); ok {
		draft.SourceType = st
	}
	if sid, ok := sourceUpdates["source_skill_id"].(string); ok {
		draft.SourceSkillID = sid
	}
	if sn, ok := sourceUpdates["source_skill_name"].(string); ok {
		draft.SourceSkillName = sn
	}

	_, err := asyncjob.Enqueue(r.Context(), db, asyncjob.EnqueueRequest{
		JobType:      pluginDraftGenerateJobType,
		ResourceType: "plugin_draft",
		ResourceID:   draftID,
		Payload: pluginDraftGeneratePayload{
			DraftID:      draftID,
			Name:         draft.Name,
			Description:  body.Description,
			SkillContent: skillContent,
			UserID:       userID,
		},
		MaxAttempts:  1,
		CreateUserID: userID,
	})
	if err != nil {
		_ = db.Model(&draft).Updates(map[string]any{
			"generate_status": generateStatusFailed,
			"updated_at":      time.Now().UTC(),
		}).Error
		common.ReplyErr(w, "enqueue failed", http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, toDraftResponse(draft))
}

// PolishPluginDraftInfo handles POST /plugin-drafts:polish-info
// Loads the current user's llm_config and proxies to the Python polish_info endpoint.
// Body: { "fields": {...}, "target_fields": [...] }
func PolishPluginDraftInfo(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Fields       map[string]string `json:"fields"`
		TargetFields []string          `json:"target_fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if len(body.TargetFields) == 0 {
		common.ReplyErr(w, "target_fields is required", http.StatusBadRequest)
		return
	}

	llmConfig, err := modelconfig.LoadLLMConfig(r.Context(), store.DB(), userID)
	if err != nil {
		llmConfig = map[string]any{}
	}

	resp, err := algo.PolishPluginInfo(r.Context(), algo.PolishPluginInfoRequest{
		Fields:       body.Fields,
		TargetFields: body.TargetFields,
		LLMConfig:    llmConfig,
	})
	if err != nil {
		common.ReplyErr(w, "polish failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, resp)
}

// AIRepairPluginDraft handles POST /plugin-drafts/{draft_id}:ai-repair
// Enqueues an async repair job and returns immediately with status=repairing.
// The client polls generate_status until it leaves the repairing state.
func AIRepairPluginDraft(w http.ResponseWriter, r *http.Request) {
	draftID := common.PathVar(r, "draft_id")
	userID := common.UserID(r)
	if draftID == "" {
		common.ReplyErr(w, "draft_id required", http.StatusBadRequest)
		return
	}
	if isBuiltinPluginID(draftID) {
		common.ReplyErr(w, "built-in plugins cannot be modified", http.StatusForbidden)
		return
	}

	var body struct {
		RepairHint string `json:"repair_hint"`
		Target     string `json:"target"` // 'statemachine' | 'ui' | 'scenario'
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}

	db := store.DB()
	var draft orm.PluginDraft
	if err := db.Where("id = ? AND created_by = ?", draftID, userID).First(&draft).Error; err != nil {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}

	if draft.PluginYAMLContent == "" || draft.StateYAMLContent == "" {
		common.ReplyErr(w, "draft has no generated content to repair", http.StatusBadRequest)
		return
	}

	llmConfig, err := modelconfig.LoadLLMConfig(r.Context(), db, userID)
	if err != nil {
		llmConfig = map[string]any{}
	}

	var warnings []string
	if draft.GenerateWarning != "" {
		for _, w := range strings.Split(draft.GenerateWarning, "; ") {
			// Strip stale repair-failure markers: they are not actionable context for the LLM.
			if !strings.HasPrefix(w, "[修复失败]") {
				warnings = append(warnings, w)
			}
		}
	}

	log.Printf("[ai_repair] draft_id=%s target=%q hint_len=%d prev_status=%q warnings=%v plugin_yaml_empty=%v state_yaml_empty=%v",
		draftID, body.Target, len(body.RepairHint), draft.GenerateStatus,
		warnings, draft.PluginYAMLContent == "", draft.StateYAMLContent == "")

	prevStatus := draft.GenerateStatus
	payload := pluginDraftRepairPayload{
		DraftID:    draftID,
		UserID:     userID,
		Target:     strings.TrimSpace(body.Target),
		RepairHint: strings.TrimSpace(body.RepairHint),
		Warnings:   warnings,
		PrevStatus: prevStatus,
		LLMConfig:  llmConfig,
	}

	// Set status to repairing before enqueueing so the client sees it immediately.
	if err := db.Model(&draft).Update("generate_status", generateStatusRepairing).Error; err != nil {
		common.ReplyErr(w, "lock failed", http.StatusInternalServerError)
		return
	}

	if _, err := asyncjob.Enqueue(r.Context(), db, asyncjob.EnqueueRequest{
		JobType:      pluginDraftRepairJobType,
		ResourceType: "plugin_draft",
		ResourceID:   draftID,
		Payload:      payload,
		MaxAttempts:  1,
		CreateUserID: userID,
	}); err != nil {
		// Roll back status if we can't enqueue.
		log.Printf("[ai_repair] enqueue failed draft_id=%s err=%v, rolling back to prev_status=%q", draftID, err, prevStatus)
		_ = db.Model(&draft).Update("generate_status", prevStatus)
		common.ReplyErr(w, "enqueue failed", http.StatusInternalServerError)
		return
	}
	log.Printf("[ai_repair] job enqueued draft_id=%s status=repairing", draftID)

	draft.GenerateStatus = generateStatusRepairing
	common.ReplyOK(w, toDraftResponse(draft))
}
