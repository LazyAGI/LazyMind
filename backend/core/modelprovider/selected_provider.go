package modelprovider

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

type setSelectedProviderRequest struct {
	GroupID string `json:"group_id"`
}

type setSharedProviderRequest struct {
	GroupID string `json:"group_id"`
	Share   bool   `json:"share"`
}

type selectedProviderItem struct {
	Category            string `json:"category"`
	GroupID             string `json:"group_id"`
	UserModelProviderID string `json:"user_model_provider_id"`
	ProviderName        string `json:"provider_name"`
	GroupName           string `json:"group_name"`
	BaseURL             string `json:"base_url"`
	Share               bool   `json:"share"`
}

type selectedProvidersResponse struct {
	Selections []selectedProviderItem `json:"selections"`
}

// GetSelectedProviders returns the current user's selected provider groups (OCR, search, etc.).
//
// GET /model_providers/selected_providers
func GetSelectedProviders(w http.ResponseWriter, r *http.Request) {
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
	out, err := loadSelectedProviders(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "query selected providers failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, selectedProvidersResponse{Selections: out})
}

// SetSelectedProvider sets the selected provider group for a given category for the current user.
// The category is derived from the group's parent provider.
//
// PUT /model_providers/selected_providers
func SetSelectedProvider(w http.ResponseWriter, r *http.Request) {
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

	var req setSelectedProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	groupID := strings.TrimSpace(req.GroupID)
	if groupID == "" {
		common.ReplyErr(w, "group_id is required", http.StatusBadRequest)
		return
	}

	// Load the group and its parent provider to get the category.
	var group orm.UserModelProviderGroup
	if err := db.WithContext(r.Context()).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", groupID, userID).
		Take(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "group not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query group failed", http.StatusInternalServerError)
		return
	}

	var parent orm.UserModelProvider
	if err := db.WithContext(r.Context()).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", group.UserModelProviderID, userID).
		Take(&parent).Error; err != nil {
		common.ReplyErr(w, "query provider failed", http.StatusInternalServerError)
		return
	}

	category := parent.Category
	now := time.Now()

	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var row orm.UserSelectedProvider
		findErr := tx.Where("user_id = ? AND category = ?", userID, category).Take(&row).Error
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			return tx.Create(&orm.UserSelectedProvider{
				UserID:                   userID,
				UserName:                 userName,
				Category:                 category,
				UserModelProviderGroupID: groupID,
				Share:                    false,
				CreatedAt:                now,
				UpdatedAt:                now,
			}).Error
		}
		if findErr != nil {
			return findErr
		}
		return tx.Model(&orm.UserSelectedProvider{}).
			Where("id = ?", row.ID).
			Updates(map[string]any{
				"user_model_provider_group_id": groupID,
				"user_name":                    userName,
				"updated_at":                   now,
			}).Error
	})
	if err != nil {
		common.ReplyErr(w, "save selected provider failed", http.StatusInternalServerError)
		return
	}

	out, err := loadSelectedProviders(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "query selected providers failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, selectedProvidersResponse{Selections: out})
}

// SetSharedProvider sets or clears the share flag for a selected provider row.
// Protected by document.write permission (admin only).
//
// PUT /model_providers/selected_providers/share
func SetSharedProvider(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	var req setSharedProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	groupID := strings.TrimSpace(req.GroupID)
	if groupID == "" {
		common.ReplyErr(w, "group_id is required", http.StatusBadRequest)
		return
	}

	now := time.Now()
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var row orm.UserSelectedProvider
		if err := tx.Where("user_model_provider_group_id = ?", groupID).
			First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("not found")
			}
			return err
		}
		if req.Share {
			// Clear any existing share=true for this category first.
			if err := tx.Model(&orm.UserSelectedProvider{}).
				Where("category = ? AND share = ?", row.Category, true).
				Updates(map[string]any{"share": false, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		// Scope to the exact row (by id) to avoid touching other users' rows that
		// reference the same group_id, which would violate the unique partial index.
		return tx.Model(&orm.UserSelectedProvider{}).
			Where("id = ?", row.ID).
			Updates(map[string]any{"share": req.Share, "updated_at": now}).Error
	})
	if err != nil {
		if err.Error() == "not found" {
			common.ReplyErr(w, "group not found in selected providers", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "update share failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"ok": true})
}

func loadSelectedProviders(ctx context.Context, db *gorm.DB, userID string) ([]selectedProviderItem, error) {
	out := make([]selectedProviderItem, 0)
	err := db.WithContext(ctx).Table("user_selected_providers usp").
		Select(
			"usp.category, "+
				"usp.user_model_provider_group_id AS group_id, "+
				"usp.share, "+
				"g.user_model_provider_id, "+
				"p.name AS provider_name, "+
				"g.name AS group_name, "+
				"g.base_url",
		).
		Joins("JOIN user_model_provider_groups g ON g.id = usp.user_model_provider_group_id AND g.deleted_at IS NULL").
		Joins("JOIN user_model_providers p ON p.id = g.user_model_provider_id AND p.deleted_at IS NULL").
		Where("usp.user_id = ?", userID).
		Order("usp.category ASC").
		Scan(&out).Error
	return out, err
}
