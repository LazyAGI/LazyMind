package main

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScheduleDescriptionRejectionDoesNotSaveDraft(t *testing.T) {
	a := newNotificationDraftAPI(t)
	created := draftScheduleData(t, a, "POST", "/schedules", map[string]any{"name": "original", "prompt_template": "allowed", "cron_expr": "0 9 * * *"})
	id := created["id"].(string)
	check := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"passed":false,"matched_word":{"word":"private-match","action":"block"}}`))
	}))
	defer check.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", check.URL)
	for _, tc := range []struct{ method, path string }{{"PUT", "/schedules/" + id}, {"POST", "/schedules"}} {
		res := a.request(tc.method, tc.path, "owner", map[string]any{"name": "changed", "prompt_template": "blocked", "cron_expr": "0 10 * * *"})
		var body struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if res.Code != 400 || body.Code != 2003102 {
			t.Fatalf("unexpected error: %d %s", res.Code, res.Body.String())
		}
		if strings.Contains(res.Body.String(), "private-match") {
			t.Fatal("leaked match details")
		}
	}
	var row orm.UserSchedule
	if err := a.db.First(&row, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if row.Name != "original" || row.PromptTemplate != "allowed" || row.CronExpr != "0 9 * * *" {
		t.Fatal("rejected edit changed task")
	}
	var count int64
	a.db.Model(&orm.UserSchedule{}).Count(&count)
	if count != 1 {
		t.Fatal("rejected creation persisted")
	}
}
