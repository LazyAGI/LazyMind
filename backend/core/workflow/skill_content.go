package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"gorm.io/gorm"
	skillbuiltin "lazymind/core/skillv2/builtin"
)

const builtinSkillIDPrefix = "builtin:"

var errWorkflowSourceSkillNotFound = errors.New("plugin source skill not found")

type skillPackageFile struct {
	Path     string `json:"path"`
	BlobHash string `json:"blob_hash,omitempty"`
	Size     int64  `json:"size"`
	Mime     string `json:"mime,omitempty"`
	FileType string `json:"file_type,omitempty"`
	Binary   bool   `json:"binary"`
	Content  string `json:"content,omitempty"`
}

type workflowSourceSkillSnapshot struct {
	SkillID    string             `json:"skill_id"`
	Name       string             `json:"name"`
	RevisionID string             `json:"revision_id"`
	RevisionNo int64              `json:"revision_no"`
	TreeHash   string             `json:"tree_hash"`
	Files      []skillPackageFile `json:"files"`
}

func (s workflowSourceSkillSnapshot) skillMD() string {
	for _, file := range s.Files {
		if file.Path == "SKILL.md" {
			return file.Content
		}
	}
	return ""
}

func isWorkflowSourceSkillNotFound(err error) bool {
	return errors.Is(err, errWorkflowSourceSkillNotFound) || errors.Is(err, gorm.ErrRecordNotFound)
}

// loadWorkflowSourceSkill reads normal skills from the v2 revision store and
// resolves immutable templates through the shared builtin package catalog.
func loadWorkflowSourceSkill(ctx context.Context, db *gorm.DB, userID, skillID string) (workflowSourceSkillSnapshot, error) {
	if strings.HasPrefix(skillID, builtinSkillIDPrefix) {
		return loadWorkflowBuiltinSkillPackage(skillID)
	}

	var skill struct {
		SkillName      string
		HeadRevisionID *string
	}
	if err := db.WithContext(ctx).Table("skills").Select("skill_name, head_revision_id").Where("id=? AND owner_user_id=? AND deleted_at IS NULL", skillID, userID).Take(&skill).Error; err != nil {
		return workflowSourceSkillSnapshot{}, err
	}
	if skill.HeadRevisionID == nil || *skill.HeadRevisionID == "" {
		return workflowSourceSkillSnapshot{}, errWorkflowSourceSkillNotFound
	}
	return loadWorkflowSourceSkillRevision(ctx, db, userID, skillID, *skill.HeadRevisionID)
}

func loadWorkflowSourceSkillRevision(ctx context.Context, db *gorm.DB, userID, skillID, revisionID string) (workflowSourceSkillSnapshot, error) {
	var skill struct{ SkillName string }
	if err := db.WithContext(ctx).Table("skills").Select("skill_name").Where("id=? AND owner_user_id=? AND deleted_at IS NULL", skillID, userID).Take(&skill).Error; err != nil {
		return workflowSourceSkillSnapshot{}, err
	}
	var revision struct {
		ID         string
		RevisionNo int64
		TreeHash   string
	}
	if err := db.WithContext(ctx).Table("skill_revisions").Select("id, revision_no, tree_hash").
		Where("id = ? AND skill_id = ?", revisionID, skillID).Take(&revision).Error; err != nil {
		return workflowSourceSkillSnapshot{}, err
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
		return workflowSourceSkillSnapshot{}, err
	}
	snapshot := workflowSourceSkillSnapshot{SkillID: skillID, Name: skill.SkillName, RevisionID: revision.ID, RevisionNo: revision.RevisionNo, TreeHash: revision.TreeHash}
	for _, entry := range entries {
		file := skillPackageFile{Path: entry.Path, Size: entry.Size, Mime: entry.Mime, FileType: entry.FileType, Binary: entry.Binary}
		if entry.BlobHash != nil {
			file.BlobHash = *entry.BlobHash
			if !entry.Binary {
				var blob struct{ Content []byte }
				if err := db.WithContext(ctx).Table("skill_blobs").Select("content").Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
					return workflowSourceSkillSnapshot{}, err
				}
				file.Content = string(blob.Content)
			}
		}
		snapshot.Files = append(snapshot.Files, file)
	}
	if strings.TrimSpace(snapshot.skillMD()) == "" {
		return workflowSourceSkillSnapshot{}, errWorkflowSourceSkillNotFound
	}
	return snapshot, nil
}

func loadWorkflowBuiltinSkillPackage(templateID string) (workflowSourceSkillSnapshot, error) {
	id := strings.TrimPrefix(templateID, builtinSkillIDPrefix)
	uid := strings.SplitN(id, ":", 2)[0]
	pkg, found, err := skillbuiltin.PackageByUID(uid)
	if err != nil {
		return workflowSourceSkillSnapshot{}, err
	}
	if !found {
		return workflowSourceSkillSnapshot{}, errWorkflowSourceSkillNotFound
	}
	snapshot := workflowSourceSkillSnapshot{SkillID: templateID, Name: pkg.Name, RevisionID: "builtin:" + uid}
	for filePath, data := range pkg.Files {
		snapshot.Files = append(snapshot.Files, skillPackageFile{Path: filePath, Size: int64(len(data)), Content: string(data)})
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

func loadWorkflowBuiltinSkill(templateID string) (string, string, error) {
	content, name, found, err := skillbuiltin.SkillContent(templateID)
	if err != nil {
		return "", "", err
	}
	if !found {
		return "", "", errWorkflowSourceSkillNotFound
	}
	return content, name, nil
}
