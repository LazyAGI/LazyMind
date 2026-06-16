package skill

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"lazymind/core/common/orm"
	"lazymind/core/evolution"
)

type parentFrontmatter struct {
	Name        string `yaml:"name"`
	Category    string `yaml:"category,omitempty"`
	Description string `yaml:"description"`
}

func normalizeExt(ext string) string {
	ext = strings.TrimSpace(strings.TrimPrefix(ext, "."))
	if ext == "" {
		return "md"
	}
	return strings.ToLower(ext)
}

func validatePathSegment(segment string) error {
	segment = strings.TrimSpace(segment)
	switch {
	case segment == "":
		return errors.New("path segment required")
	case segment == "." || segment == "..":
		return errors.New("invalid path segment")
	case strings.Contains(segment, "/") || strings.Contains(segment, "\\"):
		return errors.New("path segment cannot contain slash")
	}
	return nil
}

func parentRelativePath(category, skillName string) string {
	return evolution.ParentSkillRelativePath(category, skillName)
}

func childRelativePath(category, parentSkillName, childName, ext string) string {
	return filepath.ToSlash(filepath.Join(strings.TrimSpace(category), strings.TrimSpace(parentSkillName), fmt.Sprintf("%s.%s", strings.TrimSpace(childName), normalizeExt(ext))))
}

func storedSkillContent(row orm.SkillResource) (string, error) {
	return row.Content, nil
}

func skillContentSize(content string) int64 {
	return int64(len([]byte(content)))
}

func mimeTypeForExt(ext string) string {
	ext = strings.TrimSpace(ext)
	if ext == "" {
		return "text/plain; charset=utf-8"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	if mt := mime.TypeByExtension(strings.ToLower(ext)); mt != "" {
		if strings.HasPrefix(mt, "text/") && !strings.Contains(strings.ToLower(mt), "charset=") {
			return mt + "; charset=utf-8"
		}
		return mt
	}
	switch strings.ToLower(ext) {
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".py", ".sh", ".js", ".ts", ".json", ".yaml", ".yml", ".txt":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func parseTags(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		return nil
	}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		if trimmed := strings.TrimSpace(tag); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

func tagsJSON(tags []string) json.RawMessage {
	if len(tags) == 0 {
		return nil
	}
	uniq := make([]string, 0, len(tags))
	seen := map[string]struct{}{}
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		uniq = append(uniq, trimmed)
	}
	if len(uniq) == 0 {
		return nil
	}
	sort.Strings(uniq)
	b, _ := json.Marshal(uniq)
	return b
}

func parseFrontmatter(content string) (*parentFrontmatter, string, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return nil, "", errors.New("parent skill content must start with YAML frontmatter")
	}
	rest := strings.TrimPrefix(content, "---\n")
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return nil, "", errors.New("parent skill content must contain closing frontmatter separator")
	}
	yamlPart := rest[:idx]
	body := rest[idx+5:]
	if strings.TrimSpace(body) == "" {
		return nil, "", errors.New("parent skill content must include markdown body")
	}
	var meta parentFrontmatter
	if err := yaml.Unmarshal([]byte(yamlPart), &meta); err != nil {
		return nil, "", fmt.Errorf("invalid skill frontmatter: %w", err)
	}
	return &meta, body, nil
}

func parentSkillBody(content string) (string, error) {
	_, body, err := parseFrontmatter(content)
	if err != nil {
		return "", err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", errors.New("content required")
	}
	return body, nil
}

func buildParentSkillContent(name, category, description, body string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", errors.New("name required")
	}
	category = strings.TrimSpace(category)
	if category == "" {
		return "", "", errors.New("category required")
	}
	description = strings.TrimSpace(description)
	if description == "" {
		return "", "", errors.New("description required")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", "", errors.New("content required")
	}
	meta, err := yaml.Marshal(parentFrontmatter{
		Name:        name,
		Category:    category,
		Description: description,
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal skill frontmatter failed: %w", err)
	}
	content := fmt.Sprintf("---\n%s---\n%s", string(meta), body)
	resolvedDescription, err := validateParentSkillContent(name, description, content)
	if err != nil {
		return "", "", err
	}
	return content, resolvedDescription, nil
}

func validateParentSkillContent(name, description, content string) (string, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	content = strings.TrimSpace(content)
	if content == "" {
		return "", errors.New("content required")
	}
	meta, _, err := parseFrontmatter(content)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(meta.Name) == "" {
		return "", errors.New("frontmatter name required")
	}
	if strings.TrimSpace(meta.Description) == "" {
		return "", errors.New("frontmatter description required")
	}
	if strings.TrimSpace(meta.Name) != name {
		return "", errors.New("request name and frontmatter name must match")
	}
	resolvedDescription := strings.TrimSpace(meta.Description)
	if description != "" && description != resolvedDescription {
		return "", errors.New("request description and frontmatter description must match")
	}
	return resolvedDescription, nil
}
