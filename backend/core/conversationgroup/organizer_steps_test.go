package conversationgroup

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"testing"
)

func TestOrganizerDisplaySteps(t *testing.T) {
	for _, tc := range []struct {
		name, status, stage, prep, checkpoint, code string
		active                                      int
		detail                                      string
	}{
		{"preparing", "running", "preparing", `{"total":41,"current":20,"batch_current":1,"batch_total":3}`, `{}`, "", 0, ""},
		{"reuse", "running", "organizing", `{"sealed":true}`, `{}`, "", 1, ""},
		{"audit", "running", "organizing", `{"sealed":true}`, `{"pending":{"operations":[{"op":"merge","source_ids":["a","b"],"target_id":"a","name":"工作","scope":"工作事务"}]}}`, "", 1, "auditing"},
		{"review", "running", "final", `{"sealed":true}`, `{"cursor":100}`, "", 2, ""},
		{"failure restored", "failed", "failed", `{"sealed":true}`, `{"cursor":100}`, "incremental_step_failed", 2, ""},
		{"apply failure", "failed", "failed", `{"sealed":true}`, `{"cursor":100}`, "apply_failed", 3, ""},
		{"canceling", "running", "canceling", `{"total":41}`, `{}`, "", 0, "canceling"},
		{"canceled", "canceled", "canceled", `{"total":41}`, `{}`, "", 0, ""},
		{"complete", "succeeded", "completed", `{"sealed":true}`, `{"cursor":100}`, "", 4, ""},
		{"confirmed", "confirmed", "confirmed", `{"sealed":true}`, `{"cursor":100}`, "", 4, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := organizerSteps(orm.ConversationOrganizerRun{Status: tc.status, Stage: tc.stage, PreparationJSON: json.RawMessage(tc.prep), CheckpointJSON: json.RawMessage(tc.checkpoint), ProgressTotal: 100, ErrorCode: tc.code})
			for i, step := range steps {
				want := "pending"
				if i < tc.active {
					want = "completed"
				}
				if i == tc.active {
					want = "active"
					if tc.status == "failed" || tc.status == "canceled" {
						want = tc.status
					}
					if step.Detail != tc.detail {
						t.Fatalf("detail=%s want=%s", step.Detail, tc.detail)
					}
				}
				if step.Status != want {
					t.Fatalf("step %d: %s want %s", i, step.Status, want)
				}
			}
			if tc.name == "reuse" && steps[0].Detail != "reused" {
				t.Fatal("missing reuse explanation")
			}
			if tc.name == "preparing" && (steps[0].Current != 2 || steps[0].Completed != 1 || steps[0].Total != 3) {
				t.Fatalf("incorrect batch progress: %+v", steps[0])
			}
		})
	}
}
