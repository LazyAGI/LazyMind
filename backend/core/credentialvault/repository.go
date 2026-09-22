package credentialvault

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type VaultAccount struct {
	ID                string     `gorm:"column:id;primaryKey" json:"-"`
	CloudIssuer       string     `gorm:"column:cloud_issuer" json:"cloud_issuer"`
	CloudAccountID    string     `gorm:"column:cloud_account_id" json:"cloud_account_id"`
	VaultID           string     `gorm:"column:vault_id" json:"vault_id"`
	VaultMemberID     string     `gorm:"column:vault_member_id" json:"vault_member_id"`
	ClientMemberKey   string     `gorm:"column:client_member_key" json:"-"`
	SigningKeyVersion int        `gorm:"column:signing_key_version" json:"-"`
	KeyShardID        int        `gorm:"column:key_shard_id" json:"key_shard_id"`
	ActiveKeyID       string     `gorm:"column:active_key_id" json:"active_key_id"`
	BackupEnabled     bool       `gorm:"column:backup_enabled" json:"enabled"`
	VaultETag         string     `gorm:"column:vault_etag" json:"-"`
	RecordCount       int64      `gorm:"column:record_count" json:"backed_up"`
	LastBackupAt      *time.Time `gorm:"column:last_backup_at" json:"last_backup_at,omitempty"`
	LastSucceededAt   *time.Time `gorm:"column:last_succeeded_at" json:"last_succeeded_at,omitempty"`
	CreatedAt         time.Time  `gorm:"column:created_at" json:"-"`
	UpdatedAt         time.Time  `gorm:"column:updated_at" json:"-"`
}

func (VaultAccount) TableName() string { return "cloud_credential_vault_accounts" }

type CredentialBinding struct {
	ID                          string    `gorm:"column:id;primaryKey"`
	CloudIssuer                 string    `gorm:"column:cloud_issuer"`
	CloudAccountID              string    `gorm:"column:cloud_account_id"`
	VaultID                     string    `gorm:"column:vault_id"`
	CloudRecordID               string    `gorm:"column:cloud_record_id"`
	LocalProviderGroupID        string    `gorm:"column:local_provider_group_id"`
	LastCloudRevision           int64     `gorm:"column:last_cloud_revision"`
	LastLocalCredentialRevision int64     `gorm:"column:last_local_credential_revision"`
	LastETag                    string    `gorm:"column:last_etag"`
	BackupState                 string    `gorm:"column:backup_state"`
	CreatedAt                   time.Time `gorm:"column:created_at"`
	UpdatedAt                   time.Time `gorm:"column:updated_at"`
}

func (CredentialBinding) TableName() string { return "cloud_credential_bindings" }

type backupOutboxRow struct {
	ID                      string    `gorm:"column:id;primaryKey"`
	CloudIssuer             string    `gorm:"column:cloud_issuer"`
	CloudAccountID          string    `gorm:"column:cloud_account_id"`
	LocalProviderGroupID    string    `gorm:"column:local_provider_group_id"`
	LocalCredentialRevision int64     `gorm:"column:local_credential_revision"`
	Operation               string    `gorm:"column:operation"`
	BackupState             string    `gorm:"column:backup_state"`
	AttemptCount            int       `gorm:"column:attempt_count"`
	NextAttemptAt           time.Time `gorm:"column:next_attempt_at"`
	LastErrorCode           int       `gorm:"column:last_error_code"`
	CreatedAt               time.Time `gorm:"column:created_at"`
	UpdatedAt               time.Time `gorm:"column:updated_at"`
}

func (backupOutboxRow) TableName() string { return "credential_backup_outbox" }

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

func (repository *Repository) Account(ctx context.Context, scope AccountScope) (VaultAccount, error) {
	if repository == nil || repository.db == nil || !validAccountScope(scope) {
		return VaultAccount{}, ErrLocalSecureStoreUnavailable
	}
	var account VaultAccount
	err := repository.db.WithContext(ctx).Where("cloud_issuer = ? AND cloud_account_id = ?", scope.CloudIssuer, scope.CloudAccountID).Take(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return VaultAccount{}, ErrLocalKeyNotFound
	}
	return account, err
}

func (repository *Repository) SaveAccount(ctx context.Context, account VaultAccount) error {
	if repository == nil || repository.db == nil {
		return ErrLocalSecureStoreUnavailable
	}
	return repository.db.WithContext(ctx).Save(&account).Error
}

func (repository *Repository) SetBackupEnabled(ctx context.Context, scope AccountScope, enabled bool, now time.Time) error {
	result := repository.db.WithContext(ctx).Model(&VaultAccount{}).
		Where("cloud_issuer = ? AND cloud_account_id = ?", scope.CloudIssuer, scope.CloudAccountID).
		Updates(map[string]any{"backup_enabled": enabled, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLocalKeyNotFound
	}
	return nil
}

func EnqueueCredentialBackup(tx *gorm.DB, groupID string, revision int64, operation BackupOperation, now time.Time) error {
	if tx == nil || groupID == "" || revision < 1 || (operation != BackupUpsert && operation != BackupDelete) {
		return ErrInvalidContract
	}
	if !tx.Migrator().HasTable(&VaultAccount{}) || !tx.Migrator().HasTable(&backupOutboxRow{}) {
		return nil
	}
	var accounts []VaultAccount
	if err := tx.Where("backup_enabled = ?", true).Find(&accounts).Error; err != nil {
		return err
	}
	for _, account := range accounts {
		var current backupOutboxRow
		err := tx.Where("cloud_issuer = ? AND cloud_account_id = ? AND local_provider_group_id = ?", account.CloudIssuer, account.CloudAccountID, groupID).Take(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			current = backupOutboxRow{
				ID: uuid.NewString(), CloudIssuer: account.CloudIssuer, CloudAccountID: account.CloudAccountID,
				LocalProviderGroupID: groupID, LocalCredentialRevision: revision, Operation: string(operation),
				BackupState: string(BackupPending), NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&current).Error; err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if revision >= current.LocalCredentialRevision {
			if err := tx.Model(&current).Updates(map[string]any{
				"local_credential_revision": revision, "operation": string(operation), "backup_state": string(BackupPending),
				"attempt_count": 0, "next_attempt_at": now, "last_error_code": 0, "updated_at": now,
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (repository *Repository) EnqueueExistingCredentials(ctx context.Context, scope AccountScope, now time.Time) error {
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var groups []struct {
			ID                 string
			CredentialRevision int64
		}
		if err := tx.Table("user_model_provider_groups").Select("id, credential_revision").
			Where("deleted_at IS NULL AND credential_revision > 0 AND TRIM(api_key_ciphertext) <> ''").Find(&groups).Error; err != nil {
			return err
		}
		for _, group := range groups {
			row := backupOutboxRow{
				ID: uuid.NewString(), CloudIssuer: scope.CloudIssuer, CloudAccountID: scope.CloudAccountID,
				LocalProviderGroupID: group.ID, LocalCredentialRevision: group.CredentialRevision,
				Operation: string(BackupUpsert), BackupState: string(BackupPending), NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
			}
			var existing backupOutboxRow
			err := tx.Where("cloud_issuer = ? AND cloud_account_id = ? AND local_provider_group_id = ?", scope.CloudIssuer, scope.CloudAccountID, group.ID).Take(&existing).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else if group.CredentialRevision >= existing.LocalCredentialRevision {
				if err := tx.Model(&existing).Updates(map[string]any{
					"local_credential_revision": group.CredentialRevision, "operation": string(BackupUpsert),
					"backup_state": string(BackupPending), "attempt_count": 0, "next_attempt_at": now, "last_error_code": 0, "updated_at": now,
				}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (repository *Repository) ClaimDue(ctx context.Context, scope AccountScope, now time.Time) (BackupOutboxItem, bool, error) {
	var result BackupOutboxItem
	found := false
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row backupOutboxRow
		err := tx.Where("cloud_issuer = ? AND cloud_account_id = ? AND backup_state IN ? AND next_attempt_at <= ?", scope.CloudIssuer, scope.CloudAccountID, []string{string(BackupPending), string(BackupFailed)}, now).
			Order("next_attempt_at ASC, updated_at ASC").Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := tx.Model(&row).Updates(map[string]any{"backup_state": string(BackupRunning), "updated_at": now}).Error; err != nil {
			return err
		}
		result = outboxDomain(row)
		result.State = BackupRunning
		found = true
		return nil
	})
	return result, found, err
}

func (repository *Repository) Binding(ctx context.Context, scope AccountScope, groupID string) (CredentialBinding, error) {
	var binding CredentialBinding
	err := repository.db.WithContext(ctx).Where("cloud_issuer = ? AND cloud_account_id = ? AND local_provider_group_id = ?", scope.CloudIssuer, scope.CloudAccountID, groupID).Take(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return CredentialBinding{}, ErrLocalKeyNotFound
	}
	return binding, err
}

func (repository *Repository) Complete(ctx context.Context, scope AccountScope, item BackupOutboxItem, binding CredentialBinding, now time.Time) error {
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&binding).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", item.ID).Delete(&backupOutboxRow{}).Error; err != nil {
			return err
		}
		return tx.Model(&VaultAccount{}).Where("cloud_issuer = ? AND cloud_account_id = ?", scope.CloudIssuer, scope.CloudAccountID).
			Updates(map[string]any{"last_succeeded_at": now, "last_backup_at": now, "updated_at": now}).Error
	})
}

func (repository *Repository) Retry(ctx context.Context, item BackupOutboxItem) error {
	return repository.db.WithContext(ctx).Model(&backupOutboxRow{}).Where("id = ?", item.ID).Updates(map[string]any{
		"backup_state": string(item.State), "attempt_count": item.AttemptCount, "next_attempt_at": item.NextAttemptAt,
		"last_error_code": item.LastErrorCode, "updated_at": item.UpdatedAt,
	}).Error
}

func (repository *Repository) Discard(ctx context.Context, itemID string) error {
	return repository.db.WithContext(ctx).Where("id = ?", itemID).Delete(&backupOutboxRow{}).Error
}

func (repository *Repository) Conflict(ctx context.Context, itemID string, code int, now time.Time) error {
	return repository.db.WithContext(ctx).Model(&backupOutboxRow{}).Where("id = ?", itemID).
		Updates(map[string]any{"backup_state": string(BackupConflict), "last_error_code": code, "updated_at": now}).Error
}

func (repository *Repository) Counts(ctx context.Context, scope AccountScope) (backedUp, pending, failed int64, err error) {
	if err = repository.db.WithContext(ctx).Model(&CredentialBinding{}).Where("cloud_issuer = ? AND cloud_account_id = ? AND backup_state <> ?", scope.CloudIssuer, scope.CloudAccountID, string(BackupConflict)).Count(&backedUp).Error; err != nil {
		return
	}
	query := repository.db.WithContext(ctx).Model(&backupOutboxRow{}).Where("cloud_issuer = ? AND cloud_account_id = ?", scope.CloudIssuer, scope.CloudAccountID)
	if err = query.Where("backup_state IN ?", []string{string(BackupPending), string(BackupRunning)}).Count(&pending).Error; err != nil {
		return
	}
	err = repository.db.WithContext(ctx).Model(&backupOutboxRow{}).Where("cloud_issuer = ? AND cloud_account_id = ? AND backup_state IN ?", scope.CloudIssuer, scope.CloudAccountID, []string{string(BackupFailed), string(BackupConflict)}).Count(&failed).Error
	return
}

func outboxDomain(row backupOutboxRow) BackupOutboxItem {
	return BackupOutboxItem{
		ID: row.ID, LocalProviderGroupID: row.LocalProviderGroupID, LocalCredentialRevision: row.LocalCredentialRevision,
		Operation: BackupOperation(row.Operation), State: BackupState(row.BackupState), AttemptCount: row.AttemptCount,
		NextAttemptAt: row.NextAttemptAt, LastErrorCode: row.LastErrorCode, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
