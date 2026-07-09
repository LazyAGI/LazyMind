package search

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
)

type ServiceDeps struct {
	DB *gorm.DB
}

type Service struct {
	db *gorm.DB
}

func NewService(deps ServiceDeps) *Service {
	return &Service{db: deps.DB}
}

func (s *Service) RebuildSkill(ctx context.Context, skillID string) error {
	if s == nil || s.db == nil {
		return nil
	}
	return RebuildSkillTx(ctx, s.db.WithContext(ctx), skillID, time.Now())
}

func (s *Service) DeleteSkill(ctx context.Context, skillID string) error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db.WithContext(ctx).Where("skill_id = ?", skillID).Delete(&indexRow{}).Error
	if isMissingIndexTable(err) {
		return nil
	}
	return err
}

func (s *Service) Contains(ctx context.Context, skillID, keyword string) (bool, error) {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return true, nil
	}
	if s == nil || s.db == nil {
		return false, nil
	}
	if err := s.ensureFresh(ctx, skillID); err != nil {
		return false, err
	}
	var count int64
	err := s.db.WithContext(ctx).Model(&indexRow{}).
		Where("skill_id = ? AND LOWER(content) LIKE ?", skillID, "%"+keyword+"%").
		Count(&count).Error
	if isMissingIndexTable(err) {
		return containsHeadText(ctx, s.db, skillID, keyword)
	}
	return count > 0, err
}

func RebuildSkillTx(ctx context.Context, tx *gorm.DB, skillID string, now time.Time) error {
	if tx == nil {
		return nil
	}
	var skill skillRow
	if err := tx.WithContext(ctx).Where("id = ?", skillID).Take(&skill).Error; err != nil {
		return err
	}
	if skill.DeletedAt != nil || skill.HeadRevisionID == nil {
		err := tx.WithContext(ctx).Where("skill_id = ?", skillID).Delete(&indexRow{}).Error
		if isMissingIndexTable(err) {
			return nil
		}
		return err
	}
	content, err := searchContentForRevision(ctx, tx, skill, *skill.HeadRevisionID)
	if err != nil {
		return err
	}
	err = tx.WithContext(ctx).Save(&indexRow{
		SkillID:        skill.ID,
		OwnerUserID:    skill.OwnerUserID,
		HeadRevisionID: *skill.HeadRevisionID,
		Content:        content,
		UpdatedAt:      now,
	}).Error
	if isMissingIndexTable(err) {
		return nil
	}
	return err
}

func (s *Service) ensureFresh(ctx context.Context, skillID string) error {
	var skill skillRow
	if err := s.db.WithContext(ctx).Select("id", "head_revision_id", "deleted_at").Where("id = ?", skillID).Take(&skill).Error; err != nil {
		return err
	}
	if skill.DeletedAt != nil || skill.HeadRevisionID == nil {
		return s.DeleteSkill(ctx, skillID)
	}
	var row indexRow
	err := s.db.WithContext(ctx).Select("skill_id", "head_revision_id").Where("skill_id = ?", skillID).Take(&row).Error
	if err == nil && row.HeadRevisionID == *skill.HeadRevisionID {
		return nil
	}
	if isMissingIndexTable(err) {
		return nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	return s.RebuildSkill(ctx, skillID)
}

func containsHeadText(ctx context.Context, db *gorm.DB, skillID, keyword string) (bool, error) {
	var skill skillRow
	if err := db.WithContext(ctx).Where("id = ?", skillID).Take(&skill).Error; err != nil {
		return false, err
	}
	if skill.DeletedAt != nil || skill.HeadRevisionID == nil {
		return false, nil
	}
	content, err := searchContentForRevision(ctx, db, skill, *skill.HeadRevisionID)
	if err != nil {
		return false, err
	}
	return strings.Contains(strings.ToLower(content), keyword), nil
}

func isMissingIndexTable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "skill_search_indexes") &&
		(strings.Contains(msg, "no such table") || strings.Contains(msg, "does not exist") || strings.Contains(msg, "sqlstate 42p01"))
}

func searchContentForRevision(ctx context.Context, tx *gorm.DB, skill skillRow, revisionID string) (string, error) {
	parts := []string{skill.SkillName, skill.Category, skill.Description, string(skill.Tags)}
	var rows []struct {
		Path    string
		Content []byte
	}
	if err := tx.WithContext(ctx).
		Table("skill_revision_entries AS e").
		Select("e.path, b.content").
		Joins("JOIN skill_blobs AS b ON b.hash = e.blob_hash").
		Where("e.revision_id = ? AND e.entry_type = ? AND b.\"binary\" = ?", revisionID, "file", false).
		Order("e.path ASC").
		Find(&rows).Error; err != nil {
		return "", err
	}
	for _, row := range rows {
		parts = append(parts, row.Path, string(row.Content))
	}
	return strings.Join(parts, "\n"), nil
}

type indexRow struct {
	SkillID        string    `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	OwnerUserID    string    `gorm:"column:owner_user_id;type:varchar(255);not null"`
	HeadRevisionID string    `gorm:"column:head_revision_id;type:varchar(36);not null"`
	Content        string    `gorm:"column:content;type:text;not null"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null"`
}

func (indexRow) TableName() string { return "skill_search_indexes" }

type skillRow struct {
	ID             string     `gorm:"column:id;type:varchar(36);primaryKey"`
	OwnerUserID    string     `gorm:"column:owner_user_id;type:varchar(255);not null"`
	Category       string     `gorm:"column:category;type:varchar(128);not null"`
	SkillName      string     `gorm:"column:skill_name;type:varchar(255);not null"`
	Description    string     `gorm:"column:description;type:text"`
	Tags           []byte     `gorm:"column:tags;type:json"`
	HeadRevisionID *string    `gorm:"column:head_revision_id;type:varchar(36)"`
	DeletedAt      *time.Time `gorm:"column:deleted_at"`
}

func (skillRow) TableName() string { return "skills" }
