package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	appLog "lazymind/core/log"
	"lazymind/core/store"
)

var (
	errDraftPreviewParentOnly  = errors.New("only parent skill supports draft preview")
	errDraftPreviewNotFound    = errors.New("skill draft not found")
	errAutoEvoApplyConflict    = errors.New("auto_evo apply conflict")
	errAutoEvoTaskRunning      = errors.New("auto_evo task is running")
	errPendingRemoveSuggestion = errors.New("skill has pending remove suggestion")
)

func normalizedSkillUpdateStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" || status == "pending_confirm" {
		return evolution.UpdateStatusUpToDate
	}
	return status
}

func List(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var parents []orm.SkillResource
	if err := db.WithContext(r.Context()).
		Where("owner_user_id = ? AND node_type = ?", userID, evolution.SkillNodeTypeParent).
		Order("updated_at DESC").
		Find(&parents).Error; err != nil {
		common.ReplyErr(w, "query skills failed", http.StatusInternalServerError)
		return
	}
	var children []orm.SkillResource
	if err := db.WithContext(r.Context()).
		Where("owner_user_id = ? AND node_type = ?", userID, evolution.SkillNodeTypeChild).
		Order("created_at ASC").
		Find(&children).Error; err != nil {
		common.ReplyErr(w, "query skills failed", http.StatusInternalServerError)
		return
	}
	suggestionRows := make([]orm.SkillResource, 0, len(parents)+len(children))
	suggestionRows = append(suggestionRows, parents...)
	suggestionRows = append(suggestionRows, children...)
	suggestionStatesByKey, err := loadSuggestionStatesByKey(r.Context(), db, userID, suggestionRows)
	if err != nil {
		common.ReplyErr(w, "query skills failed", http.StatusInternalServerError)
		return
	}

	childMap := make(map[string][]orm.SkillResource)
	for _, child := range children {
		key := child.Category + "/" + child.ParentSkillName
		childMap[key] = append(childMap[key], child)
	}

	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	filterTags := compactStrings(r.URL.Query()["tags"])
	filtered := make([]skillListParentEntry, 0, len(parents))
	total := 0
	for _, parent := range parents {
		parentTags := parseTags(parent.Tags)
		if !skillMatchesListFilters(parent.SkillName, parent.Description, parent.Category, parentTags, keyword, category, filterTags) {
			continue
		}
		key := parent.Category + "/" + parent.SkillName
		childrenForParent := childMap[key]
		total++
		filtered = append(filtered, skillListParentEntry{
			parent:   parent,
			children: childrenForParent,
		})
	}
	catalog, err := loadBuiltinCatalog()
	if err != nil {
		common.ReplyErr(w, "load builtin skills failed", http.StatusInternalServerError)
		return
	}
	enabledBuiltinUIDs := enabledBuiltinSkillUIDs(parents)
	for _, builtin := range catalog {
		if _, exists := enabledBuiltinUIDs[builtin.UID]; exists {
			continue
		}
		if !skillMatchesListFilters(builtin.Name, builtin.Description, builtin.Category, builtin.Tags, keyword, category, filterTags) {
			continue
		}
		total++
		builtinItem := builtin
		filtered = append(filtered, skillListParentEntry{builtin: &builtinItem})
	}

	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	pageSize := parsePositiveInt(r.URL.Query().Get("page_size"), 20)
	if pageSize > 100 {
		pageSize = 100
	}
	pageItems := paginateSkillListParents(filtered, page, pageSize)
	items := make([]map[string]any, 0, len(pageItems))
	for _, item := range pageItems {
		if item.builtin != nil {
			items = append(items, builtinListResponse(*item.builtin))
			continue
		}
		items = append(items, parentListResponse(item.parent, item.children, suggestionStatesByKey))
	}

	common.ReplyOK(w, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     total,
	})
}

func Get(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	skillID := common.PathVar(r, "skill_id")
	if skillID == "" {
		common.ReplyErr(w, "missing skill_id", http.StatusBadRequest)
		return
	}
	item, err := getReadableSkillDetail(r.Context(), db, userID, skillID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "skill not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query skill failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, item)
}

func CreateManaged(w http.ResponseWriter, r *http.Request) {
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
	var req createSkillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	req.Category = strings.TrimSpace(req.Category)
	req.ParentSkillID = strings.TrimSpace(req.ParentSkillID)
	req.ParentSkillName = strings.TrimSpace(req.ParentSkillName)
	req.Content = strings.TrimSpace(req.Content)
	isChildCreate := req.ParentSkillID != "" || req.ParentSkillName != ""
	appLog.Logger.Info().
		Str("route", "POST /api/core/skills").
		Str("user_id", userID).
		Str("category", req.Category).
		Str("name", req.Name).
		Str("parent_skill_id", req.ParentSkillID).
		Str("parent_skill_name", req.ParentSkillName).
		Int("children_count", len(req.Children)).
		Msg("direct skill management create requested")
	if req.Name == "" || req.Content == "" || (!isChildCreate && req.Category == "") {
		common.ReplyErr(w, "name/category/content required", http.StatusBadRequest)
		return
	}
	if err := validatePathSegment(req.Name); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !isChildCreate || req.ParentSkillID == "" {
		if err := validatePathSegment(req.Category); err != nil {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	createdSkillID := ""
	if !isChildCreate {
		if err := createParentSkill(r.Context(), db, userID, userName, req); err != nil {
			replySkillError(w, err)
			return
		}
	} else {
		if req.ParentSkillID == "" && req.ParentSkillName == "" {
			common.ReplyErr(w, "parent_skill_id required", http.StatusBadRequest)
			return
		}
		if req.ParentSkillID == "" {
			if err := validatePathSegment(req.ParentSkillName); err != nil {
				common.ReplyErr(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		if len(req.Children) > 0 {
			common.ReplyErr(w, "children is not allowed when creating child skill", http.StatusBadRequest)
			return
		}
		row, err := createChildSkill(r.Context(), db, userID, userName, req)
		if err != nil {
			replySkillError(w, err)
			return
		}
		createdSkillID = row.ID
	}

	var row orm.SkillResource
	if createdSkillID != "" {
		if err := db.WithContext(r.Context()).Where("id = ? AND owner_user_id = ?", createdSkillID, userID).Take(&row).Error; err != nil {
			common.ReplyErr(w, "query skill failed", http.StatusInternalServerError)
			return
		}
	} else {
		relativePath := parentRelativePath(req.Category, req.Name)
		if err := db.WithContext(r.Context()).Where("owner_user_id = ? AND relative_path = ?", userID, relativePath).Take(&row).Error; err != nil {
			common.ReplyErr(w, "query skill failed", http.StatusInternalServerError)
			return
		}
	}
	item, err := getSkillDetail(r.Context(), db, userID, row.ID)
	if err != nil {
		common.ReplyErr(w, "query skill failed", http.StatusInternalServerError)
		return
	}
	appLog.Logger.Warn().
		Str("route", "POST /api/core/skills").
		Str("user_id", userID).
		Str("skill_id", row.ID).
		Str("category", req.Category).
		Str("name", req.Name).
		Str("parent_skill_id", req.ParentSkillID).
		Str("parent_skill_name", req.ParentSkillName).
		Msg("direct skill management create executed")
	common.ReplyOK(w, item)
}

func UpdateManaged(w http.ResponseWriter, r *http.Request) {
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
	skillID := common.PathVar(r, "skill_id")
	if skillID == "" {
		common.ReplyErr(w, "missing skill_id", http.StatusBadRequest)
		return
	}
	var req updateSkillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	appLog.Logger.Warn().
		Str("route", "PATCH /api/core/skills/{skill_id}").
		Str("user_id", userID).
		Str("skill_id", skillID).
		Msg("direct skill management update requested")
	if err := updateSkill(r.Context(), db, userID, userName, skillID, req); err != nil {
		replySkillError(w, err)
		return
	}
	item, err := getSkillDetail(r.Context(), db, userID, skillID)
	if err != nil {
		common.ReplyErr(w, "query skill failed", http.StatusInternalServerError)
		return
	}
	appLog.Logger.Warn().
		Str("route", "PATCH /api/core/skills/{skill_id}").
		Str("user_id", userID).
		Str("skill_id", skillID).
		Msg("direct skill management update executed")
	common.ReplyOK(w, item)
}

func DeleteManaged(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	skillID := common.PathVar(r, "skill_id")
	if skillID == "" {
		common.ReplyErr(w, "missing skill_id", http.StatusBadRequest)
		return
	}
	appLog.Logger.Warn().
		Str("route", "DELETE /api/core/skills/{skill_id}").
		Str("user_id", userID).
		Str("skill_id", skillID).
		Msg("direct skill management delete requested")
	if err := DeleteSkill(r.Context(), db, userID, skillID); err != nil {
		replySkillError(w, err)
		return
	}
	appLog.Logger.Warn().
		Str("route", "DELETE /api/core/skills/{skill_id}").
		Str("user_id", userID).
		Str("skill_id", skillID).
		Msg("direct skill management delete executed")
	common.ReplyOK(w, map[string]any{"deleted": true})
}

func Generate(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	skillID := common.PathVar(r, "skill_id")
	if skillID == "" {
		common.ReplyErr(w, "missing skill_id", http.StatusBadRequest)
		return
	}
	var req generateSkillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.SuggestionIDs = compactStrings(req.SuggestionIDs)
	req.UserInstruct = strings.TrimSpace(req.UserInstruct)
	if len(req.SuggestionIDs) == 0 && req.UserInstruct == "" {
		common.ReplyErr(w, "suggestion_ids or user_instruct required", http.StatusBadRequest)
		return
	}

	var row orm.SkillResource
	if err := db.WithContext(r.Context()).Where("id = ? AND owner_user_id = ?", skillID, userID).Take(&row).Error; err != nil {
		common.ReplyErr(w, "skill not found", http.StatusNotFound)
		return
	}
	if row.NodeType != evolution.SkillNodeTypeParent {
		common.ReplyErr(w, "only parent skill supports generate", http.StatusBadRequest)
		return
	}

	useDraft := len(req.SuggestionIDs) == 0 && req.UserInstruct != ""
	content, err := skillGenerateBaseContent(row, useDraft)
	if err != nil {
		if errors.Is(err, errDraftPreviewNotFound) {
			common.ReplyErr(w, err.Error(), http.StatusNotFound)
		} else {
			common.ReplyErr(w, "read skill content failed", http.StatusInternalServerError)
		}
		return
	}
	var suggestions []orm.ResourceSuggestion
	if len(req.SuggestionIDs) > 0 {
		suggestions, err = evolution.LoadApprovedSuggestions(r.Context(), db, userID, evolution.ResourceTypeSkill, skillSuggestionResourceKey(row), req.SuggestionIDs)
		if err != nil {
			common.ReplyErr(w, "query suggestions failed", http.StatusInternalServerError)
			return
		}
		if len(suggestions) == 0 {
			common.ReplyErr(w, "no accepted suggestions found", http.StatusBadRequest)
			return
		}
	}

	outdated := false
	resolver := evolution.NewSuggestionOutdatedResolver(db)
	for _, suggestion := range suggestions {
		isOutdated, err := resolver.Resolve(r.Context(), suggestion)
		if err != nil {
			common.ReplyErr(w, "check suggestion outdated failed", http.StatusInternalServerError)
			return
		}
		if isOutdated {
			outdated = true
			break
		}
	}

	algoReq := algo.SkillGenerateRequest{
		Content:      content,
		Suggestions:  toAlgoSuggestions(suggestions),
		UserInstruct: req.UserInstruct,
	}
	appLog.Logger.Info().
		Str("route", "/skills/{skill_id}:generate").
		Str("skill_id", row.ID).
		Str("user_id", userID).
		Str("payload", payloadForLog(algoReq)).
		Msg("requesting external skill generate")
	generated, err := algo.GenerateSkill(r.Context(), algoReq)
	if err != nil {
		common.ReplyErr(w, "skill generate failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	if _, err := validateParentSkillContent(row.SkillName, "", generated); err != nil {
		common.ReplyErr(w, "generated skill content invalid: "+err.Error(), http.StatusBadGateway)
		return
	}

	now := time.Now()
	ids := suggestionIDs(suggestions)
	if useDraft && len(ids) == 0 {
		ids = evolution.DraftSuggestionIDs(row.Ext)
	}
	update := map[string]any{
		"draft_source_version": row.Version,
		"draft_content":        generated,
		"draft_status":         "pending_confirm",
		"draft_updated_at":     now,
		"update_status":        evolution.UpdateStatusUpToDate,
		"updated_at":           now,
		"ext":                  evolution.WithDraftSuggestionIDs(row.Ext, ids),
	}
	if err := db.WithContext(r.Context()).Model(&orm.SkillResource{}).Where("id = ?", row.ID).Updates(update).Error; err != nil {
		common.ReplyErr(w, "update skill draft failed", http.StatusInternalServerError)
		return
	}
	_ = db.WithContext(r.Context()).Model(&orm.SkillResource{}).
		Where("owner_user_id = ? AND node_type = ? AND category = ? AND parent_skill_name = ?", userID, evolution.SkillNodeTypeChild, row.Category, row.SkillName).
		Updates(map[string]any{"update_status": evolution.UpdateStatusUpToDate, "updated_at": now}).Error
	common.ReplyOK(w, generateSkillResponse{
		DraftStatus:        "pending_confirm",
		DraftSourceVersion: row.Version,
		DraftPath:          "",
		Outdated:           outdated,
	})
}

func skillGenerateBaseContent(row orm.SkillResource, useDraft bool) (string, error) {
	if !useDraft {
		return storedSkillContent(row)
	}
	if strings.TrimSpace(row.DraftStatus) != "pending_confirm" {
		return "", errDraftPreviewNotFound
	}
	content := row.DraftContent
	if strings.TrimSpace(content) == "" {
		return "", errors.New("read skill draft failed")
	}
	return content, nil
}

func DraftPreview(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	skillID := common.PathVar(r, "skill_id")
	if skillID == "" {
		common.ReplyErr(w, "missing skill_id", http.StatusBadRequest)
		return
	}

	item, err := buildDraftPreviewResponse(r.Context(), db, userID, skillID)
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			common.ReplyErr(w, "skill not found", http.StatusNotFound)
		case errors.Is(err, errDraftPreviewParentOnly):
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		case errors.Is(err, errDraftPreviewNotFound):
			common.ReplyErr(w, err.Error(), http.StatusNotFound)
		default:
			common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	common.ReplyOK(w, item)
}

func Confirm(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	skillID := common.PathVar(r, "skill_id")
	if skillID == "" {
		common.ReplyErr(w, "missing skill_id", http.StatusBadRequest)
		return
	}
	var row orm.SkillResource
	if err := db.WithContext(r.Context()).Where("id = ? AND owner_user_id = ?", skillID, userID).Take(&row).Error; err != nil {
		common.ReplyErr(w, "skill not found", http.StatusNotFound)
		return
	}
	if row.NodeType != evolution.SkillNodeTypeParent {
		common.ReplyErr(w, "only parent skill supports confirm", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(row.DraftStatus) != "pending_confirm" {
		common.ReplyErr(w, "skill draft not found", http.StatusNotFound)
		return
	}
	if row.Version != row.DraftSourceVersion {
		common.ReplyErr(w, "skill draft version conflict", http.StatusConflict)
		return
	}
	content := row.DraftContent
	if strings.TrimSpace(content) == "" {
		common.ReplyErr(w, "read skill draft failed", http.StatusInternalServerError)
		return
	}
	description, err := validateParentSkillContent(row.SkillName, "", content)
	if err != nil {
		common.ReplyErr(w, "skill draft content invalid: "+err.Error(), http.StatusBadRequest)
		return
	}
	hash := evolution.HashContent(content)
	now := time.Now()
	ids := evolution.DraftSuggestionIDs(row.Ext)
	update := map[string]any{
		"description":          description,
		"content_hash":         hash,
		"content":              content,
		"content_size":         skillContentSize(content),
		"mime_type":            mimeTypeForExt(row.FileExt),
		"version":              row.Version + 1,
		"draft_content":        "",
		"draft_source_version": 0,
		"draft_status":         "",
		"draft_updated_at":     nil,
		"update_status":        evolution.UpdateStatusUpToDate,
		"updated_at":           now,
		"ext":                  evolution.WithDraftSuggestionIDs(row.Ext, nil),
	}
	if err := db.WithContext(r.Context()).Model(&orm.SkillResource{}).Where("id = ? AND version = ?", row.ID, row.Version).Updates(update).Error; err != nil {
		common.ReplyErr(w, "confirm skill draft failed", http.StatusInternalServerError)
		return
	}
	_ = db.WithContext(r.Context()).Model(&orm.SkillResource{}).
		Where("owner_user_id = ? AND node_type = ? AND category = ? AND parent_skill_name = ?", userID, evolution.SkillNodeTypeChild, row.Category, row.SkillName).
		Updates(map[string]any{"update_status": evolution.UpdateStatusUpToDate, "updated_at": now}).Error
	if err := evolution.UpdateSuggestionStatus(r.Context(), db, ids, evolution.SuggestionStatusApplied); err != nil {
		common.ReplyErr(w, "update suggestion status failed", http.StatusInternalServerError)
		return
	}
	item, err := getSkillDetail(r.Context(), db, userID, row.ID)
	if err != nil {
		common.ReplyErr(w, "query skill failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, item)
}

func Discard(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	skillID := common.PathVar(r, "skill_id")
	if skillID == "" {
		common.ReplyErr(w, "missing skill_id", http.StatusBadRequest)
		return
	}
	var row orm.SkillResource
	if err := db.WithContext(r.Context()).Where("id = ? AND owner_user_id = ?", skillID, userID).Take(&row).Error; err != nil {
		common.ReplyErr(w, "skill not found", http.StatusNotFound)
		return
	}
	if row.NodeType != evolution.SkillNodeTypeParent {
		common.ReplyErr(w, "only parent skill supports discard", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(row.DraftStatus) != "pending_confirm" {
		common.ReplyErr(w, "skill draft not found", http.StatusNotFound)
		return
	}
	now := time.Now()
	update := map[string]any{
		"draft_source_version": 0,
		"draft_content":        "",
		"draft_status":         "",
		"draft_updated_at":     nil,
		"update_status":        evolution.UpdateStatusUpToDate,
		"updated_at":           now,
		"ext":                  evolution.WithDraftSuggestionIDs(row.Ext, nil),
	}
	if err := db.WithContext(r.Context()).Model(&orm.SkillResource{}).Where("id = ?", row.ID).Updates(update).Error; err != nil {
		common.ReplyErr(w, "discard skill draft failed", http.StatusInternalServerError)
		return
	}
	_ = db.WithContext(r.Context()).Model(&orm.SkillResource{}).
		Where("owner_user_id = ? AND node_type = ? AND category = ? AND parent_skill_name = ?", userID, evolution.SkillNodeTypeChild, row.Category, row.SkillName).
		Updates(map[string]any{"update_status": evolution.UpdateStatusUpToDate, "updated_at": now}).Error
	common.ReplyOK(w, map[string]any{"discarded": true})
}

func getReadableSkillDetail(ctx context.Context, db *gorm.DB, userID, skillID string) (map[string]any, error) {
	var row orm.SkillResource
	if err := db.WithContext(ctx).Where("id = ?", skillID).Take(&row).Error; err != nil {
		return nil, err
	}
	if strings.TrimSpace(row.OwnerUserID) == strings.TrimSpace(userID) {
		return getSkillDetail(ctx, db, row.OwnerUserID, skillID)
	}

	allowed, err := hasSharedSkillReadAccess(ctx, db, userID, row)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, gorm.ErrRecordNotFound
	}
	return getSkillDetail(ctx, db, row.OwnerUserID, skillID)
}

func hasSharedSkillReadAccess(ctx context.Context, db *gorm.DB, userID string, row orm.SkillResource) (bool, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false, nil
	}

	rootSkill, err := sharedSkillRoot(ctx, db, row)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if strings.TrimSpace(rootSkill.ID) == "" {
		return false, nil
	}

	sourceMatch := db.Where("skill_share_tasks.source_skill_id = ?", rootSkill.ID)
	sourceRelativeRoot := filepath.ToSlash(filepath.Join(strings.TrimSpace(rootSkill.Category), strings.TrimSpace(rootSkill.SkillName)))
	if sourceRelativeRoot != "" && sourceRelativeRoot != "." {
		sourceMatch = sourceMatch.Or("skill_share_tasks.source_relative_root = ?", sourceRelativeRoot)
	}
	if strings.TrimSpace(rootSkill.Category) != "" && strings.TrimSpace(rootSkill.SkillName) != "" {
		sourceMatch = sourceMatch.Or("skill_share_tasks.source_category = ? AND skill_share_tasks.source_parent_skill_name = ?", rootSkill.Category, rootSkill.SkillName)
	}

	var count int64
	if err := db.WithContext(ctx).
		Model(&orm.SkillShareItem{}).
		Joins("JOIN skill_share_tasks ON skill_share_tasks.id = skill_share_items.share_task_id").
		Where("skill_share_items.target_user_id = ?", userID).
		Where("skill_share_tasks.source_user_id = ?", rootSkill.OwnerUserID).
		Where(sourceMatch).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func sharedSkillRoot(ctx context.Context, db *gorm.DB, row orm.SkillResource) (orm.SkillResource, error) {
	if row.NodeType == evolution.SkillNodeTypeParent {
		return row, nil
	}
	if row.NodeType != evolution.SkillNodeTypeChild {
		return orm.SkillResource{}, nil
	}

	parentName := strings.TrimSpace(row.ParentSkillName)
	if parentName == "" {
		return orm.SkillResource{}, nil
	}
	var parent orm.SkillResource
	if err := db.WithContext(ctx).
		Where("owner_user_id = ? AND node_type = ? AND category = ? AND skill_name = ?", row.OwnerUserID, evolution.SkillNodeTypeParent, row.Category, parentName).
		Take(&parent).Error; err != nil {
		return orm.SkillResource{}, err
	}
	return parent, nil
}

func parentForChild(ctx context.Context, db *gorm.DB, child orm.SkillResource) (orm.SkillResource, error) {
	parentName := strings.TrimSpace(child.ParentSkillName)
	if parentName == "" {
		return orm.SkillResource{}, gorm.ErrRecordNotFound
	}
	var parent orm.SkillResource
	err := db.WithContext(ctx).
		Where("owner_user_id = ? AND node_type = ? AND category = ? AND skill_name = ?", child.OwnerUserID, evolution.SkillNodeTypeParent, child.Category, parentName).
		Take(&parent).Error
	return parent, err
}

func getSkillDetail(ctx context.Context, db *gorm.DB, userID, skillID string) (map[string]any, error) {
	var row orm.SkillResource
	if err := db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", skillID, userID).Take(&row).Error; err != nil {
		return nil, err
	}
	suggestionRows := []orm.SkillResource{row}
	if row.NodeType == evolution.SkillNodeTypeParent {
		var children []orm.SkillResource
		if err := db.WithContext(ctx).
			Where("owner_user_id = ? AND node_type = ? AND category = ? AND parent_skill_name = ?", userID, evolution.SkillNodeTypeChild, row.Category, row.SkillName).
			Order("created_at ASC").
			Find(&children).Error; err != nil {
			return nil, err
		}
		suggestionRows = append(suggestionRows, children...)
	}
	suggestionStatesByKey, err := loadSuggestionStatesByKey(ctx, db, userID, suggestionRows)
	if err != nil {
		return nil, err
	}
	suggestionState := canonicalSkillSuggestionState(suggestionStatesByKey[skillSuggestionResourceKey(row)])
	content, err := storedSkillContent(row)
	if err != nil {
		return nil, err
	}
	item := map[string]any{
		"skill_id":                       row.ID,
		"name":                           row.SkillName,
		"description":                    row.Description,
		"category":                       row.Category,
		"tags":                           parseTags(row.Tags),
		"auto_evo":                       row.AutoEvo,
		"auto_evo_apply_status":          evolution.NormalizeAutoEvoApplyStatus(row.AutoEvoApplyStatus),
		"auto_evo_generation":            row.AutoEvoGeneration,
		"auto_evo_error":                 row.AutoEvoError,
		"is_enabled":                     row.IsEnabled,
		"update_status":                  normalizedSkillUpdateStatus(row.UpdateStatus),
		"has_pending_review_suggestions": suggestionState.Status == evolution.SuggestionStatusPendingReview,
		"has_pending_remove_suggestion":  suggestionState.HasPendingRemove,
		"suggestion_status":              suggestionState.Status,
		"node_type":                      row.NodeType,
		"builtin_skill_uid":              "",
		"origin_builtin_skill_uid":       row.OriginBuiltinSkillUID,
		"is_builtin_template":            false,
		"activation_status":              builtinActivationStatus(row.OriginBuiltinSkillUID),
		"readonly":                       false,
		"parent_id":                      "",
		"parent_skill_id":                "",
		"parent_skill_name":              row.ParentSkillName,
		"content":                        content,
		"file_ext":                       row.FileExt,
	}
	if row.NodeType == evolution.SkillNodeTypeChild {
		if parent, err := parentForChild(ctx, db, row); err == nil {
			item["parent_id"] = parent.ID
			item["parent_skill_id"] = parent.ID
		}
	}
	if row.NodeType == evolution.SkillNodeTypeParent {
		var children []orm.SkillResource
		if err := db.WithContext(ctx).
			Where("owner_user_id = ? AND node_type = ? AND category = ? AND parent_skill_name = ?", userID, evolution.SkillNodeTypeChild, row.Category, row.SkillName).
			Order("created_at ASC").
			Find(&children).Error; err != nil {
			return nil, err
		}
		childItems := make([]map[string]any, 0, len(children))
		for _, child := range children {
			childSuggestionState := canonicalSkillSuggestionState(suggestionStatesByKey[skillSuggestionResourceKey(child)])
			childContent, _ := storedSkillContent(child)
			childItems = append(childItems, map[string]any{
				"skill_id":                       child.ID,
				"name":                           child.SkillName,
				"description":                    child.Description,
				"tags":                           parseTags(child.Tags),
				"file_ext":                       child.FileExt,
				"auto_evo":                       child.AutoEvo,
				"auto_evo_apply_status":          evolution.NormalizeAutoEvoApplyStatus(child.AutoEvoApplyStatus),
				"auto_evo_generation":            child.AutoEvoGeneration,
				"auto_evo_error":                 child.AutoEvoError,
				"is_enabled":                     child.IsEnabled,
				"update_status":                  normalizedSkillUpdateStatus(child.UpdateStatus),
				"has_pending_review_suggestions": childSuggestionState.Status == evolution.SuggestionStatusPendingReview,
				"has_pending_remove_suggestion":  childSuggestionState.HasPendingRemove,
				"suggestion_status":              childSuggestionState.Status,
				"node_type":                      child.NodeType,
				"builtin_skill_uid":              "",
				"origin_builtin_skill_uid":       child.OriginBuiltinSkillUID,
				"is_builtin_template":            false,
				"activation_status":              builtinActivationStatus(child.OriginBuiltinSkillUID),
				"readonly":                       false,
				"parent_id":                      row.ID,
				"parent_skill_id":                row.ID,
				"parent_skill_name":              child.ParentSkillName,
				"content":                        childContent,
			})
		}
		item["children"] = childItems
	} else {
		item["children"] = []any{}
	}
	return item, nil
}

func buildDraftPreviewResponse(ctx context.Context, db *gorm.DB, userID, skillID string) (draftPreviewResponse, error) {
	var row orm.SkillResource
	if err := db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", skillID, userID).Take(&row).Error; err != nil {
		return draftPreviewResponse{}, err
	}
	if row.NodeType != evolution.SkillNodeTypeParent {
		return draftPreviewResponse{}, errDraftPreviewParentOnly
	}
	if strings.TrimSpace(row.DraftStatus) != "pending_confirm" {
		return draftPreviewResponse{}, errDraftPreviewNotFound
	}

	currentContent, err := storedSkillContent(row)
	if err != nil {
		return draftPreviewResponse{}, err
	}

	draftContent := row.DraftContent
	if strings.TrimSpace(draftContent) == "" {
		return draftPreviewResponse{}, errors.New("read skill draft failed")
	}

	diff, err := buildContentDiff(currentContent, draftContent)
	if err != nil {
		return draftPreviewResponse{}, err
	}

	outdated, err := draftSuggestionsOutdated(ctx, db, row)
	if err != nil {
		return draftPreviewResponse{}, err
	}

	return draftPreviewResponse{
		SkillID:            row.ID,
		DraftStatus:        row.DraftStatus,
		DraftSourceVersion: row.DraftSourceVersion,
		CurrentContent:     currentContent,
		DraftContent:       draftContent,
		Diff:               diff,
		Outdated:           outdated,
	}, nil
}

func draftSuggestionsOutdated(ctx context.Context, db *gorm.DB, row orm.SkillResource) (bool, error) {
	ids := evolution.DraftSuggestionIDs(row.Ext)
	if len(ids) == 0 {
		return false, nil
	}

	suggestions, err := evolution.LoadApprovedSuggestions(ctx, db, row.OwnerUserID, evolution.ResourceTypeSkill, skillSuggestionResourceKey(row), ids)
	if err != nil {
		return false, err
	}

	resolver := evolution.NewSuggestionOutdatedResolver(db)
	for _, suggestion := range suggestions {
		isOutdated, err := resolver.Resolve(ctx, suggestion)
		if err != nil {
			return false, err
		}
		if isOutdated {
			return true, nil
		}
	}
	return false, nil
}

func createParentSkill(ctx context.Context, db *gorm.DB, userID, userName string, req createSkillRequest) error {
	fullContent, description, err := buildParentSkillContent(req.Name, req.Description, req.Content)
	if err != nil {
		return err
	}
	return createParentSkillWithContent(ctx, db, userID, userName, req, fullContent, description)
}

func createParentSkillWithContent(ctx context.Context, db *gorm.DB, userID, userName string, req createSkillRequest, fullContent, description string) error {
	relPath := parentRelativePath(req.Category, req.Name)
	var count int64
	if err := db.WithContext(ctx).
		Model(&orm.SkillResource{}).
		Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", userID, evolution.SkillNodeTypeParent, req.Name).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return gorm.ErrDuplicatedKey
	}
	if err := db.WithContext(ctx).Model(&orm.SkillResource{}).Where("owner_user_id = ? AND relative_path = ?", userID, relPath).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return gorm.ErrDuplicatedKey
	}
	for _, child := range req.Children {
		if err := validatePathSegment(child.Name); err != nil {
			return err
		}
	}

	now := time.Now()
	enabled := true
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}
	parent := orm.SkillResource{
		ID:              evolution.BuildSuggestionRecord("", "", "", "", "", "").ID,
		OwnerUserID:     userID,
		OwnerUserName:   userName,
		Category:        req.Category,
		ParentSkillName: "",
		SkillName:       req.Name,
		NodeType:        evolution.SkillNodeTypeParent,
		Description:     description,
		Tags:            tagsJSON(req.Tags),
		FileExt:         "md",
		RelativePath:    relPath,
		Content:         fullContent,
		ContentSize:     skillContentSize(fullContent),
		MimeType:        mimeTypeForExt("md"),
		ContentHash:     evolution.HashContent(fullContent),
		Version:         1,
		AutoEvo:         req.AutoEvo,
		IsEnabled:       enabled,
		UpdateStatus:    evolution.UpdateStatusUpToDate,
		CreateUserID:    userID,
		CreateUserName:  userName,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	children := make([]orm.SkillResource, 0, len(req.Children))
	for _, child := range req.Children {
		ext := normalizeExt(child.FileExt)
		rel := childRelativePath(req.Category, req.Name, child.Name, ext)
		children = append(children, orm.SkillResource{
			ID:              evolution.BuildSuggestionRecord("", "", "", "", "", "").ID,
			OwnerUserID:     userID,
			OwnerUserName:   userName,
			Category:        req.Category,
			ParentSkillName: req.Name,
			SkillName:       child.Name,
			NodeType:        evolution.SkillNodeTypeChild,
			Description:     strings.TrimSpace(child.Description),
			Tags:            tagsJSON(child.Tags),
			FileExt:         ext,
			RelativePath:    rel,
			Content:         child.Content,
			ContentSize:     skillContentSize(child.Content),
			MimeType:        mimeTypeForExt(ext),
			ContentHash:     evolution.HashContent(child.Content),
			Version:         1,
			AutoEvo:         child.AutoEvo,
			IsEnabled:       enabled,
			UpdateStatus:    evolution.UpdateStatusUpToDate,
			CreateUserID:    userID,
			CreateUserName:  userName,
			CreatedAt:       now,
			UpdatedAt:       now,
		})
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&parent).Error; err != nil {
			return err
		}
		if len(children) > 0 {
			if err := tx.Create(&children).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func resolveParentSkill(ctx context.Context, db *gorm.DB, userID, parentSkillID, category, parentSkillName string) (orm.SkillResource, error) {
	parentSkillID = strings.TrimSpace(parentSkillID)
	parentSkillName = strings.TrimSpace(parentSkillName)
	category = strings.TrimSpace(category)
	var parent orm.SkillResource
	if parentSkillID != "" {
		err := db.WithContext(ctx).
			Where("id = ? AND owner_user_id = ? AND node_type = ?", parentSkillID, userID, evolution.SkillNodeTypeParent).
			Take(&parent).Error
		return parent, err
	}
	if parentSkillName == "" {
		return orm.SkillResource{}, errors.New("parent_skill_id required")
	}
	query := db.WithContext(ctx).
		Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", userID, evolution.SkillNodeTypeParent, parentSkillName)
	if category != "" {
		query = query.Where("category = ?", category)
	}
	err := query.Take(&parent).Error
	return parent, err
}

func createChildSkill(ctx context.Context, db *gorm.DB, userID, userName string, req createSkillRequest) (orm.SkillResource, error) {
	parent, err := resolveParentSkill(ctx, db, userID, req.ParentSkillID, req.Category, req.ParentSkillName)
	if err != nil {
		return orm.SkillResource{}, err
	}
	ext := normalizeExt(req.FileExt)
	relPath := childRelativePath(parent.Category, parent.SkillName, req.Name, ext)
	var count int64
	if err := db.WithContext(ctx).Model(&orm.SkillResource{}).Where("owner_user_id = ? AND relative_path = ?", userID, relPath).Count(&count).Error; err != nil {
		return orm.SkillResource{}, err
	}
	if count > 0 {
		return orm.SkillResource{}, gorm.ErrDuplicatedKey
	}
	now := time.Now()
	row := orm.SkillResource{
		ID:              evolution.BuildSuggestionRecord("", "", "", "", "", "").ID,
		OwnerUserID:     userID,
		OwnerUserName:   userName,
		Category:        parent.Category,
		ParentSkillName: parent.SkillName,
		SkillName:       req.Name,
		NodeType:        evolution.SkillNodeTypeChild,
		Description:     strings.TrimSpace(req.Description),
		Tags:            tagsJSON(req.Tags),
		FileExt:         ext,
		RelativePath:    relPath,
		Content:         req.Content,
		ContentSize:     skillContentSize(req.Content),
		MimeType:        mimeTypeForExt(ext),
		ContentHash:     evolution.HashContent(req.Content),
		Version:         1,
		AutoEvo:         req.AutoEvo,
		IsEnabled:       parent.IsEnabled,
		UpdateStatus:    normalizedSkillUpdateStatus(parent.UpdateStatus),
		CreateUserID:    userID,
		CreateUserName:  userName,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		return orm.SkillResource{}, err
	}
	return row, nil
}

func updateSkill(ctx context.Context, db *gorm.DB, userID, userName, skillID string, req updateSkillRequest) error {
	var row orm.SkillResource
	if err := db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", skillID, userID).Take(&row).Error; err != nil {
		return err
	}
	if req.AutoEvo != nil && evolution.HasAutoEvoWorker(evolution.AutoEvoWorkerKey(evolution.ResourceTypeSkill, row.ID)) {
		return errAutoEvoTaskRunning
	}
	if row.NodeType == evolution.SkillNodeTypeParent {
		return updateParentSkill(ctx, db, userID, userName, &row, req)
	}
	return updateChildSkill(ctx, db, userID, &row, req)
}

func DeleteSkill(ctx context.Context, db *gorm.DB, userID, skillID string) error {
	var row orm.SkillResource
	if err := db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", skillID, userID).Take(&row).Error; err != nil {
		return err
	}
	if row.NodeType == evolution.SkillNodeTypeParent {
		return deleteParentSkill(ctx, db, userID, &row)
	}
	return deleteChildSkill(ctx, db, &row)
}

func deleteParentSkill(ctx context.Context, db *gorm.DB, userID string, row *orm.SkillResource) error {
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var children []orm.SkillResource
		if err := tx.Where("owner_user_id = ? AND node_type = ? AND category = ? AND parent_skill_name = ?", userID, evolution.SkillNodeTypeChild, row.Category, row.SkillName).Find(&children).Error; err != nil {
			return err
		}
		resourceKeys := append(skillSuggestionResourceKeys(children), skillSuggestionResourceKey(*row))
		resourceKeys = compactStrings(resourceKeys)
		if len(resourceKeys) > 0 {
			if err := tx.Where("user_id = ? AND resource_type = ? AND resource_key IN ?", userID, evolution.ResourceTypeSkill, resourceKeys).Delete(&orm.ResourceSuggestion{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("owner_user_id = ? AND node_type = ? AND category = ? AND parent_skill_name = ?", userID, evolution.SkillNodeTypeChild, row.Category, row.SkillName).Delete(&orm.SkillResource{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ? AND owner_user_id = ?", row.ID, userID).Delete(&orm.SkillResource{}).Error
	}); err != nil {
		return err
	}
	return nil
}

func deleteChildSkill(ctx context.Context, db *gorm.DB, row *orm.SkillResource) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if resourceKey := skillSuggestionResourceKey(*row); resourceKey != "" {
			if err := tx.Where("user_id = ? AND resource_type = ? AND resource_key = ?", row.OwnerUserID, evolution.ResourceTypeSkill, resourceKey).Delete(&orm.ResourceSuggestion{}).Error; err != nil {
				return err
			}
		}
		return tx.Where("id = ? AND owner_user_id = ?", row.ID, row.OwnerUserID).Delete(&orm.SkillResource{}).Error
	})
}

func enableParentSkillAutoEvoWithDiscardedDraft(ctx context.Context, db *gorm.DB, row *orm.SkillResource) error {
	now := time.Now()
	update := map[string]any{
		"auto_evo":              true,
		"auto_evo_generation":   gorm.Expr("auto_evo_generation + 1"),
		"auto_evo_apply_status": evolution.AutoEvoApplyStatusIdle,
		"auto_evo_error":        "",
		"auto_evo_finished_at":  nil,
		"draft_content":         "",
		"draft_source_version":  0,
		"draft_status":          "",
		"draft_updated_at":      nil,
		"update_status":         evolution.UpdateStatusUpToDate,
		"updated_at":            now,
		"ext":                   evolution.WithDraftSuggestionIDs(row.Ext, nil),
	}
	if err := db.WithContext(ctx).Model(&orm.SkillResource{}).Where("id = ?", row.ID).Updates(update).Error; err != nil {
		return err
	}
	_ = db.WithContext(ctx).Model(&orm.SkillResource{}).
		Where("owner_user_id = ? AND node_type = ? AND category = ? AND parent_skill_name = ?",
			row.OwnerUserID, evolution.SkillNodeTypeChild, row.Category, row.SkillName).
		Updates(map[string]any{"update_status": evolution.UpdateStatusUpToDate, "updated_at": now}).Error
	var refreshed orm.SkillResource
	if err := db.WithContext(ctx).Where("id = ?", row.ID).Take(&refreshed).Error; err != nil {
		return err
	}
	if err := ensureSkillAutoEvolutionScheduled(refreshed); err != nil {
		appLog.Logger.Warn().Err(err).Str("skill_id", row.ID).Msg("auto_evo schedule on PATCH failed")
	}
	return nil
}

func updateParentSkill(ctx context.Context, db *gorm.DB, userID, userName string, row *orm.SkillResource, req updateSkillRequest) error {
	pendingDraft := strings.TrimSpace(row.DraftStatus) == "pending_confirm"
	if pendingDraft && req.AutoEvo != nil && *req.AutoEvo {
		if err := ensureNoPendingRemoveSuggestionForAutoEvo(ctx, db, userID, *row); err != nil {
			return err
		}
		return enableParentSkillAutoEvoWithDiscardedDraft(ctx, db, row)
	}
	if pendingDraft {
		return errors.New("parent skill has pending_confirm draft")
	}
	if req.ParentSkillID != nil {
		return errors.New("parent_skill_id cannot be updated")
	}
	if req.ParentSkillName != nil {
		return errors.New("parent_skill_name cannot be updated")
	}
	if req.AutoEvo != nil && *req.AutoEvo {
		if err := ensureNoPendingRemoveSuggestionForAutoEvo(ctx, db, userID, *row); err != nil {
			return err
		}
	}
	currentContent, err := storedSkillContent(*row)
	if err != nil {
		return err
	}
	currentBody, err := parentSkillBody(currentContent)
	if err != nil {
		return err
	}
	oldCategory := row.Category
	oldName := row.SkillName
	newName := row.SkillName
	if req.Name != nil {
		newName = strings.TrimSpace(*req.Name)
		if err := validatePathSegment(newName); err != nil {
			return err
		}
	}
	newCategory := row.Category
	if req.Category != nil {
		newCategory = strings.TrimSpace(*req.Category)
		if err := validatePathSegment(newCategory); err != nil {
			return err
		}
	}
	newBody := currentBody
	if req.Content != nil {
		newBody = strings.TrimSpace(*req.Content)
	}
	newDescription := row.Description
	if req.Description != nil {
		newDescription = strings.TrimSpace(*req.Description)
	}
	newContent, resolvedDescription, err := buildParentSkillContent(newName, newDescription, newBody)
	if err != nil {
		return err
	}
	newDescription = resolvedDescription
	if oldCategory != newCategory || oldName != newName {
		var count int64
		if oldName != newName {
			if err := db.WithContext(ctx).
				Model(&orm.SkillResource{}).
				Where("owner_user_id = ? AND node_type = ? AND skill_name = ? AND id <> ?", userID, evolution.SkillNodeTypeParent, newName, row.ID).
				Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return gorm.ErrDuplicatedKey
			}
		}
		newRelativePath := parentRelativePath(newCategory, newName)
		if err := db.WithContext(ctx).
			Model(&orm.SkillResource{}).
			Where("owner_user_id = ? AND relative_path = ? AND id <> ?", userID, newRelativePath, row.ID).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return gorm.ErrDuplicatedKey
		}
	}
	row.RelativePath = parentRelativePath(newCategory, newName)

	now := time.Now()
	update := map[string]any{
		"skill_name":    newName,
		"description":   newDescription,
		"category":      newCategory,
		"tags":          row.Tags,
		"relative_path": row.RelativePath,
		"content":       newContent,
		"content_size":  skillContentSize(newContent),
		"mime_type":     mimeTypeForExt("md"),
		"content_hash":  evolution.HashContent(newContent),
		"updated_at":    now,
	}
	if req.Tags != nil {
		update["tags"] = tagsJSON(*req.Tags)
	}
	if req.AutoEvo != nil {
		update["auto_evo"] = *req.AutoEvo
		update["auto_evo_generation"] = gorm.Expr("auto_evo_generation + 1")
		if *req.AutoEvo {
			update["auto_evo_apply_status"] = evolution.AutoEvoApplyStatusIdle
			update["auto_evo_error"] = ""
			update["auto_evo_finished_at"] = nil
		} else {
			update["auto_evo_apply_status"] = evolution.AutoEvoApplyStatusIdle
			update["auto_evo_error"] = ""
			update["auto_evo_started_at"] = nil
			update["auto_evo_finished_at"] = time.Now()
		}
	}
	if req.IsEnabled != nil {
		update["is_enabled"] = *req.IsEnabled
	}
	if err := db.WithContext(ctx).Model(&orm.SkillResource{}).Where("id = ?", row.ID).Updates(update).Error; err != nil {
		return err
	}

	var children []orm.SkillResource
	if err := db.WithContext(ctx).
		Where("owner_user_id = ? AND node_type = ? AND category = ? AND parent_skill_name = ?", userID, evolution.SkillNodeTypeChild, oldCategory, oldName).
		Find(&children).Error; err != nil {
		return err
	}
	for _, child := range children {
		childRelative := childRelativePath(newCategory, newName, child.SkillName, child.FileExt)
		updateChild := map[string]any{
			"category":          newCategory,
			"parent_skill_name": newName,
			"relative_path":     childRelative,
			"updated_at":        now,
		}
		if req.IsEnabled != nil {
			updateChild["is_enabled"] = *req.IsEnabled
		}
		if err := db.WithContext(ctx).Model(&orm.SkillResource{}).Where("id = ?", child.ID).Updates(updateChild).Error; err != nil {
			return err
		}
	}

	if req.AutoEvo != nil {
		var refreshed orm.SkillResource
		if err := db.WithContext(ctx).Where("id = ?", row.ID).Take(&refreshed).Error; err != nil {
			return err
		}
		if *req.AutoEvo {
			if err := ensureSkillAutoEvolutionScheduled(refreshed); err != nil {
				appLog.Logger.Warn().Err(err).Str("skill_id", row.ID).Msg("auto_evo schedule on PATCH failed")
			}
		}
	}

	return nil
}

func updateChildSkill(ctx context.Context, db *gorm.DB, userID string, row *orm.SkillResource, req updateSkillRequest) error {
	if req.Category != nil && strings.TrimSpace(*req.Category) != strings.TrimSpace(row.Category) {
		return errors.New("child skill category is immutable")
	}
	if req.Category != nil || req.IsEnabled != nil {
		return errors.New("child skill only supports name/description/tags/content/file_ext/auto_evo/parent_skill_id updates")
	}
	if req.AutoEvo != nil && *req.AutoEvo {
		if err := ensureNoPendingRemoveSuggestionForAutoEvo(ctx, db, userID, *row); err != nil {
			return err
		}
	}
	currentContent, err := storedSkillContent(*row)
	if err != nil {
		return err
	}
	newName := row.SkillName
	if req.Name != nil {
		newName = strings.TrimSpace(*req.Name)
		if err := validatePathSegment(newName); err != nil {
			return err
		}
	}
	newContent := currentContent
	if req.Content != nil {
		newContent = *req.Content
	}
	newDescription := row.Description
	if req.Description != nil {
		newDescription = strings.TrimSpace(*req.Description)
	}
	newTags := row.Tags
	if req.Tags != nil {
		newTags = tagsJSON(*req.Tags)
	}
	newExt := row.FileExt
	if req.FileExt != nil {
		newExt = normalizeExt(*req.FileExt)
	}
	newCategory := row.Category
	newParentSkillName := row.ParentSkillName
	var newParent *orm.SkillResource
	if req.ParentSkillID != nil || req.ParentSkillName != nil {
		parentSkillID := ""
		if req.ParentSkillID != nil {
			parentSkillID = strings.TrimSpace(*req.ParentSkillID)
			if parentSkillID == "" {
				return errors.New("parent_skill_id required")
			}
		}
		parentSkillName := ""
		if req.ParentSkillName != nil {
			parentSkillName = strings.TrimSpace(*req.ParentSkillName)
			if parentSkillID == "" {
				if parentSkillName == "" {
					return errors.New("parent_skill_name required")
				}
				if err := validatePathSegment(parentSkillName); err != nil {
					return err
				}
			}
		}
		parent, err := resolveParentSkill(ctx, db, userID, parentSkillID, "", parentSkillName)
		if err != nil {
			return err
		}
		newParent = &parent
		newCategory = parent.Category
		newParentSkillName = parent.SkillName
	}
	newRelative := row.RelativePath
	if newName != row.SkillName || newExt != row.FileExt || newCategory != row.Category || newParentSkillName != row.ParentSkillName {
		newRelative = childRelativePath(newCategory, newParentSkillName, newName, newExt)
		var count int64
		if err := db.WithContext(ctx).
			Model(&orm.SkillResource{}).
			Where("owner_user_id = ? AND relative_path = ? AND id <> ?", userID, newRelative, row.ID).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return gorm.ErrDuplicatedKey
		}
	}
	update := map[string]any{
		"skill_name":        newName,
		"category":          newCategory,
		"parent_skill_name": newParentSkillName,
		"file_ext":          newExt,
		"relative_path":     newRelative,
		"description":       newDescription,
		"tags":              newTags,
		"content":           newContent,
		"content_size":      skillContentSize(newContent),
		"mime_type":         mimeTypeForExt(newExt),
		"content_hash":      evolution.HashContent(newContent),
		"updated_at":        time.Now(),
	}
	if newParent != nil {
		update["is_enabled"] = newParent.IsEnabled
		update["update_status"] = normalizedSkillUpdateStatus(newParent.UpdateStatus)
	}
	if req.AutoEvo != nil {
		update["auto_evo"] = *req.AutoEvo
		update["auto_evo_generation"] = gorm.Expr("auto_evo_generation + 1")
		if *req.AutoEvo {
			update["auto_evo_apply_status"] = evolution.AutoEvoApplyStatusIdle
			update["auto_evo_error"] = ""
			update["auto_evo_finished_at"] = nil
		} else {
			update["auto_evo_apply_status"] = evolution.AutoEvoApplyStatusIdle
			update["auto_evo_error"] = ""
			update["auto_evo_started_at"] = nil
			update["auto_evo_finished_at"] = time.Now()
		}
	}
	if err := db.WithContext(ctx).Model(&orm.SkillResource{}).Where("id = ?", row.ID).Updates(update).Error; err != nil {
		return err
	}

	if req.AutoEvo != nil {
		var refreshed orm.SkillResource
		if err := db.WithContext(ctx).Where("id = ?", row.ID).Take(&refreshed).Error; err != nil {
			return err
		}
		if *req.AutoEvo {
			if err := ensureSkillAutoEvolutionScheduled(refreshed); err != nil {
				appLog.Logger.Warn().Err(err).Str("skill_id", row.ID).Msg("auto_evo schedule on PATCH failed for child skill")
			}
		}
	}

	return nil
}

func parentListResponse(parent orm.SkillResource, children []orm.SkillResource, suggestionStatesByKey map[string]skillSuggestionState) map[string]any {
	parentSuggestionState := canonicalSkillSuggestionState(suggestionStatesByKey[skillSuggestionResourceKey(parent)])
	childItems := make([]map[string]any, 0, len(children))
	sort.Slice(children, func(i, j int) bool { return children[i].CreatedAt.Before(children[j].CreatedAt) })
	for _, child := range children {
		childItems = append(childItems, childListResponse(parent, child, suggestionStatesByKey))
	}
	return map[string]any{
		"skill_id":                       parent.ID,
		"name":                           parent.SkillName,
		"description":                    parent.Description,
		"category":                       parent.Category,
		"tags":                           parseTags(parent.Tags),
		"auto_evo":                       parent.AutoEvo,
		"auto_evo_apply_status":          evolution.NormalizeAutoEvoApplyStatus(parent.AutoEvoApplyStatus),
		"auto_evo_generation":            parent.AutoEvoGeneration,
		"auto_evo_error":                 parent.AutoEvoError,
		"is_enabled":                     parent.IsEnabled,
		"update_status":                  normalizedSkillUpdateStatus(parent.UpdateStatus),
		"has_pending_review_suggestions": parentSuggestionState.Status == evolution.SuggestionStatusPendingReview,
		"has_pending_remove_suggestion":  parentSuggestionState.HasPendingRemove,
		"suggestion_status":              parentSuggestionState.Status,
		"node_type":                      parent.NodeType,
		"builtin_skill_uid":              "",
		"origin_builtin_skill_uid":       parent.OriginBuiltinSkillUID,
		"is_builtin_template":            false,
		"activation_status":              builtinActivationStatus(parent.OriginBuiltinSkillUID),
		"readonly":                       false,
		"children":                       childItems,
	}
}

func childListResponse(parent, child orm.SkillResource, suggestionStatesByKey map[string]skillSuggestionState) map[string]any {
	childSuggestionState := canonicalSkillSuggestionState(suggestionStatesByKey[skillSuggestionResourceKey(child)])
	return map[string]any{
		"skill_id":                       child.ID,
		"name":                           child.SkillName,
		"description":                    child.Description,
		"category":                       parent.Category,
		"tags":                           parseTags(child.Tags),
		"parent_id":                      parent.ID,
		"parent_skill_id":                parent.ID,
		"parent_skill_name":              parent.SkillName,
		"file_ext":                       child.FileExt,
		"auto_evo":                       child.AutoEvo,
		"auto_evo_apply_status":          evolution.NormalizeAutoEvoApplyStatus(child.AutoEvoApplyStatus),
		"auto_evo_generation":            child.AutoEvoGeneration,
		"auto_evo_error":                 child.AutoEvoError,
		"is_enabled":                     parent.IsEnabled,
		"update_status":                  normalizedSkillUpdateStatus(parent.UpdateStatus),
		"has_pending_review_suggestions": childSuggestionState.Status == evolution.SuggestionStatusPendingReview,
		"has_pending_remove_suggestion":  childSuggestionState.HasPendingRemove,
		"suggestion_status":              childSuggestionState.Status,
		"node_type":                      child.NodeType,
		"builtin_skill_uid":              "",
		"origin_builtin_skill_uid":       child.OriginBuiltinSkillUID,
		"is_builtin_template":            false,
		"activation_status":              builtinActivationStatus(child.OriginBuiltinSkillUID),
		"readonly":                       false,
	}
}

type skillListParentEntry struct {
	parent   orm.SkillResource
	children []orm.SkillResource
	builtin  *builtinSkill
}

func paginateSkillListParents(entries []skillListParentEntry, page, pageSize int) []skillListParentEntry {
	total := len(entries)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	return entries[start:end]
}

type skillSuggestionState struct {
	Status           string
	HasPendingRemove bool
}

func canonicalSkillSuggestionState(state skillSuggestionState) skillSuggestionState {
	state.Status = evolution.CanonicalSuggestionStatus(state.Status)
	return state
}

func mergeSkillSuggestionState(current skillSuggestionState, status, action string) skillSuggestionState {
	current.Status = evolution.MergeSuggestionStatus(current.Status, status)
	if strings.TrimSpace(action) == evolution.SuggestionActionRemove && strings.TrimSpace(status) == evolution.SuggestionStatusPendingReview {
		current.HasPendingRemove = true
	}
	return current
}

func loadSuggestionStatesByKey(ctx context.Context, db *gorm.DB, userID string, skillRows []orm.SkillResource) (map[string]skillSuggestionState, error) {
	targetKeys := make(map[string]struct{}, len(skillRows))
	keys := make([]string, 0, len(skillRows))
	for _, row := range skillRows {
		key := skillSuggestionResourceKey(row)
		if key == "" {
			continue
		}
		targetKeys[key] = struct{}{}
		keys = append(keys, key)
	}
	keys = compactStrings(keys)
	if len(keys) == 0 {
		return map[string]skillSuggestionState{}, nil
	}

	var rows []struct {
		ResourceKey     string `gorm:"column:resource_key"`
		RelativePath    string `gorm:"column:relative_path"`
		Category        string `gorm:"column:category"`
		ParentSkillName string `gorm:"column:parent_skill_name"`
		SkillName       string `gorm:"column:skill_name"`
		Status          string `gorm:"column:status"`
		Action          string `gorm:"column:action"`
	}
	query := db.WithContext(ctx).
		Model(&orm.ResourceSuggestion{}).
		Select("resource_key", "relative_path", "category", "parent_skill_name", "skill_name", "status", "action").
		Where("user_id = ? AND resource_type = ? AND status IN ?",
			strings.TrimSpace(userID),
			evolution.ResourceTypeSkill,
			evolution.VisibleSuggestionStatuses(),
		)
	query = query.Where("resource_key IN ?", keys)
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}

	result := make(map[string]skillSuggestionState, len(rows))
	for _, row := range rows {
		key := strings.TrimSpace(row.ResourceKey)
		if _, ok := targetKeys[key]; ok {
			result[key] = mergeSkillSuggestionState(result[key], row.Status, row.Action)
		}
	}
	return result, nil
}

func skillSuggestionResourceKeys(rows []orm.SkillResource) []string {
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		key := skillSuggestionResourceKey(row)
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	return compactStrings(keys)
}

func skillSuggestionResourceKey(row orm.SkillResource) string {
	return evolution.SkillSuggestionResourceKey(row)
}

func containsAllTags(have, need []string) bool {
	if len(need) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(have))
	for _, item := range have {
		set[strings.TrimSpace(item)] = struct{}{}
	}
	for _, item := range need {
		if _, ok := set[strings.TrimSpace(item)]; !ok {
			return false
		}
	}
	return true
}

func parsePositiveInt(raw string, fallback int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	var value int
	_, err := fmt.Sscanf(raw, "%d", &value)
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func suggestionIDs(rows []orm.ResourceSuggestion) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.ID) != "" {
			out = append(out, strings.TrimSpace(row.ID))
		}
	}
	return out
}

func hasPendingRemoveSuggestion(rows []orm.ResourceSuggestion) bool {
	for _, row := range rows {
		if strings.TrimSpace(row.Action) == evolution.SuggestionActionRemove {
			return true
		}
	}
	return false
}

func disableSkillAutoEvoForPendingRemove(ctx context.Context, db *gorm.DB, row orm.SkillResource) error {
	now := time.Now()
	update := map[string]any{
		"auto_evo":              false,
		"auto_evo_apply_status": evolution.AutoEvoApplyStatusIdle,
		"auto_evo_error":        "",
		"auto_evo_started_at":   nil,
		"auto_evo_finished_at":  now,
		"auto_evo_generation":   gorm.Expr("auto_evo_generation + 1"),
		"updated_at":            now,
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&orm.SkillResource{}).Where("id = ?", row.ID).Updates(update).Error; err != nil {
			return err
		}
		if row.NodeType == evolution.SkillNodeTypeParent {
			return tx.Model(&orm.SkillResource{}).
				Where("owner_user_id = ? AND node_type = ? AND category = ? AND parent_skill_name = ?",
					row.OwnerUserID, evolution.SkillNodeTypeChild, row.Category, row.SkillName).
				Updates(update).Error
		}
		return nil
	})
}

func ensureNoPendingRemoveSuggestionForAutoEvo(ctx context.Context, db *gorm.DB, userID string, row orm.SkillResource) error {
	resourceKey := evolution.SkillSuggestionResourceKey(row)
	if resourceKey == "" {
		return nil
	}
	pending, err := evolution.LoadAutoApplicableSuggestions(ctx, db, userID, evolution.ResourceTypeSkill, resourceKey)
	if err != nil {
		return err
	}
	if !hasPendingRemoveSuggestion(pending) {
		return nil
	}
	if err := disableSkillAutoEvoForPendingRemove(ctx, db, row); err != nil {
		return err
	}
	return errPendingRemoveSuggestion
}

func toAlgoSuggestions(rows []orm.ResourceSuggestion) []algo.Suggestion {
	out := make([]algo.Suggestion, 0, len(rows))
	for _, row := range rows {
		out = append(out, algo.Suggestion{Title: row.Title, Content: row.Content, Reason: row.Reason})
	}
	return out
}

func applySkillAutoEvolution(ctx context.Context, db *gorm.DB, row orm.SkillResource) (bool, error) {
	resourceKey := evolution.SkillSuggestionResourceKey(row)
	if resourceKey == "" {
		return false, nil
	}

	pending, err := evolution.LoadAutoApplicableSuggestions(ctx, db, row.OwnerUserID, evolution.ResourceTypeSkill, resourceKey)
	if err != nil {
		return false, err
	}
	if len(pending) == 0 {
		return false, nil
	}
	if hasPendingRemoveSuggestion(pending) {
		if err := disableSkillAutoEvoForPendingRemove(ctx, db, row); err != nil {
			return false, err
		}
		return false, nil
	}

	content, contentErr := storedSkillContent(row)
	if contentErr != nil {
		return false, contentErr
	}

	generated, genErr := algo.GenerateSkill(ctx, algo.SkillGenerateRequest{
		Content:     content,
		Suggestions: toAlgoSuggestions(pending),
	})
	if genErr != nil {
		return false, genErr
	}
	if strings.TrimSpace(generated) == "" {
		return false, errors.New("auto_evo generated empty content")
	}

	update := map[string]any{
		"content":               generated,
		"content_hash":          evolution.HashContent(generated),
		"content_size":          skillContentSize(generated),
		"mime_type":             mimeTypeForExt(row.FileExt),
		"version":               row.Version + 1,
		"draft_content":         "",
		"draft_source_version":  0,
		"draft_status":          "",
		"draft_updated_at":      nil,
		"update_status":         evolution.UpdateStatusUpToDate,
		"auto_evo_apply_status": evolution.AutoEvoApplyStatusRunning,
		"auto_evo_error":        "",
		"updated_at":            time.Now(),
		"ext":                   evolution.WithDraftSuggestionIDs(row.Ext, nil),
	}
	if row.NodeType == evolution.SkillNodeTypeParent {
		description, valErr := validateParentSkillContent(row.SkillName, "", generated)
		if valErr != nil {
			return false, valErr
		}
		update["description"] = description
	}

	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&orm.SkillResource{}).
			Where("id = ? AND version = ? AND auto_evo = ? AND auto_evo_generation = ?",
				row.ID, row.Version, true, row.AutoEvoGeneration).
			Updates(update)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errAutoEvoApplyConflict
		}

		if row.NodeType == evolution.SkillNodeTypeParent {
			if childErr := tx.Model(&orm.SkillResource{}).
				Where("owner_user_id = ? AND node_type = ? AND category = ? AND parent_skill_name = ?",
					row.OwnerUserID, evolution.SkillNodeTypeChild, row.Category, row.SkillName).
				Updates(map[string]any{
					"update_status": evolution.UpdateStatusUpToDate,
					"updated_at":    time.Now(),
				}).Error; childErr != nil {
				return childErr
			}
		}

		return evolution.UpdateSuggestionStatus(ctx, tx, suggestionIDs(pending), evolution.SuggestionStatusApplied)
	}); err != nil {
		if errors.Is(err, errAutoEvoApplyConflict) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func ensureSkillAutoEvolutionScheduled(row orm.SkillResource) error {
	if !row.AutoEvo {
		return nil
	}
	workerKey := evolution.AutoEvoWorkerKey(evolution.ResourceTypeSkill, row.ID)
	if !evolution.TryAcquireAutoEvoWorker(workerKey) {
		return nil
	}

	db := store.DB()
	if db == nil {
		evolution.ReleaseAutoEvoWorker(workerKey)
		return errors.New("store not initialized")
	}

	var latest orm.SkillResource
	if err := db.WithContext(context.Background()).Where("id = ?", row.ID).Take(&latest).Error; err != nil {
		evolution.ReleaseAutoEvoWorker(workerKey)
		return err
	}
	if !latest.AutoEvo {
		evolution.ReleaseAutoEvoWorker(workerKey)
		return nil
	}

	pending, err := evolution.LoadAutoApplicableSuggestions(context.Background(), db, latest.OwnerUserID, evolution.ResourceTypeSkill, evolution.SkillSuggestionResourceKey(latest))
	if err != nil {
		evolution.ReleaseAutoEvoWorker(workerKey)
		return err
	}
	if len(pending) == 0 {
		_ = db.WithContext(context.Background()).Model(&orm.SkillResource{}).
			Where("id = ?", latest.ID).
			Updates(map[string]any{
				"auto_evo_apply_status": evolution.AutoEvoApplyStatusIdle,
				"auto_evo_error":        "",
				"auto_evo_finished_at":  time.Now(),
				"updated_at":            time.Now(),
			}).Error
		evolution.ReleaseAutoEvoWorker(workerKey)
		return nil
	}
	if hasPendingRemoveSuggestion(pending) {
		if err := disableSkillAutoEvoForPendingRemove(context.Background(), db, latest); err != nil {
			evolution.ReleaseAutoEvoWorker(workerKey)
			return err
		}
		evolution.ReleaseAutoEvoWorker(workerKey)
		return nil
	}

	now := time.Now()
	if err := db.WithContext(context.Background()).Model(&orm.SkillResource{}).
		Where("id = ?", latest.ID).
		Updates(map[string]any{
			"auto_evo_apply_status": evolution.AutoEvoApplyStatusRunning,
			"auto_evo_started_at":   now,
			"auto_evo_finished_at":  nil,
			"auto_evo_error":        "",
			"updated_at":            now,
		}).Error; err != nil {
		evolution.ReleaseAutoEvoWorker(workerKey)
		return err
	}

	go runSkillAutoEvolutionLoop(latest.ID, workerKey)
	return nil
}

func runSkillAutoEvolutionLoop(skillID, workerKey string) {
	defer evolution.ReleaseAutoEvoWorker(workerKey)

	ctx := context.Background()
	db := store.DB()
	if db == nil {
		return
	}

	for {
		var row orm.SkillResource
		if err := db.WithContext(ctx).Where("id = ?", skillID).Take(&row).Error; err != nil {
			return
		}
		if !row.AutoEvo {
			return
		}

		pending, err := evolution.LoadAutoApplicableSuggestions(ctx, db, row.OwnerUserID, evolution.ResourceTypeSkill, evolution.SkillSuggestionResourceKey(row))
		if err != nil {
			_ = db.WithContext(ctx).Model(&orm.SkillResource{}).Where("id = ?", row.ID).Updates(map[string]any{
				"auto_evo_apply_status": evolution.AutoEvoApplyStatusFailed,
				"auto_evo_error":        err.Error(),
				"auto_evo_finished_at":  time.Now(),
				"updated_at":            time.Now(),
			}).Error
			return
		}
		if len(pending) == 0 {
			_ = db.WithContext(ctx).Model(&orm.SkillResource{}).Where("id = ?", row.ID).Updates(map[string]any{
				"auto_evo_apply_status": evolution.AutoEvoApplyStatusIdle,
				"auto_evo_error":        "",
				"auto_evo_finished_at":  time.Now(),
				"updated_at":            time.Now(),
			}).Error
			return
		}
		if hasPendingRemoveSuggestion(pending) {
			_ = disableSkillAutoEvoForPendingRemove(ctx, db, row)
			return
		}

		generation := row.AutoEvoGeneration
		applied, err := applySkillAutoEvolution(ctx, db, row)
		if err != nil {
			_ = db.WithContext(ctx).Model(&orm.SkillResource{}).Where("id = ?", row.ID).Updates(map[string]any{
				"auto_evo_apply_status": evolution.AutoEvoApplyStatusFailed,
				"auto_evo_error":        err.Error(),
				"auto_evo_finished_at":  time.Now(),
				"updated_at":            time.Now(),
			}).Error
			return
		}
		if !applied {
			var latest orm.SkillResource
			if reloadErr := db.WithContext(ctx).Where("id = ?", row.ID).Take(&latest).Error; reloadErr != nil {
				return
			}
			if !latest.AutoEvo {
				return
			}
			if latest.AutoEvoGeneration != generation {
				continue
			}
		}
	}
}

func replySkillError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ReplyErr(w, "skill not found", http.StatusNotFound)
	case errors.Is(err, gorm.ErrDuplicatedKey):
		common.ReplyErr(w, "skill already exists", http.StatusConflict)
	case errors.Is(err, errPendingRemoveSuggestion):
		common.ReplyErr(w, err.Error(), http.StatusConflict)
	case errors.Is(err, errAutoEvoTaskRunning):
		common.ReplyErr(w, err.Error(), http.StatusConflict)
	default:
		message := strings.TrimSpace(err.Error())
		status := http.StatusBadRequest
		if strings.Contains(message, "failed") || strings.Contains(message, "invalid") || strings.Contains(message, "required") || strings.Contains(message, "immutable") || strings.Contains(message, "supports") || strings.Contains(message, "pending_confirm") || strings.Contains(message, "cannot") {
			status = http.StatusBadRequest
		} else {
			status = http.StatusInternalServerError
		}
		common.ReplyErr(w, message, status)
	}
}
