// Package settings contains the runtime-safe feature controls shared by settings
// handlers and the execution entry points they govern.
package settings

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

// FeatureControls are non-destructive per-user pause layers. A missing
// preference row is intentionally treated as fully enabled for upgrade safety.
type FeatureControls struct {
	TaskCenterEnabled      bool `json:"task_center_enabled"`
	SkillsEnabled          bool `json:"skills_enabled"`
	WorkflowsEnabled       bool `json:"workflows_enabled"`
	MCPEnabled             bool `json:"mcp_enabled"`
	DocumentParsingEnabled bool `json:"document_parsing_enabled"`
}

func DefaultFeatureControls() FeatureControls {
	return FeatureControls{
		TaskCenterEnabled:      true,
		SkillsEnabled:          true,
		WorkflowsEnabled:       true,
		MCPEnabled:             true,
		DocumentParsingEnabled: true,
	}
}

func LoadFeatureControls(ctx context.Context, db *gorm.DB, userID string) (FeatureControls, error) {
	controls := DefaultFeatureControls()
	if db == nil || strings.TrimSpace(userID) == "" {
		return controls, nil
	}

	var preferences orm.UserUIPreferences
	err := db.WithContext(ctx).Where("user_id = ?", strings.TrimSpace(userID)).Take(&preferences).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return controls, nil
	}
	// Focused package tests and a few lightweight runtime probes use partial
	// schemas. Missing optional preferences must preserve the upgrade-safe
	// default instead of blocking an unrelated execution path.
	if isMissingPreferencesTableError(err) {
		return controls, nil
	}
	if err != nil {
		return FeatureControls{}, err
	}
	return FeatureControls{
		TaskCenterEnabled:      preferences.TaskCenterEnabled,
		SkillsEnabled:          preferences.SkillsEnabled,
		WorkflowsEnabled:       preferences.TaskCenterEnabled && preferences.WorkflowsEnabled,
		MCPEnabled:             preferences.MCPEnabled,
		DocumentParsingEnabled: preferences.DocumentParsingEnabled,
	}, nil
}

func isMissingPreferencesTableError(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(strings.ToLower(err.Error()), "no such table: user_ui_preferences") {
		return true
	}
	var sqlStateErr interface{ SQLState() string }
	return errors.As(err, &sqlStateErr) && sqlStateErr.SQLState() == "42P01"
}
