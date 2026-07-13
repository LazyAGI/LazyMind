package handler

import (
	"archive/zip"
	"errors"
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
	userID := strings.TrimSpace(common.UserID(r))
	installed := map[string]string{}
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
		}
	}

	items := make([]map[string]any, 0, len(skillbuiltin.Manifests))
	for _, manifest := range skillbuiltin.Manifests {
		pkg, err := skillbuiltin.LoadPackage(manifest)
		if err != nil {
			replyServiceError(w, err)
			return
		}
		items = append(items, map[string]any{
			"builtin_skill_uid":  pkg.UID,
			"name":               pkg.Name,
			"description":        pkg.Description,
			"category":           pkg.Category,
			"content":            string(pkg.Files["SKILL.md"]),
			"installed":          installed[pkg.UID] != "",
			"installed_skill_id": installed[pkg.UID],
		})
	}
	common.ReplyOK(w, map[string]any{"items": items, "total": len(items)})
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

	pkg, found, err := skillbuiltin.PackageByUID(uid)
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
