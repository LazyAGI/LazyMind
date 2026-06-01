package modelprovider

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/store"
)

type verifiedGroupItem struct {
	GroupID             string `json:"group_id"`
	UserModelProviderID string `json:"user_model_provider_id"`
	ProviderName        string `json:"provider_name"`
	GroupName           string `json:"group_name"`
	BaseURL             string `json:"base_url"`
	Category            string `json:"category"`
	Source              string `json:"source,omitempty"`         // "own" | "shared"
	SharedByName        string `json:"shared_by_name,omitempty"` // sharer's display name
	SharedByID          string `json:"shared_by_id,omitempty"`   // sharer's user_id
}

type verifiedGroupResponse struct {
	Ready bool               `json:"ready"`
	Group *verifiedGroupItem `json:"group,omitempty"`
}

// GetVerifiedProvider returns the verified provider group for the given category.
// It first checks the user's own selection (user_selected_providers), then falls back
// to any share=true row for the same category.
//
// GET /model_providers/verified?category=ocr
func GetVerifiedProvider(w http.ResponseWriter, r *http.Request) {
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

	// 1. Check user's own selection.
	item, err := loadVerifiedGroupForUser(r.Context(), db, userID, category, false)
	if err != nil {
		common.ReplyErr(w, "query verified provider failed", http.StatusInternalServerError)
		return
	}
	if item != nil {
		item.Source = "own"
		common.ReplyOK(w, verifiedGroupResponse{Ready: true, Group: item})
		return
	}

	// 2. Fallback to shared selection.
	item, err = loadVerifiedGroupForUser(r.Context(), db, userID, category, true)
	if err != nil {
		common.ReplyErr(w, "query shared provider failed", http.StatusInternalServerError)
		return
	}
	if item != nil {
		item.Source = "shared"
		common.ReplyOK(w, verifiedGroupResponse{Ready: true, Group: item})
		return
	}

	common.ReplyOK(w, verifiedGroupResponse{Ready: false})
}

// loadVerifiedGroupForUser loads the verified group for a user's selected provider.
// When sharedOnly is true, it looks for any share=true row (any user) for the category.
func loadVerifiedGroupForUser(ctx context.Context, db *gorm.DB, userID, category string, sharedOnly bool) (*verifiedGroupItem, error) {
	type row struct {
		GroupID             string `gorm:"column:group_id"`
		UserModelProviderID string `gorm:"column:user_model_provider_id"`
		ProviderName        string `gorm:"column:provider_name"`
		GroupName           string `gorm:"column:group_name"`
		BaseURL             string `gorm:"column:base_url"`
		Category            string `gorm:"column:category"`
		SharedByID          string `gorm:"column:shared_by_id"`
		SharedByName        string `gorm:"column:shared_by_name"`
	}
	var r row
	q := db.WithContext(ctx).Table("user_selected_providers usp").
		Select(
			"usp.user_model_provider_group_id AS group_id, "+
				"g.user_model_provider_id, "+
				"p.name AS provider_name, "+
				"g.name AS group_name, "+
				"g.base_url, "+
				"p.category, "+
				"usp.user_id AS shared_by_id, "+
				"usp.user_name AS shared_by_name",
		).
		Joins("JOIN user_model_provider_groups g ON g.id = usp.user_model_provider_group_id AND g.deleted_at IS NULL AND g.is_verified = ?", true).
		Joins("JOIN user_model_providers p ON p.id = g.user_model_provider_id AND p.deleted_at IS NULL").
		Where("usp.category = ?", category)

	if sharedOnly {
		q = q.Where("usp.share = ?", true)
	} else {
		q = q.Where("usp.user_id = ?", userID)
	}

	err := q.First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item := &verifiedGroupItem{
		GroupID:             r.GroupID,
		UserModelProviderID: r.UserModelProviderID,
		ProviderName:        r.ProviderName,
		GroupName:           r.GroupName,
		BaseURL:             r.BaseURL,
		Category:            r.Category,
		SharedByID:          r.SharedByID,
		SharedByName:        r.SharedByName,
	}
	return item, nil
}
