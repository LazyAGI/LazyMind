package doc

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

func validateAcademicScenario(ctx context.Context, db *gorm.DB, user, scenario, level string) error {
	if scenario != "academic_papers" {
		return nil
	}
	if effectiveProcessingLevel(level) == ProcessingLevelStored {
		return fmt.Errorf("academic paper libraries require at least parsed processing")
	}
	return nil
}
