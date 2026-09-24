package modelprovider

import (
	"context"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
)

type temporaryCredential struct {
	provider  credentialvault.ProviderCredential
	expiresAt time.Time
}

type CredentialRestoreSink struct {
	db          *gorm.DB
	keys        *credentialvault.LocalKeyManager
	localUserID string
	now         func() time.Time
	mu          sync.Mutex
	temporary   map[credentialvault.AccountScope]map[string]temporaryCredential
	timers      map[credentialvault.AccountScope]*time.Timer
}

var temporaryCredentialSinks = struct {
	sync.RWMutex
	byUser map[string]*CredentialRestoreSink
}{byUser: make(map[string]*CredentialRestoreSink)}

func SetTemporaryCredentialSink(localUserID string, sink *CredentialRestoreSink) {
	localUserID = strings.TrimSpace(localUserID)
	if localUserID == "" {
		return
	}
	temporaryCredentialSinks.Lock()
	if sink == nil {
		delete(temporaryCredentialSinks.byUser, localUserID)
	} else {
		temporaryCredentialSinks.byUser[localUserID] = sink
	}
	temporaryCredentialSinks.Unlock()
}

func ResolveTemporaryCredential(localUserID, recordID string) (credentialvault.ProviderCredential, bool) {
	temporaryCredentialSinks.RLock()
	sink := temporaryCredentialSinks.byUser[strings.TrimSpace(localUserID)]
	temporaryCredentialSinks.RUnlock()
	if sink == nil {
		return credentialvault.ProviderCredential{}, false
	}
	return sink.TemporaryCredential(recordID)
}

func NewCredentialRestoreSink(db *gorm.DB, keys *credentialvault.LocalKeyManager, localUserID string, now func() time.Time) (*CredentialRestoreSink, error) {
	if db == nil || keys == nil || localUserID == "" || now == nil {
		return nil, credentialvault.ErrInvalidContract
	}
	return &CredentialRestoreSink{
		db: db, keys: keys, localUserID: localUserID, now: now,
		temporary: make(map[credentialvault.AccountScope]map[string]temporaryCredential), timers: make(map[credentialvault.AccountScope]*time.Timer),
	}, nil
}

func (sink *CredentialRestoreSink) Inspect(ctx context.Context, scope credentialvault.AccountScope, recordID string) (credentialvault.LocalRestoreState, error) {
	if sink == nil || sink.db == nil || !restoreSinkScope(scope) || !restoreSinkUUID.MatchString(recordID) {
		return credentialvault.LocalRestoreState{}, credentialvault.ErrInvalidContract
	}
	var binding credentialvault.CredentialBinding
	err := sink.db.WithContext(ctx).Where("cloud_issuer = ? AND cloud_account_id = ? AND cloud_record_id = ?", scope.CloudIssuer, scope.CloudAccountID, recordID).Take(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return credentialvault.LocalRestoreState{}, nil
	}
	if err != nil {
		return credentialvault.LocalRestoreState{}, err
	}
	var group orm.UserModelProviderGroup
	if err := sink.db.WithContext(ctx).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", binding.LocalProviderGroupID, sink.localUserID).Take(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return credentialvault.LocalRestoreState{
				Exists: true, LocalProviderGroupID: binding.LocalProviderGroupID,
				CurrentLocalRevision:   binding.LastLocalCredentialRevision + 1,
				LastBoundLocalRevision: binding.LastLocalCredentialRevision, LastBoundCloudRevision: binding.LastCloudRevision,
			}, nil
		}
		return credentialvault.LocalRestoreState{}, err
	}
	return credentialvault.LocalRestoreState{
		Exists: true, LocalProviderGroupID: group.ID, CurrentLocalRevision: group.CredentialRevision,
		LastBoundLocalRevision: binding.LastLocalCredentialRevision, LastBoundCloudRevision: binding.LastCloudRevision,
	}, nil
}

func (sink *CredentialRestoreSink) PersistTrusted(ctx context.Context, scope credentialvault.AccountScope, credentials []credentialvault.RestoredCredential, resolutions map[string]credentialvault.ConflictResolution, now time.Time) error {
	if sink == nil || sink.db == nil || sink.keys == nil {
		return credentialvault.ErrLocalSecureStoreUnavailable
	}
	if !restoreSinkScope(scope) || len(credentials) < 1 || len(credentials) > 1000 || now.IsZero() {
		return credentialvault.ErrInvalidContract
	}
	if sink.db.Migrator().HasTable(&orm.DefaultModelProvider{}) {
		if err := syncUserProvidersFromDefaults(ctx, sink.db, sink.localUserID, ""); err != nil {
			return err
		}
	}
	return sink.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for index := range credentials {
			credential := credentials[index]
			resolution, found := resolutions[credential.RecordID]
			if !found || !validRestoredCredential(credential) ||
				resolution != credentialvault.ConflictFail && resolution != credentialvault.ConflictReplaceLocal && resolution != credentialvault.ConflictSaveCopy {
				return credentialvault.ErrInvalidContract
			}
			if err := sink.persistOne(ctx, tx, scope, credential, resolution, now.UTC()); err != nil {
				return err
			}
		}
		return nil
	})
}

func (sink *CredentialRestoreSink) persistOne(ctx context.Context, tx *gorm.DB, scope credentialvault.AccountScope, restored credentialvault.RestoredCredential, resolution credentialvault.ConflictResolution, now time.Time) error {
	var binding credentialvault.CredentialBinding
	bindingErr := tx.Where("cloud_issuer = ? AND cloud_account_id = ? AND cloud_record_id = ?", scope.CloudIssuer, scope.CloudAccountID, restored.RecordID).Take(&binding).Error
	bindingFound := bindingErr == nil
	if bindingErr != nil && !errors.Is(bindingErr, gorm.ErrRecordNotFound) {
		return bindingErr
	}
	var group orm.UserModelProviderGroup
	groupFound := false
	if bindingFound && resolution != credentialvault.ConflictSaveCopy {
		err := tx.Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", binding.LocalProviderGroupID, sink.localUserID).Take(&group).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return credentialvault.ErrLocalConflict
			}
			return err
		}
		groupFound = true
		if group.CredentialRevision != binding.LastLocalCredentialRevision && resolution == credentialvault.ConflictFail {
			return credentialvault.ErrLocalConflict
		}
	}
	var provider orm.UserModelProvider
	if groupFound {
		if err := tx.Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", group.UserModelProviderID, sink.localUserID).Take(&provider).Error; err != nil {
			return err
		}
	} else {
		if err := tx.Where("create_user_id = ? AND deleted_at IS NULL AND (default_model_provider_id = ? OR LOWER(name) = LOWER(?))", sink.localUserID, restored.Provider.ProviderCatalogKey, restored.Provider.ProviderCatalogKey).Order("default_model_provider_id DESC").Take(&provider).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return credentialvault.ErrLocalConflict
			}
			return err
		}
	}
	localRevision := int64(1)
	groupID := uuid.NewString()
	if groupFound {
		groupID = group.ID
		localRevision = max(group.CredentialRevision, 0) + 1
	}
	ciphertext, err := encryptModelProviderAPIKeyBytes(sink.keys, sink.localUserID, groupID, localRevision, restored.Provider.APIKey)
	if err != nil {
		return err
	}
	if groupFound {
		if err := tx.Model(&orm.UserModelProviderGroup{}).Where("id = ? AND create_user_id = ?", group.ID, sink.localUserID).Updates(map[string]any{
			"name": restored.Provider.DisplayName, "base_url": restored.Provider.BaseURL, "api_key": "", "api_key_ciphertext": ciphertext,
			"credential_version": modelProviderCredentialVersion, "credential_revision": localRevision, "is_verified": true, "updated_at": now,
		}).Error; err != nil {
			return err
		}
	} else {
		group = orm.UserModelProviderGroup{
			ID: groupID, UserModelProviderID: provider.ID, Name: restored.Provider.DisplayName, BaseURL: restored.Provider.BaseURL,
			APIKey: "", APIKeyCiphertext: ciphertext, CredentialVersion: modelProviderCredentialVersion,
			CredentialRevision: localRevision, IsVerified: true,
			BaseModel: orm.BaseModel{CreateUserID: sink.localUserID, CreatedAt: now, UpdatedAt: now},
		}
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
		if tx.Migrator().HasTable(&orm.DefaultModel{}) && tx.Migrator().HasTable(&orm.UserModelProviderGroupModel{}) {
			if _, err := seedGroupModelsFromDefaults(tx, ctx, &group, &provider, restored.Provider.BaseURL, sink.localUserID, "", now); err != nil {
				return err
			}
		}
	}
	if !bindingFound {
		binding = credentialvault.CredentialBinding{
			ID: uuid.NewString(), CloudIssuer: scope.CloudIssuer, CloudAccountID: scope.CloudAccountID,
			CreatedAt: now,
		}
	}
	binding.VaultID, binding.CloudRecordID, binding.LocalProviderGroupID = restored.VaultID, restored.RecordID, groupID
	binding.LastCloudRevision, binding.LastLocalCredentialRevision = restored.Revision, localRevision
	binding.LastETag, binding.BackupState, binding.UpdatedAt = restored.ETag, string(credentialvault.BackupSucceeded), now
	return tx.Save(&binding).Error
}

func (sink *CredentialRestoreSink) ActivateTemporary(ctx context.Context, scope credentialvault.AccountScope, credentials []credentialvault.RestoredCredential, expiresAt time.Time) error {
	if sink == nil || !restoreSinkScope(scope) || len(credentials) < 1 || len(credentials) > 1000 || !expiresAt.After(sink.now()) {
		return credentialvault.ErrInvalidContract
	}
	if sink.db.Migrator().HasTable(&orm.DefaultModelProvider{}) {
		if err := syncUserProvidersFromDefaults(ctx, sink.db, sink.localUserID, ""); err != nil {
			return err
		}
	}
	values := make(map[string]temporaryCredential, len(credentials))
	for _, restored := range credentials {
		if strings.TrimSpace(restored.Provider.DisplayName) == "" || strings.TrimSpace(restored.Provider.BaseURL) == "" {
			var provider orm.UserModelProvider
			if err := sink.db.WithContext(ctx).Where("create_user_id = ? AND deleted_at IS NULL AND (default_model_provider_id = ? OR LOWER(name) = LOWER(?))", sink.localUserID, restored.Provider.ProviderCatalogKey, restored.Provider.ProviderCatalogKey).Take(&provider).Error; err != nil {
				clearTemporaryValues(values)
				return credentialvault.ErrLocalConflict
			}
			if strings.TrimSpace(restored.Provider.DisplayName) == "" {
				restored.Provider.DisplayName = provider.Name
			}
			if strings.TrimSpace(restored.Provider.BaseURL) == "" {
				restored.Provider.BaseURL = provider.BaseURL
			}
		}
		if !validRestoredCredential(restored) {
			clearTemporaryValues(values)
			return credentialvault.ErrInvalidContract
		}
		provider := restored.Provider
		provider.LocalProviderGroupID = restored.RecordID
		provider.APIKey = append([]byte(nil), restored.Provider.APIKey...)
		values[restored.RecordID] = temporaryCredential{provider: provider, expiresAt: expiresAt}
	}
	sink.mu.Lock()
	if timer := sink.timers[scope]; timer != nil {
		timer.Stop()
	}
	clearTemporaryValues(sink.temporary[scope])
	sink.temporary[scope] = values
	delay := time.Until(expiresAt)
	if delay < 0 {
		delay = 0
	}
	sink.timers[scope] = time.AfterFunc(delay, func() {
		sink.mu.Lock()
		clearTemporaryValues(sink.temporary[scope])
		delete(sink.temporary, scope)
		delete(sink.timers, scope)
		sink.mu.Unlock()
	})
	sink.mu.Unlock()
	return nil
}

func (sink *CredentialRestoreSink) ClearTemporary(_ context.Context, scope credentialvault.AccountScope) error {
	if sink == nil || !restoreSinkScope(scope) {
		return credentialvault.ErrInvalidContract
	}
	sink.mu.Lock()
	if timer := sink.timers[scope]; timer != nil {
		timer.Stop()
	}
	clearTemporaryValues(sink.temporary[scope])
	delete(sink.temporary, scope)
	delete(sink.timers, scope)
	sink.mu.Unlock()
	return nil
}

func (sink *CredentialRestoreSink) TemporaryCredential(recordID string) (credentialvault.ProviderCredential, bool) {
	if sink == nil || !restoreSinkUUID.MatchString(recordID) {
		return credentialvault.ProviderCredential{}, false
	}
	now := sink.now()
	sink.mu.Lock()
	defer sink.mu.Unlock()
	var result credentialvault.ProviderCredential
	found := false
	for scope, values := range sink.temporary {
		value, exists := values[recordID]
		if !exists {
			continue
		}
		if !now.Before(value.expiresAt) {
			clear(value.provider.APIKey)
			delete(values, recordID)
			if len(values) == 0 {
				delete(sink.temporary, scope)
			}
			continue
		}
		if found {
			return credentialvault.ProviderCredential{}, false
		}
		result = value.provider
		result.APIKey = append([]byte(nil), value.provider.APIKey...)
		found = true
	}
	return result, found
}

func validRestoredCredential(restored credentialvault.RestoredCredential) bool {
	return restoreSinkUUID.MatchString(restored.RecordID) && restored.Revision > 0 && restoreSinkUUID.MatchString(restored.VaultID) &&
		regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(restored.ETag) && strings.TrimSpace(restored.Provider.ProviderCatalogKey) != "" &&
		len(restored.Provider.ProviderCatalogKey) <= 128 && strings.TrimSpace(restored.Provider.DisplayName) != "" && len(restored.Provider.DisplayName) <= 255 &&
		strings.TrimSpace(restored.Provider.BaseURL) != "" && len(restored.Provider.BaseURL) <= 1024 && len(restored.Provider.APIKey) > 0 && len(restored.Provider.APIKey) <= 512
}

func clearTemporaryValues(values map[string]temporaryCredential) {
	for key, value := range values {
		clear(value.provider.APIKey)
		delete(values, key)
	}
}

func restoreSinkScope(scope credentialvault.AccountScope) bool {
	if strings.TrimSpace(scope.CloudIssuer) != scope.CloudIssuer || !restoreSinkUUID.MatchString(scope.CloudAccountID) {
		return false
	}
	parsed, err := url.Parse(scope.CloudIssuer)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(parsed.Hostname())
	return parsed.Scheme == "http" && (strings.EqualFold(parsed.Hostname(), "localhost") || ip != nil && ip.IsLoopback())
}

var restoreSinkUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var _ credentialvault.RestoreSink = (*CredentialRestoreSink)(nil)
