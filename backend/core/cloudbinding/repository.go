package cloudbinding

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db  *gorm.DB
	now func() time.Time
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db, now: time.Now}
}

func (r *Repository) FindByCloudIDs(ctx context.Context, issuer, accountID, resourceType string, resourceIDs []string) (map[string]Binding, error) {
	result := make(map[string]Binding)
	if r == nil || r.db == nil || len(resourceIDs) == 0 {
		return result, nil
	}
	var rows []Binding
	err := r.db.WithContext(ctx).
		Where("cloud_issuer = ? AND cloud_account_id = ? AND resource_type = ? AND cloud_resource_id IN ?",
			strings.TrimSpace(issuer), strings.TrimSpace(accountID), strings.TrimSpace(resourceType), resourceIDs).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.CloudResourceID] = row
	}
	return result, nil
}

func (r *Repository) FindByLocalID(ctx context.Context, issuer, accountID, resourceType, localResourceID string) (Binding, bool, error) {
	if r == nil || r.db == nil {
		return Binding{}, false, gorm.ErrInvalidDB
	}
	var row Binding
	err := r.db.WithContext(ctx).Where(
		"cloud_issuer = ? AND cloud_account_id = ? AND resource_type = ? AND local_resource_id = ?",
		strings.TrimSpace(issuer), strings.TrimSpace(accountID), strings.TrimSpace(resourceType), strings.TrimSpace(localResourceID),
	).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Binding{}, false, nil
	}
	return row, err == nil, err
}

func (r *Repository) Upsert(ctx context.Context, binding Binding) (Binding, error) {
	if r == nil || r.db == nil {
		return Binding{}, gorm.ErrInvalidDB
	}
	if err := validateBinding(binding); err != nil {
		return Binding{}, err
	}
	now := r.now().UTC()
	if strings.TrimSpace(binding.ID) == "" {
		binding.ID = uuid.NewString()
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	binding.UpdatedAt = now
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "cloud_issuer"}, {Name: "cloud_account_id"}, {Name: "resource_type"}, {Name: "cloud_resource_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"client_resource_key", "cloud_content_hash", "local_resource_id", "local_resource_ref",
			"installed_local_revision_id", "installed_local_content_hash", "cloud_resource_name", "updated_at",
		}),
	}).Create(&binding).Error
	if err != nil {
		return Binding{}, err
	}
	var stored Binding
	err = r.db.WithContext(ctx).Where(
		"cloud_issuer = ? AND cloud_account_id = ? AND resource_type = ? AND cloud_resource_id = ?",
		binding.CloudIssuer, binding.CloudAccountID, binding.ResourceType, binding.CloudResourceID,
	).Take(&stored).Error
	return stored, err
}

func validateBinding(binding Binding) error {
	required := []string{
		binding.CloudIssuer, binding.CloudAccountID, binding.ResourceType, binding.CloudResourceID,
		binding.ClientResourceKey, binding.CloudContentHash, binding.LocalResourceID,
		binding.InstalledLocalRevisionID, binding.InstalledLocalContentHash, binding.CloudResourceName,
	}
	for _, value := range required {
		if strings.TrimSpace(value) == "" {
			return errors.New("cloud resource binding is incomplete")
		}
	}
	if binding.ResourceType != "skill" && binding.ResourceType != "workflow" {
		return errors.New("cloud resource binding type must be skill or workflow")
	}
	return nil
}
