package taskcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func removalDB(t *testing.T) *orm.DB {
	t.Helper()
	return orm.MigrateTestDB(t, &orm.TaskCenterTask{}, &orm.Conversation{}, &orm.UserSchedule{}, &orm.AsyncJob{})
}
func seedRemoval(t *testing.T, db *orm.DB, id, conv, status string) {
	t.Helper()
	if err := db.Create(&orm.TaskCenterTask{ID: id, UserID: "u", ConversationID: conv, Status: status, TaskType: "background_chat"}).Error; err != nil {
		t.Fatal(err)
	}
}
func TestRemovalScopeAndAtomicity(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[fail], func(t *testing.T) {
			db := removalDB(t)
			now := time.Now().UTC()
			parent := "c"
			for _, c := range []orm.Conversation{{ID: "c", BaseModel: orm.BaseModel{CreateUserID: "u", UpdatedAt: now}}, {ID: "side", ParentConversationID: &parent, RelationType: "sidechat", BaseModel: orm.BaseModel{CreateUserID: "u"}}, {ID: "fork", BaseModel: orm.BaseModel{CreateUserID: "u"}}} {
				if err := db.Create(&c).Error; err != nil {
					t.Fatal(err)
				}
			}
			seedRemoval(t, db, "t1", "c", "running")
			seedRemoval(t, db, "t2", "c", "failed")
			seedRemoval(t, db, "t3", "side", "pending")
			seedRemoval(t, db, "independent", "fork", "succeeded")
			preview, err := previewTaskRemoval(t.Context(), db.DB, "u", "t1")
			if err != nil {
				t.Fatal(err)
			}
			if preview.TaskCount != 3 || preview.ConversationCount != 2 || preview.ActiveTaskCount != 2 {
				t.Fatalf("unexpected impact: %+v", preview)
			}
			if fail {
				if err := db.Callback().Create().Before("gorm:create").Register("fail_removal_job", func(tx *gorm.DB) {
					if tx.Statement.Table == "async_jobs" {
						tx.AddError(errors.New("synthetic secret database failure"))
					}
				}); err != nil {
					t.Fatal(err)
				}
			}
			result, err := removeTaskWithImpact(t.Context(), db.DB, "u", "t1", preview.ImpactVersion)
			if fail {
				if err == nil {
					t.Fatal("expected rollback")
				}
				var jobs int64
				if err := db.Model(&orm.AsyncJob{}).Count(&jobs).Error; err != nil || jobs != 0 {
					t.Fatalf("rolled back jobs=%d err=%v", jobs, err)
				}
			} else if err != nil || len(result.RemovedTaskIDs) != 3 || len(result.TrashedConversationIDs) != 2 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			var tasks []orm.TaskCenterTask
			if err := db.Order("id").Find(&tasks).Error; err != nil {
				t.Fatal(err)
			}
			for _, task := range tasks {
				if task.ID == "independent" || fail {
					if task.ArchivedAt != nil {
						t.Fatalf("unexpected removal %s", task.ID)
					}
				} else if task.ArchivedAt == nil {
					t.Fatalf("not removed %s", task.ID)
				}
			}
			var c orm.Conversation
			if err := db.First(&c, "id=?", "c").Error; err != nil {
				t.Fatal(err)
			}
			if (c.DeletedAt != nil) == fail {
				t.Fatalf("conversation rollback=%v: %+v", fail, c)
			}
			if !fail {
				again, err := removeTaskWithImpact(t.Context(), db.DB, "u", "t1", preview.ImpactVersion)
				if err != nil || len(again.RemovedTaskIDs) != 3 {
					t.Fatalf("retry=%+v %v", again, err)
				}
				var reloaded orm.Conversation
				db.First(&reloaded, "id=?", "c")
				if !reloaded.TrashExpiresAt.Equal(*c.TrashExpiresAt) {
					t.Fatal("retry extended retention")
				}
				if err := UpdateTaskStatus(context.Background(), db.DB, "t1", "succeeded"); err != nil {
					t.Fatal(err)
				}
				var task orm.TaskCenterTask
				db.First(&task, "id=?", "t1")
				if task.Status != "canceled" {
					t.Fatal("late completion revived task")
				}
			}
		})
	}
}

func TestRemovalPreservesScheduleDefinitionAndCountsRemainingExecutions(t *testing.T) {
	db := removalDB(t)
	next := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	plan := orm.UserSchedule{ID: "plan", UserID: "u", Name: "daily", CronExpr: "0 8 * * *", PromptTemplate: "fake prompt", Enabled: true, RunCount: 2, NextRunAt: next, CreatedAt: time.Now().UTC()}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"removed", "kept"} {
		if err := db.Create(&orm.TaskCenterTask{ID: id, UserID: "u", ScheduleID: &plan.ID, Status: "pending", TaskType: "scheduled"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := removeTaskWithImpact(t.Context(), db.DB, "u", "removed", ""); err != nil {
		t.Fatal(err)
	}
	var saved orm.UserSchedule
	if err := db.First(&saved, "id=?", plan.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.RunCount != 1 || !saved.Enabled || !saved.NextRunAt.Equal(next) || saved.CronExpr != plan.CronExpr || saved.PromptTemplate != plan.PromptTemplate {
		t.Fatalf("schedule was changed: %+v", saved)
	}
}

func TestRemovalHTTPRejectsInvalidForeignAndStaleRequestsWithoutLeakingErrors(t *testing.T) {
	db := removalDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.Create(&orm.Conversation{ID: "c", BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
		t.Fatal(err)
	}
	seedRemoval(t, db, "task", "c", "failed")
	for _, tc := range []struct {
		owner, body string
		status      int
	}{
		{"", "", 401}, {"other", "", 404}, {"u", "{", 400}, {"u", `{"impact_version":"stale"}`, 409},
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/core/task-center/tasks/task:remove", strings.NewReader(tc.body))
		r.Header.Set("X-User-Id", tc.owner)
		w := httptest.NewRecorder()
		RemoveTaskHandler(w, r)
		if w.Code != tc.status {
			t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
		}
	}
	if err := db.Callback().Update().Before("gorm:update").Register("synthetic_removal_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "task_center_tasks" {
			tx.AddError(errors.New("synthetic-secret SQL internal-path"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/core/task-center/tasks/task:remove", nil)
	r.Header.Set("X-User-Id", "u")
	w := httptest.NewRecorder()
	RemoveTaskHandler(w, r)
	if w.Code != 500 || strings.Contains(w.Body.String(), "synthetic-secret") || strings.Contains(w.Body.String(), "SQL") {
		t.Fatalf("unsafe error: %d %s", w.Code, w.Body.String())
	}
	var c orm.Conversation
	var task orm.TaskCenterTask
	if err := db.First(&c, "id=?", "c").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&task, "id=?", "task").Error; err != nil {
		t.Fatal(err)
	}
	if c.DeletedAt != nil || task.ArchivedAt != nil {
		t.Fatal("failed deletion changed data")
	}
}
func TestRemovalOrphansOwnershipAndChangedScope(t *testing.T) {
	db := removalDB(t)
	seedRemoval(t, db, "orphan", "missing", "pending")
	p, err := previewTaskRemoval(t.Context(), db.DB, "u", "orphan")
	if err != nil {
		t.Fatal(err)
	}
	if p.ConversationCount != 0 || p.TaskCount != 1 {
		t.Fatal(p)
	}
	r, err := removeTaskWithImpact(t.Context(), db.DB, "u", "orphan", p.ImpactVersion)
	if err != nil || len(r.RemovedTaskIDs) != 1 {
		t.Fatalf("orphan %+v %v", r, err)
	}
	c := orm.Conversation{ID: "c", BaseModel: orm.BaseModel{CreateUserID: "u"}}
	db.Create(&c)
	seedRemoval(t, db, "t", "c", "failed")
	p, err = previewTaskRemoval(t.Context(), db.DB, "u", "t")
	if err != nil {
		t.Fatal(err)
	}
	seedRemoval(t, db, "new", "c", "running")
	if _, err = removeTaskWithImpact(t.Context(), db.DB, "u", "t", p.ImpactVersion); !errors.Is(err, errRemovalScopeChanged) {
		t.Fatalf("changed scope: %v", err)
	}
	var task orm.TaskCenterTask
	db.First(&task, "id=?", "t")
	if task.ArchivedAt != nil {
		t.Fatal("stale preview changed task")
	}
	if _, err = previewTaskRemoval(t.Context(), db.DB, "other", "t"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("ownership: %v", err)
	}
	foreign := orm.Conversation{ID: "foreign", BaseModel: orm.BaseModel{CreateUserID: "other"}}
	db.Create(&foreign)
	seedRemoval(t, db, "bad-link", "foreign", "failed")
	if _, err = removeTaskWithImpact(t.Context(), db.DB, "u", "bad-link", ""); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign link: %v", err)
	}
}

func TestRemovalJobRunsOnlyAfterCommitAndRejectsLateTaskCreation(t *testing.T) {
	db := removalDB(t)
	c := orm.Conversation{ID: "c", BaseModel: orm.BaseModel{CreateUserID: "u"}}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	seedRemoval(t, db, "running", "c", "running")
	preview, err := previewTaskRemoval(t.Context(), db.DB, "u", "running")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := removeTaskWithImpact(t.Context(), db.DB, "u", "running", preview.ImpactVersion); err != nil {
		t.Fatal(err)
	}
	var jobs []orm.AsyncJob
	if err := db.Find(&jobs).Error; err != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	pending, err := PendingRemovalCancellation(t.Context(), db.DB, "u", "c")
	if err != nil || !pending {
		t.Fatalf("restore fence %v %v", pending, err)
	}
	previous := OnRemovalCancelHook
	t.Cleanup(func() { OnRemovalCancelHook = previous })
	calls := 0
	OnRemovalCancelHook = func(ctx context.Context, owner, id string, before time.Time, _ []string) error {
		calls++
		var saved orm.Conversation
		if err := db.First(&saved, "id=?", id).Error; err != nil || saved.DeletedAt == nil || owner != "u" || before.IsZero() {
			t.Fatalf("before commit: %+v %v", saved, err)
		}
		return nil
	}
	_, err = runRemovalCancellation(t.Context(), asyncjob.Job{CreateUserID: "u", PayloadJSON: jobs[0].PayloadJSON}, nil)
	if err != nil || calls != 1 {
		t.Fatalf("cancel %v %d", err, calls)
	}
	if err := CreateTask(t.Context(), db.DB, &orm.TaskCenterTask{ID: "late", UserID: "u", ConversationID: "c", Status: "running"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("late create: %v", err)
	}
}

func TestRemovalConcurrentRetriesQueueCancellationOnce(t *testing.T) {
	db := removalDB(t)
	if err := db.Create(&orm.Conversation{ID: "c", BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
		t.Fatal(err)
	}
	seedRemoval(t, db, "running", "c", "running")
	p, err := previewTaskRemoval(t.Context(), db.DB, "u", "running")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := removeTaskWithImpact(t.Context(), db.DB, "u", "running", p.ImpactVersion)
			results <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := db.Model(&orm.AsyncJob{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("cancellation count=%d err=%v", count, err)
	}
}

func TestRemovalHonorsOrganizerAndChildConversationBoundaries(t *testing.T) {
	db := removalDB(t)
	if err := db.AutoMigrate(&orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{}); err != nil {
		t.Fatal(err)
	}
	parent := "c"
	for _, c := range []orm.Conversation{{ID: "c", BaseModel: orm.BaseModel{CreateUserID: "u"}}, {ID: "child", ParentConversationID: &parent, RelationType: "sidechat", BaseModel: orm.BaseModel{CreateUserID: "u"}}} {
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	seedRemoval(t, db, "root-task", "c", "failed")
	seedRemoval(t, db, "child-task", "child", "failed")
	if _, err := previewTaskRemoval(t.Context(), db.DB, "u", "child-task"); !errors.Is(err, errRemovalBlocked) {
		t.Fatalf("child boundary: %v", err)
	}
	run := orm.ConversationOrganizerRun{ID: "organizer", UserID: "u", Status: "running", SnapshotJSON: json.RawMessage(`{}`), ModelConfigJSON: json.RawMessage(`{}`), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationOrganizerSnapshotItem{RunID: run.ID, ConversationID: "c", UserID: "u"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := removeTaskWithImpact(t.Context(), db.DB, "u", "root-task", ""); !errors.Is(err, errRemovalBlocked) {
		t.Fatalf("organizer boundary: %v", err)
	}
	var c orm.Conversation
	if err := db.First(&c, "id=?", "c").Error; err != nil || c.DeletedAt != nil {
		t.Fatalf("locked conversation changed: %v", err)
	}
}
