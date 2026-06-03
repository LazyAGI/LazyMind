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
}

type verifiedGroupResponse struct {
	Ready        bool   `json:"ready"`
	Source       string `json:"source,omitempty"`         // "own" | "shared"
	SharedByName string `json:"shared_by_name,omitempty"` // sharer's display name
	SharedByID   string `json:"shared_by_id,omitempty"`   // sharer's user_id
	ProviderName string `json:"provider_name,omitempty"`
	GroupName    string `json:"group_name,omitempty"`
}

type sharedProviderDetail struct {
	UserID       string
	UserName     string
	ProviderName string
	GroupName    string
}

// GetVerifiedProvider checks whether a cloud provider category is ready for the current user.
// It follows the selected model ready semantics: check the user's own selection first,
// then fall back to a shared admin selection for the same category.
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

	ownCount, err := countValidProviderSelection(r.Context(), db, userID, category, false)
	if err != nil {
		common.ReplyErr(w, "query verified provider failed", http.StatusInternalServerError)
		return
	}
	if ownCount > 0 {
		common.ReplyOK(w, verifiedGroupResponse{Ready: true, Source: "own"})
		return
	}

	sharedCount, err := countValidProviderSelection(r.Context(), db, userID, category, true)
	if err != nil {
		common.ReplyErr(w, "query verified provider failed", http.StatusInternalServerError)
		return
	}
	if sharedCount > 0 {
		resp := verifiedGroupResponse{Ready: true, Source: "shared"}
		if detail, detailErr := getSharedProviderDetail(r.Context(), db, category); detailErr == nil && detail != nil {
			resp.SharedByName = detail.UserName
			resp.SharedByID = detail.UserID
			resp.ProviderName = detail.ProviderName
			resp.GroupName = detail.GroupName
		}
		common.ReplyOK(w, resp)
		return
	}

	common.ReplyOK(w, verifiedGroupResponse{Ready: false})
}

func loadVerifiedGroupsForUser(ctx context.Context, db *gorm.DB, userID, category string) ([]verifiedGroupItem, error) {
	type row struct {
		GroupID             string `gorm:"column:group_id"`
		UserModelProviderID string `gorm:"column:user_model_provider_id"`
		ProviderName        string `gorm:"column:provider_name"`
		GroupName           string `gorm:"column:group_name"`
		BaseURL             string `gorm:"column:base_url"`
		Category            string `gorm:"column:category"`
	}
	var rows []row
	err := db.WithContext(ctx).Table("user_model_provider_groups g").
		Select(
			"g.id AS group_id, "+
				"g.user_model_provider_id, "+
				"p.name AS provider_name, "+
				"g.name AS group_name, "+
				"g.base_url, "+
				"p.category",
		).
		Joins("JOIN user_model_providers p ON p.id = g.user_model_provider_id AND p.create_user_id = g.create_user_id AND p.deleted_at IS NULL").
		Where("g.create_user_id = ? AND p.category = ? AND g.deleted_at IS NULL AND g.is_verified = ?", userID, category, true).
		Order("p.name ASC, g.name ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := make([]verifiedGroupItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, verifiedGroupItem{
			GroupID:             r.GroupID,
			UserModelProviderID: r.UserModelProviderID,
			ProviderName:        r.ProviderName,
			GroupName:           r.GroupName,
			BaseURL:             r.BaseURL,
			Category:            r.Category,
		})
	}
	return out, nil
}

func countValidProviderSelection(ctx context.Context, db *gorm.DB, userID, category string, sharedOnly bool) (int64, error) {
	var count int64
	q := db.WithContext(ctx).Table("user_selected_providers usp").
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
		Where("usp.category = ? AND p.category = ?", category, category)
	if sharedOnly {
		q = q.Where("usp.share = ?", true)
	} else {
		q = q.Where("usp.user_id = ?", userID)
	}
	err := q.Count(&count).Error
	return count, err
}

func getSharedProviderDetail(ctx context.Context, db *gorm.DB, category string) (*sharedProviderDetail, error) {
	var row struct {
		UserID       string `gorm:"column:user_id"`
		UserName     string `gorm:"column:user_name"`
		ProviderName string `gorm:"column:provider_name"`
		GroupName    string `gorm:"column:group_name"`
	}
	err := db.WithContext(ctx).Table("user_selected_providers usp").
		Select(
			"usp.user_id, "+
				"usp.user_name, "+
				"p.name AS provider_name, "+
				"g.name AS group_name",
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
		Where("usp.category = ? AND p.category = ? AND usp.share = ?", category, category, true).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sharedProviderDetail{
		UserID:       row.UserID,
		UserName:     row.UserName,
		ProviderName: row.ProviderName,
		GroupName:    row.GroupName,
	}, nil
}
