package handler

import (
	"net/http"
	"strings"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	skillservice "lazymind/core/skillv2/service"
)

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
	keyword := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keyword")))
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		source, err := newSkillService(db).GetSkill(r.Context(), skillservice.GetSkillRequest{SkillID: row.SourceSkillID})
		if err != nil {
			replyServiceError(w, err)
			return
		}
		if category != "" && source.Category != category {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(source.Name+" "+source.SkillName+" "+source.Description), keyword) {
			continue
		}
		items = append(items, marketItemDTO(row, source))
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
	common.ReplyOK(w, marketItemDTO(item, source))
}

func marketItemDTO(item orm.SkillMarketItem, source skillservice.SkillDetail) map[string]any {
	return map[string]any{
		"id":              item.ID,
		"market_item_id":  item.ID,
		"source_skill_id": item.SourceSkillID,
		"status":          item.Status,
		"icon":            item.Icon,
		"sort_order":      item.SortOrder,
		"version_note":    item.VersionNote,
		"published_at":    item.PublishedAt,
		"created_at":      item.CreatedAt,
		"updated_at":      item.UpdatedAt,
		"source":          skillDetailDTO(source),
	}
}
