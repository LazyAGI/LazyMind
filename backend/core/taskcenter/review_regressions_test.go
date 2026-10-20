package taskcenter

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestCompletedTaskRemovalCancelsActiveSidechatWithoutTaskRow(t *testing.T) {
	db := removalDB(t)
	setupChatExecutionState(t, db)
	parent := "c"
	for _, c := range []orm.Conversation{
		{ID: parent, BaseModel: orm.BaseModel{CreateUserID: "u"}},
		{ID: "side", ParentConversationID: &parent, RelationType: "sidechat", BaseModel: orm.BaseModel{CreateUserID: "u"}},
	} {
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	seedRemoval(t, db, "completed", parent, "succeeded")
	if err := RegisterChatExecution(t.Context(), db.DB, "u", "side", "side_100"); err != nil {
		t.Fatal(err)
	}
	result, err := removeTaskWithImpact(t.Context(), db.DB, "u", "completed", "")
	if err != nil || !result.CancellationPending || len(result.RemovedTaskIDs) != 1 {
		t.Fatalf("active sidechat was not canceled: %+v %v", result, err)
	}
	var jobs []orm.AsyncJob
	if err := db.Find(&jobs).Error; err != nil || len(jobs) != 1 || jobs[0].ResourceID != "side" {
		t.Fatalf("wrong cancellation scope: %+v %v", jobs, err)
	}
	var payload struct {
		IDs []string `json:"chat_execution_ids"`
	}
	if err := json.Unmarshal(jobs[0].PayloadJSON, &payload); err != nil || !reflect.DeepEqual(payload.IDs, []string{"side_100"}) {
		t.Fatalf("missing sidechat execution snapshot: %s %v", jobs[0].PayloadJSON, err)
	}
	pending, err := PendingRemovalCancellation(t.Context(), db.DB, "u", "side")
	if err != nil || !pending {
		t.Fatalf("missing sidechat restore fence: %v %v", pending, err)
	}
	if _, err := removeTaskWithImpact(t.Context(), db.DB, "u", "completed", ""); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&orm.AsyncJob{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("duplicate jobs: %d %v", count, err)
	}
}

func TestRestoreRemovedScheduleRunRecountsOnlyMatchingTasks(t *testing.T) {
	db := removalDB(t)
	schedule := orm.UserSchedule{ID: "schedule", UserID: "u", RunCount: 2}
	if err := db.Create(&schedule).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Conversation{ID: "c", BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
		t.Fatal(err)
	}
	for _, task := range []orm.TaskCenterTask{
		{ID: "removed", UserID: "u", ConversationID: "c", Status: "succeeded", TaskType: "scheduled", ScheduleID: &schedule.ID},
		{ID: "visible", UserID: "u", ConversationID: "other", Status: "succeeded", TaskType: "scheduled", ScheduleID: &schedule.ID},
	} {
		if err := db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := removeTaskWithImpact(t.Context(), db.DB, "u", "removed", ""); err != nil {
		t.Fatal(err)
	}
	check := func(want int) {
		t.Helper()
		var saved orm.UserSchedule
		if err := db.First(&saved, "id=?", schedule.ID).Error; err != nil || saved.RunCount != want {
			t.Fatalf("run_count=%d want=%d err=%v", saved.RunCount, want, err)
		}
	}
	check(1)
	if err := db.Callback().Update().Before("gorm:update").Register("fail_schedule_recount", func(tx *gorm.DB) {
		if tx.Statement.Table == "user_schedules" {
			tx.AddError(errors.New("synthetic count failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := RestoreTasksForConversations(t.Context(), db.DB, "u", []string{"c"}, ArchivedReasonConversationTrash, time.Now().UTC()); err == nil {
		t.Fatal("expected recount failure")
	}
	var stillHidden orm.TaskCenterTask
	if err := db.First(&stillHidden, "id=?", "removed").Error; err != nil || stillHidden.ArchivedAt == nil {
		t.Fatalf("failed recount committed restore: %+v %v", stillHidden, err)
	}
	if err := db.Callback().Update().Remove("fail_schedule_recount"); err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{ArchivedReasonConversationArchive, ArchivedReasonConversationTrash} {
		if err := db.Transaction(func(tx *gorm.DB) error {
			return RestoreTasksForConversations(t.Context(), tx, "u", []string{"c"}, reason, time.Now().UTC())
		}); err != nil {
			t.Fatal(err)
		}
		if reason == ArchivedReasonConversationArchive {
			check(1)
		} else {
			check(2)
		}
	}
	if err := RestoreTasksForConversations(t.Context(), db.DB, "u", []string{"c"}, ArchivedReasonConversationTrash, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	check(2)
}

func TestRemovalFindsOwnedExecutorRunsWithoutTaskRows(t *testing.T) {
	for _, kind := range []string{"workflow", "external", "foreign_external"} {
		t.Run(kind, func(t *testing.T) {
			db := removalDB(t)
			if err := db.AutoMigrate(&orm.WorkflowSession{}, &orm.ExternalChatRun{}); err != nil {
				t.Fatal(err)
			}
			parent := "c"
			for _, c := range []orm.Conversation{{ID: parent, BaseModel: orm.BaseModel{CreateUserID: "u"}}, {ID: "side", ParentConversationID: &parent, RelationType: "sidechat", BaseModel: orm.BaseModel{CreateUserID: "u"}}} {
				if err := db.Create(&c).Error; err != nil {
					t.Fatal(err)
				}
			}
			seedRemoval(t, db, "completed", "c", "succeeded")
			created := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
			if kind == "workflow" {
				if err := db.Create(&orm.WorkflowSession{ID: "run", ConversationID: "side", CreateUserID: "u", Status: "active", CreatedAt: created}).Error; err != nil {
					t.Fatal(err)
				}
			} else {
				owner := "u"
				if kind == "foreign_external" {
					owner = "other"
				}
				if err := db.Create(&orm.ExternalChatRun{ID: "run", ConversationID: "side", ActorUserID: owner, Status: "running", Provider: "external", RequestID: "request", CreatedAt: created}).Error; err != nil {
					t.Fatal(err)
				}
			}
			result, err := removeTaskWithImpact(t.Context(), db.DB, "u", "completed", "")
			if err != nil || result.CancellationPending != (kind != "foreign_external") {
				t.Fatalf("wrong executor scope: %+v %v", result, err)
			}
		})
	}
}
