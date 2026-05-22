package evolution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	appLog "lazymind/core/log"
	"lazymind/core/store"
)

var errInvalidSuggestionFilter = errors.New("invalid suggestion filter")

func ListSuggestions(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	pageSize := parsePositiveInt(r.URL.Query().Get("page_size"), 20)
	if pageSize > 100 {
		pageSize = 100
	}

	query := db.WithContext(r.Context()).Model(&orm.ResourceSuggestion{})
	var err error
	query, err = applySuggestionListFilters(r.Context(), db, query, r.URL.Query())
	if err != nil {
		if errors.Is(err, errInvalidSuggestionFilter) {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		common.ReplyErr(w, "query suggestions failed", http.StatusInternalServerError)
		return
	}
	query = query.Where("status IN ?", VisibleSuggestionStatuses())
	if keyword := strings.TrimSpace(r.URL.Query().Get("keyword")); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("title LIKE ? OR content LIKE ?", like, like)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.ReplyErr(w, "query suggestions failed", http.StatusInternalServerError)
		return
	}

	var rows []orm.ResourceSuggestion
	if err := query.
		Order("CASE WHEN action = 'remove' THEN 0 ELSE 1 END, created_at DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&rows).Error; err != nil {
		common.ReplyErr(w, "query suggestions failed", http.StatusInternalServerError)
		return
	}

	resolver := NewSuggestionOutdatedResolver(db)
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		item, err := suggestionResponse(r.Context(), resolver, row)
		if err != nil {
			common.ReplyErr(w, "resolve suggestion outdated failed", http.StatusInternalServerError)
			return
		}
		items = append(items, item)
	}
	common.ReplyOK(w, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     total,
	})
}

func GetSuggestion(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	id := common.PathVar(r, "id")
	if id == "" {
		common.ReplyErr(w, "missing suggestion id", http.StatusBadRequest)
		return
	}

	var row orm.ResourceSuggestion
	if err := db.WithContext(r.Context()).
		Where("id = ? AND status IN ?", id, VisibleSuggestionStatuses()).
		Take(&row).Error; err != nil {
		common.ReplyErr(w, "suggestion not found", http.StatusNotFound)
		return
	}
	item, err := suggestionResponse(r.Context(), NewSuggestionOutdatedResolver(db), row)
	if err != nil {
		common.ReplyErr(w, "resolve suggestion outdated failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, item)
}

func ApproveSuggestion(w http.ResponseWriter, r *http.Request) {
	reviewSuggestion(w, r, SuggestionStatusAccepted)
}

func RejectSuggestion(w http.ResponseWriter, r *http.Request) {
	reviewSuggestion(w, r, SuggestionStatusRejected)
}

type batchReviewSuggestionsRequest struct {
	IDs []string `json:"ids"`
}

func BatchApproveSuggestions(w http.ResponseWriter, r *http.Request) {
	batchReviewSuggestions(w, r, SuggestionStatusAccepted)
}

func BatchRejectSuggestions(w http.ResponseWriter, r *http.Request) {
	batchReviewSuggestions(w, r, SuggestionStatusRejected)
}

func reviewSuggestion(w http.ResponseWriter, r *http.Request, targetStatus string) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	id := common.PathVar(r, "id")
	if id == "" {
		common.ReplyErr(w, "missing suggestion id", http.StatusBadRequest)
		return
	}

	var row orm.ResourceSuggestion
	if err := db.WithContext(r.Context()).Where("id = ?", id).Take(&row).Error; err != nil {
		common.ReplyErr(w, "suggestion not found", http.StatusNotFound)
		return
	}
	now := time.Now()
	reviewerID := store.UserID(r)
	reviewerName := store.UserName(r)
	update := map[string]any{
		"status":        targetStatus,
		"reviewer_id":   strings.TrimSpace(reviewerID),
		"reviewer_name": strings.TrimSpace(reviewerName),
		"reviewed_at":   now,
		"updated_at":    now,
	}
	if err := db.WithContext(r.Context()).Model(&orm.ResourceSuggestion{}).Where("id = ?", id).Updates(update).Error; err != nil {
		common.ReplyErr(w, "update suggestion failed", http.StatusInternalServerError)
		return
	}
	row.Status = targetStatus
	row.ReviewerID = strings.TrimSpace(reviewerID)
	row.ReviewerName = strings.TrimSpace(reviewerName)
	row.ReviewedAt = &now
	row.UpdatedAt = now
	item, err := suggestionResponse(r.Context(), NewSuggestionOutdatedResolver(db), row)
	if err != nil {
		common.ReplyErr(w, "resolve suggestion outdated failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, item)
}

func batchReviewSuggestions(w http.ResponseWriter, r *http.Request, targetStatus string) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	var req batchReviewSuggestionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.IDs = compactSuggestionIDs(req.IDs)
	if len(req.IDs) == 0 {
		common.ReplyErr(w, "ids required", http.StatusBadRequest)
		return
	}

	var rows []orm.ResourceSuggestion
	if err := db.WithContext(r.Context()).Where("id IN ?", req.IDs).Find(&rows).Error; err != nil {
		common.ReplyErr(w, "query suggestions failed", http.StatusInternalServerError)
		return
	}
	if len(rows) != len(req.IDs) {
		common.ReplyErr(w, "suggestion not found", http.StatusNotFound)
		return
	}

	rowsByID := make(map[string]orm.ResourceSuggestion, len(rows))
	for _, row := range rows {
		rowsByID[row.ID] = row
	}
	orderedRows := make([]orm.ResourceSuggestion, 0, len(req.IDs))
	for _, id := range req.IDs {
		row, ok := rowsByID[id]
		if !ok {
			common.ReplyErr(w, "suggestion not found", http.StatusNotFound)
			return
		}
		orderedRows = append(orderedRows, row)
	}

	now := time.Now()
	reviewerID := strings.TrimSpace(store.UserID(r))
	reviewerName := strings.TrimSpace(store.UserName(r))
	update := map[string]any{
		"status":        targetStatus,
		"reviewer_id":   reviewerID,
		"reviewer_name": reviewerName,
		"reviewed_at":   now,
		"updated_at":    now,
	}
	if err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		return tx.Model(&orm.ResourceSuggestion{}).Where("id IN ?", req.IDs).Updates(update).Error
	}); err != nil {
		common.ReplyErr(w, "update suggestion failed", http.StatusInternalServerError)
		return
	}

	resolver := NewSuggestionOutdatedResolver(db)
	items := make([]map[string]any, 0, len(orderedRows))
	for _, row := range orderedRows {
		row.Status = targetStatus
		row.ReviewerID = reviewerID
		row.ReviewerName = reviewerName
		row.ReviewedAt = &now
		row.UpdatedAt = now

		if row.Action == SuggestionActionRemove && targetStatus == SuggestionStatusAccepted {
			if ApplyRemoveSuggestion != nil {
				if err := ApplyRemoveSuggestion(r.Context(), db, row); err != nil {
					appLog.Logger.Error().
						Err(err).
						Str("suggestion_id", row.ID).
						Str("action", row.Action).
						Msg("apply remove suggestion failed during batch approve")
				}
			}
		}

		item, err := suggestionResponse(r.Context(), resolver, row)
		if err != nil {
			common.ReplyErr(w, "resolve suggestion outdated failed", http.StatusInternalServerError)
			return
		}
		items = append(items, item)
	}
	common.ReplyOK(w, map[string]any{"items": items})
}

func suggestionResponse(ctx context.Context, resolver *SuggestionOutdatedResolver, row orm.ResourceSuggestion) (map[string]any, error) {
	outdated := false
	if resolver != nil {
		var err error
		outdated, err = resolver.Resolve(ctx, row)
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"id":                row.ID,
		"user_id":           row.UserID,
		"resource_type":     row.ResourceType,
		"resource_key":      row.ResourceKey,
		"category":          row.Category,
		"parent_skill_name": row.ParentSkillName,
		"skill_name":        row.SkillName,
		"file_ext":          row.FileExt,
		"relative_path":     row.RelativePath,
		"action":            row.Action,
		"session_id":        row.SessionID,
		"title":             row.Title,
		"content":           row.Content,
		"reason":            row.Reason,
		"full_content":      row.FullContent,
		"status":            row.Status,
		"invalid_reason":    row.InvalidReason,
		"reviewer_id":       row.ReviewerID,
		"reviewer_name":     row.ReviewerName,
		"reviewed_at":       row.ReviewedAt,
		"created_at":        row.CreatedAt,
		"updated_at":        row.UpdatedAt,
		"outdated":          outdated,
	}, nil
}

func applySuggestionListFilters(ctx context.Context, db *gorm.DB, query *gorm.DB, values url.Values) (*gorm.DB, error) {
	if resourceType := strings.TrimSpace(values.Get("resource_type")); resourceType != "" {
		query = query.Where("resource_type = ?", resourceType)
	}
	if resourceKey := strings.TrimSpace(values.Get("resource_key")); resourceKey != "" {
		query = query.Where("resource_key = ?", resourceKey)
	}
	if statuses := filterVisibleStatuses(values["status"]); len(statuses) > 0 {
		query = query.Where("status IN ?", statuses)
	}

	var err error
	query, err = applySuggestionEvolutionFilter(ctx, db, query, values.Get("evolution_id"))
	if err != nil {
		return nil, err
	}
	return query, nil
}

func filterVisibleStatuses(rawStatuses []string) []string {
	if len(rawStatuses) == 0 {
		return nil
	}
	visible := make(map[string]struct{}, len(VisibleSuggestionStatuses()))
	for _, status := range VisibleSuggestionStatuses() {
		visible[status] = struct{}{}
	}

	seen := make(map[string]struct{}, len(rawStatuses))
	out := make([]string, 0, len(rawStatuses))
	for _, status := range rawStatuses {
		normalized := strings.TrimSpace(status)
		if normalized == "" {
			continue
		}
		if _, ok := visible[normalized]; !ok {
			continue
		}
		if _, duplicated := seen[normalized]; duplicated {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func applySuggestionEvolutionFilter(ctx context.Context, db *gorm.DB, query *gorm.DB, evolutionID string) (*gorm.DB, error) {
	evolutionID = strings.TrimSpace(evolutionID)
	if evolutionID == "" {
		return query, nil
	}

	resourceType, resourceID, ok := strings.Cut(evolutionID, ":")
	resourceType = strings.TrimSpace(resourceType)
	resourceID = strings.TrimSpace(resourceID)
	if !ok || resourceType == "" || resourceID == "" {
		return nil, fmt.Errorf("%w: evolution_id must use <resource_type>:<resource_id>", errInvalidSuggestionFilter)
	}

	switch resourceType {
	case ResourceTypeSkill:
		return applySuggestionSkillFilter(ctx, db, query, resourceID)
	case ResourceTypeMemory:
		return applySuggestionMemoryFilter(ctx, db, query, resourceID)
	case ResourceTypeUserPreference:
		return applySuggestionPreferenceFilter(ctx, db, query, resourceID)
	default:
		return nil, fmt.Errorf("%w: unsupported evolution_id resource_type %q", errInvalidSuggestionFilter, resourceType)
	}
}

func applySuggestionSkillFilter(ctx context.Context, db *gorm.DB, query *gorm.DB, skillID string) (*gorm.DB, error) {
	skillID = strings.TrimSpace(skillID)
	if skillID == "" {
		return query, nil
	}

	var skill orm.SkillResource
	err := db.WithContext(ctx).
		Select("id", "category", "parent_skill_name", "skill_name", "relative_path", "node_type").
		Where("id = ?", skillID).
		Take(&skill).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return query.Where("1 = 0"), nil
	}
	if err != nil {
		return nil, err
	}

	resourceKey := SkillSuggestionResourceKey(skill)
	return query.Where("resource_type = ? AND resource_key = ?", ResourceTypeSkill, resourceKey), nil
}

func applySuggestionMemoryFilter(ctx context.Context, db *gorm.DB, query *gorm.DB, memoryID string) (*gorm.DB, error) {
	memoryID = strings.TrimSpace(memoryID)
	if memoryID == "" {
		return query, nil
	}

	var row orm.SystemMemory
	err := db.WithContext(ctx).Select("id", "user_id").Where("id = ?", memoryID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return query.Where("1 = 0"), nil
	}
	if err != nil {
		return nil, err
	}
	query = query.Where(
		"resource_type = ? AND (resource_key = ? OR TRIM(COALESCE(resource_key, '')) = '')",
		ResourceTypeMemory,
		SystemResourceKey(ResourceTypeMemory),
	)
	if strings.TrimSpace(row.UserID) != "" {
		query = query.Where("user_id = ?", strings.TrimSpace(row.UserID))
	}
	return query, nil
}

func applySuggestionPreferenceFilter(ctx context.Context, db *gorm.DB, query *gorm.DB, preferenceID string) (*gorm.DB, error) {
	preferenceID = strings.TrimSpace(preferenceID)
	if preferenceID == "" {
		return query, nil
	}

	var row orm.SystemUserPreference
	err := db.WithContext(ctx).Select("id", "user_id").Where("id = ?", preferenceID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return query.Where("1 = 0"), nil
	}
	if err != nil {
		return nil, err
	}
	query = query.Where(
		"resource_type = ? AND (resource_key = ? OR TRIM(COALESCE(resource_key, '')) = '')",
		ResourceTypeUserPreference,
		SystemResourceKey(ResourceTypeUserPreference),
	)
	if strings.TrimSpace(row.UserID) != "" {
		query = query.Where("user_id = ?", strings.TrimSpace(row.UserID))
	}
	return query, nil
}

func parsePositiveInt(raw string, fallback int) int {
	if raw = strings.TrimSpace(raw); raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

func compactQueryValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
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

func compactSuggestionIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
