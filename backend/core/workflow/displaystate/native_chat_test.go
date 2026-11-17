package displaystate

import (
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestNativeRetryCompletionExposesNextStep(t *testing.T) {
	for _, sample := range []struct {
		name, driver, step, successor, want, reason string
	}{
		{"successful retry", "completed", "succeeded", "", "waiting", "next_step_required"},
		{"driver still coordinating", "generating", "succeeded", "", "running", "preparing"},
		{"missing bound driver", "", "succeeded", "", "running", "preparing"},
		{"failed retry", "completed", "failed", "", "failed", "failed"},
		{"queued successor", "completed", "succeeded", "queued", "running", "executing"},
		{"running successor", "completed", "succeeded", "running", "running", "executing"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.WorkflowSessionStep{}, &orm.ChatHistory{})
			session := orm.WorkflowSession{ID: "session", ConversationID: "conversation", Status: "active",
				ControllerHost: "lazymind", TriggerHistoryID: "original-turn",
				ControlBindingJSON: `{"chat_history_id":"retry-turn"}`}
			original := orm.ChatHistory{ID: "original-turn", ConversationID: session.ConversationID, RunStatus: "completed"}
			if err := db.Create(&original).Error; err != nil {
				t.Fatal(err)
			}
			if sample.driver != "" {
				retry := orm.ChatHistory{ID: "retry-turn", ConversationID: session.ConversationID, RunStatus: sample.driver}
				if err := db.Create(&retry).Error; err != nil {
					t.Fatal(err)
				}
			}
			for attempt := 1; attempt <= 3; attempt++ {
				status, validity := "failed", "stale"
				if attempt == 3 {
					status, validity = sample.step, "effective"
				}
				row := orm.WorkflowSessionStep{ID: string(rune('a' + attempt)), SessionID: session.ID,
					StepID: "experiment", TaskID: string(rune('a' + attempt)), Attempt: attempt,
					Status: status, Validity: validity, CreatedAt: time.Now().UTC()}
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
			}
			if sample.successor != "" {
				row := orm.WorkflowSessionStep{ID: "next", SessionID: session.ID, StepID: "analyze",
					TaskID: "next", Status: sample.successor, Validity: "effective", CreatedAt: time.Now().UTC()}
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
			}
			view, err := Read(db.DB, session)
			if err != nil || view.Status != sample.want || view.Reason != sample.reason {
				t.Fatalf("view=%+v err=%v want=%s/%s", view, err, sample.want, sample.reason)
			}
			event, err := Enrich(db.DB, session, json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Display View `json:"display"`
			}
			if err := json.Unmarshal(event, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Display != view {
				t.Fatalf("reload and event disagree: %+v %+v", view, payload.Display)
			}
		})
	}
}
