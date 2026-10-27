package doc

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"lazymind/core/settings"
)

func validateAcademicScenario(ctx context.Context, db *gorm.DB, user, scenario, level string) error {
	if scenario != "academic_papers" {
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
