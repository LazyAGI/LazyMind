package taskcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestTaskDetailScheduleSummary(t *testing.T) {
	for _, tc := range []struct {
		name, scheduleOwner           string
		withSchedule, linked, visible bool
	}{
		{"owned", "owner", true, true, true},
		{"other owner", "other", true, true, false},
		{"missing schedule", "owner", false, true, false},
		{"ordinary task", "owner", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.TaskCenterTask{}, &orm.UserSchedule{}, &orm.SubAgentTask{}, &orm.WorkflowSession{})
			store.Init(db.DB, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			now := time.Date(2026, 9, 24, 1, 0, 0, 0, time.UTC)
			schedule := orm.UserSchedule{ID: "schedule", UserID: tc.scheduleOwner, Name: "每日简报", CronExpr: "0 9 * * *", Timezone: "Asia/Shanghai", Enabled: false, RunCount: 3, LastRunAt: &now, NextRunAt: now.Add(24 * time.Hour), PromptTemplate: "private prompt", KbIDs: "[]", FileIDs: "[]", CreatedAt: now}
			if tc.withSchedule {
				if err := db.Create(&schedule).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&schedule).Update("enabled", false).Error; err != nil {
					t.Fatal(err)
				}
			}
			task := orm.TaskCenterTask{ID: "run", UserID: "owner", TaskType: "background_chat", Status: "succeeded", CreatedAt: now, UpdatedAt: now}
			if tc.linked {
				task.ScheduleID = &schedule.ID
				task.TaskType = "scheduled"
			}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/task-center/tasks/run", nil)
			req.Header.Set("X-User-Id", "owner")
			rec := httptest.NewRecorder()
			GetTaskByID(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var response map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			summary, exists := response["schedule"].(map[string]any)
			if exists != tc.visible {
				t.Fatalf("schedule visibility=%v want=%v: %s", exists, tc.visible, rec.Body.String())
			}
			if tc.visible {
				for key, want := range map[string]any{"id": "schedule", "name": "每日简报", "cron_expr": "0 9 * * *", "timezone": "Asia/Shanghai", "enabled": false, "run_count": float64(3)} {
					if summary[key] != want {
						t.Errorf("%s=%v want=%v", key, summary[key], want)
					}
				}
				for key, want := range map[string]time.Time{"last_run_at": now, "next_run_at": now.Add(24 * time.Hour)} {
					value, _ := summary[key].(string)
					got, err := time.Parse(time.RFC3339, value)
					if err != nil || !got.Equal(want) {
						t.Errorf("%s=%v want instant=%v", key, summary[key], want)
					}
				}
				if response["schedule_name"] != schedule.Name {
					t.Error("missing schedule name")
				}
				if _, ok := summary["prompt_template"]; ok {
					t.Error("summary exposed private configuration")
				}
			} else if response["schedule_name"] != nil {
				t.Error("unexpected schedule name")
			}
			req.Header.Set("X-User-Id", "other")
			rec = httptest.NewRecorder()
			GetTaskByID(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("another user can read task: %d", rec.Code)
			}
		})
	}
}

func TestTaskDetailScheduleQueryFailureUsesCataloguedError(t *testing.T) {
	// Keep the task readable while the schedule table is unavailable.
	db := orm.MigrateTestDB(t, &orm.TaskCenterTask{}, &orm.SubAgentTask{}, &orm.WorkflowSession{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	scheduleID := "schedule"
	task := orm.TaskCenterTask{ID: "run", UserID: "owner", TaskType: "scheduled", Status: "succeeded", ScheduleID: &scheduleID, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/task-center/tasks/run", nil)
	req.Header.Set("X-User-Id", "owner")
	rec := httptest.NewRecorder()
	GetTaskByID(rec, req)
	var response common.APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusInternalServerError || response.Code != 2001493 || response.Message != "query task failed" || response.Data != nil {
		t.Fatalf("unexpected query failure response: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
