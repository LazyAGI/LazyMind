// Package toolpolicy owns the per-user visibility policy for LazyMind agents.
// External MCP authorization deliberately does not use this policy.
package toolpolicy

import (
	"context"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"strings"
)

func Disabled(ctx context.Context, db *gorm.DB, userID string) ([]string, error) {
	if db == nil || strings.TrimSpace(userID) == "" {
		return nil, nil
	}
	var names []string
	err := db.WithContext(ctx).Model(&orm.UserDisabledTool{}).Where("create_user_id = ? AND deleted_at IS NULL", userID).Order("tool_name ASC").Pluck("tool_name", &names).Error
	return names, err
}

func Merge(groups ...[]string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, group := range groups {
		for _, name := range group {
			name = strings.TrimSpace(name)
			if name != "" && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}
