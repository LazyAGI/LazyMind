package resourcechange

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func LatestSummaryForResource(ctx context.Context, db *gorm.DB, userID, resourceType, resourceID string) (*VersionChangeSummary, error) {
	summaries, err := LatestSummariesForResources(ctx, db, userID, resourceType, []string{resourceID})
	if err != nil {
		return nil, err
	}
	if summary, ok := summaries[strings.TrimSpace(resourceID)]; ok {
		return &summary, nil
	}
	return nil, nil
}

func LatestSummariesForResources(ctx context.Context, db *gorm.DB, userID, resourceType string, resourceIDs []string) (map[string]VersionChangeSummary, error) {
	ids := compactStrings(resourceIDs)
	out := make(map[string]VersionChangeSummary, len(ids))
	if db == nil || strings.TrimSpace(userID) == "" || strings.TrimSpace(resourceType) == "" || len(ids) == 0 {
		return out, nil
	}
	var rows []orm.ResourceVersion
	if err := db.WithContext(ctx).
		Where("user_id = ? AND resource_type = ? AND resource_id IN ?", strings.TrimSpace(userID), strings.TrimSpace(resourceType), ids).
		Order("resource_id ASC, created_at DESC, id DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		resourceID := strings.TrimSpace(row.ResourceID)
		if resourceID == "" {
			continue
		}
		if _, exists := out[resourceID]; exists {
			continue
		}
		out[resourceID] = VersionChangeSummary{
			ChangeSource:  row.ChangeSource,
			SourceRefType: row.SourceRefType,
			SourceRefID:   row.SourceRefID,
			ChangedAt:     row.CreatedAt,
		}
	}
	return out, nil
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
