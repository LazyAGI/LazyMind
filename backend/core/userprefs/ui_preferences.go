package userprefs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type uiPreferencesResponse struct {
	ChatPreferenceNoticeDismissed bool   `json:"chat_preference_notice_dismissed"`
	DeveloperModeActive           bool   `json:"developer_mode_active"`
	UserPreferenceConfigured      bool   `json:"user_preference_configured"`
	UpdatedAt                     string `json:"updated_at"`
}

type uiPreferencesPatchRequest struct {
	ChatPreferenceNoticeDismissed *bool `json:"chat_preference_notice_dismissed"`
	DeveloperModeActive           *bool `json:"developer_mode_active"`
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
	if req.ChatPreferenceNoticeDismissed == nil && req.DeveloperModeActive == nil {
		common.ReplyErr(w, "no valid fields to update", http.StatusBadRequest)
		return
	}

	row, err := UpsertUserUIPreferences(r.Context(), db, userID, req)
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

func LoadUserUIPreferences(ctx context.Context, db *gorm.DB, userID string) (orm.UserUIPreferences, error) {
	var row orm.UserUIPreferences
	err := db.WithContext(ctx).
		Where("user_id = ?", strings.TrimSpace(userID)).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return orm.UserUIPreferences{UserID: strings.TrimSpace(userID)}, nil
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
			UserID:    userID,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if req.ChatPreferenceNoticeDismissed != nil {
			row.ChatPreferenceNoticeDismissed = *req.ChatPreferenceNoticeDismissed
		}
		if req.DeveloperModeActive != nil {
			row.DeveloperModeActive = *req.DeveloperModeActive
		}
		if err := db.WithContext(ctx).Create(&row).Error; err != nil {
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
	if err := db.WithContext(ctx).Model(&orm.UserUIPreferences{}).
		Where("user_id = ?", userID).
		Updates(updates).Error; err != nil {
		return orm.UserUIPreferences{}, err
	}
	row.UpdatedAt = now
	return row, nil
}

func LoadUserPreferenceConfigured(ctx context.Context, db *gorm.DB, userID string) (bool, error) {
	var row orm.SystemUserPreference
	err := db.WithContext(ctx).
		Where("user_id = ?", strings.TrimSpace(userID)).
		Order("created_at ASC").
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(row.AgentPersona) != "" ||
		strings.TrimSpace(row.PreferredName) != "" ||
		strings.TrimSpace(row.ResponseStyle) != "", nil
}

func buildUIPreferencesResponse(row orm.UserUIPreferences, userPreferenceConfigured bool) uiPreferencesResponse {
	updatedAt := ""
	if !row.UpdatedAt.IsZero() {
		updatedAt = row.UpdatedAt.Format(time.RFC3339Nano)
	}
	return uiPreferencesResponse{
		ChatPreferenceNoticeDismissed: row.ChatPreferenceNoticeDismissed,
		DeveloperModeActive:           row.DeveloperModeActive,
		UserPreferenceConfigured:      userPreferenceConfigured,
		UpdatedAt:                     updatedAt,
	}
}
