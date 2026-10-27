package displaystate

import (
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func TestDurableDisplayStates(t *testing.T) {
	for _, sample := range []struct {
		name, session, step, mode, want      string
		stopped, skip, stale, runningSibling bool
	}{
		{name: "preparing", session: "waiting", want: "running"},
		{name: "running", session: "active", step: "running", want: "running"},
		{name: "approval", session: "waiting", step: "succeeded", mode: "human", want: "waiting"},
		{name: "skip approval", session: "waiting", step: "succeeded", mode: "human", skip: true, want: "failed"},
		{name: "automatic no advance", session: "waiting", step: "succeeded", mode: "auto", want: "failed"},
		{name: "interrupted", session: "waiting", step: "interrupted", mode: "human", want: "failed"},
		{name: "failed", session: "failed", step: "failed", mode: "human", want: "failed"},
		{name: "driver failure cannot discard pending review", session: "failed", step: "succeeded", mode: "human", want: "waiting"},
		{name: "user pause", session: "waiting", step: "interrupted", stopped: true, want: "paused"},
		{name: "pause before dispatch", session: "waiting", stopped: true, want: "paused"},
		{name: "completed", session: "completed", step: "succeeded", want: "completed"},
		{name: "parallel execution wins", session: "waiting", step: "succeeded", mode: "human", runningSibling: true, want: "running"},
		{name: "stale approval ignored", session: "waiting", step: "succeeded", mode: "human", stale: true, want: "running"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.WorkflowSessionStep{}, &orm.WorkflowRevision{}, &orm.WorkflowApprovalPreference{}, &orm.WorkflowReviewCheckpoint{})
			now := time.Now().UTC()
			session := orm.WorkflowSession{ID: "session", Status: sample.session, WorkflowID: "workflow", CreateUserID: "owner", WorkflowRevisionID: "revision"}
			if sample.stopped {
				session.LastStoppedAt = &now
			}
			graph := graphengine.CompiledStateGraph{Nodes: map[string]graphengine.CompiledNode{"step": {ID: "step", Mode: sample.mode}}}
			if err := db.Create(&orm.WorkflowRevision{ID: "revision", WorkflowResourceID: "resource", RevisionNo: 1, CompiledGraph: graph.JSON()}).Error; err != nil {
				t.Fatal(err)
			}
			if sample.step != "" {
				step := orm.WorkflowSessionStep{ID: "attempt", SessionID: session.ID, StepID: "step", TaskID: "task", Status: sample.step, Attempt: 1, Validity: "effective", CreatedAt: now.Add(-time.Second)}
				if sample.stale {
					step.Validity = "stale"
				}
				if err := db.Create(&step).Error; err != nil {
					t.Fatal(err)
				}
			}
			if sample.skip {
				if err := db.Create(&orm.WorkflowApprovalPreference{UserID: "owner", WorkflowID: "workflow", StepID: "*", ApprovalRequired: false}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if sample.runningSibling {
				if err := db.Create(&orm.WorkflowSessionStep{ID: "sibling", SessionID: session.ID, StepID: "sibling", TaskID: "sibling-task", Status: "pending", Validity: "effective", CreatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
			}
			view, err := Read(db.DB, session)
			if err != nil || view.Status != sample.want {
				t.Fatalf("view=%+v err=%v want=%s", view, err, sample.want)
			}
			if (view.ApprovalStepID != "") != (sample.want == "waiting") {
				t.Fatalf("unexpected approval: %+v", view)
			}
			payload, err := Enrich(db.DB, session, json.RawMessage(`{"display":{"status":"waiting","approval_step_id":"old"}}`))
			if err != nil {
				t.Fatal(err)
			}
			var event struct {
				Display View `json:"display"`
			}
			if err := json.Unmarshal(payload, &event); err != nil {
				t.Fatal(err)
			}
			if event.Display != view {
				t.Fatalf("snapshot/event disagree: %+v %+v", view, event.Display)
			}
		})
	}
}

func TestReadErrorIsNotAnExecutionFailure(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.WorkflowSession{})
	if _, err := Read(db.DB, orm.WorkflowSession{ID: "missing-steps-table"}); err == nil {
		t.Fatal("must propagate a failed state read")
	}
}

func TestChatStopOwnershipSurvivesHandOffAndResume(t *testing.T) {
	for _, sample := range []struct{ name, host, binding, want string }{
		{"original hand-off", "lazymind", "", "trigger"},
		{"resumed hand-off", "lazymind", `{"chat_history_id":"resumed"}`, "resumed"},
		{"external workflow", "external-agent", `{"chat_history_id":"external"}`, ""},
	} {
		t.Run(sample.name, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.WorkflowSessionStep{})
			session := orm.WorkflowSession{ID: "session", Status: "active", ControllerHost: sample.host,
				TriggerHistoryID: "trigger", ControlBindingJSON: sample.binding}
			view, err := Read(db.DB, session)
			if err != nil || view.ChatHistoryID != sample.want {
				t.Fatalf("view=%+v err=%v want history=%s", view, err, sample.want)
			}
			payload, err := Enrich(db.DB, session, json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			var event struct {
				Display View `json:"display"`
			}
			if err := json.Unmarshal(payload, &event); err != nil {
				t.Fatal(err)
			}
			if event.Display != view {
				t.Fatalf("snapshot/event disagree: %+v %+v", view, event.Display)
			}
		})
	}
}

func TestParallelReviewUsesEffectiveAttemptAndUnstartedSuccessor(t *testing.T) {
	for _, scenario := range []string{"pending", "accepted", "newer-failed-attempt", "successor-started", "external-pending", "external-stale"} {
		t.Run(scenario, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.WorkflowSessionStep{}, &orm.WorkflowRevision{}, &orm.WorkflowApprovalPreference{}, &orm.WorkflowReviewCheckpoint{})
			now := time.Now().UTC()
			session := orm.WorkflowSession{ID: "session", Status: "waiting", WorkflowRevisionID: "revision"}
			graph := graphengine.CompiledStateGraph{Nodes: map[string]graphengine.CompiledNode{
				"review": {ID: "review", Mode: "human"}, "sibling": {ID: "sibling", Mode: "auto"},
			}, ControlEdges: []graphengine.CompiledEdge{{From: "review", To: "successor"}}}
			if err := db.Create(&orm.WorkflowRevision{ID: "revision", CompiledGraph: graph.JSON()}).Error; err != nil {
				t.Fatal(err)
			}
			steps := []orm.WorkflowSessionStep{
				{ID: "review", TaskID: "review", SessionID: session.ID, StepID: "review", Attempt: 1, Status: "succeeded", Validity: "effective", CreatedAt: now},
				{ID: "sibling", TaskID: "sibling", SessionID: session.ID, StepID: "sibling", Attempt: 1, Status: "succeeded", Validity: "effective", CreatedAt: now.Add(time.Second)},
			}
			if scenario == "newer-failed-attempt" {
				steps = append(steps, orm.WorkflowSessionStep{ID: "new-review", TaskID: "new-review", SessionID: session.ID, StepID: "review", Attempt: 2, Status: "failed", Validity: "effective", CreatedAt: now.Add(time.Second)})
			}
			if scenario == "successor-started" {
				steps = append(steps, orm.WorkflowSessionStep{ID: "successor", TaskID: "successor", SessionID: session.ID, StepID: "successor", Attempt: 1, Status: "succeeded", Validity: "effective", CreatedAt: now.Add(time.Second)})
			}
			if scenario == "external-pending" || scenario == "external-stale" {
				session.ControllerHost, session.ControlProtocol = "external-agent", "workflow.control.v1"
				if scenario == "external-stale" {
					steps[0].Validity = "stale"
				}
				if err := db.Create(&orm.WorkflowReviewCheckpoint{ID: "checkpoint", SessionID: session.ID, StepID: "review", AttemptID: "review", Status: "pending"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "accepted" {
				if err := db.Create(&orm.WorkflowReviewCheckpoint{ID: "checkpoint", SessionID: session.ID, StepID: "review", AttemptID: "review", Status: "accepted"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&steps).Error; err != nil {
				t.Fatal(err)
			}
			view, err := Read(db.DB, session)
			if err != nil {
				t.Fatal(err)
			}
			wantApproval := scenario == "pending" || scenario == "external-pending"
			if (view.ApprovalStepID == "review") != wantApproval {
				t.Fatalf("wrong review for %s: %+v", scenario, view)
			}
		})
	}
}
