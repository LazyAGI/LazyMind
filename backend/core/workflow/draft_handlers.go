package workflow

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// uuidPattern matches a standard UUID v4 string (8-4-4-4-12 hex digits with hyphens).
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// workflowIDPattern extracts the `id:` field from plugin.yaml.
// Matches bare or single/double-quoted values on a line of its own.
var workflowIDPattern = regexp.MustCompile(`(?m)^id:\s*["']?([^"'\n]+?)["']?\s*$`)

// extractWorkflowID returns the plugin id from a plugin.yaml string, or "" if not found.
func extractWorkflowID(yamlContent string) string {
	if m := workflowIDPattern.FindStringSubmatch(yamlContent); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// isBuiltinWorkflowID returns true when id does not look like a UUID.
// Built-in plugin IDs are human-readable strings (e.g. "image-plugin"),
// while user draft IDs are always UUID v4 strings generated on creation.
// This check is a first-line defence; the DB query's WHERE created_by=userID
// would reject any mistaken match anyway, but returning 403 explicitly avoids
// confusing "not found" responses and makes the intent clear.
func isBuiltinWorkflowID(id string) bool {
	return !uuidPattern.MatchString(strings.ToLower(id))
}

// draftResponse is the JSON shape returned for a single WorkflowDraft.
type draftResponse struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Content               string `json:"content"`
	WorkflowYAMLContent   string `json:"workflow_yaml_content"`
	StateYAMLContent      string `json:"state_yaml_content"`
	StateLayoutContent    string `json:"state_layout_content"`
	ScenarioContent       string `json:"scenario_content"`
	DriverContent         string `json:"driver_content"`
	ScriptsContent        string `json:"scripts_content"`
	DesignBriefContent    string `json:"design_brief_content"`
	GenerateStatus        string `json:"generate_status"`
	GenerateError         string `json:"generate_error"`
	GenerateWarning       string `json:"generate_warning"`
	Version               int    `json:"version"`
	CreatedBy             string `json:"created_by"`
	CreatedAt             string `json:"created_at"`
	UpdatedAt             string `json:"updated_at"`
	SourceType            string `json:"source_type"`
	SourceSkillID         string `json:"source_skill_id"`
	SourceSkillName       string `json:"source_skill_name"`
	SourceSkillRevisionID string `json:"source_skill_revision_id"`
	SourceSkillRevisionNo int64  `json:"source_skill_revision_no"`
	SourceSkillTreeHash   string `json:"source_skill_tree_hash"`
	SourceAnalysisID      string `json:"source_analysis_id"`
	Published             bool   `json:"published"`
	PublishedWorkflowRef  string `json:"published_workflow_ref"`
	CurrentRevisionID     string `json:"current_revision_id"`
	CurrentRevisionNo     int64  `json:"current_revision_no"`
	PublishedStatus       string `json:"published_status"`
	BaseRevisionID        string `json:"base_revision_id"`
	DraftDirty            bool   `json:"draft_dirty"`
	LastRepairRunID       string `json:"last_repair_run_id"`
}

func toDraftResponse(d orm.WorkflowDraft) draftResponse {
	return draftResponse{
		ID:                    d.ID,
		Name:                  d.Name,
		Content:               d.Content,
		WorkflowYAMLContent:   d.WorkflowYAMLContent,
		StateYAMLContent:      d.StateYAMLContent,
		StateLayoutContent:    d.StateLayoutContent,
		ScenarioContent:       d.ScenarioContent,
		DriverContent:         d.DriverContent,
		ScriptsContent:        d.ScriptsContent,
		DesignBriefContent:    d.DesignBriefContent,
		GenerateStatus:        d.GenerateStatus,
		GenerateError:         d.GenerateError,
		GenerateWarning:       d.GenerateWarning,
		Version:               d.Version,
		CreatedBy:             d.CreatedBy,
		CreatedAt:             d.CreatedAt.Format(time.RFC3339),
		UpdatedAt:             d.UpdatedAt.Format(time.RFC3339),
		SourceType:            d.SourceType,
		SourceSkillID:         d.SourceSkillID,
		SourceSkillName:       d.SourceSkillName,
		SourceSkillRevisionID: d.SourceSkillRevisionID,
		SourceSkillRevisionNo: d.SourceSkillRevisionNo,
		SourceSkillTreeHash:   d.SourceSkillTreeHash,
		SourceAnalysisID:      d.SourceAnalysisID,
		BaseRevisionID:        d.BaseRevisionID,
	}
}

func toEnrichedDraftResponse(db *gorm.DB, d orm.WorkflowDraft) draftResponse {
	resp := toDraftResponse(d)
	var repairRun orm.WorkflowRepairRun
	if db.Where("draft_id=?", d.ID).Order("created_at DESC").First(&repairRun).Error == nil {
		resp.LastRepairRunID = repairRun.ID
	}
	if d.WorkflowID != "" {
		var p orm.WorkflowResource
		if db.Where("owner_user_id=? AND plugin_id=?", d.CreatedBy, d.WorkflowID).First(&p).Error == nil {
			resp.Published, resp.PublishedWorkflowRef = true, p.WorkflowRef
			resp.CurrentRevisionID, resp.CurrentRevisionNo, resp.PublishedStatus = p.HeadRevisionID, p.Version, p.Status
			baseID := d.BaseRevisionID
			if baseID == "" {
				baseID = p.HeadRevisionID
			}
			resp.BaseRevisionID = baseID
			var base orm.WorkflowRevision
			if db.Where("id=? AND plugin_resource_id=?", baseID, p.ID).First(&base).Error == nil {
				if files, err := workflowFiles(d); err == nil {
					resp.DraftDirty = workflowTreeHash(files) != base.TreeHash
				}
			}
		}
	}
	return resp
}

// ListWorkflowDrafts handles GET /workflow-drafts
// Returns the drafts owned by the current user, paginated.
func ListWorkflowDrafts(w http.ResponseWriter, r *http.Request) {
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
	if err := db.Model(&orm.WorkflowDraft{}).Where("created_by = ?", userID).Count(&total).Error; err != nil {
		common.ReplyErr(w, "query failed", http.StatusInternalServerError)
		return
	}

	var drafts []orm.WorkflowDraft
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
		records = append(records, toEnrichedDraftResponse(db, d))
	}

	common.ReplyOK(w, map[string]any{
		"records": records,
		"total":   total,
	})
}

// CreateWorkflowDraft handles POST /workflow-drafts
// Body: { "name": "...", "content": "...", "source_type": "blank|ai|skill" }
func CreateWorkflowDraft(w http.ResponseWriter, r *http.Request) {
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

	draft := orm.WorkflowDraft{
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

	common.ReplyOK(w, toEnrichedDraftResponse(store.DB(), draft))
}

// GetWorkflowDraft handles GET /workflow-drafts/{draft_id}
func GetWorkflowDraft(w http.ResponseWriter, r *http.Request) {
	draftID := common.PathVar(r, "draft_id")
	userID := common.UserID(r)
	if draftID == "" {
		common.ReplyErr(w, "draft_id required", http.StatusBadRequest)
		return
	}

	var draft orm.WorkflowDraft
	if err := store.DB().Where("id = ? AND created_by = ?", draftID, userID).First(&draft).Error; err != nil {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}

	common.ReplyOK(w, toEnrichedDraftResponse(store.DB(), draft))
}

// SaveWorkflowDraft handles POST /workflow-drafts/{draft_id}:save
//
//	Body: {
//	  "content": "...",
//	  "workflow_yaml_content": "...",
//	  "state_yaml_content": "...",
//	  "state_layout_content": "...",   // no version check, last-write-wins
//	  "scenario_content": "...",
//	  "scripts_content": "...",
//	  "version": 3                      // required when sending workflow_yaml_content or state_yaml_content
//	}
//
// Returns 409 Conflict when version is stale (another write already incremented it).
func SaveWorkflowDraft(w http.ResponseWriter, r *http.Request) {
	draftID := common.PathVar(r, "draft_id")
	userID := common.UserID(r)
	if draftID == "" {
		common.ReplyErr(w, "draft_id required", http.StatusBadRequest)
		return
	}
	if isBuiltinWorkflowID(draftID) {
		common.ReplyErr(w, "built-in plugins cannot be modified", http.StatusForbidden)
		return
	}

	var body struct {
		Content             *string `json:"content"`
		WorkflowYAMLContent *string `json:"workflow_yaml_content"`
		StateYAMLContent    *string `json:"state_yaml_content"`
		StateLayoutContent  *string `json:"state_layout_content"`
		ScenarioContent     *string `json:"scenario_content"`
		ScriptsContent      *string `json:"scripts_content"`
		// Version is the caller's last-known version. Required when writing
		// workflow_yaml_content or state_yaml_content; ignored otherwise.
		Version *int `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}

	db := store.DB()
	var draft orm.WorkflowDraft
	if err := db.Where("id = ? AND created_by = ?", draftID, userID).First(&draft).Error; err != nil {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}

	// --- Optimistic-lock check for versioned fields ---
	needsVersionCheck := body.WorkflowYAMLContent != nil || body.StateYAMLContent != nil
	if needsVersionCheck && body.Version == nil {
		common.ReplyErr(w, "version required", http.StatusBadRequest)
		return
	}

	updates := map[string]any{"updated_at": time.Now().UTC()}
	if body.Content != nil {
		updates["content"] = *body.Content
	}
	if body.WorkflowYAMLContent != nil {
		updates["workflow_yaml_content"] = *body.WorkflowYAMLContent
		// Keep workflow_id in sync so the per-user unique index can enforce deduplication.
		updates["workflow_id"] = extractWorkflowID(*body.WorkflowYAMLContent)
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
	if needsVersionCheck {
		updates["version"] = gorm.Expr("version + 1")
	}

	query := db.Model(&orm.WorkflowDraft{}).Where("id = ? AND created_by = ?", draftID, userID)
	if needsVersionCheck {
		query = query.Where("version = ?", *body.Version)
	}
	result := query.Updates(updates)
	if result.Error != nil {
		err := result.Error
		if strings.Contains(err.Error(), "idx_workflow_drafts_user_workflow_id") ||
			strings.Contains(err.Error(), "unique") && strings.Contains(err.Error(), "workflow_id") {
			common.ReplyErr(w, "plugin id already exists for this user", http.StatusConflict)
			return
		}
		common.ReplyErr(w, "save failed", http.StatusInternalServerError)
		return
	}
	if needsVersionCheck && result.RowsAffected == 0 {
		// The version predicate and update execute as one SQL statement, so two
		// concurrent writers cannot both pass a separate check and overwrite one
		// another. Return the winner's authoritative state to the stale caller.
		if err := db.Where("id = ? AND created_by = ?", draftID, userID).First(&draft).Error; err != nil {
			common.ReplyErr(w, "reload failed", http.StatusInternalServerError)
			return
		}
		common.ReplyErrWithData(w, "conflict", toDraftResponse(draft), http.StatusConflict)
		return
	}
	// Reload to return the authoritative post-save state.
	if err := db.Where("id = ?", draftID).First(&draft).Error; err != nil {
		common.ReplyErr(w, "reload failed", http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, toEnrichedDraftResponse(store.DB(), draft))
}

// DeleteWorkflowDraft handles DELETE /workflow-drafts/{draft_id}
func DeleteWorkflowDraft(w http.ResponseWriter, r *http.Request) {
	draftID := common.PathVar(r, "draft_id")
	userID := common.UserID(r)
	if draftID == "" {
		common.ReplyErr(w, "draft_id required", http.StatusBadRequest)
		return
	}
	if isBuiltinWorkflowID(draftID) {
		common.ReplyErr(w, "built-in plugins cannot be modified", http.StatusForbidden)
		return
	}

	db := store.DB()
	var draft orm.WorkflowDraft
	if err := db.Select("id").Where("id = ? AND created_by = ?", draftID, userID).First(&draft).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			common.ReplyErr(w, "not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "delete failed", http.StatusInternalServerError)
		return
	}

	// Analyses and repair runs are draft-scoped cached generation state. Remove
	// them atomically with the draft so a later import of the same Skill cannot
	// reuse decisions made for a Workflow the user explicitly deleted.
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("draft_id = ? AND user_id = ?", draftID, userID).Delete(&orm.WorkflowRepairRun{}).Error; err != nil {
			return err
		}
		if err := tx.Where("draft_id = ? AND user_id = ?", draftID, userID).Delete(&orm.WorkflowGenerationAnalysis{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ? AND created_by = ?", draftID, userID).Delete(&orm.WorkflowDraft{}).Error
	}); err != nil {
		common.ReplyErr(w, "delete failed", http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, nil)
}
