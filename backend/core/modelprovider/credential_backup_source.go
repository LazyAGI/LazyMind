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
	var group orm.UserModelProviderGroup
	if err := source.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", groupID).Take(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return credentialvault.ProviderCredential{}, nil
		}
		return credentialvault.ProviderCredential{}, err
	}
	apiKey, err := apiKeyForGroup(source.DB.WithContext(ctx), &group)
	if err != nil {
		return credentialvault.ProviderCredential{}, err
	}
	var provider orm.UserModelProvider
	if err := source.DB.WithContext(ctx).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", group.UserModelProviderID, group.CreateUserID).Take(&provider).Error; err != nil {
		return credentialvault.ProviderCredential{}, err
	}
	catalogKey := strings.TrimSpace(provider.DefaultModelProviderID)
	if catalogKey == "" {
		catalogKey = strings.TrimSpace(provider.Name)
	}
	return credentialvault.ProviderCredential{
		LocalProviderGroupID: group.ID, CredentialRevision: group.CredentialRevision,
		ProviderCatalogKey: catalogKey, DisplayName: group.Name, BaseURL: group.BaseURL, APIKey: []byte(apiKey),
	}, nil
}

var _ credentialvault.ProviderCredentialSource = CredentialBackupSource{}
