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
	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
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
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Content               string `json:"content"`
	PluginYAMLContent     string `json:"plugin_yaml_content"`
	StateYAMLContent      string `json:"state_yaml_content"`
	StateLayoutContent    string `json:"state_layout_content"`
	ScenarioContent       string `json:"scenario_content"`
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
	PublishedPluginRef    string `json:"published_plugin_ref"`
	CurrentRevisionID     string `json:"current_revision_id"`
	CurrentRevisionNo     int64  `json:"current_revision_no"`
	PublishedStatus       string `json:"published_status"`
	BaseRevisionID        string `json:"base_revision_id"`
	DraftDirty            bool   `json:"draft_dirty"`
	LastRepairRunID       string `json:"last_repair_run_id"`
}

func toDraftResponse(d orm.PluginDraft) draftResponse {
	return draftResponse{
		ID:                    d.ID,
		Name:                  d.Name,
		Content:               d.Content,
		PluginYAMLContent:     d.PluginYAMLContent,
		StateYAMLContent:      d.StateYAMLContent,
		StateLayoutContent:    d.StateLayoutContent,
		ScenarioContent:       d.ScenarioContent,
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

func toEnrichedDraftResponse(db *gorm.DB, d orm.PluginDraft) draftResponse {
	resp := toDraftResponse(d)
	var repairRun orm.PluginRepairRun
	if db.Where("draft_id=?", d.ID).Order("created_at DESC").First(&repairRun).Error == nil {
		resp.LastRepairRunID = repairRun.ID
	}
	if d.PluginID != "" {
		var p orm.PluginResource
		if db.Where("owner_user_id=? AND plugin_id=?", d.CreatedBy, d.PluginID).First(&p).Error == nil {
			resp.Published, resp.PublishedPluginRef = true, p.PluginRef
			resp.CurrentRevisionID, resp.CurrentRevisionNo, resp.PublishedStatus = p.HeadRevisionID, p.Version, p.Status
			baseID := d.BaseRevisionID
			if baseID == "" {
				baseID = p.HeadRevisionID
			}
			resp.BaseRevisionID = baseID
			var base orm.PluginRevision
			if db.Where("id=? AND plugin_resource_id=?", baseID, p.ID).First(&base).Error == nil {
				if files, err := pluginFiles(d); err == nil {
					resp.DraftDirty = pluginTreeHash(files) != base.TreeHash
				}
			}
		}
	}
	return resp
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
		records = append(records, toEnrichedDraftResponse(db, d))
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

	common.ReplyOK(w, toEnrichedDraftResponse(store.DB(), draft))
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

	common.ReplyOK(w, toEnrichedDraftResponse(store.DB(), draft))
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
	if needsVersionCheck && body.Version == nil {
		common.ReplyErr(w, "version required", http.StatusBadRequest)
		return
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
	if needsVersionCheck {
		updates["version"] = gorm.Expr("version + 1")
	}

	query := db.Model(&orm.PluginDraft{}).Where("id = ? AND created_by = ?", draftID, userID)
	if needsVersionCheck {
		query = query.Where("version = ?", *body.Version)
	}
	result := query.Updates(updates)
	if result.Error != nil {
		err := result.Error
		if strings.Contains(err.Error(), "idx_plugin_drafts_user_plugin_id") ||
			strings.Contains(err.Error(), "unique") && strings.Contains(err.Error(), "plugin_id") {
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

	db := store.DB()
	var draft orm.PluginDraft
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
	// reuse decisions made for a Plugin the user explicitly deleted.
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("draft_id = ? AND user_id = ?", draftID, userID).Delete(&orm.PluginRepairRun{}).Error; err != nil {
			return err
		}
		if err := tx.Where("draft_id = ? AND user_id = ?", draftID, userID).Delete(&orm.PluginGenerationAnalysis{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ? AND created_by = ?", draftID, userID).Delete(&orm.PluginDraft{}).Error
	}); err != nil {
		common.ReplyErr(w, "delete failed", http.StatusInternalServerError)
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
		Reanalyze   bool   `json:"reanalyze"`
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
	var skillSnapshot pluginSourceSkillSnapshot
	if body.SkillID != "" {
		snapshot, err := loadPluginSourceSkill(r.Context(), db, userID, body.SkillID)
		if err != nil {
			if isPluginSourceSkillNotFound(err) {
				common.ReplyErr(w, "skill not found", http.StatusNotFound)
			} else {
				common.ReplyErr(w, "skill not found", http.StatusInternalServerError)
			}
			return
		}
		skillSnapshot = snapshot
		skillContent = snapshot.skillMD()
		skillName = snapshot.Name
	}

	sourceUpdates := map[string]any{
		"generate_status": generateStatusGenerating,
		"updated_at":      time.Now().UTC(),
	}
	if body.SkillID != "" {
		sourceUpdates["generate_status"] = generateStatusAnalyzing
	}
	// Set source_type on first generation (don't overwrite if already set by CreatePluginDraft).
	if draft.SourceType == "" {
		if body.SkillID != "" {
			sourceUpdates["source_type"] = "skill"
		} else {
			sourceUpdates["source_type"] = "ai"
		}
	}
	if body.SkillID != "" {
		sourceUpdates["source_type"] = "skill"
	}
	if body.SkillID != "" {
		sourceUpdates["source_skill_id"] = body.SkillID
		sourceUpdates["source_skill_name"] = skillName
	}
	if body.SkillID != "" {
		sourceUpdates["source_skill_revision_id"] = skillSnapshot.RevisionID
		sourceUpdates["source_skill_revision_no"] = skillSnapshot.RevisionNo
		sourceUpdates["source_skill_tree_hash"] = skillSnapshot.TreeHash
	}

	if err := db.Model(&draft).Updates(sourceUpdates).Error; err != nil {
		common.ReplyErr(w, "update failed", http.StatusInternalServerError)
		return
	}
	draft.GenerateStatus = generateStatusGenerating
	if body.SkillID != "" {
		draft.GenerateStatus = generateStatusAnalyzing
	}
	if st, ok := sourceUpdates["source_type"].(string); ok {
		draft.SourceType = st
	}
	if sid, ok := sourceUpdates["source_skill_id"].(string); ok {
		draft.SourceSkillID = sid
	}
	if sn, ok := sourceUpdates["source_skill_name"].(string); ok {
		draft.SourceSkillName = sn
	}

	var skillPackage map[string]any
	if body.SkillID != "" {
		if b, marshalErr := json.Marshal(skillSnapshot); marshalErr == nil {
			_ = json.Unmarshal(b, &skillPackage)
		}
	}
	selectedCandidateJSON := ""
	reusableScripts := map[string]string(nil)
	if body.SkillID != "" && !body.Reanalyze {
		var cached orm.PluginGenerationAnalysis
		// A rejection is evaluator-dependent rather than a reusable generated artifact.
		// Re-run it so prompt/model improvements cannot leave a Skill permanently
		// blocked by an old negative analysis. Positive and confirmation-required
		// analyses remain reusable for an unchanged Skill revision.
		cacheErr := db.Where("user_id=? AND source_skill_id=? AND source_skill_revision_id=? AND source_skill_tree_hash=? AND status IN ?", userID, body.SkillID, skillSnapshot.RevisionID, skillSnapshot.TreeHash, []string{"generatable", "needs_confirmation"}).Order("created_at DESC").First(&cached).Error
		if cacheErr == nil {
			now := time.Now().UTC()
			clone := cached
			clone.ID = uuid.NewString()
			clone.DraftID = draft.ID
			clone.CreatedAt = now
			clone.UpdatedAt = now
			packageJSON, _ := json.Marshal(manifestOnlySkillPackage(skillPackage))
			clone.SourcePackageJSON = string(packageJSON)
			if err := db.Create(&clone).Error; err == nil {
				draftStatus := clone.Status
				if draftStatus == "generatable" {
					draftStatus = generateStatusGenerating
				}
				_ = db.Model(&draft).Updates(map[string]any{"source_analysis_id": clone.ID, "generate_status": draftStatus, "generate_error": clone.VerdictMessage, "generate_warning": ignoredScriptWarningJSON(clone.ScriptReportJSON), "updated_at": now}).Error
				if clone.Status == "needs_confirmation" || clone.Status == "rejected" {
					_ = db.Where("id=?", draft.ID).First(&draft).Error
					common.ReplyOK(w, toEnrichedDraftResponse(db, draft))
					return
				}
				selectedCandidateJSON = cachedAnalysisContext(clone)
				reusableScripts = reusableSkillScriptsJSON(skillPackage, clone.ScriptReportJSON)
			}
		}
	}
	_, err := asyncjob.Enqueue(r.Context(), db, asyncjob.EnqueueRequest{
		JobType:      pluginDraftGenerateJobType,
		ResourceType: "plugin_draft",
		ResourceID:   draftID,
		Payload: pluginDraftGeneratePayload{
			DraftID:               draftID,
			Name:                  draft.Name,
			Description:           body.Description,
			SkillContent:          skillContent,
			SkillPackage:          skillPackage,
			SourceSkillRevisionID: skillSnapshot.RevisionID,
			SelectedCandidateJSON: selectedCandidateJSON,
			ReusableScripts:       reusableScripts,
			UserID:                userID,
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

	common.ReplyOK(w, toEnrichedDraftResponse(store.DB(), draft))
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
		RepairHint       string `json:"repair_hint"`
		Target           string `json:"target"` // 'statemachine' | 'ui' | 'scenario'
		Mode             string `json:"mode"`
		DraftVersion     int    `json:"draft_version"`
		SourceAnalysisID string `json:"source_analysis_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.Target == "" {
		body.Target = "statemachine"
	}
	if body.Target != "statemachine" && body.Target != "ui" && body.Target != "scenario" && body.Target != "scripts" && body.Target != "full" {
		common.ReplyErr(w, "invalid repair target", http.StatusBadRequest)
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
	if body.DraftVersion == 0 {
		body.DraftVersion = draft.Version
	}
	if body.DraftVersion != draft.Version {
		common.ReplyErr(w, "repair stale draft", http.StatusConflict)
		return
	}
	if body.Mode == "" {
		body.Mode = "plugin_local"
	}
	if body.Mode != "plugin_local" && body.Mode != "source_aware" {
		common.ReplyErr(w, "invalid repair mode", http.StatusBadRequest)
		return
	}
	if body.Mode == "source_aware" {
		if body.SourceAnalysisID == "" {
			body.SourceAnalysisID = draft.SourceAnalysisID
		}
		var analysis orm.PluginGenerationAnalysis
		if body.SourceAnalysisID == "" || db.Where("id=? AND draft_id=?", body.SourceAnalysisID, draft.ID).First(&analysis).Error != nil {
			common.ReplyErr(w, "source analysis not found", http.StatusBadRequest)
			return
		}
		body.RepairHint = body.RepairHint + "\nRespect source analysis: " + analysis.CandidatesJSON + "\nCoverage: " + analysis.CoverageReportJSON + "\nTool mappings: " + analysis.ToolMappingReportJSON
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
		DraftID:      draftID,
		UserID:       userID,
		Target:       strings.TrimSpace(body.Target),
		RepairHint:   strings.TrimSpace(body.RepairHint),
		Warnings:     warnings,
		PrevStatus:   prevStatus,
		LLMConfig:    llmConfig,
		DraftVersion: draft.Version,
		Mode:         body.Mode,
	}
	repairRun := orm.PluginRepairRun{ID: uuid.NewString(), DraftID: draft.ID, UserID: userID, BasePluginRevisionID: draft.BaseRevisionID, DraftVersionBefore: draft.Version, Target: body.Target, Mode: body.Mode, SourceAnalysisID: body.SourceAnalysisID, SourceSkillRevisionID: draft.SourceSkillRevisionID, RepairHint: body.RepairHint, DiagnosticsBeforeJSON: diagnosticsJSON(diagnosePlugin(draft.PluginYAMLContent, draft.StateYAMLContent, draft.ScenarioContent, draft.ScriptsContent)), ChangesJSON: "{}", DiagnosticsAfterJSON: "{}", Status: "queued", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := db.Create(&repairRun).Error; err != nil {
		common.ReplyErr(w, "create repair run failed", http.StatusInternalServerError)
		return
	}
	payload.RepairRunID = repairRun.ID

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
	common.ReplyOK(w, toEnrichedDraftResponse(store.DB(), draft))
}
