package orm

import (
	"testing"
)

func TestTaskSchedulerModelsAutoMigrate(t *testing.T) {
	db := MigrateTestDB(t, &TaskCenterTask{}, &UserSchedule{}, &AutomationGroup{})

	for _, model := range []any{
		&TaskCenterTask{},
		&UserSchedule{},
		&AutomationGroup{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

	if !db.Migrator().HasColumn(&TaskCenterTask{}, "id") {
		t.Fatal("expected task_center_tasks.id column")
	}
	if !db.Migrator().HasColumn(&TaskCenterTask{}, "task_type") {
		t.Fatal("expected task_center_tasks.task_type column")
	}
	if !db.Migrator().HasColumn(&TaskCenterTask{}, "status") {
		t.Fatal("expected task_center_tasks.status column")
	}

	if !db.Migrator().HasColumn(&UserSchedule{}, "user_id") {
		t.Fatal("expected user_schedules.user_id column")
	}
	if !db.Migrator().HasColumn(&UserSchedule{}, "cron_expr") {
		t.Fatal("expected user_schedules.cron_expr column")
	}

	if !db.Migrator().HasColumn(&AutomationGroup{}, "id") {
		t.Fatal("expected automation_groups.id column")
	}
	if !db.Migrator().HasColumn(&AutomationGroup{}, "name") {
		t.Fatal("expected automation_groups.name column")
	}
}

func TestPromptModelsAutoMigrate(t *testing.T) {
	db := MigrateTestDB(t, &Prompt{}, &PromptCategory{}, &PromptUserState{})

	for _, model := range []any{
		&Prompt{},
		&PromptCategory{},
		&PromptUserState{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

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

	if !db.Migrator().HasColumn(&PromptCategory{}, "name") {
		t.Fatal("expected prompt_categories.name column")
	}
}

func TestDatasetModelsAutoMigrate(t *testing.T) {
	db := MigrateTestDB(t, &Dataset{}, &DefaultDataset{})

	for _, model := range []any{
		&Dataset{},
		&DefaultDataset{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

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
