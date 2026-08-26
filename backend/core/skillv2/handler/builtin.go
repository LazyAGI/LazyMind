package handler

import (
	"archive/zip"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	skillbuiltin "lazymind/core/skillv2/builtin"
	skillservice "lazymind/core/skillv2/service"
)

// ListBuiltinSkills returns the immutable skill templates shipped with LazyMind.
// A template only becomes an editable user skill after EnableBuiltinSkill copies
// its complete package into the user's skill store.
func ListBuiltinSkills(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID := strings.TrimSpace(common.UserID(r))
	var rows []orm.SkillV2Skill
	if userID != "" {
		if err := db.WithContext(r.Context()).
			Where("owner_user_id = ? AND deleted_at IS NULL", userID).
			Order("created_at ASC").Find(&rows).Error; err != nil {
			replyServiceError(w, err)
			return
		}
	}

	packages, err := skillbuiltin.Packages()
	if err != nil {
		replyServiceError(w, err)
		return
	}
	packages = visibleBuiltinPackages(packages)
	items := make([]map[string]any, 0, len(packages))
	for _, pkg := range packages {
		installedID := matchingInstalledSkillID(rows, pkg)
		items = append(items, map[string]any{
			"builtin_skill_uid":  pkg.UID,
			"name":               pkg.Name,
			"description":        pkg.Description,
			"category":           pkg.Category,
			"tags":               pkg.Tags,
			"version":            pkg.Version,
			"content":            string(pkg.Files["SKILL.md"]),
			"installed":          installedID != "",
			"installed_skill_id": installedID,
		})
	}
	common.ReplyOK(w, map[string]any{"items": items, "total": len(items)})
}

func visibleBuiltinPackages(packages []skillbuiltin.Package) []skillbuiltin.Package {
	visible := make([]skillbuiltin.Package, 0, len(packages))
	for _, pkg := range packages {
		if pkg.MarketVisible {
			visible = append(visible, pkg)
		}
	}
	return visible
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
		replyBuiltinSkillDetail(w, r, newSkillService(db), existing.ID, userID)
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		replyServiceError(w, err)
		return
	}
	var trashed orm.SkillV2Skill
	err = db.WithContext(r.Context()).
		Where("owner_user_id = ? AND origin_builtin_skill_uid = ? AND deleted_at IS NOT NULL", userID, uid).
		Order("deleted_at DESC, created_at ASC").
		Take(&trashed).Error
	if err == nil {
		service := newSkillService(db)
		if err := service.RestoreSkill(r.Context(), skillservice.RestoreSkillRequest{SkillID: trashed.ID, UserID: userID}); err != nil {
			replyServiceError(w, err)
			return
		}
		replyBuiltinSkillDetail(w, r, service, trashed.ID, userID)
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		replyServiceError(w, err)
		return
	}

	pkg, found, err := skillbuiltin.PackageByUID(uid)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	if !found {
		replyError(w, "builtin skill not found", http.StatusNotFound)
		return
	}
	if claimedID, claimed, err := claimLegacyBuiltinInstall(r, db, userID, pkg); err != nil {
		replyServiceError(w, err)
		return
	} else if claimed {
		replyBuiltinSkillDetail(w, r, newSkillService(db), claimedID, userID)
		return
	}
	source := skillservice.SourceInput{}
	if pkg.ArchivePath != "" {
		source = skillservice.SourceInput{
			Type:       "builtin_zip",
			StoredPath: pkg.ArchivePath,
			Filename:   fmt.Sprintf("%s@%s#%s", pkg.UID, pkg.Version, pkg.SHA256),
		}
	} else {
		zipPath, err := writeSkillPackageZip(pkg.Files)
		if err != nil {
			replyServiceError(w, err)
			return
		}
		defer os.Remove(zipPath)
		source = skillservice.SourceInput{Type: "local_zip", StoredPath: zipPath, Filename: pkg.UID + ".zip"}
	}

	service := newSkillService(db)
	resp, err := service.CreateSkill(r.Context(), skillservice.CreateSkillRequest{
		OwnerUserID:           userID,
		OwnerUserName:         userName,
		CreateUserID:          userID,
		CreateUserName:        userName,
		Name:                  pkg.Name,
		Category:              pkg.Category,
		OriginBuiltinSkillUID: pkg.UID,
		Description:           pkg.Description,
		IsEnabled:             boolPtr(true),
		Source:                source,
	})
	if err != nil {
		if existingID := installedBuiltinSkillID(r, db, userID, uid); existingID != "" {
			replyBuiltinSkillDetail(w, r, service, existingID, userID)
			return
		}
		if claimedID, claimed, claimErr := claimLegacyBuiltinInstall(r, db, userID, pkg); claimErr != nil {
			replyServiceError(w, claimErr)
			return
		} else if claimed {
			replyBuiltinSkillDetail(w, r, service, claimedID, userID)
			return
		}
		replyServiceError(w, err)
		return
	}
	replyBuiltinSkillDetail(w, r, service, resp.SkillID, userID)
}

func claimLegacyBuiltinInstall(r *http.Request, db *gorm.DB, userID string, pkg skillbuiltin.Package) (string, bool, error) {
	if strings.TrimSpace(pkg.Category) == "" || strings.TrimSpace(pkg.Name) == "" {
		return "", false, nil
	}
	var rows []orm.SkillV2Skill
	if err := db.WithContext(r.Context()).
		Where("owner_user_id = ? AND deleted_at IS NULL", userID).
		Order("created_at ASC").
		Find(&rows).Error; err != nil {
		return "", false, err
	}
	existing, ok := matchingLegacyInstalledSkill(rows, pkg)
	if !ok {
		return "", false, nil
	}
	claimed, err := markBuiltinInstallClaimed(r, db, userID, existing, pkg)
	if err != nil {
		return "", false, err
	}
	return existing.ID, claimed, nil
}

func matchingInstalledSkillID(rows []orm.SkillV2Skill, pkg skillbuiltin.Package) string {
	uid := strings.TrimSpace(pkg.UID)
	if uid != "" {
		for _, row := range rows {
			if strings.TrimSpace(row.OriginBuiltinSkillUID) == uid {
				return row.ID
			}
		}
	}
	if row, ok := matchingLegacyInstalledSkill(rows, pkg); ok {
		return row.ID
	}
	return ""
}

func matchingLegacyInstalledSkill(rows []orm.SkillV2Skill, pkg skillbuiltin.Package) (orm.SkillV2Skill, bool) {
	category := strings.TrimSpace(pkg.Category)
	name := strings.TrimSpace(pkg.Name)
	if category == "" || name == "" {
		return orm.SkillV2Skill{}, false
	}
	relativeRoot := path.Join(category, name)
	for _, row := range rows {
		if strings.TrimSpace(row.RelativeRoot) == relativeRoot {
			return row, true
		}
	}
	for _, row := range rows {
		if strings.TrimSpace(row.Category) == category && strings.TrimSpace(row.SkillName) == name {
			return row, true
		}
	}
	return orm.SkillV2Skill{}, false
}

func markBuiltinInstallClaimed(r *http.Request, db *gorm.DB, userID string, existing orm.SkillV2Skill, pkg skillbuiltin.Package) (bool, error) {
	updates := map[string]any{
		"is_enabled":    true,
		"update_status": "up_to_date",
		"updated_at":    time.Now(),
	}
	if strings.TrimSpace(existing.OriginBuiltinSkillUID) == "" {
		updates["origin_builtin_skill_uid"] = pkg.UID
	}
	if strings.TrimSpace(pkg.Description) != "" {
		updates["description"] = pkg.Description
	}
	result := db.WithContext(r.Context()).
		Model(&orm.SkillV2Skill{}).
		Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", existing.ID, userID).
		Updates(updates)
	if result.Error != nil {
		return false, result.Error
	}
	return true, nil
}

func installedBuiltinSkillID(r *http.Request, db *gorm.DB, userID, uid string) string {
	var existing orm.SkillV2Skill
	if err := db.WithContext(r.Context()).Select("id").
		Where("owner_user_id = ? AND origin_builtin_skill_uid = ? AND deleted_at IS NULL", userID, uid).
		Order("created_at ASC").Take(&existing).Error; err != nil {
		return ""
	}
	return existing.ID
}

func replyBuiltinSkillDetail(w http.ResponseWriter, r *http.Request, service *skillservice.SkillService, skillID, userID string) {
	detail, err := service.GetSkill(r.Context(), skillservice.GetSkillRequest{SkillID: skillID, UserID: userID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	if !detail.IsEnabled {
		enabled := true
		if _, err := service.PatchSkill(r.Context(), skillservice.PatchSkillRequest{SkillID: skillID, UserID: userID, IsEnabled: &enabled}); err != nil {
			replyServiceError(w, err)
			return
		}
		detail, err = service.GetSkill(r.Context(), skillservice.GetSkillRequest{SkillID: skillID, UserID: userID})
		if err != nil {
			replyServiceError(w, err)
			return
		}
	}
	common.ReplyOK(w, skillDetailDTO(detail))
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
