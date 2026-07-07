package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
)

const pluginDraftGenerateJobType = "plugin_draft_generate"

const (
	generateStatusGenerating   = "generating"
	generateStatusSkeletonDone = "skeleton_done"
	generateStatusStateDone    = "state_done"
	generateStatusDone         = "done"
	generateStatusFailed       = "failed"
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

// RegisterPluginDraftGenerateJob registers the async job handler.
// Call this once at startup (e.g. from main.go).
func RegisterPluginDraftGenerateJob() {
	asyncjob.Register(pluginDraftGenerateJobType, handlePluginDraftGenerateJob)
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

	// ── Phase 1: Skeleton ────────────────────────────────────────────────────
	skeletonResp, err := algo.GenerateSkeleton(ctx, algo.GenerateSkeletonRequest{
		Name:         draft.Name,
		Description:  payload.Description,
		SkillContent: payload.SkillContent,
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
		Name:       draft.Name,
		PluginYAML: skeletonResp.PluginYAML,
		LLMConfig:  llmConfig,
	})
	if err != nil {
		_ = markGenerateFailed(db, payload.DraftID, fmt.Sprintf("phase2 state_machine: %s", err))
		return asyncjob.Result{ErrorCode: generateErrAlgoFailed}, fmt.Errorf("phase2 state_machine: %w", err)
	}
	if err := db.WithContext(ctx).Model(&orm.PluginDraft{}).Where("id = ?", payload.DraftID).Updates(map[string]any{
		"state_yaml_content": stateResp.StateYAML,
		"generate_status":    generateStatusStateDone,
		"updated_at":         time.Now().UTC(),
	}).Error; err != nil {
		return asyncjob.Result{ErrorCode: generateErrSaveFailed}, fmt.Errorf("save state_machine: %w", err)
	}

	// ── Phase 3: Scenario + Scripts ──────────────────────────────────────────
	scenarioResp, err := algo.GenerateScenarioScripts(ctx, algo.GenerateScenarioScriptsRequest{
		Name:       draft.Name,
		PluginYAML: skeletonResp.PluginYAML,
		StateYAML:  stateResp.StateYAML,
		LLMConfig:  llmConfig,
	})
	if err != nil {
		// Phase 3 failure is non-fatal: skeleton + state are already saved.
		// Mark as done with a warning rather than failed.
		_ = db.WithContext(ctx).Model(&orm.PluginDraft{}).Where("id = ?", payload.DraftID).Updates(map[string]any{
			"generate_status": generateStatusDone,
			"generate_error":  fmt.Sprintf("phase3 scenario_scripts: %s (non-fatal)", err),
			"updated_at":      time.Now().UTC(),
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
