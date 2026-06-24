package datasource

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

type localFSChatSettingResponse struct {
	Enabled bool `json:"enabled"`
}

type localFSChatSettingRequest struct {
	Enabled *bool `json:"enabled"`
}

func GetLocalFSChatSetting(w http.ResponseWriter, r *http.Request) {
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

	enabled, err := LoadLocalFSChatEnabled(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "query local fs chat setting failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, localFSChatSettingResponse{Enabled: enabled})
}

func SetLocalFSChatSetting(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	userName := strings.TrimSpace(store.UserName(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var req localFSChatSettingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Enabled == nil {
		common.ReplyErr(w, "enabled required", http.StatusBadRequest)
		return
	}

	enabled, err := UpsertLocalFSChatEnabled(r.Context(), db, userID, userName, *req.Enabled)
	if err != nil {
		common.ReplyErr(w, "update local fs chat setting failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, localFSChatSettingResponse{Enabled: enabled})
}

func LoadLocalFSChatEnabled(ctx context.Context, db *gorm.DB, userID string) (bool, error) {
	var row orm.LocalFSChatSetting
	err := db.WithContext(ctx).
		Where("create_user_id = ?", strings.TrimSpace(userID)).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.Enabled, nil
}

func UpsertLocalFSChatEnabled(ctx context.Context, db *gorm.DB, userID, userName string, enabled bool) (bool, error) {
	userID = strings.TrimSpace(userID)
	userName = strings.TrimSpace(userName)
	now := time.Now()

	var row orm.LocalFSChatSetting
	err := db.WithContext(ctx).Where("create_user_id = ?", userID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := db.WithContext(ctx).Model(&orm.LocalFSChatSetting{}).Create(map[string]any{
			"create_user_id":   userID,
			"create_user_name": userName,
			"enabled":          enabled,
			"created_at":       now,
			"updated_at":       now,
		}).Error; err != nil {
			return false, err
		}
		return enabled, nil
	}
	if err != nil {
		return false, err
	}

	if err := db.WithContext(ctx).Model(&orm.LocalFSChatSetting{}).
		Where("id = ?", row.ID).
		Updates(map[string]any{
			"create_user_name": userName,
			"enabled":          enabled,
			"updated_at":       now,
		}).Error; err != nil {
		return false, err
	}
	return enabled, nil
}
