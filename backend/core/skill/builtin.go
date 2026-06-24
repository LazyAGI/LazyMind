package skill

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	appLog "lazymind/core/log"
	"lazymind/core/resourcechange"
	"lazymind/core/store"
)

type builtinSkillManifest struct {
	UID      string
	Category string
	DirName  string
}

type builtinSkill struct {
	UID         string
	Category    string
	Name        string
	Description string
	Tags        []string
	Content     string
	Children    []builtinSkillFile
}

type builtinSkillFile struct {
	Name         string
	Description  string
	RelativePath string
	FileExt      string
	Content      string
}

const builtinSkillIDPrefix = "builtin:"

var builtinSkillManifests = []builtinSkillManifest{
	{
		UID:      "bsk_01JZ7Q3YF6Q2Z4HM9V8K7D1R3P",
		Category: "research",
		DirName:  "deep-research",
	},
	{
		UID:      "bsk_01JZ7Q4AJ1X9N5B2C8M6T0W3EY",
		Category: "review",
		DirName:  "single-document-review",
	},
	{
		UID:      "bsk_01JZ7Q4RPN6K3Y8V1D5H2A9S0B",
		Category: "review",
		DirName:  "systematic-document-and-literature-review",
	},
	{
		UID:      "bsk_01JZ7Q58M4E7C2N9X6P1D3V0KA",
		Category: "search",
		DirName:  "paper-search",
	},
	{
		UID:      "bsk_01K0M8SCV7PAPERSEARCH9Q2X3A4B",
		Category: "search",
		DirName:  "sciverse-paper-search",
	},
}

var (
	builtinCatalogOnce sync.Once
	builtinCatalog     []builtinSkill
	builtinCatalogErr  error
)

func builtinSkillsRoot() string {
	if value := strings.TrimSpace(os.Getenv("LAZYMIND_BUILTIN_SKILLS_DIR")); value != "" {
		return value
	}
	if info, err := os.Stat("/skills"); err == nil && info.IsDir() {
		return "/skills"
	}
	wd, err := os.Getwd()
	if err == nil {
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
	}
	return ""
}

func loadBuiltinCatalog() ([]builtinSkill, error) {
	builtinCatalogOnce.Do(func() {
		root := builtinSkillsRoot()
		if root == "" {
			appLog.Logger.Warn().Msg("builtin skills root not found; skip builtin skill catalog")
			builtinCatalog = []builtinSkill{}
			return
		}

		items := make([]builtinSkill, 0, len(builtinSkillManifests))
		for _, manifest := range builtinSkillManifests {
			item, err := loadBuiltinSkill(root, manifest)
			if err != nil {
				builtinCatalogErr = err
				return
			}
			items = append(items, item)
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].Category != items[j].Category {
				return items[i].Category < items[j].Category
			}
			return items[i].Name < items[j].Name
		})
		builtinCatalog = items
	})
	return builtinCatalog, builtinCatalogErr
}

func loadBuiltinSkill(root string, manifest builtinSkillManifest) (builtinSkill, error) {
	if err := validatePathSegment(manifest.Category); err != nil {
		return builtinSkill{}, err
	}
	skillDir := filepath.Join(root, manifest.Category, manifest.DirName)
	contentBytes, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return builtinSkill{}, err
	}
	content := string(contentBytes)
	meta, _, err := parseFrontmatter(content)
	if err != nil {
		return builtinSkill{}, err
	}
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = strings.TrimSpace(manifest.DirName)
	}
	if err := validatePathSegment(name); err != nil {
		return builtinSkill{}, err
	}
	description, err := validateParentSkillContent(name, "", content)
	if err != nil {
		return builtinSkill{}, err
	}

	children := make([]builtinSkillFile, 0)
	if err := filepath.WalkDir(skillDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == skillDir {
			return nil
		}
		baseName := strings.TrimSpace(filepath.Base(path))
		if d.IsDir() {
			if strings.HasPrefix(baseName, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if baseName == "SKILL.md" {
			return nil
		}
		rel, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		childContent, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		ext := normalizeExt(filepath.Ext(rel))
		childName := strings.TrimSuffix(rel, filepath.Ext(rel))
		children = append(children, builtinSkillFile{
			Name:         childName,
			Description:  rel,
			RelativePath: rel,
			FileExt:      ext,
			Content:      string(childContent),
		})
		return nil
	}); err != nil {
		return builtinSkill{}, err
	}
	sort.Slice(children, func(i, j int) bool {
		return children[i].RelativePath < children[j].RelativePath
	})

	return builtinSkill{
		UID:         strings.TrimSpace(manifest.UID),
		Category:    strings.TrimSpace(manifest.Category),
		Name:        name,
		Description: description,
		Content:     content,
		Children:    children,
	}, nil
}

func builtinSkillByUID(uid string) (builtinSkill, bool, error) {
	uid = strings.TrimSpace(uid)
	catalog, err := loadBuiltinCatalog()
	if err != nil {
		return builtinSkill{}, false, err
	}
	for _, item := range catalog {
		if item.UID == uid {
			return item, true, nil
		}
	}
	return builtinSkill{}, false, nil
}

func builtinTemplateID(uid string) string {
	return builtinSkillIDPrefix + strings.TrimSpace(uid)
}

func builtinListResponse(item builtinSkill) map[string]any {
	children := make([]map[string]any, 0, len(item.Children))
	for _, child := range item.Children {
		children = append(children, map[string]any{
			"skill_id":                  builtinTemplateID(item.UID + ":" + child.RelativePath),
			"name":                      child.Name,
			"description":               child.Description,
			"category":                  item.Category,
			"tags":                      []string{},
			"parent_id":                 builtinTemplateID(item.UID),
			"parent_skill_id":           builtinTemplateID(item.UID),
			"parent_skill_name":         item.Name,
			"file_ext":                  child.FileExt,
			"auto_evo":                  false,
			"auto_evo_apply_status":     evolution.AutoEvoApplyStatusIdle,
			"auto_evo_generation":       0,
			"auto_evo_error":            "",
			"is_enabled":                true,
			"update_status":             evolution.UpdateStatusUpToDate,
			"has_pending_review_result": false,
			"review_status":             "none",
			"node_type":                 evolution.SkillNodeTypeChild,
			"builtin_skill_uid":         item.UID,
			"is_builtin_template":       true,
			"activation_status":         "available",
			"readonly":                  true,
			"content":                   "",
			"origin_builtin_skill_uid":  "",
		})
	}
	return map[string]any{
		"skill_id":                  builtinTemplateID(item.UID),
		"name":                      item.Name,
		"description":               item.Description,
		"category":                  item.Category,
		"tags":                      item.Tags,
		"auto_evo":                  false,
		"auto_evo_apply_status":     evolution.AutoEvoApplyStatusIdle,
		"auto_evo_generation":       0,
		"auto_evo_error":            "",
		"is_enabled":                true,
		"update_status":             evolution.UpdateStatusUpToDate,
		"has_pending_review_result": false,
		"review_status":             "none",
		"node_type":                 evolution.SkillNodeTypeParent,
		"children":                  children,
		"builtin_skill_uid":         item.UID,
		"is_builtin_template":       true,
		"activation_status":         "available",
		"readonly":                  true,
		"content":                   "",
		"origin_builtin_skill_uid":  "",
	}
}

func skillMatchesListFilters(name, description, category string, tags []string, keyword, filterCategory string, filterTags []string) bool {
	if keyword != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(keyword)) && !strings.Contains(strings.ToLower(description), strings.ToLower(keyword)) {
		return false
	}
	if filterCategory != "" && category != filterCategory {
		return false
	}
	return len(filterTags) == 0 || containsAllTags(tags, filterTags)
}

func enabledBuiltinSkillUIDs(parents []orm.SkillResource) map[string]struct{} {
	enabled := make(map[string]struct{})
	for _, parent := range parents {
		uid := strings.TrimSpace(parent.OriginBuiltinSkillUID)
		if uid != "" {
			enabled[uid] = struct{}{}
		}
	}
	return enabled
}

func builtinActivationStatus(originBuiltinSkillUID string) string {
	if strings.TrimSpace(originBuiltinSkillUID) == "" {
		return ""
	}
	return "enabled"
}

func EnableBuiltinSkill(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	userName := strings.TrimSpace(store.UserName(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	uid := strings.TrimSpace(common.PathVar(r, "builtin_skill_uid"))
	if uid == "" {
		common.ReplyErr(w, "missing builtin_skill_uid", http.StatusBadRequest)
		return
	}
	item, ok, err := builtinSkillByUID(uid)
	if err != nil {
		common.ReplyErr(w, "load builtin skill failed", http.StatusInternalServerError)
		return
	}
	if !ok {
		common.ReplyErr(w, "builtin skill not found", http.StatusNotFound)
		return
	}

	var existing orm.SkillResource
	err = db.WithContext(r.Context()).
		Where("owner_user_id = ? AND node_type = ? AND origin_builtin_skill_uid = ?",
			userID, evolution.SkillNodeTypeParent, uid).
		Take(&existing).Error
	if err == nil {
		detail, detailErr := getSkillDetail(r.Context(), db, userID, existing.ID)
		if detailErr != nil {
			common.ReplyErr(w, "query skill failed", http.StatusInternalServerError)
			return
		}
		common.ReplyOK(w, detail)
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "query skill failed", http.StatusInternalServerError)
		return
	}

	created, err := createBuiltinSkillCopy(r.Context(), db, userID, userName, item)
	if err != nil {
		replySkillError(w, err)
		return
	}
	detail, err := getSkillDetail(r.Context(), db, userID, created.ID)
	if err != nil {
		common.ReplyErr(w, "query skill failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, detail)
}

func createBuiltinSkillCopy(ctx context.Context, db *gorm.DB, userID, userName string, item builtinSkill) (orm.SkillResource, error) {
	name := strings.TrimSpace(item.Name)
	if name == "" {
		return orm.SkillResource{}, errors.New("builtin skill name required")
	}
	if err := ensureDBParentSkillIdentityAvailable(ctx, db, userID, item.Category, name, ""); err != nil {
		return orm.SkillResource{}, err
	}
	fullContent := item.Content
	description, err := validateParentSkillContent(name, "", fullContent)
	if err != nil {
		return orm.SkillResource{}, err
	}

	now := time.Now()
	parent := orm.SkillResource{
		ID:                    evolution.NewID(),
		OwnerUserID:           userID,
		OwnerUserName:         userName,
		OriginBuiltinSkillUID: item.UID,
		Category:              item.Category,
		ParentSkillName:       "",
		SkillName:             name,
		NodeType:              evolution.SkillNodeTypeParent,
		Description:           description,
		Tags:                  tagsJSON(item.Tags),
		FileExt:               "md",
		RelativePath:          parentRelativePath(item.Category, name),
		Content:               fullContent,
		ContentSize:           skillContentSize(fullContent),
		MimeType:              mimeTypeForExt("md"),
		ContentHash:           evolution.HashContent(fullContent),
		Version:               1,
		AutoEvo:               false,
		IsEnabled:             true,
		UpdateStatus:          evolution.UpdateStatusUpToDate,
		CreateUserID:          userID,
		CreateUserName:        userName,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	children := make([]orm.SkillResource, 0, len(item.Children))
	for _, child := range item.Children {
		rel := filepath.ToSlash(filepath.Join(item.Category, name, child.RelativePath))
		children = append(children, orm.SkillResource{
			ID:                    evolution.NewID(),
			OwnerUserID:           userID,
			OwnerUserName:         userName,
			OriginBuiltinSkillUID: item.UID,
			Category:              item.Category,
			ParentSkillName:       name,
			SkillName:             child.Name,
			NodeType:              evolution.SkillNodeTypeChild,
			Description:           child.Description,
			FileExt:               child.FileExt,
			RelativePath:          rel,
			Content:               child.Content,
			ContentSize:           skillContentSize(child.Content),
			MimeType:              mimeTypeForExt(child.FileExt),
			ContentHash:           evolution.HashContent(child.Content),
			Version:               1,
			AutoEvo:               false,
			IsEnabled:             true,
			UpdateStatus:          evolution.UpdateStatusUpToDate,
			CreateUserID:          userID,
			CreateUserName:        userName,
			CreatedAt:             now,
			UpdatedAt:             now,
		})
	}

	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		source := resourcechange.Source{
			ChangeSource: resourcechange.ChangeSourceDirectSave,
			ChangedAt:    now,
		}
		if err := resourcechange.CreateModel(ctx, tx, &parent, resourcechange.ContentChange{
			ResourceType:  orm.ResourceUpdateResourceTypeSkill,
			ResourceID:    parent.ID,
			UserID:        userID,
			FromVersion:   0,
			ToVersion:     parent.Version,
			BeforeContent: "",
			AfterContent:  parent.Content,
			Source:        source,
		}); err != nil {
			return err
		}
		for i := range children {
			child := &children[i]
			if err := resourcechange.CreateModel(ctx, tx, child, resourcechange.ContentChange{
				ResourceType:  orm.ResourceUpdateResourceTypeSkill,
				ResourceID:    child.ID,
				UserID:        userID,
				FromVersion:   0,
				ToVersion:     child.Version,
				BeforeContent: "",
				AfterContent:  child.Content,
				Source:        source,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return orm.SkillResource{}, err
	}
	return parent, nil
}

func ensureDBParentSkillIdentityAvailable(ctx context.Context, db *gorm.DB, userID, category, skillName, excludeID string) error {
	relPath := parentRelativePath(category, skillName)
	var count int64
	query := db.WithContext(ctx).
		Model(&orm.SkillResource{}).
		Where("owner_user_id = ? AND category = ? AND node_type = ? AND skill_name = ?", userID, category, evolution.SkillNodeTypeParent, skillName)
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return gorm.ErrDuplicatedKey
	}
	query = db.WithContext(ctx).
		Model(&orm.SkillResource{}).
		Where("owner_user_id = ? AND relative_path = ?", userID, relPath)
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return gorm.ErrDuplicatedKey
	}
	return nil
}

func ensureNoBuiltinParentSkillConflict(category, skillName string) error {
	relPath := parentRelativePath(category, skillName)
	if err := ensureNoBuiltinRelativePathConflict(relPath); err != nil {
		return err
	}
	catalog, err := loadBuiltinCatalog()
	if err != nil {
		return err
	}
	for _, item := range catalog {
		if strings.TrimSpace(item.Category) == category && strings.TrimSpace(item.Name) == skillName {
			return gorm.ErrDuplicatedKey
		}
	}
	return nil
}

func ensureNoBuiltinRelativePathConflict(relPath string) error {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" {
		return nil
	}
	catalog, err := loadBuiltinCatalog()
	if err != nil {
		return err
	}
	for _, item := range catalog {
		if filepath.ToSlash(parentRelativePath(item.Category, item.Name)) == relPath {
			return gorm.ErrDuplicatedKey
		}
		for _, child := range item.Children {
			childRelPath := filepath.ToSlash(filepath.Join(item.Category, item.Name, child.RelativePath))
			if childRelPath == relPath {
				return gorm.ErrDuplicatedKey
			}
		}
	}
	return nil
}
