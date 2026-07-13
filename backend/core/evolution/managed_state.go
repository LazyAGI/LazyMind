package evolution

import (
	"context"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/preferencefile"
	"lazymind/core/store"
)

const (
	ManagedMemoryTitle     = "智能体工作记忆"
	ManagedPreferenceTitle = "用户画像"
)

type ManagedStateItem struct {
	ResourceID             string                `json:"resource_id"`
	ResourceType           string                `json:"resource_type"`
	Title                  string                `json:"title"`
	Content                string                `json:"content"`
	AgentPersona           *string               `json:"agent_persona,omitempty"`
	PreferredName          *string               `json:"preferred_name,omitempty"`
	ResponseStyle          *string               `json:"response_style,omitempty"`
	ContentSummary         string                `json:"content_summary"`
	Version                int64                 `json:"version"`
	LatestVersionChange    *VersionChangeSummary `json:"latest_version_change"`
	HasPendingReviewResult bool                  `json:"has_pending_review_result"`
	ReviewStatus           string                `json:"review_status"`
	AutoEvo                bool                  `json:"auto_evo"`
	AutoEvoApplyStatus     string                `json:"auto_evo_apply_status"`
	AutoEvoGeneration      int64                 `json:"auto_evo_generation"`
	AutoEvoError           string                `json:"auto_evo_error"`
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

	memoryRow, err := EnsurePersonalResourceContent(r.Context(), db, userID, ResourceTypeMemory)
	if err != nil {
		common.ReplyErr(w, "query managed states failed", http.StatusInternalServerError)
		return
	}
	preferenceRow, err := EnsurePersonalResourceContent(r.Context(), db, userID, ResourceTypeUserPreference)
	if err != nil {
		common.ReplyErr(w, "query managed states failed", http.StatusInternalServerError)
		return
	}
	preference, err := preferencefile.ParseFileContent(preferenceRow.Content)
	if err != nil {
		common.ReplyErr(w, "query managed states failed", http.StatusInternalServerError)
		return
	}

	items := []ManagedStateItem{
		NewManagedStateItem(ResourceTypeMemory, memoryRow, nil),
		NewManagedStateItem(ResourceTypeUserPreference, preferenceRow, &preference),
	}

	common.ReplyOK(w, map[string]any{"items": items})
}

type VersionChangeSummary struct {
	ChangeSource  string `json:"change_source"`
	SourceRefType string `json:"source_ref_type"`
	SourceRefID   string `json:"source_ref_id"`
	ChangedAt     string `json:"changed_at"`
}

func NewManagedStateItem(resourceType string, row *PersonalResourceContent, preference *preferencefile.PreferenceFile) ManagedStateItem {
	item := ManagedStateItem{
		ResourceType:       strings.TrimSpace(resourceType),
		Title:              ManagedStateTitle(resourceType),
		ReviewStatus:       ReviewStatusNone,
		AutoEvoApplyStatus: NormalizeAutoEvoApplyStatus(""),
	}
	if row == nil {
		return item
	}
	item.ResourceID = strings.TrimSpace(row.ResourceID)
	item.Content = row.Content
	item.ContentSummary = ManagedStateSummary(row.Content)
	item.Version = row.Version
	item.LatestVersionChange = row.LatestVersionChange
	item.HasPendingReviewResult = row.HasPendingReviewResult
	item.ReviewStatus = CanonicalReviewStatus(row.ReviewStatus)
	item.AutoEvo = row.AutoEvo
	item.AutoEvoApplyStatus = NormalizeAutoEvoApplyStatus(row.AutoEvoApplyStatus)
	item.AutoEvoGeneration = row.AutoEvoGeneration
	item.AutoEvoError = row.AutoEvoError
	if preference != nil {
		item.Content = preference.Content
		item.AgentPersona = stringPtr(preference.AgentPersona)
		item.PreferredName = stringPtr(preference.PreferredName)
		item.ResponseStyle = stringPtr(preference.ResponseStyle)
		item.ContentSummary = ManagedStateSummary(preference.Content)
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
	out := map[string]string{
		ResourceTypeMemory:         ReviewStatusNone,
		ResourceTypeUserPreference: ReviewStatusNone,
	}
	var rows []struct {
		ResourceType string `gorm:"column:resource_type"`
		Count        int64  `gorm:"column:count"`
	}
	if err := db.WithContext(ctx).
		Table("personal_resources AS pr").
		Select("pr.resource_type, COUNT(s.id) AS count").
		Joins("JOIN personal_resource_review_sessions AS s ON s.resource_id = pr.id AND s.status = ?", "active").
		Where("pr.user_id = ?", strings.TrimSpace(userID)).
		Group("pr.resource_type").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.Count > 0 {
			out[strings.TrimSpace(row.ResourceType)] = ReviewStatusPending
		}
	}
	return out, nil
}

func ManagedReviewStatusForResource(ctx context.Context, db *gorm.DB, userID, resourceType string) (string, error) {
	var count int64
	if err := db.WithContext(ctx).
		Table("personal_resources AS pr").
		Joins("JOIN personal_resource_review_sessions AS s ON s.resource_id = pr.id AND s.status = ?", "active").
		Where("pr.user_id = ? AND pr.resource_type = ?", strings.TrimSpace(userID), strings.TrimSpace(resourceType)).
		Count(&count).Error; err != nil {
		return ReviewStatusNone, err
	}
	if count > 0 {
		return ReviewStatusPending, nil
	}
	return ReviewStatusNone, nil
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
