package knowledge_market

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// MarketList returns the published knowledge market catalog with optional
// category, domain and keyword filters plus pagination.
func MarketList(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	if category != "" && category != "industry" && category != "evaluation" {
		common.ReplyErr(w, "invalid category", http.StatusBadRequest)
		return
	}
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	keyword := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keyword")))

	base := db.WithContext(r.Context()).Where("status = ?", "published")
	if category != "" {
		base = base.Where("category = ?", category)
	}
	if domain != "" {
		base = base.Where("domain = ?", domain)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		base = base.Where(
			"LOWER(name) LIKE ? OR LOWER(description) LIKE ? OR LOWER(domain) LIKE ?",
			like, like, like,
		)
	}

	var total int64
	if err := base.Model(&orm.KnowledgeMarketItem{}).Count(&total).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	page := positiveQueryInt(r, "page", 1)
	pageSize := positiveQueryInt(r, "page_size", 20)
	if pageSize > 100 {
		pageSize = 100
	}

	var rows []orm.KnowledgeMarketItem
	if err := base.
		Order("sort_order ASC").
		Order("updated_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&rows).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, listItemDTO(row))
	}
	common.ReplyOK(w, map[string]any{"items": items, "page": page, "page_size": pageSize, "total": total})
}

// MarketDomains returns the distinct domains of published items grouped by
// category, so clients can scope the domain filter to the active category.
func MarketDomains(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	var rows []struct {
		Category string `gorm:"column:category"`
		Domain   string `gorm:"column:domain"`
	}
	if err := db.WithContext(r.Context()).
		Model(&orm.KnowledgeMarketItem{}).
		Where("status = ?", "published").
		Where("domain <> ''").
		Distinct().
		Order("category ASC").
		Order("domain ASC").
		Find(&rows).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	grouped := map[string][]string{"industry": {}, "evaluation": {}}
	for _, row := range rows {
		grouped[row.Category] = append(grouped[row.Category], row.Domain)
	}
	common.ReplyOK(w, map[string]any{"domains": grouped})
}

// MarketGet returns the full detail of one published knowledge market item.
func MarketGet(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	marketItemID := common.PathVar(r, "market_item_id")
	if marketItemID == "" {
		common.ReplyErr(w, "missing market_item_id", http.StatusBadRequest)
		return
	}
	var item orm.KnowledgeMarketItem
	if err := db.WithContext(r.Context()).
		Where("id = ? AND status = ?", marketItemID, "published").
		Take(&item).Error; err != nil {
		replyServiceError(w, err)
		return
	}
	common.ReplyOK(w, detailDTO(item))
}

// listItemDTO builds the lightweight list representation of a market item.
// tags is returned as-is for display only and never used for filtering.
func listItemDTO(item orm.KnowledgeMarketItem) map[string]any {
	// Version fields are intentionally not exposed: the product shows the
	// install/update state and the user's last actual update time instead of a
	// catalog-maintained version label.
	return map[string]any{
		"id":                item.ID,
		"category":          item.Category,
		"name":              item.Name,
		"description":       item.Description,
		"icon":              item.Icon,
		"domain":            item.Domain,
		"tags":              item.Tags,
		"online_access_url": item.OnlineAccessURL,
		"data_source":       item.DataSource,
		"sort_order":        item.SortOrder,
		"created_at":        item.CreatedAt,
		"updated_at":        item.UpdatedAt,
	}
}

// detailDTO extends the list representation with the full detail fields.
func detailDTO(item orm.KnowledgeMarketItem) map[string]any {
	base := listItemDTO(item)
	base["package_url"] = item.PackageURL
	base["package_revision"] = item.PackageRevision
	base["sample_questions"] = item.SampleQuestions
	return base
}

// requireDB returns the shared core database or fails the request when the
// store has not been initialized yet.
func requireDB(w http.ResponseWriter) (*gorm.DB, bool) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return nil, false
	}
	return db, true
}

// replyServiceError maps known gorm errors to API error responses.
func replyServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ReplyErr(w, "knowledge market item not found", http.StatusNotFound)
	default:
		common.ReplyErr(w, "internal server error", http.StatusInternalServerError)
	}
}

// positiveQueryInt parses a positive integer query parameter, falling back to
// the given default when the value is missing or invalid.
func positiveQueryInt(r *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(key)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
