package metadata

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/versionfs"
)

const ExternalCategory = "external"

const (
	MaxSkillNameLength        = 80
	MaxSkillDescriptionLength = 1024
)

type Metadata struct {
	Name        string
	Description string
	Version     string
	Category    string
	Tags        []string
}

type LengthError struct {
	Field string
	Max   int
}

func (e *LengthError) Error() string {
	return fmt.Sprintf("skill %s cannot exceed %d characters", e.Field, e.Max)
}

func IsNameLengthError(err error) bool {
	var lengthErr *LengthError
	return errors.As(err, &lengthErr) && lengthErr.Field == "name"
}

type frontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Version     string   `yaml:"version"`
	Category    string   `yaml:"category"`
	Tags        []string `yaml:"tags"`
}

func ParseRequired(content []byte) (Metadata, error) {
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter is required")
	}
	rest := strings.TrimPrefix(normalized, "---\n")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter closing separator is required")
	}
	var raw frontmatter
	if err := yaml.Unmarshal([]byte(rest[:idx]), &raw); err != nil {
		return Metadata{}, fmt.Errorf("invalid SKILL.md frontmatter: %w", err)
	}
	meta := Metadata{
		Name:        strings.TrimSpace(raw.Name),
		Description: strings.TrimSpace(raw.Description),
		Version:     strings.TrimSpace(raw.Version),
		Category:    strings.TrimSpace(raw.Category),
		Tags:        compact(raw.Tags),
	}
	if meta.Name == "" {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter field \"name\" is required")
	}
	if err := validatePathSegment(meta.Name); err != nil {
		return Metadata{}, fmt.Errorf("invalid SKILL.md frontmatter field \"name\": %w", err)
	}
	if err := ValidateNameLength(meta.Name); err != nil {
		return Metadata{}, err
	}
	if meta.Description == "" {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter field \"description\" is required")
	}
	if err := ValidateDescriptionLength(meta.Description); err != nil {
		return Metadata{}, err
	}
	return meta, nil
}

func ValidateNameLength(name string) error {
	if utf8.RuneCountInString(strings.TrimSpace(name)) > MaxSkillNameLength {
		return &LengthError{Field: "name", Max: MaxSkillNameLength}
	}
	return nil
}

func ValidateDescriptionLength(description string) error {
	if utf8.RuneCountInString(strings.TrimSpace(description)) > MaxSkillDescriptionLength {
		return &LengthError{Field: "description", Max: MaxSkillDescriptionLength}
	}
	return nil
}

func compact(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func FromFiles(files map[string][]byte) (Metadata, error) {
	content, ok := files["SKILL.md"]
	if !ok {
		return Metadata{}, fmt.Errorf("skill package must contain SKILL.md")
	}
	return ParseRequired(content)
}

func FromEntries(ctx context.Context, tx *gorm.DB, entries map[string]versionfs.Entry) (Metadata, error) {
	entry, ok := entries["SKILL.md"]
	if !ok || entry.EntryType != versionfs.EntryTypeFile || strings.TrimSpace(entry.BlobHash) == "" {
		return Metadata{}, fmt.Errorf("skill package must contain SKILL.md")
	}
	var blob orm.SkillV2Blob
	if err := tx.WithContext(ctx).Where("hash = ?", entry.BlobHash).Take(&blob).Error; err != nil {
		return Metadata{}, err
	}
	if blob.Binary || blob.StorageBackend != "postgres" {
		return Metadata{}, fmt.Errorf("SKILL.md must be a text file")
	}
	return ParseRequired(blob.Content)
}

func FromRevision(ctx context.Context, tx *gorm.DB, revisionID string) (Metadata, error) {
	var rows []orm.SkillV2RevisionEntry
	if err := tx.WithContext(ctx).Where("revision_id = ?", revisionID).Find(&rows).Error; err != nil {
		return Metadata{}, err
	}
	entries := make(map[string]versionfs.Entry, len(rows))
	for _, row := range rows {
		blobHash := ""
		if row.BlobHash != nil {
			blobHash = *row.BlobHash
		}
		entries[row.Path] = versionfs.Entry{
			Path:      row.Path,
			EntryType: row.EntryType,
			BlobHash:  blobHash,
			Size:      row.Size,
			Mime:      row.Mime,
			FileType:  row.FileType,
			Binary:    row.Binary,
			Mode:      row.Mode,
		}
	}
	return FromEntries(ctx, tx, entries)
}

func SyncPublished(ctx context.Context, tx *gorm.DB, skillID string, entries map[string]versionfs.Entry, now time.Time) error {
	meta, err := FromEntries(ctx, tx, entries)
	if err != nil {
		return err
	}
	return Sync(ctx, tx, skillID, meta, now)
}

func SyncRevision(ctx context.Context, tx *gorm.DB, skillID, revisionID string, now time.Time) error {
	meta, err := FromRevision(ctx, tx, revisionID)
	if err != nil {
		return err
	}
	return Sync(ctx, tx, skillID, meta, now)
}

func Sync(ctx context.Context, tx *gorm.DB, skillID string, meta Metadata, now time.Time) error {
	var skill orm.SkillV2Skill
	if err := tx.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", skillID).Take(&skill).Error; err != nil {
		return err
	}
	var conflicts int64
	if err := tx.WithContext(ctx).Model(&orm.SkillV2Skill{}).
		Where("owner_user_id = ? AND category = ? AND skill_name = ? AND deleted_at IS NULL AND id <> ?", skill.OwnerUserID, skill.Category, meta.Name, skill.ID).
		Count(&conflicts).Error; err != nil {
		return err
	}
	if conflicts > 0 {
		return fmt.Errorf("skill name conflict")
	}
	return tx.WithContext(ctx).Model(&orm.SkillV2Skill{}).
		Where("id = ? AND deleted_at IS NULL", skill.ID).
		Updates(map[string]any{
			"skill_name":    meta.Name,
			"description":   meta.Description,
			"relative_root": path.Join(skill.Category, meta.Name),
			"updated_at":    now,
		}).Error
}

func validatePathSegment(segment string) error {
	switch {
	case segment == "." || segment == "..":
		return fmt.Errorf("invalid path segment")
	case strings.Contains(segment, "/") || strings.Contains(segment, `\`):
		return fmt.Errorf("path segment cannot contain slash")
	default:
		return nil
	}
}
