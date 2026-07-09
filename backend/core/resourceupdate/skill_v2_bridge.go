package resourceupdate

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	skillservice "lazymind/core/skillv2/service"
)

func skillV2TablesReady(db *gorm.DB) bool {
	if db == nil {
		return false
	}
	return db.Migrator().HasTable(&orm.SkillV2Skill{}) &&
		db.Migrator().HasTable(&orm.SkillV2Revision{}) &&
		db.Migrator().HasTable(&orm.SkillV2RevisionEntry{}) &&
		db.Migrator().HasTable(&orm.SkillV2Draft{})
}

func mapSkillPatchResultToV2Resource(ctx context.Context, db *gorm.DB, result SkillReviewResult) (orm.SkillV2Skill, error) {
	if !skillV2TablesReady(db) {
		return orm.SkillV2Skill{}, gorm.ErrRecordNotFound
	}
	var row orm.SkillV2Skill
	err := db.WithContext(ctx).
		Where("owner_user_id = ? AND skill_name = ? AND created_at <= ?",
			strings.TrimSpace(result.UserID), strings.TrimSpace(result.SkillName), result.Time).
		Order("created_at DESC").
		Take(&row).Error
	return row, err
}

func skillV2ResourceByID(ctx context.Context, db *gorm.DB, userID, resourceID string) (orm.SkillV2Skill, error) {
	if !skillV2TablesReady(db) {
		return orm.SkillV2Skill{}, gorm.ErrRecordNotFound
	}
	var row orm.SkillV2Skill
	err := db.WithContext(ctx).
		Where("id = ? AND owner_user_id = ?", strings.TrimSpace(resourceID), strings.TrimSpace(userID)).
		Take(&row).Error
	return row, err
}

func applySkillV2PatchResult(ctx context.Context, tx *gorm.DB, result SkillReviewResult, resource orm.SkillV2Skill) error {
	content := strings.TrimSpace(result.SkillContent)
	meta, err := validateSkillReviewContent(result.SkillName, content)
	if err != nil {
		return err
	}
	if strings.TrimSpace(meta.Category) == "" {
		meta.Category = strings.TrimSpace(resource.Category)
	}
	_, err = newSkillV2Service(tx).AcceptReview(ctx, skillservice.AcceptReviewRequest{
		SkillID:     resource.ID,
		UserID:      strings.TrimSpace(result.UserID),
		ReviewID:    strings.TrimSpace(result.ID),
		Name:        strings.TrimSpace(meta.Name),
		Category:    strings.TrimSpace(meta.Category),
		Description: strings.TrimSpace(meta.Description),
		Files:       map[string][]byte{"SKILL.md": []byte(content)},
	})
	if err != nil {
		return err
	}
	return updateSkillReviewStatus(ctx, tx, result.ID, reviewStatusAccepted)
}

func createSkillV2FromNewResult(ctx context.Context, tx *gorm.DB, result SkillReviewResult, userName string) (orm.SkillV2Skill, error) {
	if !skillV2TablesReady(tx) {
		return orm.SkillV2Skill{}, gorm.ErrRecordNotFound
	}
	content := strings.TrimSpace(result.SkillContent)
	meta, err := validateSkillReviewContent(result.SkillName, content)
	if err != nil {
		return orm.SkillV2Skill{}, err
	}
	category := strings.TrimSpace(meta.Category)
	if category == "" {
		category = "system"
	}
	if err := validatePathSegment(category); err != nil {
		return orm.SkillV2Skill{}, err
	}
	if err := validatePathSegment(meta.Name); err != nil {
		return orm.SkillV2Skill{}, err
	}
	zipPath, err := writeSkillV2InlineZip(content)
	if err != nil {
		return orm.SkillV2Skill{}, err
	}
	defer os.Remove(zipPath)
	resp, err := newSkillV2Service(tx).CreateSkill(ctx, skillservice.CreateSkillRequest{
		OwnerUserID:    strings.TrimSpace(result.UserID),
		OwnerUserName:  strings.TrimSpace(userName),
		CreateUserID:   strings.TrimSpace(result.UserID),
		CreateUserName: strings.TrimSpace(userName),
		Name:           strings.TrimSpace(meta.Name),
		Category:       category,
		Description:    strings.TrimSpace(meta.Description),
		Source: skillservice.SourceInput{
			Type:       "local_zip",
			StoredPath: zipPath,
			Filename:   "skill-review.zip",
		},
	})
	if err != nil {
		return orm.SkillV2Skill{}, err
	}
	var row orm.SkillV2Skill
	if err := tx.WithContext(ctx).Where("id = ?", resp.SkillID).Take(&row).Error; err != nil {
		return orm.SkillV2Skill{}, err
	}
	return row, nil
}

func skillV2CurrentContent(ctx context.Context, db *gorm.DB, userID, skillName string) (string, bool, error) {
	if !skillV2TablesReady(db) {
		return "", false, nil
	}
	var row orm.SkillV2Skill
	if err := db.WithContext(ctx).
		Where("owner_user_id = ? AND skill_name = ?", strings.TrimSpace(userID), strings.TrimSpace(skillName)).
		Order("created_at DESC").
		Take(&row).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", false, nil
		}
		return "", false, err
	}
	file, err := newSkillV2Service(db).ReadFile(ctx, skillservice.FileRef{SkillID: row.ID, RefType: "head", Path: "SKILL.md"})
	if err != nil {
		return "", false, err
	}
	return file.Content, true, nil
}

func newSkillV2Service(db *gorm.DB) *skillservice.SkillService {
	root := strings.TrimSpace(os.Getenv("LAZYMIND_SKILL_OBJECT_ROOT"))
	if root == "" {
		root = filepath.Join(uploadRootForSkillV2Bridge(), "skill-objects")
	}
	return skillservice.NewSkillService(skillservice.SkillServiceDeps{
		DB:        db,
		BlobStore: skillservice.NewBlobStore(db, skillservice.NewLocalObjectStore(root)),
	})
}

func uploadRootForSkillV2Bridge() string {
	if v := strings.TrimSpace(os.Getenv("LAZYMIND_UPLOAD_ROOT")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "/var/lib/lazymind/uploads"
}

func writeSkillV2InlineZip(content string) (string, error) {
	f, err := os.CreateTemp("", "lazymind-skill-review-*.zip")
	if err != nil {
		return "", err
	}
	cleanup := func(closeErr error) (string, error) {
		_ = f.Close()
		_ = os.Remove(f.Name())
		if closeErr != nil {
			return "", closeErr
		}
		return "", fmt.Errorf("write skill zip failed")
	}
	zw := zip.NewWriter(f)
	entry, err := zw.Create("SKILL.md")
	if err != nil {
		return cleanup(err)
	}
	if _, err := io.WriteString(entry, content); err != nil {
		return cleanup(err)
	}
	if err := zw.Close(); err != nil {
		return cleanup(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
