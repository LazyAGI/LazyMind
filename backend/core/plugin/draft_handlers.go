package plugin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// draftResponse is the JSON shape returned for a single PluginDraft.
type draftResponse struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Content           string `json:"content"`
	PluginYAMLContent string `json:"plugin_yaml_content"`
	StateYAMLContent  string `json:"state_yaml_content"`
	ScenarioContent   string `json:"scenario_content"`
	ScriptsContent    string `json:"scripts_content"`
	GenerateStatus    string `json:"generate_status"`
	CreatedBy         string `json:"created_by"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

func toDraftResponse(d orm.PluginDraft) draftResponse {
	return draftResponse{
		ID:                d.ID,
		Name:              d.Name,
		Content:           d.Content,
		PluginYAMLContent: d.PluginYAMLContent,
		StateYAMLContent:  d.StateYAMLContent,
		ScenarioContent:   d.ScenarioContent,
		ScriptsContent:    d.ScriptsContent,
		GenerateStatus:    d.GenerateStatus,
		CreatedBy:         d.CreatedBy,
		CreatedAt:         d.CreatedAt.Format(time.RFC3339),
		UpdatedAt:         d.UpdatedAt.Format(time.RFC3339),
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
// Body: { "name": "...", "content": "..." }
func CreatePluginDraft(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Name    string `json:"name"`
		Content string `json:"content"`
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

	draft := orm.PluginDraft{
		ID:        uuid.New().String(),
		Name:      body.Name,
		Content:   body.Content,
		CreatedBy: userID,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
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
// Body: { "content": "...", "plugin_yaml_content": "...", "state_yaml_content": "...", "scenario_content": "...", "scripts_content": "..." }
// Persists all provided fields; absent fields are left unchanged.
func SavePluginDraft(w http.ResponseWriter, r *http.Request) {
	draftID := common.PathVar(r, "draft_id")
	userID := common.UserID(r)
	if draftID == "" {
		common.ReplyErr(w, "draft_id required", http.StatusBadRequest)
		return
	}

	var body struct {
		Content           *string `json:"content"`
		PluginYAMLContent *string `json:"plugin_yaml_content"`
		StateYAMLContent  *string `json:"state_yaml_content"`
		ScenarioContent   *string `json:"scenario_content"`
		ScriptsContent    *string `json:"scripts_content"`
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

	updates := map[string]any{"updated_at": time.Now().UTC()}
	if body.Content != nil {
		updates["content"] = *body.Content
	}
	if body.PluginYAMLContent != nil {
		updates["plugin_yaml_content"] = *body.PluginYAMLContent
	}
	if body.StateYAMLContent != nil {
		updates["state_yaml_content"] = *body.StateYAMLContent
	}
	if body.ScenarioContent != nil {
		updates["scenario_content"] = *body.ScenarioContent
	}
	if body.ScriptsContent != nil {
		updates["scripts_content"] = *body.ScriptsContent
	}

	if err := db.Model(&draft).Updates(updates).Error; err != nil {
		common.ReplyErr(w, "save failed", http.StatusInternalServerError)
		return
	}

	// Reload to get the updated values.
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
	if body.SkillID != "" {
		var skillRow struct {
			Content string
		}
		if err := db.Raw("SELECT content FROM skill_resources WHERE id = ? AND owner_user_id = ?", body.SkillID, userID).Scan(&skillRow).Error; err != nil || skillRow.Content == "" {
			common.ReplyErr(w, "skill not found", http.StatusBadRequest)
			return
		}
		skillContent = skillRow.Content
	}

	if err := db.Model(&draft).Updates(map[string]any{
		"generate_status": generateStatusGenerating,
		"updated_at":      time.Now().UTC(),
	}).Error; err != nil {
		common.ReplyErr(w, "update failed", http.StatusInternalServerError)
		return
	}
	draft.GenerateStatus = generateStatusGenerating

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
