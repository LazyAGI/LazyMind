package resourcechange

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type versionResponse struct {
	ID            string    `json:"id"`
	ResourceType  string    `json:"resource_type"`
	ResourceID    string    `json:"resource_id"`
	UserID        string    `json:"user_id"`
	ChangeSource  string    `json:"change_source"`
	FromVersion   int64     `json:"from_version"`
	ToVersion     int64     `json:"to_version"`
	SourceRefType string    `json:"source_ref_type"`
	SourceRefID   string    `json:"source_ref_id"`
	BeforeContent string    `json:"before_content"`
	AfterContent  string    `json:"after_content"`
	Diff          string    `json:"diff"`
	CreatedAt     time.Time `json:"created_at"`
}

func ListVersions(w http.ResponseWriter, r *http.Request) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	page := parsePositiveQueryInt(r.URL.Query().Get("page"), 1, 0)
	pageSize := parsePositiveQueryInt(r.URL.Query().Get("page_size"), 20, 100)
	query := db.WithContext(r.Context()).Model(&orm.ResourceVersion{}).Where("user_id = ?", userID)
	if resourceType := strings.TrimSpace(r.URL.Query().Get("resource_type")); resourceType != "" {
		query = query.Where("resource_type = ?", resourceType)
	}
	if resourceID := strings.TrimSpace(r.URL.Query().Get("resource_id")); resourceID != "" {
		query = query.Where("resource_id = ?", resourceID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.ReplyErr(w, "query resource versions failed", http.StatusInternalServerError)
		return
	}
	var rows []orm.ResourceVersion
	if err := query.Order("created_at DESC, id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&rows).Error; err != nil {
		common.ReplyErr(w, "query resource versions failed", http.StatusInternalServerError)
		return
	}
	items := make([]versionResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, versionToResponse(row))
	}
	common.ReplyOK(w, map[string]any{"items": items, "page": page, "page_size": pageSize, "total": total})
}

func GetVersion(w http.ResponseWriter, r *http.Request) {
	db, userID, ok := requestDBAndUser(w, r)
	if !ok {
		return
	}
	versionID := strings.TrimSpace(common.PathVar(r, "version_id"))
	if versionID == "" {
		common.ReplyErr(w, "missing version_id", http.StatusBadRequest)
		return
	}
	var row orm.ResourceVersion
	if err := db.WithContext(r.Context()).Where("id = ? AND user_id = ?", versionID, userID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "resource version not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query resource version failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, versionToResponse(row))
}

func requestDBAndUser(w http.ResponseWriter, r *http.Request) (*gorm.DB, string, bool) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return nil, "", false
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return nil, "", false
	}
	return db, userID, true
}

func versionToResponse(row orm.ResourceVersion) versionResponse {
	return versionResponse{
		ID:            row.ID,
		ResourceType:  row.ResourceType,
		ResourceID:    row.ResourceID,
		UserID:        row.UserID,
		ChangeSource:  row.ChangeSource,
		FromVersion:   row.FromVersion,
		ToVersion:     row.ToVersion,
		SourceRefType: row.SourceRefType,
		SourceRefID:   row.SourceRefID,
		BeforeContent: row.BeforeContent,
		AfterContent:  row.AfterContent,
		Diff:          row.Diff,
		CreatedAt:     row.CreatedAt,
	}
}

func parsePositiveQueryInt(value string, def, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		n = def
	}
	if max > 0 && n > max {
		return max
	}
	return n
}
