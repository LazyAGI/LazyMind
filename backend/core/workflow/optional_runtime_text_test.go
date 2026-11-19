package workflow

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/executor"
	"lazymind/core/workflow/graphengine"
)

func TestOptionalRuntimeTextFidelity(t *testing.T) {
	for _, tc := range []struct {
		name, kind, slotFlag, inputFlag, prompt string
		required                                bool
	}{
		{"explicit_optional_fallback", "text", ", required: false", ", required: false", "Use {{user_input}} when report is absent.", false},
		{"step_optional_fallback", "text", "", ", required: false", "Use {{user_input}} when report is absent.", false},
		{"slot_optional_fallback", "text", ", required: false", "", "Use {{user_input}} when report is absent.", false},
		{"omitted_is_not_explicit_optional", "text", "", "", "Task: {{user_input}}", true},
		{"no_fallback", "text", ", required: false", ", required: false", "Read report.", true},
		{"required_text", "text", ", required: true", ", required: true", "Task: {{user_input}}", true},
		{"required_slot_optional_step", "text", ", required: true", ", required: false", "Task: {{user_input}}", true},
		{"required_file", "file", ", required: true", ", required: true", "Task: {{user_input}}", true},
		{"optional_file", "file", ", required: false", ", required: false", "Task: {{user_input}}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := fmt.Sprintf("id: fallback\nslots:\n  - {id: report, type: %s, external: true%s}\n  - {id: out, type: text}\nsteps:\n  - {id: capture}\n", tc.kind, tc.slotFlag)
			s := fmt.Sprintf("steps:\n  capture:\n    inputs: [{slot: report%s}]\n    outputs: [out]\n    prompt: '%s'\ntransitions:\n  __start__: [{to: capture}]\n  capture: [{to: __end__}]\n", tc.inputFlag, tc.prompt)
			if tc.inputFlag == "" {
				s = strings.Replace(s, "[{slot: report}]", "[report]", 1)
			}
			nw, ns, _, _ := applyDeterministicStepIORepairs(w, s)
			c := graphengine.Compile(nw, ns, "", graphengine.ProfileGenerationPhase)
			if c.Graph == nil || hasGraphStateMachineCompileErrors(c.Diagnostics) {
				t.Fatalf("invalid fixture: %v", c.Diagnostics)
			}
			node := c.Graph.Nodes["capture"]
			if got := !graphengine.Evaluate(node.Input, nil).Satisfied; got != tc.required {
				t.Fatalf("missing material blocked=%v, want %v", got, tc.required)
			}
			if !tc.required && tc.kind == "text" && (nw != w || ns != s) {
				t.Fatal("optional fallback was rewritten")
			}
		})
	}
}

// Exercise the durable transition without a model. The output is a test fixture.
func TestOptionalRuntimeTextAttemptKeepsExplicitBinding(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(fmt.Sprint(bound), func(t *testing.T) {
			db, hash := setupBatchTransitionSession(t)
			w := "id: fallback\nslots:\n  - {id: report, type: text, external: true, required: false}\n  - {id: notes, type: text, external: true, required: false}\n  - {id: out, type: text}\nsteps:\n  - {id: branch_b}\n"
			s := "steps:\n  branch_b:\n    inputs: [{slot: report, required: false}, {slot: notes, required: false}]\n    outputs: [out]\n    prompt: 'Use bound report when present, otherwise {{user_input}}.'\ntransitions:\n  __start__: [{to: branch_b}]\n  branch_b: [{to: __end__}]\n"
			nw, ns, _, changed := applyDeterministicStepIORepairs(w, s)
			if changed {
				t.Fatal("multiple optional materials changed")
			}
			compiled := graphengine.Compile(nw, ns, "", graphengine.ProfileGenerationPhase)
			if compiled.Graph == nil || hasGraphStateMachineCompileErrors(compiled.Diagnostics) {
				t.Fatalf("compile: %v", compiled.Diagnostics)
			}
			compiled.Graph.GraphHash = hash
			if err := db.Model(&orm.WorkflowRevision{}).Where("id = ?", "batch-revision").Update("compiled_graph", compiled.Graph.JSON()).Error; err != nil {
				t.Fatal(err)
			}
			if bound {
				if err := db.Create(&orm.WorkflowInputBinding{ID: "explicit-report", WorkflowSessionID: "batch-session", MaterialID: "report", ResourceType: "input_resource", ResourceID: "immutable-report", ResourceRevision: 1, ContentHash: "sha256:fixture", Validity: "effective"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			rec, response := runBatchTransition(t, db, hash, "advance", []map[string]any{{"target_step_id": "branch_b", "task_id": "text-task", "user_input": "runtime fallback"}})
			if rec.Code != http.StatusOK || response["accepted"] != true {
				t.Fatalf("transition: %s", rec.Body.String())
			}
			var step orm.WorkflowSessionStep
			if err := db.First(&step, "task_id = ?", "text-task").Error; err != nil {
				t.Fatal(err)
			}
			var bindings []orm.WorkflowAttemptInputBinding
			if err := db.Where("attempt_id = ?", step.ID).Find(&bindings).Error; err != nil {
				t.Fatal(err)
			}
			if bound {
				if len(bindings) != 1 || bindings[0].SourceID != "immutable-report" || bindings[0].ContentHash != "sha256:fixture" {
					t.Fatalf("explicit binding lost: %+v", bindings)
				}
			} else if len(bindings) != 0 {
				t.Fatal("runtime text must not create an implicit material")
			}
			ctx, err := (executor.DBContextLoader{DB: db.DB}).LoadAttemptContext(t.Context(), step.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(ctx.Objective, "runtime fallback") {
				t.Fatal("runtime text lost")
			}
			if err := executor.ValidateRequiredOutputs(t.Context(), db.DB, ctx); err == nil {
				t.Fatal("missing output accepted")
			}
			if err := db.Create(&orm.WorkflowSlotRevision{ID: "output", SessionID: step.SessionID, SlotID: "out", Slot: "out", ProducerAttemptID: step.ID, Validity: "effective", ContentSnapshot: json.RawMessage(`"fixture output"`)}).Error; err != nil {
				t.Fatal(err)
			}
			if err := executor.ValidateRequiredOutputs(t.Context(), db.DB, ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
