package orm

import (
	"path/filepath"
	"testing"
)

// TestTaskSchedulerModelsAutoMigrate verifies that task center, schedule, and automation tables are created.
func TestTaskSchedulerModelsAutoMigrate(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "task-scheduler.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}

	if err := db.AutoMigrate(&TaskCenterTask{}, &UserSchedule{}, &AutomationGroup{}); err != nil {
		t.Fatalf("auto migrate task scheduler models: %v", err)
	}

	// Verify tables exist.
	for _, model := range []any{
		&TaskCenterTask{},
		&UserSchedule{},
		&AutomationGroup{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

	// Verify TaskCenterTask columns.
	if !db.Migrator().HasColumn(&TaskCenterTask{}, "id") {
		t.Fatal("expected task_center_tasks.id column")
	}
	if !db.Migrator().HasColumn(&TaskCenterTask{}, "task_type") {
		t.Fatal("expected task_center_tasks.task_type column")
	}
	if !db.Migrator().HasColumn(&TaskCenterTask{}, "status") {
		t.Fatal("expected task_center_tasks.status column")
	}

	// Verify UserSchedule columns.
	if !db.Migrator().HasColumn(&UserSchedule{}, "user_id") {
		t.Fatal("expected user_schedules.user_id column")
	}
	if !db.Migrator().HasColumn(&UserSchedule{}, "cron_expr") {
		t.Fatal("expected user_schedules.cron_expr column")
	}

	// Verify AutomationGroup columns.
	if !db.Migrator().HasColumn(&AutomationGroup{}, "id") {
		t.Fatal("expected automation_groups.id column")
	}
	if !db.Migrator().HasColumn(&AutomationGroup{}, "name") {
		t.Fatal("expected automation_groups.name column")
	}
}

// TestPromptModelsAutoMigrate verifies that prompt-related tables are created correctly.
func TestPromptModelsAutoMigrate(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "prompt.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}

	if err := db.AutoMigrate(&Prompt{}, &PromptCategory{}, &PromptUserState{}); err != nil {
		t.Fatalf("auto migrate prompt models: %v", err)
	}

	// Verify tables exist.
	for _, model := range []any{
		&Prompt{},
		&PromptCategory{},
		&PromptUserState{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

	// Verify Prompt columns.
	if !db.Migrator().HasColumn(&Prompt{}, "id") {
		t.Fatal("expected prompts.id column")
	}
	if !db.Migrator().HasColumn(&Prompt{}, "name") {
		t.Fatal("expected prompts.name column")
	}
	if !db.Migrator().HasColumn(&Prompt{}, "content") {
		t.Fatal("expected prompts.content column")
	}
	if !db.Migrator().HasColumn(&Prompt{}, "category") {
		t.Fatal("expected prompts.category column")
	}

	// Verify PromptCategory columns.
	if !db.Migrator().HasColumn(&PromptCategory{}, "name") {
		t.Fatal("expected prompt_categories.name column")
	}
}

// TestDatasetModelsAutoMigrate verifies that dataset-related tables are created correctly.
func TestDatasetModelsAutoMigrate(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "dataset.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}

	if err := db.AutoMigrate(&Dataset{}, &DefaultDataset{}); err != nil {
		t.Fatalf("auto migrate dataset models: %v", err)
	}

	// Verify tables exist.
	for _, model := range []any{
		&Dataset{},
		&DefaultDataset{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

	// Verify Dataset columns.
	if !db.Migrator().HasColumn(&Dataset{}, "id") {
		t.Fatal("expected datasets.id column")
	}
	if !db.Migrator().HasColumn(&Dataset{}, "display_name") {
		t.Fatal("expected datasets.display_name column")
	}
	if !db.Migrator().HasColumn(&Dataset{}, "kb_id") {
		t.Fatal("expected datasets.kb_id column")
	}
}
