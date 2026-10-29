package modelprovider

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
)

func IsTranslationProviderAvailable(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "google translate web", "bing translate web", "sogou translate web", "deepl web", "tencent transmart web", "baidu translation", "aliyun translation", "niutrans":
		return true
	default:
		return false
	}
}

// EnsureDefaultTranslationServices enables built-in free sources for every user.
// Existing connections and the user's explicitly chosen default are preserved.
func EnsureDefaultTranslationServices(ctx context.Context, db *gorm.DB, userID string) error {
	if err := syncUserProvidersFromDefaults(ctx, db, userID, ""); err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var providers []orm.UserModelProvider
		if err := tx.Where("create_user_id = ? AND category = ? AND deleted_at IS NULL", userID, "translation").Order("name ASC").Find(&providers).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		defaultGroupID := ""
		for _, provider := range providers {
			if !IsTranslationProviderAvailable(provider.Name) || !provider.HasCapability("keyless_translation") {
				continue
			}
			var group orm.UserModelProviderGroup
			err := tx.Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL AND is_verified = ?", provider.ID, userID, true).Order("created_at ASC").Take(&group).Error
			if err == gorm.ErrRecordNotFound {
				group = orm.UserModelProviderGroup{ID: "free-" + provider.ID, UserModelProviderID: provider.ID, Name: provider.Name, BaseURL: provider.BaseURL, IsVerified: true,
					BaseModel: orm.BaseModel{CreateUserID: userID, CreatedAt: now, UpdatedAt: now}}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&group).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			if defaultGroupID == "" || provider.Name == "Google Translate Web" {
				defaultGroupID = group.ID
			}
		}
		if defaultGroupID == "" {
			return nil
		}
		selection := orm.UserSelectedProvider{UserID: userID, Category: "translation", UserModelProviderGroupID: defaultGroupID, CreatedAt: now, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&selection).Error; err != nil {
			return err
		}
		var chosen struct{ Name string }
		if err := tx.Table("user_selected_providers s").Select("p.name").
			Joins("JOIN user_model_provider_groups g ON g.id = s.user_model_provider_group_id AND g.create_user_id = s.user_id AND g.deleted_at IS NULL").
			Joins("JOIN user_model_providers p ON p.id = g.user_model_provider_id AND p.create_user_id = s.user_id AND p.deleted_at IS NULL").
			Where("s.user_id = ? AND s.category = ?", userID, "translation").Scan(&chosen).Error; err != nil {
			return err
		}
		if IsTranslationProviderAvailable(chosen.Name) {
			return nil
		}
		return tx.Model(&orm.UserSelectedProvider{}).Where("user_id = ? AND category = ?", userID, "translation").
			Updates(map[string]interface{}{"user_model_provider_group_id": defaultGroupID, "share": false, "updated_at": now}).Error
	})
}
