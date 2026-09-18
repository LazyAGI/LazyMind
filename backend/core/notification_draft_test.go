package main

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNotificationDraftAtomicScheduleSave(t *testing.T) {
	a := newNotificationDraftAPI(t)
	body := map[string]any{"name": "draft", "prompt_template": "reply ok", "cron_expr": "0 9 * * *", "timezone": "Asia/Shanghai", "notification": map[string]any{"config": notificationConfig(false)}}
	created := draftScheduleData(t, a, "POST", "/schedules", body)
	id := created["id"].(string)
	path := "/schedules/" + id + "/notifications"
	view := a.data("GET", path, "owner", nil)
	rule := notificationObject(t, view["config"])
	if notificationObject(t, notificationObject(t, rule["channels"])["desktop"])["enabled"] != false {
		t.Fatal("creation lost draft rule")
	}
	draftScheduleData(t, a, "PUT", "/schedules/"+id, map[string]any{"name": "edited", "notification": map[string]any{"revision": view["revision"], "config": notificationConfig(true)}})
	notificationError(t, a.request("PUT", "/schedules/"+id, "owner", map[string]any{"name": "must not persist", "notification": map[string]any{"revision": view["revision"], "config": notificationConfig(false)}}), 409, "NOTIFICATION_CONFIG_CONFLICT")
	var row orm.UserSchedule
	if err := a.db.First(&row, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if row.Name != "edited" {
		t.Fatal("conflict partially saved task")
	}
	latest := a.data("GET", path, "owner", nil)
	draftScheduleData(t, a, "PUT", "/schedules/"+id, map[string]any{"name": "preserved"})
	unchanged := a.data("GET", path, "owner", nil)
	if unchanged["revision"] != latest["revision"] {
		t.Fatal("ordinary edit changed notifications")
	}
}

func TestNotificationDraftValidationRollbackAndClear(t *testing.T) {
	a := newNotificationDraftAPI(t)
	body := map[string]any{"name": "invalid", "prompt_template": "reply ok", "cron_expr": "0 9 * * *", "notification": map[string]any{"config": map[string]any{"events": map[string]any{}}}}
	notificationError(t, a.request("POST", "/schedules", "owner", body), 422, "INVALID_REQUEST")
	var count int64
	a.db.Model(&orm.UserSchedule{}).Count(&count)
	if count != 0 {
		t.Fatal("invalid draft left a task behind")
	}
	path := a.schedule("clear-draft", false)
	a.completeRun("before-clear", "clear-draft", "saved result")
	before := a.data("GET", path, "owner", nil)
	notificationError(t, a.request("PUT", path, "other", map[string]any{"revision": before["revision"], "clear": true}), 404, "NOTIFICATION_NOT_FOUND")
	cleared := a.data("PUT", path, "owner", map[string]any{"revision": before["revision"], "clear": true})
	if cleared["configured"] != false || cleared["config"] != nil {
		t.Fatal("clear did not restore unconfigured state")
	}
	var run orm.TaskCenterTask
	if err := a.db.First(&run, "id = ?", "before-clear").Error; err != nil {
		t.Fatal(err)
	}
	if run.NotificationConfig == nil {
		t.Fatal("clear modified historical snapshot")
	}
	a.db.Model(&orm.TaskNotification{}).Where("task_id = ?", run.ID).Count(&count)
	if count != 1 {
		t.Fatal("clear removed history")
	}
	notificationError(t, a.request("PUT", path, "owner", map[string]any{"revision": cleared["revision"], "clear": true, "config": notificationConfig(true)}), 422, "INVALID_REQUEST")
}

func newNotificationDraftAPI(t *testing.T) *notificationAPI {
	t.Helper()
	check := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/sensitive-check" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"passed":true}`))
	}))
	t.Cleanup(check.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", check.URL)
	return newNotificationAPI(t)
}

func draftScheduleData(t *testing.T, a *notificationAPI, method, path string, body any) map[string]any {
	t.Helper()
	rec := a.request(method, path, "owner", body)
	if rec.Code != 200 {
		t.Fatalf("schedule save failed: %d %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNotificationDraftRollsBackWhenDependenciesFail(t *testing.T) {
	a := newNotificationDraftAPI(t)
	body := map[string]any{"name": "dependency failure", "prompt_template": "reply ok", "cron_expr": "0 9 * * *", "notification": map[string]any{"config": notificationConfig(false)}, "dependencies": []map[string]any{{"source_schedule_id": "missing", "window_type": "between_target_fires", "content_types": []string{"final_answer"}, "incomplete_policy": "wait_then_run_with_warning", "max_wait_seconds": 7200}}}
	if a.request("POST", "/schedules", "owner", body).Code == 200 {
		t.Fatal("invalid dependency accepted")
	}
	var count int64
	a.db.Model(&orm.UserSchedule{}).Count(&count)
	if count != 0 {
		t.Fatal("failed creation persisted a schedule")
	}
	a.schedule("retained", false)
	view := a.data("GET", "/schedules/retained/notifications", "owner", nil)
	body["notification"] = map[string]any{"revision": view["revision"], "config": notificationConfig(false)}
	delete(body, "prompt_template")
	if a.request("PUT", "/schedules/retained", "owner", body).Code == 200 {
		t.Fatal("invalid edit dependency accepted")
	}
	after := a.data("GET", "/schedules/retained/notifications", "owner", nil)
	if after["revision"] != view["revision"] {
		t.Fatal("failed task edit changed notification revision")
	}
	if a.request("PUT", "/schedules/retained", "other", map[string]any{"notification": map[string]any{"revision": view["revision"], "clear": true}}).Code != 404 {
		t.Fatal("foreign task edit not rejected")
	}
}

func TestNotificationDraftConcurrentUpdatesKeepTaskAndRuleTogether(t *testing.T) {
	a := newNotificationDraftAPI(t)
	path := a.schedule("race-draft", false)
	view := a.data("GET", path, "owner", nil)
	type outcome struct {
		name string
		code int
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	for _, name := range []string{"on", "off"} {
		go func(name string) {
			<-start
			result := a.request("PUT", "/schedules/race-draft", "owner", map[string]any{"name": name, "notification": map[string]any{"revision": view["revision"], "config": notificationConfig(name == "on")}})
			results <- outcome{name, result.Code}
		}(name)
	}
	close(start)
	winner := ""
	for i := 0; i < 2; i++ {
		result := <-results
		if result.code == 200 {
			if winner != "" {
				t.Fatal("two stale revisions succeeded")
			}
			winner = result.name
		} else if result.code != 409 {
			t.Fatalf("unexpected concurrent status %d", result.code)
		}
	}
	if winner == "" {
		t.Fatal("no update succeeded")
	}
	var row orm.UserSchedule
	if err := a.db.First(&row, "id = ?", "race-draft").Error; err != nil {
		t.Fatal(err)
	}
	latest := a.data("GET", path, "owner", nil)
	config := notificationObject(t, latest["config"])
	enabled := notificationObject(t, notificationObject(t, config["channels"])["desktop"])["enabled"]
	if row.Name != winner || enabled != (winner == "on") {
		t.Fatal("task and notification came from different updates")
	}
}
