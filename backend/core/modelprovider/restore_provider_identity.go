package modelprovider

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
)

func restoreCatalogSupplier(tx *gorm.DB, userID, catalogKey string) (orm.UserModelProvider, error) {
	var provider orm.UserModelProvider
	err := tx.Where("create_user_id = ? AND deleted_at IS NULL AND is_custom = ? AND (default_model_provider_id = ? OR LOWER(name) = LOWER(?) OR LOWER(protocol_name) = LOWER(?))", userID, false, catalogKey, catalogKey, catalogKey).Order("id ASC").Take(&provider).Error
	return provider, err
}

// A remote record has its own supplier identity. Without a local binding it
// must never append a second credential group to a supplier already in use.
func restoredSupplier(tx *gorm.DB, userID string, payload credentialvault.ProviderCredential, resolution credentialvault.ConflictResolution, now time.Time) (orm.UserModelProvider, error) {
	source, err := restoreCatalogSupplier(tx, userID, payload.ProviderCatalogKey)
	if err != nil {
		return source, err
	}
	if !hasSharedProviderEndpoint(source) {
		return source, nil
	}
	if err := lockProvider(tx, &source); err != nil {
		return source, err
	}
	name := strings.TrimSpace(payload.DisplayName)
	if name == "" {
		name = source.Name
	}
	var count int64
	if err := tx.Model(&orm.UserModelProviderGroup{}).Where("user_model_provider_id = ? AND create_user_id = ? AND deleted_at IS NULL", source.ID, userID).Count(&count).Error; err != nil {
		return source, err
	}
	if count == 0 && resolution != credentialvault.ConflictSaveCopy && strings.EqualFold(name, source.Name) && normalizeBaseURLForCompare(payload.BaseURL) == normalizeBaseURLForCompare(source.BaseURL) {
		return source, nil
	}
	row := source
	row.ID, row.Name, row.IsCustom, row.ProtocolName = common.GenerateID(), name, true, RuntimeProviderName(source)
	row.BaseURL = LazyLLMBaseURL(row.ProtocolName, payload.BaseURL)
	row.Capabilities = strings.Join(modelSupplierCapabilities(row.Capabilities), ",")
	row.ActiveKeyGroupID, row.ActiveKeyID, row.ActiveKeyInitialized, row.ActiveKeyRevision = "", "", false, 0
	row.BaseModel = orm.BaseModel{CreateUserID: userID, CreatedAt: now, UpdatedAt: now}
	return row, tx.Create(&row).Error
}

func (sink *CredentialRestoreSink) temporarySupplier(ctx context.Context, scope credentialvault.AccountScope, recordID string, payload credentialvault.ProviderCredential) (orm.UserModelProvider, error) {
	tx := sink.db.WithContext(ctx)
	var binding credentialvault.CredentialBinding
	if tx.Migrator().HasTable(&binding) {
		err := tx.Where("cloud_issuer = ? AND cloud_account_id = ? AND cloud_record_id = ?", scope.CloudIssuer, scope.CloudAccountID, recordID).Take(&binding).Error
		if err == nil {
			var group orm.UserModelProviderGroup
			if err := tx.Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", binding.LocalProviderGroupID, sink.localUserID).Take(&group).Error; err != nil {
				return orm.UserModelProvider{}, err
			}
			var provider orm.UserModelProvider
			err := tx.Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", group.UserModelProviderID, sink.localUserID).Take(&provider).Error
			return provider, err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return orm.UserModelProvider{}, err
		}
	}
	source, err := restoreCatalogSupplier(tx, sink.localUserID, payload.ProviderCatalogKey)
	if err != nil {
		return source, err
	}
	if hasSharedProviderEndpoint(source) && strings.TrimSpace(payload.BaseURL) != "" && (strings.TrimSpace(payload.DisplayName) != "" && !strings.EqualFold(payload.DisplayName, source.Name) || normalizeBaseURLForCompare(payload.BaseURL) != normalizeBaseURLForCompare(source.BaseURL)) {
		source.ProtocolName, source.Name, source.IsCustom = RuntimeProviderName(source), payload.DisplayName, true
		source.BaseURL = LazyLLMBaseURL(source.ProtocolName, payload.BaseURL)
	}
	return source, nil
}
