package userprefs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/currentmemory"
	"lazymind/core/scheduler"
	"lazymind/core/settings"
	"lazymind/core/store"
	"lazymind/core/workflow"
)

type uiPreferencesResponse struct {
	ChatPreferenceNoticeDismissed bool   `json:"chat_preference_notice_dismissed"`
	DeveloperModeActive           bool   `json:"developer_mode_active"`
	AcceptedUserAgreementVersion  string `json:"accepted_user_agreement_version"`
	TaskCenterEnabled             bool   `json:"task_center_enabled"`
	SchedulesEnabled              bool   `json:"schedules_enabled"`
	SkillsEnabled                 bool   `json:"skills_enabled"`
	WorkflowsEnabled              bool   `json:"workflows_enabled"`
	MCPEnabled                    bool   `json:"mcp_enabled"`
	DocumentParsingEnabled        bool   `json:"document_parsing_enabled"`
	PerformanceStatsEnabled       bool   `json:"performance_stats_enabled"`
	UserPreferenceConfigured      bool   `json:"user_preference_configured"`
	UpdatedAt                     string `json:"updated_at"`
}

type uiPreferencesPatchRequest struct {
	ChatPreferenceNoticeDismissed *bool   `json:"chat_preference_notice_dismissed"`
	DeveloperModeActive           *bool   `json:"developer_mode_active"`
	AcceptedUserAgreementVersion  *string `json:"accepted_user_agreement_version"`
	TaskCenterEnabled             *bool   `json:"task_center_enabled"`
	SchedulesEnabled              *bool   `json:"schedules_enabled"`
	SkillsEnabled                 *bool   `json:"skills_enabled"`
	WorkflowsEnabled              *bool   `json:"workflows_enabled"`
	MCPEnabled                    *bool   `json:"mcp_enabled"`
	DocumentParsingEnabled        *bool   `json:"document_parsing_enabled"`
	PerformanceStatsEnabled       *bool   `json:"performance_stats_enabled"`
}

func GetUIPreferences(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	row, err := LoadUserUIPreferences(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "query user ui preferences failed", http.StatusInternalServerError)
		return
	}
	configured, err := LoadUserPreferenceConfigured(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "query user preference status failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, buildUIPreferencesResponse(row, configured))
}

func PatchUIPreferences(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var req uiPreferencesPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.ChatPreferenceNoticeDismissed == nil &&
		req.DeveloperModeActive == nil &&
		req.AcceptedUserAgreementVersion == nil &&
		req.TaskCenterEnabled == nil &&
		req.SchedulesEnabled == nil &&
		req.SkillsEnabled == nil &&
		req.WorkflowsEnabled == nil &&
		req.MCPEnabled == nil &&
		req.DocumentParsingEnabled == nil &&
		req.PerformanceStatsEnabled == nil {
		common.ReplyErr(w, "no valid fields to update", http.StatusBadRequest)
		return
	}

	currentControls, err := settings.LoadFeatureControls(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "query settings controls failed", http.StatusInternalServerError)
		return
	}
	var row orm.UserUIPreferences
	err = db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var upsertErr error
		row, upsertErr = UpsertUserUIPreferences(r.Context(), tx, userID, req)
		if upsertErr != nil {
			return upsertErr
		}
		if req.SkillsEnabled != nil {
			if err := setAllSkillsEnabled(r.Context(), tx, userID, *req.SkillsEnabled); err != nil {
				return err
			}
		}
		if req.WorkflowsEnabled != nil {
			if err := setAllWorkflowsEnabled(r.Context(), tx, userID, *req.WorkflowsEnabled); err != nil {
				return err
			}
		}
		if req.SchedulesEnabled != nil && *req.SchedulesEnabled && !currentControls.SchedulesEnabled {
			return scheduler.RecomputeEnabledSchedules(r.Context(), tx, userID, time.Now().UTC())
		}
		return nil
	})
	if err != nil {
		common.ReplyErr(w, "update user ui preferences failed", http.StatusInternalServerError)
		return
	}
	configured, err := LoadUserPreferenceConfigured(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "query user preference status failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, buildUIPreferencesResponse(row, configured))
}

func setAllSkillsEnabled(ctx context.Context, db *gorm.DB, userID string, enabled bool) error {
	userID = strings.TrimSpace(userID)
	now := time.Now().UTC()
	return db.WithContext(ctx).
		Model(&orm.SkillV2Skill{}).
		Where("owner_user_id = ? AND deleted_at IS NULL", userID).
		Updates(map[string]any{"is_enabled": enabled, "updated_at": now}).Error
}

func setAllWorkflowsEnabled(ctx context.Context, db *gorm.DB, userID string, enabled bool) error {
	userID = strings.TrimSpace(userID)
	now := time.Now().UTC()
	callMode := workflow.WorkflowCallModeDisabled
	if enabled {
		callMode = workflow.WorkflowCallModeAuto
	}
	var workflowRefs []string
	if err := db.WithContext(ctx).
		Model(&orm.WorkflowResource{}).
		Where("status = ? AND (owner_user_id = ? OR owner_user_id = '')", "active", userID).
		Pluck("plugin_ref", &workflowRefs).Error; err != nil { // workflow-naming: persistence
		return err
	}
	if len(workflowRefs) == 0 {
		return nil
	}

	settings := make([]orm.UserWorkflowSetting, 0, len(workflowRefs))
	for _, workflowRef := range workflowRefs {
		settings = append(settings, orm.UserWorkflowSetting{
			UserID:      userID,
			WorkflowRef: workflowRef,
			Enabled:     enabled,
			CallMode:    callMode,
			UpdatedAt:   now,
		})
	}
	return db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "plugin_ref"}},
			DoUpdates: clause.AssignmentColumns([]string{"enabled", "call_mode", "updated_at"}),
		}).
		Create(&settings).Error
}

func LoadUserUIPreferences(ctx context.Context, db *gorm.DB, userID string) (orm.UserUIPreferences, error) {
	var row orm.UserUIPreferences
	err := db.WithContext(ctx).
		Where("user_id = ?", strings.TrimSpace(userID)).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return orm.UserUIPreferences{
			UserID:                 strings.TrimSpace(userID),
			TaskCenterEnabled:      true,
			SchedulesEnabled:       true,
			SkillsEnabled:          true,
			WorkflowsEnabled:       true,
			MCPEnabled:             true,
			DocumentParsingEnabled: true,
		}, nil
	}
	return row, err
}

func UpsertUserUIPreferences(ctx context.Context, db *gorm.DB, userID string, req uiPreferencesPatchRequest) (orm.UserUIPreferences, error) {
	userID = strings.TrimSpace(userID)
	now := time.Now().UTC()

	var row orm.UserUIPreferences
	err := db.WithContext(ctx).Where("user_id = ?", userID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row = orm.UserUIPreferences{
			UserID:                 userID,
			TaskCenterEnabled:      true,
			SchedulesEnabled:       true,
			SkillsEnabled:          true,
			WorkflowsEnabled:       true,
			MCPEnabled:             true,
			DocumentParsingEnabled: true,
			CreatedAt:              now,
			UpdatedAt:              now,
		}
		if req.ChatPreferenceNoticeDismissed != nil {
			row.ChatPreferenceNoticeDismissed = *req.ChatPreferenceNoticeDismissed
		}
		if req.DeveloperModeActive != nil {
			row.DeveloperModeActive = *req.DeveloperModeActive
		}
		if req.AcceptedUserAgreementVersion != nil {
			row.AcceptedUserAgreementVersion = strings.TrimSpace(*req.AcceptedUserAgreementVersion)
		}
		if req.TaskCenterEnabled != nil {
			row.TaskCenterEnabled = *req.TaskCenterEnabled
		}
		if req.SchedulesEnabled != nil {
			row.SchedulesEnabled = *req.SchedulesEnabled
		}
		if req.SkillsEnabled != nil {
			row.SkillsEnabled = *req.SkillsEnabled
		}
		if req.WorkflowsEnabled != nil {
			row.WorkflowsEnabled = *req.WorkflowsEnabled
		}
		if req.MCPEnabled != nil {
			row.MCPEnabled = *req.MCPEnabled
		}
		if req.DocumentParsingEnabled != nil {
			row.DocumentParsingEnabled = *req.DocumentParsingEnabled
		}
		if req.PerformanceStatsEnabled != nil {
			row.PerformanceStatsEnabled = *req.PerformanceStatsEnabled
		}
		// Feature controls default to true. Map-based create preserves an explicit
		// false from a first-time user instead of applying GORM's model default.
		if err := db.WithContext(ctx).Model(&orm.UserUIPreferences{}).Create(map[string]any{
			"user_id":                          row.UserID,
			"chat_preference_notice_dismissed": row.ChatPreferenceNoticeDismissed,
			"developer_mode_active":            row.DeveloperModeActive,
			"accepted_user_agreement_version":  row.AcceptedUserAgreementVersion,
			"task_center_enabled":              row.TaskCenterEnabled,
			"schedules_enabled":                row.SchedulesEnabled,
			"skills_enabled":                   row.SkillsEnabled,
			"workflows_enabled":                row.WorkflowsEnabled,
			"mcp_enabled":                      row.MCPEnabled,
			"document_parsing_enabled":         row.DocumentParsingEnabled,
			"performance_stats_enabled":        row.PerformanceStatsEnabled,
			"created_at":                       row.CreatedAt,
			"updated_at":                       row.UpdatedAt,
		}).Error; err != nil {
			return orm.UserUIPreferences{}, err
		}
		return row, nil
	}
	if err != nil {
		return orm.UserUIPreferences{}, err
	}

	updates := map[string]any{"updated_at": now}
	if req.ChatPreferenceNoticeDismissed != nil {
		updates["chat_preference_notice_dismissed"] = *req.ChatPreferenceNoticeDismissed
		row.ChatPreferenceNoticeDismissed = *req.ChatPreferenceNoticeDismissed
	}
	if req.DeveloperModeActive != nil {
		updates["developer_mode_active"] = *req.DeveloperModeActive
		row.DeveloperModeActive = *req.DeveloperModeActive
	}
	if req.AcceptedUserAgreementVersion != nil {
		version := strings.TrimSpace(*req.AcceptedUserAgreementVersion)
		updates["accepted_user_agreement_version"] = version
		row.AcceptedUserAgreementVersion = version
	}
	if req.TaskCenterEnabled != nil {
		updates["task_center_enabled"] = *req.TaskCenterEnabled
		row.TaskCenterEnabled = *req.TaskCenterEnabled
	}
	if req.SchedulesEnabled != nil {
		updates["schedules_enabled"] = *req.SchedulesEnabled
		row.SchedulesEnabled = *req.SchedulesEnabled
	}
	if req.SkillsEnabled != nil {
		updates["skills_enabled"] = *req.SkillsEnabled
		row.SkillsEnabled = *req.SkillsEnabled
	}
	if req.WorkflowsEnabled != nil {
		updates["workflows_enabled"] = *req.WorkflowsEnabled
		row.WorkflowsEnabled = *req.WorkflowsEnabled
	}
	if req.MCPEnabled != nil {
		updates["mcp_enabled"] = *req.MCPEnabled
		row.MCPEnabled = *req.MCPEnabled
	}
	if req.DocumentParsingEnabled != nil {
		updates["document_parsing_enabled"] = *req.DocumentParsingEnabled
		row.DocumentParsingEnabled = *req.DocumentParsingEnabled
	}
	if req.PerformanceStatsEnabled != nil {
		updates["performance_stats_enabled"] = *req.PerformanceStatsEnabled
		row.PerformanceStatsEnabled = *req.PerformanceStatsEnabled
	}
	if err := db.WithContext(ctx).Model(&orm.UserUIPreferences{}).
		Where("user_id = ?", userID).
		Updates(updates).Error; err != nil {
		return orm.UserUIPreferences{}, err
	}
	row.UpdatedAt = now
	return row, nil
}

func LoadUserPreferenceConfigured(ctx context.Context, db *gorm.DB, userID string) (bool, error) {
	row, err := currentmemory.NewRepository(db).GetEntry(
		ctx,
		userID,
		currentmemory.PreferencePath,
	)
	if errors.Is(err, currentmemory.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	document, err := currentmemory.ParsePreferences(row.Content)
	if err != nil {
		return false, err
	}
	return len(document.Preferences) > 0, nil
}

func buildUIPreferencesResponse(row orm.UserUIPreferences, userPreferenceConfigured bool) uiPreferencesResponse {
	updatedAt := ""
	if !row.UpdatedAt.IsZero() {
		updatedAt = row.UpdatedAt.Format(time.RFC3339Nano)
	}
	return uiPreferencesResponse{
		ChatPreferenceNoticeDismissed: row.ChatPreferenceNoticeDismissed,
		DeveloperModeActive:           row.DeveloperModeActive,
		AcceptedUserAgreementVersion:  row.AcceptedUserAgreementVersion,
		TaskCenterEnabled:             row.TaskCenterEnabled,
		SchedulesEnabled:              row.SchedulesEnabled,
		SkillsEnabled:                 row.SkillsEnabled,
		WorkflowsEnabled:              row.WorkflowsEnabled,
		MCPEnabled:                    row.MCPEnabled,
		DocumentParsingEnabled:        row.DocumentParsingEnabled,
		PerformanceStatsEnabled:       row.PerformanceStatsEnabled,
		UserPreferenceConfigured:      userPreferenceConfigured,
		UpdatedAt:                     updatedAt,
	}
}
