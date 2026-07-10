package handler

import (
	"archive/zip"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	skillservice "lazymind/core/skillv2/service"
)

type builtinSkillManifest struct {
	UID      string
	Category string
	DirName  string
}

type builtinSkillPackage struct {
	UID         string
	Category    string
	Name        string
	Description string
	Files       map[string][]byte
}

type builtinSkillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

var builtinSkillManifests = []builtinSkillManifest{
	{UID: "bsk_01JZ7Q3YF6Q2Z4HM9V8K7D1R3P", Category: "research", DirName: "deep-research"},
	{UID: "bsk_01JZ7Q4AJ1X9N5B2C8M6T0W3EY", Category: "review", DirName: "single-document-review"},
	{UID: "bsk_01JZ7Q4RPN6K3Y8V1D5H2A9S0B", Category: "review", DirName: "systematic-document-and-literature-review"},
	{UID: "bsk_01JZ7Q58M4E7C2N9X6P1D3V0KA", Category: "search", DirName: "paper-search"},
	{UID: "bsk_01K0M8SCV7PAPERSEARCH9Q2X3A4B", Category: "search", DirName: "sciverse-paper-search"},
}

func EnableBuiltinSkill(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, userName, ok := requireUser(w, r)
	if !ok {
		return
	}
	uid := strings.TrimSpace(common.PathVar(r, "builtin_skill_uid"))
	if uid == "" {
		replyError(w, "missing builtin_skill_uid", http.StatusBadRequest)
		return
	}

	var existing orm.SkillV2Skill
	err := db.WithContext(r.Context()).
		Where("owner_user_id = ? AND origin_builtin_skill_uid = ? AND deleted_at IS NULL", userID, uid).
		Order("created_at ASC").
		Take(&existing).Error
	if err == nil {
		detail, detailErr := newSkillService(db).GetSkill(r.Context(), skillservice.GetSkillRequest{SkillID: existing.ID, UserID: userID})
		if detailErr != nil {
			replyServiceError(w, detailErr)
			return
		}
		common.ReplyOK(w, skillDetailDTO(detail))
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		replyServiceError(w, err)
		return
	}

	pkg, found, err := builtinSkillByUID(uid)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	if !found {
		replyError(w, "builtin skill not found", http.StatusNotFound)
		return
	}
	zipPath, err := writeSkillPackageZip(pkg.Files)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	defer os.Remove(zipPath)

	resp, err := newSkillService(db).CreateSkill(r.Context(), skillservice.CreateSkillRequest{
		OwnerUserID:           userID,
		OwnerUserName:         userName,
		CreateUserID:          userID,
		CreateUserName:        userName,
		Name:                  pkg.Name,
		Category:              pkg.Category,
		OriginBuiltinSkillUID: pkg.UID,
		Description:           pkg.Description,
		IsEnabled:             boolPtr(true),
		Source: skillservice.SourceInput{
			Type:       "local_zip",
			StoredPath: zipPath,
			Filename:   pkg.UID + ".zip",
		},
	})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	detail, err := newSkillService(db).GetSkill(r.Context(), skillservice.GetSkillRequest{SkillID: resp.SkillID, UserID: userID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, skillDetailDTO(detail))
}

func builtinSkillByUID(uid string) (builtinSkillPackage, bool, error) {
	for _, manifest := range builtinSkillManifests {
		if manifest.UID != uid {
			continue
		}
		pkg, err := loadBuiltinSkillPackage(manifest)
		return pkg, err == nil, err
	}
	return builtinSkillPackage{}, false, nil
}

func loadBuiltinSkillPackage(manifest builtinSkillManifest) (builtinSkillPackage, error) {
	root := builtinSkillsRoot()
	if root == "" {
		return builtinSkillPackage{}, fmt.Errorf("builtin skills root not found")
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
		return builtinSkillPackage{}, err
	}
	skillMD, ok := files["SKILL.md"]
	if !ok {
		return builtinSkillPackage{}, fmt.Errorf("builtin skill %s missing SKILL.md", manifest.UID)
	}
	name, description := parseBuiltinSkillMDMetadata(string(skillMD))
	if name == "" {
		name = manifest.DirName
	}
	return builtinSkillPackage{
		UID:         manifest.UID,
		Category:    manifest.Category,
		Name:        name,
		Description: description,
		Files:       files,
	}, nil
}

func builtinSkillsRoot() string {
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

func parseBuiltinSkillMDMetadata(content string) (string, string) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return "", ""
	}
	rest := strings.TrimPrefix(content, "---\n")
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return "", ""
	}
	var meta builtinSkillFrontmatter
	if err := yaml.Unmarshal([]byte(rest[:idx]), &meta); err != nil {
		return "", ""
	}
	return strings.TrimSpace(meta.Name), strings.TrimSpace(meta.Description)
}

func writeSkillPackageZip(files map[string][]byte) (string, error) {
	f, err := os.CreateTemp("", "lazymind-builtin-skill-*.zip")
	if err != nil {
		return "", err
	}
	cleanup := func(closeErr error) (string, error) {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", closeErr
	}
	zipWriter := zip.NewWriter(f)
	paths := make([]string, 0, len(files))
	for filePath := range files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	for _, filePath := range paths {
		entry, err := zipWriter.Create(filePath)
		if err != nil {
			_ = zipWriter.Close()
			return cleanup(err)
		}
		if _, err := entry.Write(files[filePath]); err != nil {
			_ = zipWriter.Close()
			return cleanup(err)
		}
	}
	if err := zipWriter.Close(); err != nil {
		return cleanup(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func boolPtr(value bool) *bool {
	return &value
}
