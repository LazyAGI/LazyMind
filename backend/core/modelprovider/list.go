package modelprovider

import (
	"context"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type listItem struct {
	ID                     string   `json:"id"`
	DefaultModelProviderID string   `json:"default_model_provider_id"`
	Name                   string   `json:"name"`
	Description            string   `json:"description"`
	BaseURL                string   `json:"base_url"`
	ModelTypes             []string `json:"model_types"`
}

type listResponse struct {
	Providers []listItem `json:"providers"`
}

// ListUserProviders returns the current user's model providers. When the list
// is empty, all DefaultModelProvider rows are copied into user_model_providers.
// Optional query param: keyword — substring match on name (SQL LIKE).
func ListUserProviders(w http.ResponseWriter, r *http.Request) {
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

	userName := strings.TrimSpace(store.UserName(r))
	if err := seedUserProvidersIfEmpty(r.Context(), db, userID, userName); err != nil {
		common.ReplyErr(w, "sync model providers failed", http.StatusInternalServerError)
		return
	}

	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	q := db.WithContext(r.Context()).Model(&orm.UserModelProvider{}).
		Where("create_user_id = ? AND deleted_at IS NULL", userID)
	if keyword != "" {
		q = q.Where("name LIKE ?", "%"+keyword+"%")
	}

	var rows []orm.UserModelProvider
	if err := q.Order("name DESC").Find(&rows).Error; err != nil {
		common.ReplyErr(w, "list model providers failed", http.StatusInternalServerError)
		return
	}

	out := buildListItems(r.Context(), db, rows)
	common.ReplyOK(w, listResponse{Providers: out})
}

// ListUserProvidersWithGroups returns user_model_providers rows that have at least one non-deleted
// user_model_provider_groups row for the current user (distinct parent ids from groups, then load providers).
func ListUserProvidersWithGroups(w http.ResponseWriter, r *http.Request) {
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
	var providerIDs []string
	if err := db.WithContext(r.Context()).Model(&orm.UserModelProviderGroup{}).
		Where("create_user_id = ? AND deleted_at IS NULL", userID).
		Distinct("user_model_provider_id").
		Pluck("user_model_provider_id", &providerIDs).Error; err != nil {
		common.ReplyErr(w, "list group parent ids failed", http.StatusInternalServerError)
		return
	}
	if len(providerIDs) == 0 {
		common.ReplyOK(w, listResponse{Providers: []listItem{}})
		return
	}

	var rows []orm.UserModelProvider
	if err := db.WithContext(r.Context()).
		Where("id IN ? AND create_user_id = ? AND deleted_at IS NULL", providerIDs, userID).
		Order("name ASC").
		Find(&rows).Error; err != nil {
		common.ReplyErr(w, "list model providers failed", http.StatusInternalServerError)
		return
	}

	out := buildListItems(r.Context(), db, rows)
	common.ReplyOK(w, listResponse{Providers: out})
}

// buildListItems converts UserModelProvider rows to listItems and batch-loads
// distinct model_types from default_models for each provider.
func buildListItems(ctx context.Context, db *gorm.DB, rows []orm.UserModelProvider) []listItem {
	out := make([]listItem, 0, len(rows))
	for i := range rows {
		row := rows[i]
		out = append(out, listItem{
			ID:                     row.ID,
			DefaultModelProviderID: row.DefaultModelProviderID,
			Name:                   row.Name,
			Description:            row.Description,
			BaseURL:                row.BaseURL,
			ModelTypes:             []string{},
		})
	}
	if len(out) == 0 {
		return out
	}

	defaultProviderIDs := make([]string, 0, len(out))
	for i := range out {
		defaultProviderIDs = append(defaultProviderIDs, out[i].DefaultModelProviderID)
	}
	type modelTypeRow struct {
		DefaultModelProviderID string `gorm:"column:default_model_provider_id"`
		ModelType              string `gorm:"column:model_type"`
	}
	var mtRows []modelTypeRow
	if err := db.WithContext(ctx).
		Model(&orm.DefaultModel{}).
		Select("default_model_provider_id, model_type").
		Where("default_model_provider_id IN ? AND deleted_at IS NULL", defaultProviderIDs).
		Distinct("default_model_provider_id", "model_type").
		Find(&mtRows).Error; err == nil {
		mtMap := make(map[string][]string, len(defaultProviderIDs))
		for _, r := range mtRows {
			mtMap[r.DefaultModelProviderID] = append(mtMap[r.DefaultModelProviderID], r.ModelType)
		}
		for i := range out {
			if types, ok := mtMap[out[i].DefaultModelProviderID]; ok {
				out[i].ModelTypes = types
			}
		}
	}
	return out
}

func seedUserProvidersIfEmpty(ctx context.Context, db *gorm.DB, userID, userName string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&orm.UserModelProvider{}).
			Where("create_user_id = ? AND deleted_at IS NULL", userID).
			Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return nil
		}

		var defs []orm.DefaultModelProvider
		if err := tx.Find(&defs).Error; err != nil {
			return err
		}
		if len(defs) == 0 {
			return nil
		}

		now := time.Now()
		batch := make([]orm.UserModelProvider, len(defs))
		for i := range defs {
			d := defs[i]
			batch[i] = orm.UserModelProvider{
				ID:                     common.GenerateID(),
				DefaultModelProviderID: d.ID,
				Name:                   d.Name,
				Description:            d.Description,
				BaseURL:                d.BaseURL,
				BaseModel: orm.BaseModel{
					CreateUserID:   userID,
					CreateUserName: userName,
					CreatedAt:      now,
					UpdatedAt:      now,
					DeletedAt:      nil,
				},
			}
		}
		return tx.Create(&batch).Error
	})
}
