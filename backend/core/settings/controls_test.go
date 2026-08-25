package settings

import (
	"context"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestLoadFeatureControlsDefaultsWhenPreferencesTableIsMissing(t *testing.T) {
	db := orm.MigrateTestDB(t)

	controls, err := LoadFeatureControls(context.Background(), db.DB, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if controls != DefaultFeatureControls() {
		t.Fatalf("controls=%#v, want defaults", controls)
	}
}

func TestLoadFeatureControlsKeepsTaskControlsIndependent(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserUIPreferences{})
	now := time.Now().UTC()
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{
		"user_id":                  "user-1",
		"task_center_enabled":      false,
		"schedules_enabled":        true,
		"skills_enabled":           true,
		"workflows_enabled":        true,
		"mcp_enabled":              true,
		"document_parsing_enabled": true,
		"created_at":               now,
		"updated_at":               now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	controls, err := LoadFeatureControls(context.Background(), db.DB, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if controls.TaskCenterEnabled || !controls.WorkflowsEnabled || !controls.SchedulesEnabled {
		t.Fatalf("subtasks, workflows, and schedules must remain independent: %#v", controls)
	}
}
