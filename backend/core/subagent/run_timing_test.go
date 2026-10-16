package subagent

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/common/taskdisplay"
	"lazymind/core/tasktiming"
)

func TestAttemptTimingSurvivesResumeAndInterrupt(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.SubAgentTask{}, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{})
	ctx := context.Background()
	now := time.Now().UTC()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tasktiming.Begin(ctx, db.DB, "root", "history", "conv", "owner", now.Add(-time.Minute)))
	started := now.Add(-10 * time.Second)
	task := orm.SubAgentTask{ID: "task", ConversationID: "conv", TriggerHistoryID: "history", CreateUserID: "owner", Status: StatusRunning, ExecutionID: "old", StartedAt: &started, InputSlots: []byte(`[]`), OutputSlots: []byte(`[]`), Params: []byte(`{}`)}
	must(db.Create(&task).Error)
	must(tasktiming.RecordSubagent(ctx, db.DB, task))
	must(beginDisplayExecution(ctx, db.DB, "task", "new"))
	accepted, err := AcceptTaskStart(ctx, db.DB, "task")
	must(err)
	if !accepted {
		t.Fatal("start rejected")
	}
	_, err = InterruptConversation(ctx, db.DB, "conv", "stopped")
	must(err)
	var attempts []orm.TaskAttemptTiming
	must(db.Order("started_at ASC").Find(&attempts).Error)
	if len(attempts) != 2 || attempts[0].FinishedAt == nil || attempts[1].FinishedAt == nil {
		t.Fatalf("lost/unfinished attempts: %+v", attempts)
	}
	run, err := tasktiming.Snapshot(ctx, db.DB, "owner", "conv", "history", now.Add(time.Minute))
	must(err)
	if run.Timing.RunningAttemptCount != 0 || *run.Timing.CumulativeExecutionMS < 10000 || *run.Timing.CumulativeExecutionMS > 15000 {
		t.Fatalf("invalid cumulative: %+v", run.Timing)
	}
}

func TestOrdinaryListReturnsRootBeforeFirstChildAndIsolatesOwner(t *testing.T) {
	db := newSubagentHTTPTestDB(t)
	if err := db.AutoMigrate(&orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{}); err != nil {
		t.Fatal(err)
	}
	if err := tasktiming.Begin(context.Background(), db.DB, "accepted", "history", "conv", "owner", time.Now().UTC().Add(-10*time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"owner", "other"} {
		req := httptest.NewRequest("GET", "/conversations/conv/tasks?view=ordinary", nil)
		req = mux.SetURLVars(req, map[string]string{"conversation_id": "conv"})
		req.Header.Set("X-User-Id", owner)
		rec := httptest.NewRecorder()
		ListConversationTasks(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Data struct {
				Tasks []taskdisplay.OrdinaryTaskView `json:"tasks"`
				Runs  []taskdisplay.OrdinaryRunView  `json:"runs"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Data.Tasks) != 0 {
			t.Fatal("fabricated child")
		}
		if owner == "owner" {
			if len(body.Data.Runs) != 1 || body.Data.Runs[0].Timing == nil || *body.Data.Runs[0].Timing.ElapsedMS < 10000 {
				t.Fatalf("missing accepted run: %s", rec.Body.String())
			}
		} else if len(body.Data.Runs) != 0 {
			t.Fatal("foreign timing exposed")
		}
	}
}
