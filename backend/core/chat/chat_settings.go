package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type chatConversationDefaults struct {
	ChatExecutor   string `json:"chat_executor"`
	EnableWorkflow bool   `json:"enable_workflow"`
	WorkflowMode   string `json:"workflow_mode"`
	EnableSubagent bool   `json:"enable_subagent"`
}

type chatEntryDefaults struct {
	ThinkingDepth        string                   `json:"thinking_depth"`
	ConversationSettings chatConversationDefaults `json:"conversation_settings"`
}

type chatConversationDefaultsPatch struct {
	ChatExecutor   *string `json:"chat_executor"`
	EnableWorkflow *bool   `json:"enable_workflow"`
	WorkflowMode   *string `json:"workflow_mode"`
	EnableSubagent *bool   `json:"enable_subagent"`
}

func (p *chatConversationDefaultsPatch) hasUpdates() bool {
	return p != nil && (p.ChatExecutor != nil || p.EnableWorkflow != nil ||
		p.WorkflowMode != nil || p.EnableSubagent != nil)
}

type chatEntryDefaultsPatch struct {
	ThinkingDepth        *string                        `json:"thinking_depth"`
	ConversationSettings *chatConversationDefaultsPatch `json:"conversation_settings"`
}

func (p *chatEntryDefaultsPatch) hasUpdates() bool {
	return p != nil && (p.ThinkingDepth != nil || p.ConversationSettings.hasUpdates())
}

type chatSettingsPatchRequest struct {
	// Legacy flat fields remain accepted by installed clients.
	EnableWorkflow *bool                   `json:"enable_workflow"`
	WorkflowMode   *string                 `json:"workflow_mode"`
	EnableSubagent *bool                   `json:"enable_subagent"`
	QuickQuestion  *chatEntryDefaultsPatch `json:"quick_question"`
	NewTask        *chatEntryDefaultsPatch `json:"new_task"`
}

func (r chatSettingsPatchRequest) hasUpdates() bool {
	return r.EnableWorkflow != nil || r.WorkflowMode != nil || r.EnableSubagent != nil ||
		r.QuickQuestion.hasUpdates() || r.NewTask.hasUpdates()
}

type chatSettingsResponse struct {
	// Legacy flat fields mirror the new-task conversation defaults.
	EnableWorkflow bool              `json:"enable_workflow"`
	WorkflowMode   string            `json:"workflow_mode"`
	EnableSubagent bool              `json:"enable_subagent"`
	QuickQuestion  chatEntryDefaults `json:"quick_question"`
	NewTask        chatEntryDefaults `json:"new_task"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

func defaultUserChatSettings(userID string) orm.UserChatSettings {
	return orm.UserChatSettings{
		UserID:         strings.TrimSpace(userID),
		EnableWorkflow: true,
		WorkflowMode:   "dynamic",
		EnableSubagent: true,
	}
}

func normalizedLegacyWorkflowMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "auto" || value == "dynamic" {
		return value
	}
	return "dynamic"
}

func quickQuestionDefaultsFromLegacy(row orm.UserChatSettings) chatEntryDefaults {
	return chatEntryDefaults{
		ThinkingDepth: "medium",
		ConversationSettings: chatConversationDefaults{
			ChatExecutor:   ChatExecutorLazyMind,
			EnableWorkflow: false,
			WorkflowMode:   normalizedLegacyWorkflowMode(row.WorkflowMode),
			EnableSubagent: row.EnableSubagent,
		},
	}
}

func newTaskDefaultsFromLegacy(row orm.UserChatSettings) chatEntryDefaults {
	return chatEntryDefaults{
		ThinkingDepth: "high",
		ConversationSettings: chatConversationDefaults{
			ChatExecutor:   ChatExecutorLazyMind,
			EnableWorkflow: row.EnableWorkflow,
			WorkflowMode:   normalizedLegacyWorkflowMode(row.WorkflowMode),
			EnableSubagent: row.EnableSubagent,
		},
	}
}

func normalizeThinkingDepth(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "low", "medium", "high", "max":
		return value, true
	default:
		return "", false
	}
}

func normalizeEntryDefaults(value chatEntryDefaults) (chatEntryDefaults, bool) {
	depth, valid := normalizeThinkingDepth(value.ThinkingDepth)
	if !valid {
		return chatEntryDefaults{}, false
	}
	value.ThinkingDepth = depth
	mode := strings.ToLower(strings.TrimSpace(value.ConversationSettings.WorkflowMode))
	if mode != "auto" && mode != "dynamic" {
		return chatEntryDefaults{}, false
	}
	value.ConversationSettings.WorkflowMode = mode
	executor, valid := normalizeChatExecutor(value.ConversationSettings.ChatExecutor)
	if !valid {
		return chatEntryDefaults{}, false
	}
	value.ConversationSettings.ChatExecutor = executor
	return value, true
}

func decodeEntryDefaults(raw json.RawMessage, fallback chatEntryDefaults) chatEntryDefaults {
	if len(raw) == 0 {
		return fallback
	}
	var value chatEntryDefaults
	if err := json.Unmarshal(raw, &value); err != nil {
		return fallback
	}
	normalized, valid := normalizeEntryDefaults(value)
	if !valid {
		return fallback
	}
	return normalized
}

func loadUserChatSettings(
	ctx context.Context,
	db *gorm.DB,
	userID string,
) (orm.UserChatSettings, chatEntryDefaults, chatEntryDefaults, error) {
	row := defaultUserChatSettings(userID)
	err := db.WithContext(ctx).Where("user_id = ?", row.UserID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, quickQuestionDefaultsFromLegacy(row), newTaskDefaultsFromLegacy(row), nil
	}
	if err != nil {
		return orm.UserChatSettings{}, chatEntryDefaults{}, chatEntryDefaults{}, err
	}
	return row,
		decodeEntryDefaults(row.QuickQuestionDefaults, quickQuestionDefaultsFromLegacy(row)),
		decodeEntryDefaults(row.NewTaskDefaults, newTaskDefaultsFromLegacy(row)),
		nil
}

func userChatSettingsUpdateQuery(db *gorm.DB) *gorm.DB {
	if db != nil && db.Dialector != nil && db.Dialector.Name() == "postgres" {
		return db.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	return db
}

func loadUserChatSettingsForUpdate(
	ctx context.Context,
	db *gorm.DB,
	userID string,
) (orm.UserChatSettings, chatEntryDefaults, chatEntryDefaults, error) {
	row := defaultUserChatSettings(userID)
	query := userChatSettingsUpdateQuery(db.WithContext(ctx))
	if err := query.Where("user_id = ?", row.UserID).First(&row).Error; err != nil {
		return orm.UserChatSettings{}, chatEntryDefaults{}, chatEntryDefaults{}, err
	}
	return row,
		decodeEntryDefaults(row.QuickQuestionDefaults, quickQuestionDefaultsFromLegacy(row)),
		decodeEntryDefaults(row.NewTaskDefaults, newTaskDefaultsFromLegacy(row)),
		nil
}

func entryDefaultsForRequest(
	ctx context.Context,
	db *gorm.DB,
	userID string,
	runInBackground bool,
) (chatEntryDefaults, error) {
	_, quickQuestion, newTask, err := loadUserChatSettings(ctx, db, userID)
	if err != nil {
		return chatEntryDefaults{}, err
	}
	if runInBackground {
		return newTask, nil
	}
	return quickQuestion, nil
}

func buildChatSettingsResponse(
	row orm.UserChatSettings,
	quickQuestion chatEntryDefaults,
	newTask chatEntryDefaults,
) chatSettingsResponse {
	return chatSettingsResponse{
		EnableWorkflow: row.EnableWorkflow,
		WorkflowMode:   normalizedLegacyWorkflowMode(row.WorkflowMode),
		EnableSubagent: row.EnableSubagent,
		QuickQuestion:  quickQuestion,
		NewTask:        newTask,
		UpdatedAt:      row.UpdatedAt,
	}
}

// GetChatSettings returns the per-user defaults for quick questions and new tasks.
func GetChatSettings(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "db unavailable", http.StatusInternalServerError)
		return
	}
	row, quickQuestion, newTask, err := loadUserChatSettings(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, buildChatSettingsResponse(row, quickQuestion, newTask))
}

// PatchConversationSettings updates the Agent executor and the conversation's
// Workflow/subagent overrides through one settings boundary.
func PatchConversationSettings(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "db unavailable", http.StatusInternalServerError)
		return
	}
	convID := strings.TrimSpace(mux.Vars(r)["conversation_id"])
	if convID == "" {
		common.ReplyErr(w, "conversation_id required", http.StatusBadRequest)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}

	updates := map[string]any{}
	requestedExecutor := ""
	if raw, present := body["enable_workflow"]; present {
		if raw == nil {
			updates["enable_plugin"] = nil
		} else if v, ok := raw.(bool); ok {
			updates["enable_plugin"] = v
		}
	}
	if raw, present := body["enable_subagent"]; present {
		if raw == nil {
			updates["enable_subagent"] = nil
		} else if v, ok := raw.(bool); ok {
			updates["enable_subagent"] = v
		}
	}
	if raw, present := body["workflow_mode"]; present {
		if raw == nil {
			updates["plugin_mode"] = nil // workflow-naming: persistence
		} else if v, ok := raw.(string); ok {
			v = strings.TrimSpace(v)
			if v != "auto" && v != "dynamic" {
				common.ReplyErr(w, "workflow_mode must be 'auto' or 'dynamic'", http.StatusBadRequest)
				return
			}
			updates["plugin_mode"] = v // workflow-naming: persistence
		}
	}
	if raw, present := body["chat_executor"]; present {
		value, ok := raw.(string)
		normalized, valid := normalizeChatExecutor(value)
		if !ok || !valid {
			common.ReplyErr(w, chatExecutorValidationMessage(), http.StatusBadRequest)
			return
		}
		updates["chat_executor"] = normalized
		requestedExecutor = normalized
	}
	if len(updates) == 0 {
		common.ReplyErr(w, "no valid fields to update", http.StatusBadRequest)
		return
	}
	var conversation orm.Conversation
	if err := db.WithContext(r.Context()).Where("id = ? AND create_user_id = ?", convID, userID).
		First(&conversation).Error; err != nil {
		common.ReplyErr(w, "conversation not found", http.StatusNotFound)
		return
	}
	if requestedExecutor != "" {
		if isExternalChatProvider(requestedExecutor) {
			if err := externalChatUnavailableError(r.Context(), userID, requestedExecutor); err != nil {
				common.ReplyErr(w, err.Error(), http.StatusConflict)
				return
			}
		}
	}
	if enabled, exists := updates["enable_plugin"]; exists && enabled == false {
		var workflowCount int64
		if err := db.WithContext(r.Context()).Model(&orm.WorkflowSession{}).
			Where("conversation_id = ? AND dismissed = false", convID).
			Count(&workflowCount).Error; err != nil {
			common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if workflowCount > 0 {
			common.ReplyErr(w, "cannot disable workflows while a workflow is attached to the conversation", http.StatusConflict)
			return
		}
	}

	if err := db.WithContext(r.Context()).Model(&orm.Conversation{}).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", convID, userID).
		Updates(updates).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, nil)
}
func PatchChatSettings(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "db unavailable", http.StatusInternalServerError)
		return
	}

	var req chatSettingsPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !req.hasUpdates() {
		common.ReplyErr(w, "no valid fields to update", http.StatusBadRequest)
		return
	}
	if validationMessage := normalizeChatSettingsPatch(&req); validationMessage != "" {
		common.ReplyErr(w, validationMessage, http.StatusBadRequest)
		return
	}

	var response chatSettingsResponse
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		seed := defaultUserChatSettings(userID)
		seed.QuickQuestionDefaults, _ = json.Marshal(quickQuestionDefaultsFromLegacy(seed))
		seed.NewTaskDefaults, _ = json.Marshal(newTaskDefaultsFromLegacy(seed))
		seed.UpdatedAt = now
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}

		// Merge against the latest committed profiles. PostgreSQL row locking
		// serializes concurrent partial PATCH requests; SQLite keeps its native
		// transaction locking without emitting unsupported FOR UPDATE syntax.
		row, quickQuestion, newTask, err := loadUserChatSettingsForUpdate(r.Context(), tx, userID)
		if err != nil {
			return err
		}

		// Apply legacy flat fields first. Nested fields below take precedence when
		// both forms are sent by an installed client during a rolling upgrade.
		if req.EnableWorkflow != nil {
			row.EnableWorkflow = *req.EnableWorkflow
			newTask.ConversationSettings.EnableWorkflow = *req.EnableWorkflow
		}
		if req.WorkflowMode != nil {
			row.WorkflowMode = *req.WorkflowMode
			quickQuestion.ConversationSettings.WorkflowMode = *req.WorkflowMode
			newTask.ConversationSettings.WorkflowMode = *req.WorkflowMode
		}
		if req.EnableSubagent != nil {
			row.EnableSubagent = *req.EnableSubagent
			quickQuestion.ConversationSettings.EnableSubagent = *req.EnableSubagent
			newTask.ConversationSettings.EnableSubagent = *req.EnableSubagent
		}

		applyEntryDefaultsPatch(&quickQuestion, req.QuickQuestion)
		applyEntryDefaultsPatch(&newTask, req.NewTask)
		if req.NewTask != nil && req.NewTask.ConversationSettings != nil {
			conversation := req.NewTask.ConversationSettings
			if conversation.EnableWorkflow != nil {
				row.EnableWorkflow = newTask.ConversationSettings.EnableWorkflow
			}
			if conversation.WorkflowMode != nil {
				row.WorkflowMode = newTask.ConversationSettings.WorkflowMode
			}
			if conversation.EnableSubagent != nil {
				row.EnableSubagent = newTask.ConversationSettings.EnableSubagent
			}
		}

		quickJSON, err := json.Marshal(quickQuestion)
		if err != nil {
			return err
		}
		newTaskJSON, err := json.Marshal(newTask)
		if err != nil {
			return err
		}
		now = time.Now().UTC()
		updates := map[string]any{
			"enable_workflow":         row.EnableWorkflow,
			"plugin_mode":             row.WorkflowMode, // workflow-naming: persistence
			"enable_subagent":         row.EnableSubagent,
			"quick_question_defaults": json.RawMessage(quickJSON),
			"new_task_defaults":       json.RawMessage(newTaskJSON),
			"updated_at":              now,
		}
		if err := tx.Model(&orm.UserChatSettings{}).
			Where("user_id = ?", strings.TrimSpace(userID)).Updates(updates).Error; err != nil {
			return err
		}
		row.QuickQuestionDefaults = quickJSON
		row.NewTaskDefaults = newTaskJSON
		row.UpdatedAt = now
		response = buildChatSettingsResponse(row, quickQuestion, newTask)
		return nil
	})
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, response)
}

func normalizeChatSettingsPatch(req *chatSettingsPatchRequest) string {
	if req.WorkflowMode != nil {
		mode := strings.ToLower(strings.TrimSpace(*req.WorkflowMode))
		if mode != "auto" && mode != "dynamic" {
			return "workflow_mode must be 'auto' or 'dynamic'"
		}
		*req.WorkflowMode = mode
	}
	for _, profile := range []*chatEntryDefaultsPatch{req.QuickQuestion, req.NewTask} {
		if profile == nil {
			continue
		}
		if profile.ThinkingDepth != nil {
			depth, valid := normalizeThinkingDepth(*profile.ThinkingDepth)
			if !valid {
				return "thinking_depth must be 'low', 'medium', 'high', or 'max'"
			}
			*profile.ThinkingDepth = depth
		}
		conversation := profile.ConversationSettings
		if conversation == nil {
			continue
		}
		if conversation.WorkflowMode != nil {
			mode := strings.ToLower(strings.TrimSpace(*conversation.WorkflowMode))
			if mode != "auto" && mode != "dynamic" {
				return "workflow_mode must be 'auto' or 'dynamic'"
			}
			*conversation.WorkflowMode = mode
		}
		if conversation.ChatExecutor != nil {
			executor, valid := normalizeChatExecutor(*conversation.ChatExecutor)
			if !valid {
				return chatExecutorValidationMessage()
			}
			*conversation.ChatExecutor = executor
		}
	}
	return ""
}

func applyEntryDefaultsPatch(value *chatEntryDefaults, patch *chatEntryDefaultsPatch) {
	if patch == nil {
		return
	}
	if patch.ThinkingDepth != nil {
		value.ThinkingDepth = *patch.ThinkingDepth
	}
	conversation := patch.ConversationSettings
	if conversation == nil {
		return
	}
	if conversation.ChatExecutor != nil {
		value.ConversationSettings.ChatExecutor = *conversation.ChatExecutor
	}
	if conversation.EnableWorkflow != nil {
		value.ConversationSettings.EnableWorkflow = *conversation.EnableWorkflow
	}
	if conversation.WorkflowMode != nil {
		value.ConversationSettings.WorkflowMode = *conversation.WorkflowMode
	}
	if conversation.EnableSubagent != nil {
		value.ConversationSettings.EnableSubagent = *conversation.EnableSubagent
	}
}
