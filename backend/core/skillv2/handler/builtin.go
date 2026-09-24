package handler

import (
	"archive/zip"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

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
	packages, err := skillbuiltin.Packages()
	if err != nil {
		replyServiceError(w, err)
		return
	}
	packages = visibleBuiltinPackages(packages)

	userID := strings.TrimSpace(common.UserID(r))
	installed := map[string]string{}
	installedByIdentity := map[string]string{}
	if userID != "" {
		var rows []orm.SkillV2Skill
		if err := db.WithContext(r.Context()).
			Where("owner_user_id = ? AND origin_builtin_skill_uid <> '' AND deleted_at IS NULL", userID).
			Order("created_at ASC").Find(&rows).Error; err != nil {
			replyServiceError(w, err)
			return
		}
		for _, row := range rows {
			if _, exists := installed[row.OriginBuiltinSkillUID]; !exists {
				installed[row.OriginBuiltinSkillUID] = row.ID
			}
			identity := builtinSkillIdentity(row.Category, row.SkillName)
			if _, exists := installedByIdentity[identity]; !exists {
				installedByIdentity[identity] = row.ID
			}
		}
	}
	items := make([]map[string]any, 0, len(packages))
	for _, pkg := range packages {
		installedSkillID := installed[pkg.UID]
		if installedSkillID == "" {
			installedSkillID = installedByIdentity[builtinSkillIdentity(pkg.Category, pkg.Name)]
		}
		item := map[string]any{
			"builtin_skill_uid":  pkg.UID,
			"name":               pkg.Name,
			"description":        pkg.Description,
			"category":           pkg.Category,
			"tags":               pkg.Tags,
			"version":            pkg.Version,
			"content":            string(pkg.Files["SKILL.md"]),
			"installed":          installedSkillID != "",
			"installed_skill_id": installedSkillID,
		}
		if pkg.Provider != "" {
			item["provider"] = pkg.Provider
		}
		items = append(items, item)
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
	service := newSkillService(db)
	if compatible, found, err := installedCompatibleBuiltinSkill(r, db, userID, pkg); err != nil {
		replyServiceError(w, err)
		return
	} else if found {
		if _, err := syncInstalledBuiltinSkill(r, service, compatible.ID, userID, pkg); err != nil {
			replyServiceError(w, err)
			return
		}
		replyBuiltinSkillDetail(w, r, service, compatible.ID, userID)
		return
	}

	source, cleanup, err := sourceForBuiltinPackage(pkg)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	defer cleanup()
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
		Distribution: &skillservice.DistributionSource{
			BuiltinUID: pkg.UID, Version: pkg.Version, ArchiveSHA256: pkg.SHA256, TreeSHA256: pkg.TreeSHA256,
		},
	})
	if err != nil {
		if existingID := installedBuiltinSkillID(r, db, userID, uid); existingID != "" {
			replyBuiltinSkillDetail(w, r, service, existingID, userID)
			return
		}
		if compatible, found, findErr := installedCompatibleBuiltinSkill(r, db, userID, pkg); findErr != nil {
			replyServiceError(w, findErr)
			return
		} else if found {
			if _, syncErr := syncInstalledBuiltinSkill(r, service, compatible.ID, userID, pkg); syncErr != nil {
				replyServiceError(w, syncErr)
				return
			}
			replyBuiltinSkillDetail(w, r, service, compatible.ID, userID)
			return
		}
		replyServiceError(w, err)
		return
	}
	replyBuiltinSkillDetail(w, r, service, resp.SkillID, userID)
}

func builtinSkillIdentity(category, name string) string {
	return strings.TrimSpace(category) + "\x00" + strings.TrimSpace(name)
}

func installedCompatibleBuiltinSkill(r *http.Request, db *gorm.DB, userID string, pkg skillbuiltin.Package) (orm.SkillV2Skill, bool, error) {
	var existing orm.SkillV2Skill
	err := db.WithContext(r.Context()).
		Where("owner_user_id = ? AND category = ? AND skill_name = ? AND origin_builtin_skill_uid <> '' AND deleted_at IS NULL", userID, pkg.Category, pkg.Name).
		Order("created_at ASC").
		Take(&existing).Error
	if err == nil {
		return existing, true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return orm.SkillV2Skill{}, false, nil
	}
	return orm.SkillV2Skill{}, false, err
}

func syncInstalledBuiltinSkill(r *http.Request, service *skillservice.SkillService, skillID, userID string, pkg skillbuiltin.Package) (skillservice.PatchSkillResponse, error) {
	source, cleanup, err := sourceForBuiltinPackage(pkg)
	if err != nil {
		return skillservice.PatchSkillResponse{}, err
	}
	defer cleanup()

	enabled := true
	uid := pkg.UID
	return service.PatchSkill(r.Context(), skillservice.PatchSkillRequest{
		SkillID:               skillID,
		UserID:                userID,
		Name:                  &pkg.Name,
		Category:              &pkg.Category,
		Description:           &pkg.Description,
		IsEnabled:             &enabled,
		Source:                &source,
		OriginBuiltinSkillUID: &uid,
		Distribution: &skillservice.DistributionSource{
			BuiltinUID: pkg.UID, Version: pkg.Version, ArchiveSHA256: pkg.SHA256, TreeSHA256: pkg.TreeSHA256,
		},
	})
}

func sourceForBuiltinPackage(pkg skillbuiltin.Package) (skillservice.SourceInput, func(), error) {
	if pkg.ArchivePath != "" {
		return skillservice.SourceInput{
			Type:       "builtin_zip",
			StoredPath: pkg.ArchivePath,
			Filename:   fmt.Sprintf("%s@%s#%s", pkg.UID, pkg.Version, pkg.SHA256),
		}, func() {}, nil
	}
	zipPath, err := writeSkillPackageZip(pkg.Files)
	if err != nil {
		return skillservice.SourceInput{}, func() {}, err
	}
	return skillservice.SourceInput{Type: "local_zip", StoredPath: zipPath, Filename: pkg.UID + ".zip"}, func() { _ = os.Remove(zipPath) }, nil
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
