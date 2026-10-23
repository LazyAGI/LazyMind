package modelprovider

import (
	"context"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

type modelProviderIdentity struct {
	BrandKey   string
	ServiceKey string
	IsCustom   bool
}

func identityForModelProvider(provider orm.UserModelProvider) modelProviderIdentity {
	brand, service := ProviderServiceIdentity(provider)
	return modelProviderIdentity{BrandKey: brand, ServiceKey: service, IsCustom: provider.IsCustom}
}

// Model option responses need the real supplier identity even when the provider
// is not present in the viewer's own supplier list. No credentials are loaded.
func loadModelProviderIdentities(ctx context.Context, db *gorm.DB, ids []string) (map[string]modelProviderIdentity, error) {
	result := make(map[string]modelProviderIdentity)
	if len(ids) == 0 {
		return result, nil
	}
	unique := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) == 0 {
		return result, nil
	}
	var providers []orm.UserModelProvider
	if err := db.WithContext(ctx).Select("id, name, is_custom, category, protocol_name, default_model_provider_id").Where("id IN ? AND deleted_at IS NULL", unique).Find(&providers).Error; err != nil {
		return nil, err
	}
	for _, provider := range providers {
		result[provider.ID] = identityForModelProvider(provider)
	}
	return result, nil
}
