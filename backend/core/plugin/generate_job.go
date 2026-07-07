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
	generateStatusGenerating = "generating"
	generateStatusDone       = "done"
	generateStatusFailed     = "failed"
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

	resp, err := algo.GeneratePlugin(ctx, algo.GeneratePluginRequest{
		Name:         draft.Name,
		Description:  payload.Description,
		SkillContent: payload.SkillContent,
		LLMConfig:    llmConfig,
	})
	if err != nil {
		_ = markGenerateFailed(db, payload.DraftID)
		return asyncjob.Result{ErrorCode: generateErrAlgoFailed}, fmt.Errorf("generate plugin: %w", err)
	}

	updates := map[string]any{
		"plugin_yaml_content": resp.PluginYAML,
		"state_yaml_content":  resp.StateYAML,
		"generate_status":     generateStatusDone,
		"updated_at":          time.Now().UTC(),
	}
	if err := db.WithContext(ctx).Model(&orm.PluginDraft{}).Where("id = ?", payload.DraftID).Updates(updates).Error; err != nil {
		return asyncjob.Result{ErrorCode: generateErrSaveFailed}, fmt.Errorf("save generated content: %w", err)
	}

	return asyncjob.Result{}, nil
}

func markGenerateFailed(db *gorm.DB, draftID string) error {
	return db.Model(&orm.PluginDraft{}).Where("id = ?", draftID).Updates(map[string]any{
		"generate_status": generateStatusFailed,
		"updated_at":      time.Now().UTC(),
	}).Error
}
