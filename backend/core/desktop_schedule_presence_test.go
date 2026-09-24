package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/schedulepresence"
)

func TestDesktopSchedulePresencePausesOnlyAutomaticWorkAndSkipsMissedRuns(t *testing.T) {
	a := newNotificationAPI(t)
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "desktop")
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "desktop-owner-token")
	schedulepresence.SetActive(false)
	t.Cleanup(func() { schedulepresence.SetActive(false) })
	now := time.Now().UTC()
	schedule := orm.UserSchedule{
		ID: "desktop-pause", UserID: "owner", Name: "Daily", CronExpr: "*/10 * * * *",
		Timezone: "UTC", PromptTemplate: "hello", Enabled: true,
		NextRunAt: now.Add(-time.Hour), CreatedAt: now,
	}
	if err := a.db.Create(&schedule).Error; err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	registerCoreRoutes(router)
	request := func(active bool, token string) *httptest.ResponseRecorder {
		body := []byte(`{"active":false}`)
		if active {
			body = []byte(`{"active":true}`)
		}
		req := httptest.NewRequest(http.MethodPost, "/internal/desktop/schedule-presence", bytes.NewReader(body))
		req.Header.Set("X-LazyMind-Internal-Token", token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	if schedulepresence.Active() {
		t.Fatal("Desktop schedules started without a visible window lease")
	}
	if got := request(true, "wrong").Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthorized update = %d", got)
	}
	if got := request(true, "desktop-owner-token").Code; got != http.StatusNoContent {
		t.Fatalf("resume = %d", got)
	}
	var saved orm.UserSchedule
	if err := a.db.First(&saved, "id = ?", schedule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !saved.NextRunAt.After(now) || saved.RunCount != 0 {
		t.Fatalf("missed run was not skipped: %#v", saved)
	}
	if !schedulepresence.Active() {
		t.Fatal("Desktop schedules did not resume")
	}
	if got := request(false, "desktop-owner-token").Code; got != http.StatusNoContent {
		t.Fatalf("pause = %d", got)
	}
	if schedulepresence.Active() {
		t.Fatal("Desktop schedules remained active after close")
	}
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "local")
	if !schedulepresence.Active() {
		t.Fatal("Local background work was paused by the Desktop gate")
	}
}
