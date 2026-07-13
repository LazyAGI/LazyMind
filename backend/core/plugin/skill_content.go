package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

const builtinSkillIDPrefix = "builtin:"

var errPluginSourceSkillNotFound = errors.New("plugin source skill not found")

type pluginBuiltinSkillManifest struct {
	UID      string
	Category string
	DirName  string
}

type skillPackageFile struct {
	Path     string `json:"path"`
	BlobHash string `json:"blob_hash,omitempty"`
	Size     int64  `json:"size"`
	Mime     string `json:"mime,omitempty"`
	FileType string `json:"file_type,omitempty"`
	Binary   bool   `json:"binary"`
	Content  string `json:"content,omitempty"`
}

type pluginSourceSkillSnapshot struct {
	SkillID    string             `json:"skill_id"`
	Name       string             `json:"name"`
	RevisionID string             `json:"revision_id"`
	RevisionNo int64              `json:"revision_no"`
	TreeHash   string             `json:"tree_hash"`
	Files      []skillPackageFile `json:"files"`
}

func (s pluginSourceSkillSnapshot) skillMD() string {
	for _, file := range s.Files {
		if file.Path == "SKILL.md" {
			return file.Content
		}
	}
	return ""
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
func loadPluginSourceSkill(ctx context.Context, db *gorm.DB, userID, skillID string) (pluginSourceSkillSnapshot, error) {
	if strings.HasPrefix(skillID, builtinSkillIDPrefix) {
		return loadPluginBuiltinSkillPackage(skillID)
	}

	var skill struct {
		SkillName      string
		HeadRevisionID *string
	}
	if err := db.WithContext(ctx).Table("skills").Select("skill_name, head_revision_id").Where("id=? AND owner_user_id=? AND deleted_at IS NULL", skillID, userID).Take(&skill).Error; err != nil {
		return pluginSourceSkillSnapshot{}, err
	}
	if skill.HeadRevisionID == nil || *skill.HeadRevisionID == "" {
		return pluginSourceSkillSnapshot{}, errPluginSourceSkillNotFound
	}
	return loadPluginSourceSkillRevision(ctx, db, userID, skillID, *skill.HeadRevisionID)
}

func loadPluginSourceSkillRevision(ctx context.Context, db *gorm.DB, userID, skillID, revisionID string) (pluginSourceSkillSnapshot, error) {
	var skill struct{ SkillName string }
	if err := db.WithContext(ctx).Table("skills").Select("skill_name").Where("id=? AND owner_user_id=? AND deleted_at IS NULL", skillID, userID).Take(&skill).Error; err != nil {
		return pluginSourceSkillSnapshot{}, err
	}
	var revision struct {
		ID         string
		RevisionNo int64
		TreeHash   string
	}
	if err := db.WithContext(ctx).Table("skill_revisions").Select("id, revision_no, tree_hash").
		Where("id = ? AND skill_id = ?", revisionID, skillID).Take(&revision).Error; err != nil {
		return pluginSourceSkillSnapshot{}, err
	}
	var entries []struct {
		Path           string
		BlobHash       *string
		Size           int64
		Mime, FileType string
		Binary         bool `gorm:"column:binary"`
	}
	if err := db.WithContext(ctx).Table("skill_revision_entries").
		Select(`path, blob_hash, size, mime, file_type, "binary"`).
		Where("revision_id = ? AND entry_type = ?", revision.ID, "file").Order("path ASC").Scan(&entries).Error; err != nil {
		return pluginSourceSkillSnapshot{}, err
	}
	snapshot := pluginSourceSkillSnapshot{SkillID: skillID, Name: skill.SkillName, RevisionID: revision.ID, RevisionNo: revision.RevisionNo, TreeHash: revision.TreeHash}
	for _, entry := range entries {
		file := skillPackageFile{Path: entry.Path, Size: entry.Size, Mime: entry.Mime, FileType: entry.FileType, Binary: entry.Binary}
		if entry.BlobHash != nil {
			file.BlobHash = *entry.BlobHash
			if !entry.Binary {
				var blob struct{ Content []byte }
				if err := db.WithContext(ctx).Table("skill_blobs").Select("content").Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
					return pluginSourceSkillSnapshot{}, err
				}
				file.Content = string(blob.Content)
			}
		}
		snapshot.Files = append(snapshot.Files, file)
	}
	if strings.TrimSpace(snapshot.skillMD()) == "" {
		return pluginSourceSkillSnapshot{}, errPluginSourceSkillNotFound
	}
	return snapshot, nil
}

func loadPluginBuiltinSkillPackage(templateID string) (pluginSourceSkillSnapshot, error) {
	content, name, err := loadPluginBuiltinSkill(templateID)
	if err != nil {
		return pluginSourceSkillSnapshot{}, err
	}
	id := strings.TrimPrefix(templateID, builtinSkillIDPrefix)
	uid := strings.SplitN(id, ":", 2)[0]
	manifest, ok := pluginBuiltinManifest(uid)
	if !ok {
		return pluginSourceSkillSnapshot{}, errPluginSourceSkillNotFound
	}
	base := filepath.Join(pluginBuiltinSkillsRoot(), manifest.Category, manifest.DirName)
	snapshot := pluginSourceSkillSnapshot{SkillID: templateID, Name: name, RevisionID: "builtin:" + uid}
	err = filepath.Walk(base, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(base, path)
		if relErr != nil {
			return relErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		snapshot.Files = append(snapshot.Files, skillPackageFile{Path: filepath.ToSlash(rel), Size: info.Size(), Content: string(data)})
		return nil
	})
	if err != nil {
		return pluginSourceSkillSnapshot{}, err
	}
	if len(snapshot.Files) == 0 {
		snapshot.Files = []skillPackageFile{{Path: "SKILL.md", Content: content, Size: int64(len(content))}}
	}
	sort.Slice(snapshot.Files, func(i, j int) bool { return snapshot.Files[i].Path < snapshot.Files[j].Path })
	var treeLines []string
	for i := range snapshot.Files {
		sum := sha256.Sum256([]byte(snapshot.Files[i].Content))
		snapshot.Files[i].BlobHash = hex.EncodeToString(sum[:])
		treeLines = append(treeLines, snapshot.Files[i].Path+"\x00"+snapshot.Files[i].BlobHash)
	}
	tree := sha256.Sum256([]byte(strings.Join(treeLines, "\n")))
	snapshot.TreeHash = hex.EncodeToString(tree[:])
	return snapshot, nil
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
