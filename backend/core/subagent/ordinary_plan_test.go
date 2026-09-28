package subagent

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func publicPlanFromSnapshot(t *testing.T, db *orm.DB, taskID string) []string {
	t.Helper()
	view, err := ordinarySnapshot(t.Context(), db.DB, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.ProcessSteps) != 0 || view.ProcessState != "not_provided" {
		t.Fatal("a plan must not invent execution steps")
	}
	raw, _ := json.Marshal(view)
	var payload struct {
		Plan []string `json:"plan_steps"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Plan
}

func TestOrdinaryPlanRemoteDeliveryAndReload(t *testing.T) {
	db := remoteSubagentFixture(t)
	if err := db.AutoMigrate(&orm.WorkflowSession{}, &orm.WorkflowEvent{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSession{ID: "session-1", ConversationID: "conversation-1", CreateUserID: "user-1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.SubAgentTask{}).Where("id = ?", "task-remote").Update("execution_id", "current").Error; err != nil {
		t.Fatal(err)
	}
	steps := []string{"Read requirements", "Check constraints", "Write brief"}
	response := postRemoteTaskEvent(t, "lease-live", map[string]any{"type": "plan", "steps": steps, "scope_version": 2})
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := publicPlanFromSnapshot(t, db, "task-remote"); !reflect.DeepEqual(got, steps) {
		t.Fatalf("missing saved plan: %v", got)
	}
	var row orm.SubAgentStep
	if err := db.Where("task_id = ? AND role = ?", "task-remote", "plan").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.ExecutionID != "current" {
		t.Fatalf("unscoped plan: %q", row.ExecutionID)
	}
	task, err := GetTask(t.Context(), db.DB, "task-remote")
	if err != nil || task.DisplayRevision == 0 {
		t.Fatalf("snapshot revision did not advance: %v", err)
	}
	var events []orm.WorkflowEvent
	if err := db.Where("event_type = ?", "ordinary.task_changed").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EntityID != "attempt-remote" || string(events[0].PayloadJSON) != "{}" {
		t.Fatalf("missing safe live notification: %#v", events)
	}
	if err := beginDisplayExecution(t.Context(), db.DB, task.ID, "next"); err != nil {
		t.Fatal(err)
	}
	if got := publicPlanFromSnapshot(t, db, task.ID); len(got) != 0 {
		t.Fatalf("previous execution plan leaked: %v", got)
	}
}

func TestOrdinaryProgressSurvivesSnapshotAndExecutionReset(t *testing.T) {
	db, task := ordinaryFixture(t)
	ctx := t.Context()
	previousRevision := int64(-1)
	for _, pct := range []int{0, 25, 75, 100} {
		if err := UpdateProgress(ctx, db.DB, task.ID, pct, "SECRET internal phase", 0); err != nil {
			t.Fatal(err)
		}
		view, err := ordinarySnapshot(ctx, db.DB, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(view)
		var payload struct {
			Progress *int `json:"progress_pct"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Progress == nil || *payload.Progress != pct || view.Revision <= previousRevision {
			t.Fatalf("progress or revision missing: %s", raw)
		}
		previousRevision = view.Revision
	}
	if err := beginDisplayExecution(ctx, db.DB, task.ID, "next"); err != nil {
		t.Fatal(err)
	}
	view, err := ordinarySnapshot(ctx, db.DB, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(view)
	var payload struct {
		Progress *int `json:"progress_pct"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Progress == nil || *payload.Progress != 0 {
		t.Fatalf("stale progress after retry: %s", raw)
	}
}

func TestOrdinaryPlanCompatibilityIsScopedAndValidated(t *testing.T) {
	valid := `{"scope_version":2,"steps":["Read requirements","Check constraints","Write brief"],"private_log":"SECRET"}`
	for _, tc := range []struct {
		name, content, execution string
		offset                   time.Duration
		started, want            bool
	}{
		{"current generation", valid, "current", time.Second, true, true},
		{"already saved unscoped plan", valid, "", time.Second, true, true},
		{"old unscoped plan", valid, "", -time.Second, true, false},
		{"unscoped before restart starts", valid, "", time.Second, false, false},
		{"other generation", valid, "other", time.Second, true, false},
		{"old workflow-wide scope", `{"scope_version":1,"steps":["Read","Check","Write"]}`, "current", time.Second, true, false},
		{"invalid shape", `{"scope_version":2,"steps":["Read",{},"Write"]}`, "current", time.Second, true, false},
		{"empty item", `{"scope_version":2,"steps":["Read"," ","Write"]}`, "current", time.Second, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, task := ordinaryFixture(t)
			start := time.Now().UTC()
			updates := map[string]any{"execution_id": "current"}
			if tc.started {
				updates["started_at"] = start
			}
			if err := db.Model(task).Updates(updates).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.SubAgentStep{ID: "plan", TaskID: task.ID, ExecutionID: tc.execution, Seq: 0, Role: "plan", Content: json.RawMessage(tc.content), CreatedAt: start.Add(tc.offset)}).Error; err != nil {
				t.Fatal(err)
			}
			got := publicPlanFromSnapshot(t, db, task.ID)
			if (len(got) == 3) != tc.want {
				t.Fatalf("unexpected public plan: %v", got)
			}
		})
	}
}
