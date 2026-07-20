package handler

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	skillservice "lazymind/core/skillv2/service"
)

func MarketTags(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	var rows []struct {
		Tags []byte `gorm:"column:tags"`
	}
	if err := db.WithContext(r.Context()).
		Table("skill_market_items AS market_items").
		Select("market_items.tags AS tags").
		Joins("JOIN skills AS skills ON skills.id = market_items.source_skill_id AND skills.deleted_at IS NULL").
		Where("market_items.status = ?", "published").
		Scan(&rows).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	set := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		for _, tag := range decodeTags(row.Tags) {
			set[tag] = struct{}{}
		}
	}
	tags := make([]string, 0, len(set))
	for tag := range set {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	common.ReplyOK(w, map[string]any{"tags": tags})
}

func MarketList(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = "published"
	}
	var rows []orm.SkillMarketItem
	if err := db.WithContext(r.Context()).
		Where("status = ?", status).
		Order("sort_order ASC").
		Order("updated_at DESC").
		Find(&rows).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	installedByMarketID, err := loadInstalledMarketSkills(r.Context(), db, common.UserID(r))
	if err != nil {
		replyServiceError(w, err)
		return
	}
	keyword := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keyword")))
	tags := marketQueryTags(r)
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		source, err := newSkillService(db).GetSkill(r.Context(), skillservice.GetSkillRequest{SkillID: row.SourceSkillID})
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			replyServiceError(w, err)
			return
		}
		if len(tags) > 0 && !hasAllTags(decodeTags(row.Tags), tags) {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(source.Name+" "+source.SkillName+" "+source.Description), keyword) {
			continue
		}
		items = append(items, marketItemDTO(row, source, installedByMarketID[row.ID]))
	}
	total := len(items)
	page := positiveQueryInt(r, "page", 1)
	pageSize := positiveQueryInt(r, "page_size", 20)
	if pageSize > 100 {
		pageSize = 100
	}
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	common.ReplyOK(w, map[string]any{"items": items[start:end], "page": page, "page_size": pageSize, "total": total})
}

func marketQueryTags(r *http.Request) []string {
	values := append([]string(nil), r.URL.Query()["tags"]...)
	if len(values) == 0 {
		values = append(values, r.URL.Query().Get("category"))
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strings.Split(value, ",")...)
	}
	return compactStrings(parts)
}

func MarketGet(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	marketItemID := firstNonEmpty(common.PathVar(r, "market_item_id"), common.PathVar(r, "item_id"))
	if marketItemID == "" {
		replyError(w, "missing market_item_id", http.StatusBadRequest)
		return
	}
	var item orm.SkillMarketItem
	if err := db.WithContext(r.Context()).Where("id = ? AND status = ?", marketItemID, "published").Take(&item).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	source, err := newSkillService(db).GetSkill(r.Context(), skillservice.GetSkillRequest{SkillID: item.SourceSkillID})
	if err != nil {
		replyServiceError(w, err)
		return
	}
	installedByMarketID, err := loadInstalledMarketSkills(r.Context(), db, common.UserID(r))
	if err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, marketItemDTO(item, source, installedByMarketID[item.ID]))
}

func loadInstalledMarketSkills(ctx context.Context, db *gorm.DB, userID string) (map[string]string, error) {
	installed := map[string]string{}
	if strings.TrimSpace(userID) == "" {
		return installed, nil
	}
	var rows []struct {
		MarketItemID string `gorm:"column:market_item_id"`
		SkillID      string `gorm:"column:skill_id"`
	}
	if err := db.WithContext(ctx).
		Table("skill_market_installs AS installs").
		Select("installs.market_item_id, installs.skill_id").
		Joins("JOIN skills AS skills ON skills.id = installs.skill_id AND skills.owner_user_id = installs.user_id AND skills.deleted_at IS NULL").
		Joins("JOIN skill_market_items AS market_items ON market_items.id = installs.market_item_id AND market_items.source_skill_id <> installs.skill_id").
		Where("installs.user_id = ?", userID).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.MarketItemID != "" && row.SkillID != "" {
			installed[row.MarketItemID] = row.SkillID
		}
	}
	return installed, nil
}

func marketItemDTO(item orm.SkillMarketItem, source skillservice.SkillDetail, installedSkillID string) map[string]any {
	return map[string]any{
		"id":                 item.ID,
		"market_item_id":     item.ID,
		"source_skill_id":    item.SourceSkillID,
		"status":             item.Status,
		"tags":               decodeTags(item.Tags),
		"installed":          installedSkillID != "",
		"installed_skill_id": installedSkillID,
		"icon":               item.Icon,
		"sort_order":         item.SortOrder,
		"version_note":       item.VersionNote,
		"published_at":       item.PublishedAt,
		"created_at":         item.CreatedAt,
		"updated_at":         item.UpdatedAt,
		"source":             skillDetailDTO(source),
	}
}
