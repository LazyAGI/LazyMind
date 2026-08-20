package metadata

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/versionfs"
)

const ExternalCategory = "external"

type Metadata struct {
	Name        string
	Description string
}

type Parsed struct {
	Metadata
	Body           string
	HasName        bool
	HasDescription bool
}

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func ParseRequired(content []byte) (Metadata, error) {
	parsed, err := Parse(content)
	if err != nil {
		return Metadata{}, err
	}
	if !parsed.HasName {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter field \"name\" is required")
	}
	if err := validatePathSegment(parsed.Name); err != nil {
		return Metadata{}, fmt.Errorf("invalid SKILL.md frontmatter field \"name\": %w", err)
	}
	if !parsed.HasDescription {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter field \"description\" is required")
	}
	return parsed.Metadata, nil
}

func Parse(content []byte) (Parsed, error) {
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return Parsed{Body: normalized}, nil
	}
	rest := strings.TrimPrefix(normalized, "---\n")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return Parsed{}, fmt.Errorf("SKILL.md frontmatter closing separator is required")
	}
	var raw frontmatter
	if err := yaml.Unmarshal([]byte(rest[:idx]), &raw); err != nil {
		return Parsed{}, fmt.Errorf("invalid SKILL.md frontmatter: %w", err)
	}
	name := strings.TrimSpace(raw.Name)
	description := strings.TrimSpace(raw.Description)
	body := strings.TrimPrefix(rest[idx+len("\n---"):], "\n")
	return Parsed{
		Metadata: Metadata{
			Name:        name,
			Description: description,
		},
		Body:           body,
		HasName:        name != "",
		HasDescription: description != "",
	}, nil
}

func FirstBodyParagraph(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	paragraph := make([]string, 0)
	started := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !started {
			if trimmed == "" || isMarkdownHeading(trimmed) {
				continue
			}
			started = true
		}
		if trimmed == "" {
			break
		}
		paragraph = append(paragraph, trimmed)
	}
	text := strings.Join(strings.Fields(strings.Join(paragraph, " ")), " ")
	runes := []rune(text)
	if len(runes) > 100 {
		return string(runes[:100]) + "…"
	}
	return text
}

func isMarkdownHeading(line string) bool {
	line = strings.TrimLeft(line, " \t")
	count := 0
	for count < len(line) && line[count] == '#' {
		count++
	}
	if count == 0 || count > 6 || count >= len(line) {
		return false
	}
	return line[count] == ' ' || line[count] == '\t'
}

func FromFiles(files map[string][]byte) (Metadata, error) {
	content, ok := files["SKILL.md"]
	if !ok {
		return Metadata{}, fmt.Errorf("skill package must contain SKILL.md")
	}
	return ParseRequired(content)
}

func FromEntries(ctx context.Context, tx *gorm.DB, entries map[string]versionfs.Entry) (Metadata, error) {
	content, err := skillMDContent(ctx, tx, entries)
	if err != nil {
		return Metadata{}, err
	}
	return ParseRequired(content)
}

func skillMDContent(ctx context.Context, tx *gorm.DB, entries map[string]versionfs.Entry) ([]byte, error) {
	entry, ok := entries["SKILL.md"]
	if !ok || entry.EntryType != versionfs.EntryTypeFile || strings.TrimSpace(entry.BlobHash) == "" {
		return nil, fmt.Errorf("skill package must contain SKILL.md")
	}
	var blob orm.SkillV2Blob
	if err := tx.WithContext(ctx).Where("hash = ?", entry.BlobHash).Take(&blob).Error; err != nil {
		return nil, err
	}
	if blob.Binary || blob.StorageBackend != "postgres" {
		return nil, fmt.Errorf("SKILL.md must be a text file")
	}
	return blob.Content, nil
}

func FromRevision(ctx context.Context, tx *gorm.DB, revisionID string) (Metadata, error) {
	entries, err := entriesFromRevision(ctx, tx, revisionID)
	if err != nil {
		return Metadata{}, err
	}
	return FromEntries(ctx, tx, entries)
}

func entriesFromRevision(ctx context.Context, tx *gorm.DB, revisionID string) (map[string]versionfs.Entry, error) {
	var rows []orm.SkillV2RevisionEntry
	if err := tx.WithContext(ctx).Where("revision_id = ?", revisionID).Find(&rows).Error; err != nil {
		return nil, err
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
	return entries, nil
}

func SyncPublished(ctx context.Context, tx *gorm.DB, skillID string, entries map[string]versionfs.Entry, now time.Time) error {
	var skill orm.SkillV2Skill
	if err := tx.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", skillID).Take(&skill).Error; err != nil {
		return err
	}
	if skill.Category == ExternalCategory {
		_, err := skillMDContent(ctx, tx, entries)
		return err
	}
	meta, err := FromEntries(ctx, tx, entries)
	if err != nil {
		return err
	}
	return Sync(ctx, tx, skillID, meta, now)
}

func SyncRevision(ctx context.Context, tx *gorm.DB, skillID, revisionID string, now time.Time) error {
	entries, err := entriesFromRevision(ctx, tx, revisionID)
	if err != nil {
		return err
	}
	return SyncPublished(ctx, tx, skillID, entries, now)
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
