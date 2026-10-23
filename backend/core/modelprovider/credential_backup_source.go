package modelprovider

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
)

type CredentialBackupSource struct{ DB *gorm.DB }

func (source CredentialBackupSource) LoadCredential(ctx context.Context, groupID string) (credentialvault.ProviderCredential, error) {
	if source.DB == nil || strings.TrimSpace(groupID) == "" {
		return credentialvault.ProviderCredential{}, credentialvault.ErrInvalidContract
	}
	var result credentialvault.ProviderCredential
	err := source.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var group orm.UserModelProviderGroup
		if err := tx.Where("id = ? AND deleted_at IS NULL", groupID).Take(&group).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		provider := orm.UserModelProvider{ID: group.UserModelProviderID, BaseModel: orm.BaseModel{CreateUserID: group.CreateUserID}}
		if err := lockProvider(tx, &provider); err != nil {
			return err
		}
		// Re-read under the supplier lock: request metadata, endpoint and encrypted
		// credential revision must come from one committed supplier snapshot.
		if err := tx.Where("id = ? AND user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", groupID, provider.ID, provider.CreateUserID).Take(&group).Error; err != nil {
			return err
		}
		apiKey, err := apiKeyForGroup(tx, &group)
		if err != nil {
			return err
		}
		catalogKey := strings.TrimSpace(RuntimeProviderName(provider))
		if catalogKey == "" {
			catalogKey = strings.TrimSpace(provider.DefaultModelProviderID)
		}
		result = credentialvault.ProviderCredential{LocalProviderGroupID: group.ID, CredentialRevision: group.CredentialRevision,
			ProviderCatalogKey: catalogKey, DisplayName: group.Name, BaseURL: group.BaseURL, APIKey: []byte(apiKey), RequestConfig: provider.RequestConfig}
		if hasSharedProviderEndpoint(provider) && provider.ActiveKeyInitialized {
			index := -1
			if provider.ActiveKeyGroupID == group.ID {
				for i, key := range splitAPIKeys(apiKey) {
					if groupKeyID(group.ID, key) == provider.ActiveKeyID {
						index = i
						break
					}
				}
			}
			result.ActiveKeyIndex = &index
		}
		return nil
	})
	return result, err
}

var _ credentialvault.ProviderCredentialSource = CredentialBackupSource{}
