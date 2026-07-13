package builtin

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const TemplateIDPrefix = "builtin:"

type Manifest struct {
	UID      string
	Category string
	DirName  string
}

type Package struct {
	UID         string
	Category    string
	Name        string
	Description string
	Files       map[string][]byte
}

type skillMDFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

var Manifests = []Manifest{
	{UID: "bsk_01JZ7Q3YF6Q2Z4HM9V8K7D1R3P", Category: "research", DirName: "deep-research"},
	{UID: "bsk_01JZ7Q4AJ1X9N5B2C8M6T0W3EY", Category: "review", DirName: "single-document-review"},
	{UID: "bsk_01JZ7Q4RPN6K3Y8V1D5H2A9S0B", Category: "review", DirName: "systematic-document-and-literature-review"},
	{UID: "bsk_01JZ7Q58M4E7C2N9X6P1D3V0KA", Category: "search", DirName: "paper-search"},
	{UID: "bsk_01K0M8SCV7PAPERSEARCH9Q2X3A4B", Category: "search", DirName: "sciverse-paper-search"},
}

func TemplateID(uid string) string {
	return TemplateIDPrefix + strings.TrimSpace(uid)
}

func IsTemplateID(id string) bool {
	return strings.HasPrefix(id, TemplateIDPrefix)
}

func SkillContent(templateID string) (content, name string, ok bool, err error) {
	if !IsTemplateID(templateID) {
		return "", "", false, nil
	}
	uid := strings.TrimPrefix(templateID, TemplateIDPrefix)
	parentUID := uid
	relativePath := ""
	if idx := strings.IndexByte(uid, ':'); idx >= 0 {
		parentUID = uid[:idx]
		relativePath = uid[idx+1:]
	}
	pkg, found, err := PackageByUID(parentUID)
	if err != nil || !found {
		return "", "", false, err
	}
	if relativePath == "" {
		data, ok := pkg.Files["SKILL.md"]
		return string(data), pkg.Name, ok, nil
	}
	data, ok := pkg.Files[relativePath]
	if !ok {
		return "", "", false, nil
	}
	name = strings.TrimSuffix(relativePath, path.Ext(relativePath))
	return string(data), name, true, nil
}

func PackageByUID(uid string) (Package, bool, error) {
	uid = strings.TrimSpace(uid)
	for _, manifest := range Manifests {
		if manifest.UID != uid {
			continue
		}
		pkg, err := LoadPackage(manifest)
		return pkg, err == nil, err
	}
	return Package{}, false, nil
}

func LoadPackage(manifest Manifest) (Package, error) {
	root := Root()
	if root == "" {
		return Package{}, fmt.Errorf("builtin skills root not found")
	}
	skillDir := filepath.Join(root, manifest.Category, manifest.DirName)
	files := map[string][]byte{}
	if err := filepath.WalkDir(skillDir, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == skillDir {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(skillDir, filePath)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	}); err != nil {
		return Package{}, err
	}
	skillMD, ok := files["SKILL.md"]
	if !ok {
		return Package{}, fmt.Errorf("builtin skill %s missing SKILL.md", manifest.UID)
	}
	name, description := parseSkillMDMetadata(string(skillMD))
	if name == "" {
		name = manifest.DirName
	}
	return Package{
		UID:         manifest.UID,
		Category:    manifest.Category,
		Name:        name,
		Description: description,
		Files:       files,
	}, nil
}

func Root() string {
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
	candidates := []string{
		filepath.Join(wd, "skills"),
		filepath.Join(wd, "..", "skills"),
		filepath.Join(wd, "..", "..", "skills"),
		filepath.Join(wd, "..", "..", "..", "skills"),
	}
	for _, candidate := range candidates {
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

func parseSkillMDMetadata(content string) (string, string) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return "", ""
	}
	rest := strings.TrimPrefix(content, "---\n")
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return "", ""
	}
	var meta skillMDFrontmatter
	if err := yaml.Unmarshal([]byte(rest[:idx]), &meta); err != nil {
		return "", ""
	}
	return strings.TrimSpace(meta.Name), strings.TrimSpace(meta.Description)
}
