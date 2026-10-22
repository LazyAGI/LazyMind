package doc

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/settings"
)

func effectiveScenario(value string) string {
	if value == "" {
		return "general"
	}
	return value
}
func normalizeScenario(ctx context.Context, db *gorm.DB, user, value string) (string, error) {
	value = effectiveScenario(strings.TrimSpace(value))
	switch value {
	case "general", "academic_papers", "legal", "technical", "chinese_modern", "chinese_classical", "english_learning", "historical":
		return value, nil
	}
	// Custom capability presets already have an owner-scoped identity. Persist
	// that identity without treating their capability set as a built-in scene.
	if len(value) <= 64 {
		var count int64
		if err := db.WithContext(ctx).Table("learning_capability_profiles").Where("id=? AND owner_id=?", value, user).Count(&count).Error; err != nil {
			return "", err
		}
		if count > 0 {
			return value, nil
		}
	}
	return "", fmt.Errorf("unknown scenario_key: %s", value)
}
func validateAcademicScenario(ctx context.Context, db *gorm.DB, user, scenario, level string) error {
	if effectiveScenario(scenario) != "academic_papers" {
		return nil
	}
	if effectiveProcessingLevel(level) == ProcessingLevelStored {
		return fmt.Errorf("academic paper libraries require at least parsed processing")
	}
	controls, err := settings.LoadFeatureControls(ctx, db, user)
	if err != nil {
		return err
	}
	if !controls.DocumentParsingEnabled {
		return fmt.Errorf("enable document parsing before using an academic paper library")
	}
	return nil
}
