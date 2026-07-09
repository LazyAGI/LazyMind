package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
)

const pluginDraftGenerateJobType = "plugin_draft_generate"
const pluginDraftRepairJobType = "plugin_draft_repair"

const (
	generateStatusGenerating   = "generating"
	generateStatusBriefDone    = "brief_done"
	generateStatusSkeletonDone = "skeleton_done"
	generateStatusStateDone    = "state_done"
	generateStatusDone         = "done"
	generateStatusFailed       = "failed"
	generateStatusRepairing    = "repairing"
)

const (
	generateErrInvalidPayload = "invalid_payload"
	generateErrDraftNotFound  = "draft_not_found"
	generateErrAlgoFailed     = "algo_failed"
	generateErrSaveFailed     = "save_failed"
)

type pluginDraftGeneratePayload struct {
	DraftID      string `json:"draft_id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	SkillContent string `json:"skill_content,omitempty"`
	UserID       string `json:"user_id"`
}

type pluginDraftRepairPayload struct {
	DraftID    string         `json:"draft_id"`
	UserID     string         `json:"user_id"`
	Target     string         `json:"target"`      // 'statemachine' | 'ui' | 'scenario'
	RepairHint string         `json:"repair_hint"` // optional
	Warnings   []string       `json:"warnings,omitempty"`
	PrevStatus string         `json:"prev_status"`
	LLMConfig  map[string]any `json:"llm_config,omitempty"`
}

// RegisterPluginDraftGenerateJob registers the async job handler.
// Call this once at startup (e.g. from main.go).
func RegisterPluginDraftGenerateJob() {
	asyncjob.Register(pluginDraftGenerateJobType, handlePluginDraftGenerateJob)
	asyncjob.Register(pluginDraftRepairJobType, handlePluginDraftRepairJob)
}

func handlePluginDraftGenerateJob(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
	var payload pluginDraftGeneratePayload
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
		return asyncjob.Result{ErrorCode: generateErrInvalidPayload}, fmt.Errorf("decode payload: %w", err)
	}

	db := store.DB()
	if db == nil {
		return asyncjob.Result{ErrorCode: generateErrDraftNotFound}, fmt.Errorf("store not initialised")
	}

	var draft orm.PluginDraft
	if err := db.WithContext(ctx).Where("id = ? AND created_by = ?", payload.DraftID, payload.UserID).First(&draft).Error; err != nil {
		return asyncjob.Result{ErrorCode: generateErrDraftNotFound}, fmt.Errorf("draft not found: %w", err)
	}

	llmConfig, err := modelconfig.LoadLLMConfig(ctx, db, payload.UserID)
	if err != nil {
		llmConfig = map[string]any{}
	}

	// ── Phase 0: Design Brief ────────────────────────────────────────────────
	// Generate a design brief (Markdown) that describes slots, steps, and flow.
	// Subsequent phases receive this brief as an authoritative reference so that
	// slot IDs remain consistent across phases.
	// On failure we fall back gracefully (brief stays empty) so existing drafts are unaffected.
	designBrief := ""
	briefResp, briefErr := algo.DesignBrief(ctx, algo.DesignBriefRequest{
		Name:         draft.Name,
		Description:  payload.Description,
		SkillContent: payload.SkillContent,
		LLMConfig:    llmConfig,
	})
	if briefErr != nil {
		// Non-fatal: log and continue without a brief.
		_ = db.WithContext(ctx).Model(&orm.PluginDraft{}).Where("id = ?", payload.DraftID).Updates(map[string]any{
			"generate_warning": fmt.Sprintf("phase0 design_brief: %s", briefErr),
			"updated_at":       time.Now().UTC(),
		}).Error
	} else {
		designBrief = briefResp.DesignBrief
		if err := db.WithContext(ctx).Model(&orm.PluginDraft{}).Where("id = ?", payload.DraftID).Updates(map[string]any{
			"design_brief_content": designBrief,
			"generate_status":      generateStatusBriefDone,
			"updated_at":           time.Now().UTC(),
		}).Error; err != nil {
			return asyncjob.Result{ErrorCode: generateErrSaveFailed}, fmt.Errorf("save design_brief: %w", err)
		}
	}
	// ── Phase 1: Skeleton ────────────────────────────────────────────────────
	skeletonResp, err := algo.GenerateSkeleton(ctx, algo.GenerateSkeletonRequest{
		Name:         draft.Name,
		Description:  payload.Description,
		SkillContent: payload.SkillContent,
		DesignBrief:  designBrief,
		LLMConfig:    llmConfig,
	})
	if err != nil {
		_ = markGenerateFailed(db, payload.DraftID, fmt.Sprintf("phase1 skeleton: %s", err))
		return asyncjob.Result{ErrorCode: generateErrAlgoFailed}, fmt.Errorf("phase1 skeleton: %w", err)
	}
	if err := db.WithContext(ctx).Model(&orm.PluginDraft{}).Where("id = ?", payload.DraftID).Updates(map[string]any{
		"plugin_yaml_content": skeletonResp.PluginYAML,
		"generate_status":     generateStatusSkeletonDone,
		"updated_at":          time.Now().UTC(),
	}).Error; err != nil {
		return asyncjob.Result{ErrorCode: generateErrSaveFailed}, fmt.Errorf("save skeleton: %w", err)
	}

	// ── Phase 2: State Machine ───────────────────────────────────────────────
	stateResp, err := algo.GenerateStateMachine(ctx, algo.GenerateStateMachineRequest{
		Name:        draft.Name,
		PluginYAML:  skeletonResp.PluginYAML,
		DesignBrief: designBrief,
		LLMConfig:   llmConfig,
	})
	if err != nil {
		_ = markGenerateFailed(db, payload.DraftID, fmt.Sprintf("phase2 state_machine: %s", err))
		return asyncjob.Result{ErrorCode: generateErrAlgoFailed}, fmt.Errorf("phase2 state_machine: %w", err)
	}
	// Use the (possibly slot-repaired) plugin_yaml returned by Phase 2.
	// Falls back to Phase 1 output when Phase 2 did not modify it.
	finalPluginYAML := skeletonResp.PluginYAML
	if stateResp.PluginYAML != "" {
		finalPluginYAML = stateResp.PluginYAML
	}
	stateUpdates := map[string]any{
		"state_yaml_content":  stateResp.StateYAML,
		"plugin_yaml_content": finalPluginYAML,
		"generate_status":     generateStatusStateDone,
		"updated_at":          time.Now().UTC(),
	}
	if len(stateResp.Warnings) > 0 {
		stateUpdates["generate_warning"] = strings.Join(stateResp.Warnings, "; ")
	}
	if err := db.WithContext(ctx).Model(&orm.PluginDraft{}).Where("id = ?", payload.DraftID).Updates(stateUpdates).Error; err != nil {
		return asyncjob.Result{ErrorCode: generateErrSaveFailed}, fmt.Errorf("save state_machine: %w", err)
	}

	// ── Phase 3: Scenario + Scripts ──────────────────────────────────────────
	scenarioResp, err := algo.GenerateScenarioScripts(ctx, algo.GenerateScenarioScriptsRequest{
		Name:        draft.Name,
		PluginYAML:  finalPluginYAML,
		StateYAML:   stateResp.StateYAML,
		DesignBrief: designBrief,
		LLMConfig:   llmConfig,
	})
	if err != nil {
		// Phase 3 failure is non-fatal: skeleton + state are already saved.
		// Write to generate_warning (not generate_error) since the plugin is still usable.
		existingWarning := ""
		var currentDraft orm.PluginDraft
		if dbErr := db.WithContext(ctx).Select("generate_warning").Where("id = ?", payload.DraftID).First(&currentDraft).Error; dbErr == nil {
			existingWarning = currentDraft.GenerateWarning
		}
		newWarning := fmt.Sprintf("phase3 scenario_scripts: %s", err)
		if existingWarning != "" {
			newWarning = existingWarning + "; " + newWarning
		}
		_ = db.WithContext(ctx).Model(&orm.PluginDraft{}).Where("id = ?", payload.DraftID).Updates(map[string]any{
			"generate_status":  generateStatusDone,
			"generate_warning": newWarning,
			"version":          gorm.Expr("version + 1"),
			"updated_at":       time.Now().UTC(),
		}).Error
		return asyncjob.Result{}, nil
	}

	// Encode scripts map as JSON string for storage.
	scriptsJSON := "{}"
	if len(scenarioResp.Scripts) > 0 {
		if b, jerr := json.Marshal(scenarioResp.Scripts); jerr == nil {
			scriptsJSON = string(b)
		}
	}

	if err := db.WithContext(ctx).Model(&orm.PluginDraft{}).Where("id = ?", payload.DraftID).Updates(map[string]any{
		"scenario_content": scenarioResp.ScenarioMD,
		"scripts_content":  scriptsJSON,
		"generate_status":  generateStatusDone,
		"generate_error":   "",
		"version":          gorm.Expr("version + 1"),
		"updated_at":       time.Now().UTC(),
	}).Error; err != nil {
		return asyncjob.Result{ErrorCode: generateErrSaveFailed}, fmt.Errorf("save scenario_scripts: %w", err)
	}

	return asyncjob.Result{}, nil
}

func markGenerateFailed(db *gorm.DB, draftID string, errMsg string) error {
	return db.Model(&orm.PluginDraft{}).Where("id = ?", draftID).Updates(map[string]any{
		"generate_status": generateStatusFailed,
		"generate_error":  errMsg,
		"updated_at":      time.Now().UTC(),
	}).Error
}

func handlePluginDraftRepairJob(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
	var payload pluginDraftRepairPayload
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
		return asyncjob.Result{ErrorCode: generateErrInvalidPayload}, fmt.Errorf("decode repair payload: %w", err)
	}

	log.Printf("[repair_job] START draft_id=%s user_id=%s target=%q prev_status=%q warnings=%v hint_len=%d",
		payload.DraftID, payload.UserID, payload.Target, payload.PrevStatus,
		payload.Warnings, len(payload.RepairHint))

	db := store.DB()
	if db == nil {
		return asyncjob.Result{ErrorCode: generateErrSaveFailed}, fmt.Errorf("db unavailable")
	}

	var draft orm.PluginDraft
	if err := db.Where("id = ?", payload.DraftID).First(&draft).Error; err != nil {
		log.Printf("[repair_job] draft not found draft_id=%s err=%v", payload.DraftID, err)
		return asyncjob.Result{ErrorCode: generateErrDraftNotFound}, fmt.Errorf("draft not found: %w", err)
	}
	log.Printf("[repair_job] draft loaded draft_id=%s plugin_yaml_len=%d state_yaml_len=%d version=%d",
		payload.DraftID, len(draft.PluginYAMLContent), len(draft.StateYAMLContent), draft.Version)

	llmConfig := payload.LLMConfig
	if llmConfig == nil {
		if loaded, err := modelconfig.LoadLLMConfig(ctx, db, payload.UserID); err == nil {
			llmConfig = loaded
			log.Printf("[repair_job] llm_config loaded from DB for user_id=%s", payload.UserID)
		} else {
			llmConfig = map[string]any{}
			log.Printf("[repair_job] llm_config load failed (using empty), err=%v", err)
		}
	} else {
		log.Printf("[repair_job] llm_config from payload (keys=%d)", len(llmConfig))
	}

	restoreStatus := func(repairErr string) {
		updates := map[string]any{
			"generate_status": payload.PrevStatus,
			"updated_at":      time.Now().UTC(),
		}
		if repairErr != "" {
			updates["generate_warning"] = "[修复失败] " + repairErr
		}
		log.Printf("[repair_job] RESTORE draft_id=%s status=%q warning=%q",
			payload.DraftID, payload.PrevStatus, updates["generate_warning"])
		_ = db.Model(&orm.PluginDraft{}).Where("id = ?", payload.DraftID).Updates(updates)
	}

	if payload.Target == "scenario" {
		scenarioHint := payload.RepairHint
		if scenarioHint == "" {
			scenarioHint = "Fix or complete the scenario.md documentation."
		}
		log.Printf("[repair_job/scenario] calling algo.RepairStateMachine with target=scenario hint_len=%d", len(scenarioHint))
		resp, err := algo.RepairStateMachine(ctx, algo.RepairStateMachineRequest{
			PluginYAML: draft.PluginYAMLContent,
			StateYAML:  draft.StateYAMLContent,
			RepairHint: scenarioHint,
			Target:     "scenario",
			Warnings:   payload.Warnings,
			LLMConfig:  llmConfig,
		})
		if err != nil {
			log.Printf("[repair_job/scenario] algo error: %v", err)
			restoreStatus(err.Error())
			return asyncjob.Result{ErrorCode: generateErrAlgoFailed}, fmt.Errorf("repair scenario: %w", err)
		}
		log.Printf("[repair_job/scenario] algo returned scenario_md_len=%d (in state_yaml field)", len(resp.StateYAML))
		updates := map[string]any{
			"scenario_content": resp.StateYAML,
			"generate_status":  payload.PrevStatus,
			"generate_warning": "", // clear any previous warning on success
			"version":          draft.Version + 1,
			"updated_at":       time.Now().UTC(),
		}
		if err := db.Model(&draft).Updates(updates).Error; err != nil {
			log.Printf("[repair_job/scenario] DB save failed: %v", err)
			return asyncjob.Result{ErrorCode: generateErrSaveFailed}, fmt.Errorf("save repair: %w", err)
		}
		log.Printf("[repair_job/scenario] SUCCESS draft_id=%s new_version=%d", payload.DraftID, draft.Version+1)
		return asyncjob.Result{}, nil
	}

	// statemachine / ui target
	log.Printf("[repair_job/statemachine] calling algo.RepairStateMachine target=%q hint_len=%d warnings=%v",
		payload.Target, len(payload.RepairHint), payload.Warnings)
	resp, err := algo.RepairStateMachine(ctx, algo.RepairStateMachineRequest{
		PluginYAML: draft.PluginYAMLContent,
		StateYAML:  draft.StateYAMLContent,
		RepairHint: payload.RepairHint,
		Warnings:   payload.Warnings,
		LLMConfig:  llmConfig,
	})
	if err != nil {
		log.Printf("[repair_job/statemachine] algo error: %v", err)
		restoreStatus(err.Error())
		return asyncjob.Result{ErrorCode: generateErrAlgoFailed}, fmt.Errorf("repair statemachine: %w", err)
	}
	log.Printf("[repair_job/statemachine] algo returned state_yaml_len=%d plugin_yaml_updated=%v remaining_warnings=%v",
		len(resp.StateYAML), resp.PluginYAML != "", resp.RemainingWarnings)

	newWarning := strings.Join(resp.RemainingWarnings, "; ")
	updates := map[string]any{
		"state_yaml_content": resp.StateYAML,
		"generate_warning":   newWarning,
		"generate_status":    payload.PrevStatus,
		"version":            draft.Version + 1,
		"updated_at":         time.Now().UTC(),
	}
	if resp.PluginYAML != "" {
		updates["plugin_yaml_content"] = resp.PluginYAML
	}
	if err := db.Model(&draft).Updates(updates).Error; err != nil {
		log.Printf("[repair_job/statemachine] DB save failed: %v", err)
		return asyncjob.Result{ErrorCode: generateErrSaveFailed}, fmt.Errorf("save repair: %w", err)
	}
	log.Printf("[repair_job/statemachine] SUCCESS draft_id=%s new_version=%d status=%q warning=%q",
		payload.DraftID, draft.Version+1, payload.PrevStatus, newWarning)
	return asyncjob.Result{}, nil
}
