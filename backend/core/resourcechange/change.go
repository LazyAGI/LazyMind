package resourcechange

import (
	"context"
	"strings"
	"time"

	"github.com/pmezard/go-difflib/difflib"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

func CreateModel(ctx context.Context, db *gorm.DB, value any, change ContentChange) error {
	if err := db.WithContext(ctx).Create(value).Error; err != nil {
		return err
	}
	return RecordContentChange(ctx, db, change)
}

func CreateIntoModel(ctx context.Context, db *gorm.DB, model any, value any, change ContentChange) error {
	if err := db.WithContext(ctx).Model(model).Create(value).Error; err != nil {
		return err
	}
	return RecordContentChange(ctx, db, change)
}

func UpdateModel(ctx context.Context, db *gorm.DB, model any, scope func(*gorm.DB) *gorm.DB, updates map[string]any, change ContentChange) (int64, error) {
	query := db.WithContext(ctx).Model(model)
	if scope != nil {
		query = scope(query)
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return result.RowsAffected, result.Error
	}
	if result.RowsAffected == 0 {
		return result.RowsAffected, nil
	}
	if err := RecordContentChange(ctx, db, change); err != nil {
		return result.RowsAffected, err
	}
	return result.RowsAffected, nil
}

func DeleteModel(ctx context.Context, db *gorm.DB, model any, scope func(*gorm.DB) *gorm.DB, change ContentChange) (int64, error) {
	if err := RecordContentChange(ctx, db, change); err != nil {
		return 0, err
	}
	query := db.WithContext(ctx)
	if scope != nil {
		query = scope(query)
	}
	result := query.Delete(model)
	return result.RowsAffected, result.Error
}

func RecordContentChange(ctx context.Context, db *gorm.DB, change ContentChange) error {
	if db == nil || change.BeforeContent == change.AfterContent {
		return nil
	}
	resourceType := strings.TrimSpace(change.ResourceType)
	resourceID := strings.TrimSpace(change.ResourceID)
	userID := strings.TrimSpace(change.UserID)
	changeSource := strings.TrimSpace(change.Source.ChangeSource)
	if resourceType == "" || resourceID == "" || userID == "" || changeSource == "" {
		return nil
	}
	createdAt := change.Source.ChangedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	diff, err := buildContentDiff(change.BeforeContent, change.AfterContent)
	if err != nil {
		return err
	}
	row := orm.ResourceVersion{
		ID:            common.GenerateID(),
		ResourceType:  resourceType,
		ResourceID:    resourceID,
		UserID:        userID,
		ChangeSource:  changeSource,
		FromVersion:   change.FromVersion,
		ToVersion:     change.ToVersion,
		SourceRefType: strings.TrimSpace(change.Source.SourceRefType),
		SourceRefID:   strings.TrimSpace(change.Source.SourceRefID),
		BeforeContent: change.BeforeContent,
		AfterContent:  change.AfterContent,
		Diff:          diff,
		CreatedAt:     createdAt,
	}
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		return err
	}
	return pruneOldVersions(ctx, db, resourceType, resourceID, userID)
}

func pruneOldVersions(ctx context.Context, db *gorm.DB, resourceType, resourceID, userID string) error {
	var staleIDs []string
	if err := db.WithContext(ctx).
		Model(&orm.ResourceVersion{}).
		Select("id").
		Where("resource_type = ? AND resource_id = ? AND user_id = ?", resourceType, resourceID, userID).
		Order("created_at DESC, id DESC").
		Offset(MaxVersionsPerResource).
		Pluck("id", &staleIDs).Error; err != nil {
		return err
	}
	if len(staleIDs) == 0 {
		return nil
	}
	return db.WithContext(ctx).Where("id IN ?", staleIDs).Delete(&orm.ResourceVersion{}).Error
}

func buildContentDiff(beforeContent, afterContent string) (string, error) {
	if beforeContent == afterContent {
		return "", nil
	}
	contextLines := len(strings.Split(beforeContent, "\n")) + len(strings.Split(afterContent, "\n"))
	if contextLines < 3 {
		contextLines = 3
	}
	return difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(beforeContent),
		B:        difflib.SplitLines(afterContent),
		FromFile: "before",
		ToFile:   "after",
		Context:  contextLines,
	})
}
