package search

import (
	"context"
	"encoding/json"
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
	pattern := "%" + escapeLike(keyword) + "%"
	tagPattern := "%" + escapeLike(jsonStringContent(keyword)) + "%"
	err := s.db.WithContext(ctx).Model(&indexRow{}).
		Where("skill_id = ? AND (LOWER(content) LIKE ? ESCAPE '!' OR LOWER(content) LIKE ? ESCAPE '!')", skillID, pattern, tagPattern).
		Count(&count).Error
	if isMissingIndexTable(err) {
		return containsHeadText(ctx, s.db, skillID, keyword)
	}
	return count > 0, err
}

// KeywordScope returns a set-based, freshness-aware predicate for list queries.
// The read path guarantees fresh search results but does not repair missing or
// stale search index rows; stale and missing indexes fall back to the current
// head revision content instead of rebuilding per skill.
func (s *Service) KeywordScope(keyword string) func(*gorm.DB) *gorm.DB {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return func(db *gorm.DB) *gorm.DB { return db }
	}
	hasIndexTable := s != nil && s.db != nil && s.db.Migrator().HasTable(&indexRow{})
	pattern := "%" + escapeLike(keyword) + "%"
	tagPattern := "%" + escapeLike(jsonStringContent(keyword)) + "%"
	contentExpr := blobContentTextExpr(s.db)
	tagsExpr := tagsTextExpr(s.db)

	metadataPredicate := "(LOWER(skills.skill_name) LIKE ? ESCAPE '!' OR LOWER(skills.category) LIKE ? ESCAPE '!' OR LOWER(skills.description) LIKE ? ESCAPE '!' OR LOWER(" + tagsExpr + ") LIKE ? ESCAPE '!')"
	headPredicate := `EXISTS (
		SELECT 1
		FROM skill_revision_entries AS e
		JOIN skill_blobs AS b ON b.hash = e.blob_hash
		WHERE e.revision_id = skills.head_revision_id
		  AND e.entry_type = ?
		  AND b."binary" = ?
		  AND (LOWER(e.path) LIKE ? ESCAPE '!' OR LOWER(` + contentExpr + `) LIKE ? ESCAPE '!')
	)`
	headArgs := []any{"file", false, pattern, pattern}

	if !hasIndexTable {
		return func(db *gorm.DB) *gorm.DB {
			args := []any{pattern, pattern, pattern, tagPattern}
			args = append(args, headArgs...)
			return db.Where("("+metadataPredicate+" OR "+headPredicate+")", args...)
		}
	}

	indexPredicate := `EXISTS (
		SELECT 1
		FROM skill_search_indexes AS idx
		WHERE idx.skill_id = skills.id
		  AND idx.head_revision_id = skills.head_revision_id
		  AND (LOWER(idx.content) LIKE ? ESCAPE '!' OR LOWER(idx.content) LIKE ? ESCAPE '!')
	)`
	fallbackPredicate := `NOT EXISTS (
		SELECT 1
		FROM skill_search_indexes AS fresh_idx
		WHERE fresh_idx.skill_id = skills.id
		  AND fresh_idx.head_revision_id = skills.head_revision_id
	) AND ` + headPredicate

	return func(db *gorm.DB) *gorm.DB {
		args := []any{pattern, pattern, pattern, tagPattern, pattern, tagPattern}
		args = append(args, headArgs...)
		return db.Where("("+metadataPredicate+" OR "+indexPredicate+" OR ("+fallbackPredicate+"))", args...)
	}
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
	lowered := strings.ToLower(content)
	return strings.Contains(lowered, keyword) || strings.Contains(lowered, jsonStringContent(keyword)), nil
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

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `!`, `!!`)
	value = strings.ReplaceAll(value, `%`, `!%`)
	value = strings.ReplaceAll(value, `_`, `!_`)
	return value
}

func jsonStringContent(value string) string {
	raw, _ := json.Marshal(value)
	return strings.TrimSuffix(strings.TrimPrefix(string(raw), `"`), `"`)
}

func tagsTextExpr(db *gorm.DB) string {
	if db != nil && db.Dialector != nil {
		switch db.Dialector.Name() {
		case "mysql":
			return "CAST(skills.tags AS CHAR)"
		case "postgres":
			return "skills.tags::text"
		}
	}
	return "CAST(skills.tags AS TEXT)"
}

func blobContentTextExpr(db *gorm.DB) string {
	if db != nil && db.Dialector != nil {
		switch db.Dialector.Name() {
		case "mysql":
			return "CAST(b.content AS CHAR)"
		case "postgres":
			return "convert_from(b.content, 'UTF8')"
		}
	}
	return "CAST(b.content AS TEXT)"
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
