package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	skillservice "lazymind/core/skillv2/service"
)

const builtinSkillIDPrefix = "builtin:"

var errPluginSourceSkillNotFound = errors.New("plugin source skill not found")

type pluginBuiltinSkillManifest struct {
	UID      string
	Category string
	DirName  string
}

var pluginBuiltinSkillManifests = []pluginBuiltinSkillManifest{
	{UID: "bsk_01JZ7Q3YF6Q2Z4HM9V8K7D1R3P", Category: "research", DirName: "deep-research"},
	{UID: "bsk_01JZ7Q4AJ1X9N5B2C8M6T0W3EY", Category: "review", DirName: "single-document-review"},
	{UID: "bsk_01JZ7Q4RPN6K3Y8V1D5H2A9S0B", Category: "review", DirName: "systematic-document-and-literature-review"},
	{UID: "bsk_01JZ7Q58M4E7C2N9X6P1D3V0KA", Category: "search", DirName: "paper-search"},
	{UID: "bsk_01K0M8SCV7PAPERSEARCH9Q2X3A4B", Category: "search", DirName: "sciverse-paper-search"},
}

func isPluginSourceSkillNotFound(err error) bool {
	return errors.Is(err, errPluginSourceSkillNotFound) || errors.Is(err, gorm.ErrRecordNotFound)
}

// loadPluginSourceSkill reads normal skills from the v2 revision store. Legacy
// builtin template IDs are resolved locally because skillv2 does not expose a
// function-level reader for templates that have not been installed yet.
func loadPluginSourceSkill(ctx context.Context, db *gorm.DB, userID, skillID string) (string, string, error) {
	if strings.HasPrefix(skillID, builtinSkillIDPrefix) {
		return loadPluginBuiltinSkill(skillID)
	}

	svc := skillservice.NewSkillService(skillservice.SkillServiceDeps{DB: db})
	detail, err := svc.GetSkill(ctx, skillservice.GetSkillRequest{SkillID: skillID, UserID: userID})
	if err != nil {
		return "", "", err
	}
	file, err := svc.ReadFile(ctx, skillservice.FileRef{SkillID: skillID, RefType: "head", Path: "SKILL.md"})
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(file.Content) == "" {
		return "", "", errPluginSourceSkillNotFound
	}
	return file.Content, detail.SkillName, nil
}

func loadPluginBuiltinSkill(templateID string) (string, string, error) {
	id := strings.TrimPrefix(templateID, builtinSkillIDPrefix)
	uid, relativePath := id, "SKILL.md"
	if index := strings.IndexByte(id, ':'); index >= 0 {
		uid, relativePath = id[:index], id[index+1:]
	}
	manifest, ok := pluginBuiltinManifest(uid)
	if !ok || relativePath == "" {
		return "", "", errPluginSourceSkillNotFound
	}
	root := pluginBuiltinSkillsRoot()
	if root == "" {
		return "", "", fmt.Errorf("builtin skills root not found")
	}
	base := filepath.Join(root, manifest.Category, manifest.DirName)
	target := filepath.Clean(filepath.Join(base, filepath.FromSlash(relativePath)))
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", errPluginSourceSkillNotFound
	}
	data, err := os.ReadFile(target)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", errPluginSourceSkillNotFound
	}
	if err != nil {
		return "", "", err
	}
	name := strings.TrimSuffix(filepath.ToSlash(relativePath), filepath.Ext(relativePath))
	if relativePath == "SKILL.md" {
		name = builtinSkillName(data, manifest.DirName)
	}
	return string(data), name, nil
}

func pluginBuiltinManifest(uid string) (pluginBuiltinSkillManifest, bool) {
	for _, manifest := range pluginBuiltinSkillManifests {
		if manifest.UID == uid {
			return manifest, true
		}
	}
	return pluginBuiltinSkillManifest{}, false
}

func pluginBuiltinSkillsRoot() string {
	if value := strings.TrimSpace(os.Getenv("LAZYMIND_BUILTIN_SKILLS_DIR")); value != "" {
		return value
	}
	if info, err := os.Stat("/skills"); err == nil && info.IsDir() {
		return "/skills"
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for _, candidate := range []string{
		filepath.Join(wd, "skills"), filepath.Join(wd, "..", "skills"),
		filepath.Join(wd, "..", "..", "skills"), filepath.Join(wd, "..", "..", "..", "skills"),
	} {
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

func builtinSkillName(content []byte, fallback string) string {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return fallback
	}
	rest := strings.TrimPrefix(text, "---\n")
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return fallback
	}
	var metadata struct {
		Name string `yaml:"name"`
	}
	if yaml.Unmarshal([]byte(rest[:end]), &metadata) != nil || strings.TrimSpace(metadata.Name) == "" {
		return fallback
	}
	return strings.TrimSpace(metadata.Name)
}
