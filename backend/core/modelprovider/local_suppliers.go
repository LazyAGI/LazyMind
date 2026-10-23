package modelprovider

import (
	"context"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// Local presets keep their supplier identity when the request protocol is OpenAI.
// A custom display name alone is never evidence of the service implementation.
func isLocalOpenAISupplier(ctx context.Context, db *gorm.DB, parent orm.UserModelProvider) bool {
	return isVLLMSupplier(ctx, db, parent) || isLMDeploySupplier(ctx, db, parent)
}

func isLMDeploySupplier(ctx context.Context, db *gorm.DB, parent orm.UserModelProvider) bool {
	if normalizeProviderName(RuntimeProviderName(parent)) == "lmdeploy" || (!parent.IsCustom && normalizeProviderName(parent.Name) == "lmdeploy") {
		return true
	}
	if db == nil || parent.DefaultModelProviderID == "" || !db.Migrator().HasTable(&orm.DefaultModelProvider{}) {
		return false
	}
	var template orm.DefaultModelProvider
	if db.WithContext(ctx).Select("name").Where("id = ? AND deleted_at IS NULL", parent.DefaultModelProviderID).Take(&template).Error != nil {
		return false
	}
	return normalizeProviderName(template.Name) == "lmdeploy"
}
