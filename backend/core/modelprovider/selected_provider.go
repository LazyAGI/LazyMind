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
	Selections []selectedProviderUpsertItem `json:"selections"`
}

type selectedProviderUpsertItem struct {
	Category string `json:"category"`
	GroupID  string `json:"group_id"`
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

type verifiedProviderGroupsResponse struct {
	Groups []verifiedGroupItem `json:"groups"`
}

// ListUserProviderGroupsByCategory lists verified provider groups owned by the current user.
//
// GET /model_providers/provider_groups?category=ocr
func ListUserProviderGroupsByCategory(w http.ResponseWriter, r *http.Request) {
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
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	if category == "" {
		common.ReplyErr(w, "category is required", http.StatusBadRequest)
		return
	}

	groups, err := loadVerifiedGroupsForUser(r.Context(), db, userID, category)
	if err != nil {
		common.ReplyErr(w, "list provider groups failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, verifiedProviderGroupsResponse{Groups: groups})
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

// SetSelectedProvider saves selected provider rows by category for the current user.
// Request shape mirrors selected model selection: selections contains category and group_id.
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
	if len(req.Selections) == 0 {
		common.ReplyErr(w, "selections required", http.StatusBadRequest)
		return
	}

	selectionByCategory := make(map[string]string, len(req.Selections))
	groupIDSet := make(map[string]struct{}, len(req.Selections))
	groupIDs := make([]string, 0, len(req.Selections))
	for _, item := range req.Selections {
		category := strings.TrimSpace(item.Category)
		groupID := strings.TrimSpace(item.GroupID)
		if category == "" {
			common.ReplyErr(w, "category is required", http.StatusBadRequest)
			return
		}
		if groupID != "" {
			if _, exists := groupIDSet[groupID]; !exists {
				groupIDSet[groupID] = struct{}{}
				groupIDs = append(groupIDs, groupID)
			}
		}
	}

	type groupWithCategory struct {
		ID       string `gorm:"column:id"`
		Category string `gorm:"column:category"`
	}
	var groups []groupWithCategory
	if len(groupIDs) > 0 {
		if err := db.WithContext(r.Context()).Table("user_model_provider_groups g").
			Select("g.id, p.category").
			Joins("JOIN user_model_providers p ON p.id = g.user_model_provider_id AND p.create_user_id = g.create_user_id AND p.deleted_at IS NULL").
			Where("g.id IN ? AND g.create_user_id = ? AND p.create_user_id = ? AND g.deleted_at IS NULL", groupIDs, userID, userID).
			Scan(&groups).Error; err != nil {
			common.ReplyErr(w, "query group failed", http.StatusInternalServerError)
			return
		}
	}
	categoryByGroupID := make(map[string]string, len(groups))
	for _, group := range groups {
		categoryByGroupID[group.ID] = group.Category
	}
	for _, item := range req.Selections {
		groupID := strings.TrimSpace(item.GroupID)
		category := strings.TrimSpace(item.Category)
		if groupID != "" {
			groupCategory, ok := categoryByGroupID[groupID]
			if !ok {
				common.ReplyErr(w, "group not found", http.StatusBadRequest)
				return
			}
			if category != "" && category != groupCategory {
				common.ReplyErr(w, "category does not match group", http.StatusBadRequest)
				return
			}
			category = groupCategory
		}
		if _, exists := selectionByCategory[category]; exists {
			common.ReplyErr(w, "duplicate category in selections", http.StatusBadRequest)
			return
		}
		selectionByCategory[category] = groupID
	}

	if err := saveSelectedProviders(r.Context(), db, userID, userName, selectionByCategory); err != nil {
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

func saveSelectedProviders(ctx context.Context, db *gorm.DB, userID, userName string, selectionByCategory map[string]string) error {
	now := time.Now()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for category, groupID := range selectionByCategory {
			if groupID == "" {
				if err := tx.Where("user_id = ? AND category = ?", userID, category).
					Delete(&orm.UserSelectedProvider{}).Error; err != nil {
					return err
				}
				continue
			}
			var row orm.UserSelectedProvider
			findErr := tx.Where("user_id = ? AND category = ?", userID, category).Take(&row).Error
			if errors.Is(findErr, gorm.ErrRecordNotFound) {
				if err := tx.Create(&orm.UserSelectedProvider{
					UserID:                   userID,
					UserName:                 userName,
					Category:                 category,
					UserModelProviderGroupID: groupID,
					Share:                    false,
					CreatedAt:                now,
					UpdatedAt:                now,
				}).Error; err != nil {
					return err
				}
				continue
			}
			if findErr != nil {
				return findErr
			}
			if err := tx.Model(&orm.UserSelectedProvider{}).
				Where("id = ?", row.ID).
				Updates(map[string]any{
					"user_model_provider_group_id": groupID,
					"user_name":                    userName,
					"updated_at":                   now,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
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
		Joins(
			"JOIN user_model_provider_groups g ON "+
				"g.id = usp.user_model_provider_group_id AND "+
				"g.create_user_id = usp.user_id AND "+
				"g.deleted_at IS NULL AND "+
				"g.is_verified = ?",
			true,
		).
		Joins(
			"JOIN user_model_providers p ON "+
				"p.id = g.user_model_provider_id AND "+
				"p.create_user_id = usp.user_id AND "+
				"p.deleted_at IS NULL",
		).
		Where("usp.user_id = ?", userID).
		Order("usp.category ASC").
		Scan(&out).Error
	return out, err
}
