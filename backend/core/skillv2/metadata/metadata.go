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

type Parsed struct {
	Metadata
	Body           string
	HasName        bool
	HasDescription bool
}

type Resolved struct {
	Metadata
	Content      []byte
	UsedFallback bool
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
	parsed, err := Parse(content)
	if err != nil {
		return Metadata{}, err
	}
	if !parsed.HasName {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter field \"name\" is required")
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
	meta := Metadata{
		Name:        strings.TrimSpace(raw.Name),
		Description: strings.TrimSpace(raw.Description),
		Version:     strings.TrimSpace(raw.Version),
		Category:    strings.TrimSpace(raw.Category),
		Tags:        compact(raw.Tags),
	}
	if meta.Name != "" {
		if err := validatePathSegment(meta.Name); err != nil {
			return Parsed{}, fmt.Errorf("invalid SKILL.md frontmatter field \"name\": %w", err)
		}
		if err := ValidateNameLength(meta.Name); err != nil {
			return Parsed{}, err
		}
	}
	if meta.Description != "" {
		if err := ValidateDescriptionLength(meta.Description); err != nil {
			return Parsed{}, err
		}
	}
	body := strings.TrimPrefix(rest[idx+len("\n---"):], "\n")
	return Parsed{
		Metadata:       meta,
		Body:           body,
		HasName:        meta.Name != "",
		HasDescription: meta.Description != "",
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

func Resolve(content []byte, fallbackNames ...string) (Resolved, error) {
	return ResolveWithFallback(content, Metadata{}, fallbackNames...)
}

func ResolveWithFallback(content []byte, fallback Metadata, fallbackNames ...string) (Resolved, error) {
	parsed, err := Parse(content)
	if err != nil {
		return Resolved{}, err
	}
	if parsed.HasName && parsed.HasDescription {
		return Resolved{Metadata: parsed.Metadata, Content: content}, nil
	}
	name := parsed.Name
	if !parsed.HasName {
		candidates := append([]string{fallback.Name}, fallbackNames...)
		for _, candidate := range candidates {
			candidate = strings.TrimSpace(candidate)
			if ValidateName(candidate) == nil {
				name = candidate
				break
			}
		}
	}
	description := parsed.Description
	if !parsed.HasDescription {
		description = strings.TrimSpace(fallback.Description)
		if description == "" {
			description = FirstBodyParagraph(parsed.Body)
		}
	}
	effective, err := EffectiveDocument(content, name, description)
	if err != nil {
		return Resolved{}, err
	}
	meta, err := ParseRequired(effective)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Metadata: meta, Content: effective, UsedFallback: true}, nil
}

func EffectiveDocument(content []byte, name, description string) ([]byte, error) {
	if _, err := ParseRequired(content); err == nil {
		return content, nil
	}
	parsed, err := Parse(content)
	if err != nil {
		return nil, err
	}
	metadata := map[string]any{}
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	if strings.HasPrefix(normalized, "---\n") {
		rest := strings.TrimPrefix(normalized, "---\n")
		idx := strings.Index(rest, "\n---")
		if idx < 0 {
			return nil, fmt.Errorf("SKILL.md frontmatter closing separator is required")
		}
		if err := yaml.Unmarshal([]byte(rest[:idx]), &metadata); err != nil {
			return nil, fmt.Errorf("invalid SKILL.md frontmatter: %w", err)
		}
	}
	if !parsed.HasName {
		metadata["name"] = strings.TrimSpace(name)
	}
	if !parsed.HasDescription {
		metadata["description"] = strings.TrimSpace(description)
	}
	frontmatter, err := yaml.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	effective := []byte(fmt.Sprintf("---\n%s---\n%s", frontmatter, parsed.Body))
	if _, err := ParseRequired(effective); err != nil {
		return nil, err
	}
	return effective, nil
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

func ValidateNameLength(name string) error {
	if utf8.RuneCountInString(strings.TrimSpace(name)) > MaxSkillNameLength {
		return &LengthError{Field: "name", Max: MaxSkillNameLength}
	}
	return nil
}

func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name required")
	}
	if err := validatePathSegment(name); err != nil {
		return err
	}
	return ValidateNameLength(name)
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

func FromRevisionWithFallback(ctx context.Context, tx *gorm.DB, revisionID string, fallback Metadata) (Metadata, error) {
	entries, err := entriesFromRevision(ctx, tx, revisionID)
	if err != nil {
		return Metadata{}, err
	}
	content, err := skillMDContent(ctx, tx, entries)
	if err != nil {
		return Metadata{}, err
	}
	resolved, err := ResolveWithFallback(content, fallback)
	if err != nil {
		return Metadata{}, err
	}
	return resolved.Metadata, nil
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
	content, err := skillMDContent(ctx, tx, entries)
	if err != nil {
		return err
	}
	if skill.Category == ExternalCategory || strings.TrimSpace(skill.OriginBuiltinSkillUID) != "" {
		resolved, err := ResolveWithFallback(content, Metadata{Name: skill.SkillName, Description: skill.Description})
		if err != nil {
			return err
		}
		return Sync(ctx, tx, skillID, resolved.Metadata, now)
	}
	meta, err := ParseRequired(content)
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
		return fmt.Errorf("skill already exists")
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
