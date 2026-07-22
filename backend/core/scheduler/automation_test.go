package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func automationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.UserSchedule{}, &orm.ScheduleDependency{}, &orm.TaskCenterTask{}, &orm.ChatHistory{}, &orm.ConversationArtifact{}, &orm.TaskRunOutput{}, &orm.ScheduleFire{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestReplaceDependenciesRejectsCycle(t *testing.T) {
	db := automationTestDB(t)
	now := time.Now().UTC()
	for _, id := range []string{"a", "b", "c"} {
		if err := db.Create(&orm.UserSchedule{ID: id, UserID: "u", CronExpr: "0 9 * * *", Timezone: "UTC", PromptTemplate: id, KbIDs: "[]", FileIDs: "[]", Enabled: true, NextRunAt: now, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := replaceDependencies(db, "u", "b", []dependencyInput{{SourceScheduleID: "a"}}); err != nil {
		t.Fatal(err)
	}
	if err := replaceDependencies(db, "u", "c", []dependencyInput{{SourceScheduleID: "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := replaceDependencies(db, "u", "a", []dependencyInput{{SourceScheduleID: "c"}}); err == nil {
		t.Fatal("expected cycle to be rejected")
	}
}

func TestReplaceDependenciesRejectsMoreFrequentTarget(t *testing.T) {
	db := automationTestDB(t)
	now := time.Now().UTC()
	for id, cronExpr := range map[string]string{"weekly": "0 9 * * 1", "daily": "0 9 * * *"} {
		if err := db.Create(&orm.UserSchedule{ID: id, UserID: "u", CronExpr: cronExpr, Timezone: "UTC", PromptTemplate: id, KbIDs: "[]", FileIDs: "[]", Enabled: true, NextRunAt: now, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := replaceDependencies(db, "u", "daily", []dependencyInput{{SourceScheduleID: "weekly"}}); err != errDependencyTooSparse {
		t.Fatalf("expected frequency validation error, got %v", err)
	}
	if err := replaceDependencies(db, "u", "weekly", []dependencyInput{{SourceScheduleID: "daily"}}); err != nil {
		t.Fatalf("expected weekly target to accept daily source: %v", err)
	}
}

func TestFinalizeTaskOutputStoresPlainChatAnswer(t *testing.T) {
	db := automationTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&orm.TaskCenterTask{ID: "task", UserID: "u", ConversationID: "conv", TaskType: "scheduled", Status: "running", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ChatHistory{ID: "hist", Seq: 1, ConversationID: "conv", Result: "daily result"}).Error; err != nil {
		t.Fatal(err)
	}
	finalizeTaskOutput(context.Background(), db, "task", "conv")
	var output orm.TaskRunOutput
	if err := db.Where("task_id = ?", "task").First(&output).Error; err != nil {
		t.Fatal(err)
	}
	if output.OutputStatus != "ready" || output.FinalAnswerText != "daily result" || output.ContentHash == "" {
		t.Fatalf("unexpected output: %+v", output)
	}
}
