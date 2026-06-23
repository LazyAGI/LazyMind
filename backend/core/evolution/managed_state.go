package evolution

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/resourcechange"
	"lazymind/core/store"
)

const (
	ManagedMemoryTitle     = "智能体工作记忆"
	ManagedPreferenceTitle = "用户画像"
)

type ManagedStateItem struct {
	ResourceID             string                               `json:"resource_id"`
	ResourceType           string                               `json:"resource_type"`
	Title                  string                               `json:"title"`
	Content                string                               `json:"content"`
	AgentPersona           *string                              `json:"agent_persona,omitempty"`
	PreferredName          *string                              `json:"preferred_name,omitempty"`
	ResponseStyle          *string                              `json:"response_style,omitempty"`
	ContentSummary         string                               `json:"content_summary"`
	Version                int64                                `json:"version"`
	LatestVersionChange    *resourcechange.VersionChangeSummary `json:"latest_version_change"`
	HasPendingReviewResult bool                                 `json:"has_pending_review_result"`
	ReviewStatus           string                               `json:"review_status"`
	AutoEvo                bool                                 `json:"auto_evo"`
	AutoEvoApplyStatus     string                               `json:"auto_evo_apply_status"`
	AutoEvoGeneration      int64                                `json:"auto_evo_generation"`
	AutoEvoError           string                               `json:"auto_evo_error"`
}

func ListManagedStates(w http.ResponseWriter, r *http.Request) {
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

	memoryRow, err := LoadSystemMemory(r.Context(), db, userID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "query managed states failed", http.StatusInternalServerError)
		return
	}
	preferenceRow, err := LoadSystemUserPreference(r.Context(), db, userID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "query managed states failed", http.StatusInternalServerError)
		return
	}
	reviewStatuses, err := LoadManagedReviewStatuses(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "query managed states failed", http.StatusInternalServerError)
		return
	}

	items := []ManagedStateItem{
		NewManagedStateItem(ResourceTypeMemory, memoryRow, reviewStatuses[ResourceTypeMemory]),
		NewManagedStateItem(ResourceTypeUserPreference, preferenceRow, reviewStatuses[ResourceTypeUserPreference]),
	}
	if memoryRow != nil {
		summary, err := resourcechange.LatestSummaryForResource(r.Context(), db, userID, orm.ResourceUpdateResourceTypeMemory, memoryRow.ID)
		if err != nil {
			common.ReplyErr(w, "query managed states failed", http.StatusInternalServerError)
			return
		}
		items[0].LatestVersionChange = summary
	}
	if preferenceRow != nil {
		summary, err := resourcechange.LatestSummaryForResource(r.Context(), db, userID, orm.ResourceUpdateResourceTypeUserPreference, preferenceRow.ID)
		if err != nil {
			common.ReplyErr(w, "query managed states failed", http.StatusInternalServerError)
			return
		}
		items[1].LatestVersionChange = summary
	}

	common.ReplyOK(w, map[string]any{"items": items})
}

func LoadSystemMemory(ctx context.Context, db *gorm.DB, userID string) (*orm.SystemMemory, error) {
	var row orm.SystemMemory
	if err := db.WithContext(ctx).
		Where("user_id = ?", strings.TrimSpace(userID)).
		Order("created_at ASC").
		Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func LoadSystemUserPreference(ctx context.Context, db *gorm.DB, userID string) (*orm.SystemUserPreference, error) {
	var row orm.SystemUserPreference
	if err := db.WithContext(ctx).
		Where("user_id = ?", strings.TrimSpace(userID)).
		Order("created_at ASC").
		Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func NewManagedStateItem(resourceType string, row any, reviewStatus string) ManagedStateItem {
	reviewStatus = CanonicalReviewStatus(reviewStatus)
	item := ManagedStateItem{
		ResourceType:           strings.TrimSpace(resourceType),
		Title:                  ManagedStateTitle(resourceType),
		HasPendingReviewResult: reviewStatus == ReviewStatusPending,
		ReviewStatus:           reviewStatus,
	}
	switch typed := row.(type) {
	case *orm.SystemMemory:
		if typed != nil {
			item.ResourceID = strings.TrimSpace(typed.ID)
			item.Content = typed.Content
			item.ContentSummary = ManagedStateSummary(typed.Content)
			item.Version = typed.Version
			item.AutoEvo = typed.AutoEvo
			item.AutoEvoApplyStatus = NormalizeAutoEvoApplyStatus(typed.AutoEvoApplyStatus)
			item.AutoEvoGeneration = typed.AutoEvoGeneration
			item.AutoEvoError = typed.AutoEvoError
		}
	case *orm.SystemUserPreference:
		if typed != nil {
			item.ResourceID = strings.TrimSpace(typed.ID)
			item.Content = typed.Content
			item.AgentPersona = stringPtr(typed.AgentPersona)
			item.PreferredName = stringPtr(typed.PreferredName)
			item.ResponseStyle = stringPtr(typed.ResponseStyle)
			item.ContentSummary = ManagedStateSummary(typed.Content)
			item.Version = typed.Version
			item.AutoEvo = typed.AutoEvo
			item.AutoEvoApplyStatus = NormalizeAutoEvoApplyStatus(typed.AutoEvoApplyStatus)
			item.AutoEvoGeneration = typed.AutoEvoGeneration
			item.AutoEvoError = typed.AutoEvoError
		}
	}
	return item
}

func stringPtr(value string) *string {
	return &value
}

const (
	ReviewStatusPending = "pending"
	ReviewStatusNone    = "none"
)

func CanonicalReviewStatus(status string) string {
	if strings.TrimSpace(status) == ReviewStatusPending {
		return ReviewStatusPending
	}
	return ReviewStatusNone
}

func LoadManagedReviewStatuses(ctx context.Context, db *gorm.DB, userID string) (map[string]string, error) {
	var reviewRows []struct {
		Target string `gorm:"column:target"`
	}
	if err := db.WithContext(ctx).
		Model(&orm.MemoryReviewResult{}).
		Select("target").
		Where("user_id = ? AND state = ? AND review_status = ? AND target IN ?",
			strings.TrimSpace(userID),
			"success",
			ReviewStatusPending,
			[]string{ResourceTypeMemory, ResourceTypeUserPreference},
		).
		Find(&reviewRows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]string, len(reviewRows))
	for _, row := range reviewRows {
		resourceType := strings.TrimSpace(row.Target)
		if resourceType == "" {
			continue
		}
		result[resourceType] = ReviewStatusPending
	}
	return result, nil
}

func ManagedReviewStatusForResource(ctx context.Context, db *gorm.DB, userID, resourceType string) (string, error) {
	statuses, err := LoadManagedReviewStatuses(ctx, db, userID)
	if err != nil {
		return ReviewStatusNone, err
	}
	return CanonicalReviewStatus(statuses[strings.TrimSpace(resourceType)]), nil
}

func ManagedStateTitle(resourceType string) string {
	switch strings.TrimSpace(resourceType) {
	case ResourceTypeMemory:
		return ManagedMemoryTitle
	case ResourceTypeUserPreference:
		return ManagedPreferenceTitle
	default:
		return strings.TrimSpace(resourceType)
	}
}

func ManagedStateSummary(content string) string {
	if fields := strings.Fields(content); len(fields) > 0 {
		return strings.Join(fields, " ")
	}
	return ""
}
