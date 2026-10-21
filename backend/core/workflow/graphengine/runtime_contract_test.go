package graphengine

import (
	"encoding/json"
	"strings"
	"testing"
)

const declaredWorkflow = `
id: copied-package
runtime:
  trigger_inputs: []
  transactional_outputs: true
  publication: {step: assemble, required_slots: [result]}
  published_input_aliases: {seed: result}
  execution_limits:
    assemble: {rounds: 5, timeout: 120, tool_calls: {save: 1}}
slots:
  - {id: seed, type: json, external: true}
  - {id: result, type: json}
steps:
  - {id: assemble}
`
const declaredState = `
transitions:
  __start__: [{to: assemble}]
  assemble: [{to: __end__}]
steps:
  assemble:
    optional_inputs: [{material: seed}]
    outputs: [result]
`

func TestRuntimeContractsSurviveCopyAndEmptyInputAllowlist(t *testing.T) {
	for _, id := range []string{"copied-package", "unrelated-report"} {
		result := Compile(strings.ReplaceAll(declaredWorkflow, "copied-package", id), declaredState, "", ProfilePublish)
		if !result.Valid {
			t.Fatalf("%s: %#v", id, result.Diagnostics)
		}
		raw, err := json.Marshal(result.Graph.Runtime)
		if err != nil {
			t.Fatal(err)
		}
		var policy RuntimePolicy
		if err := json.Unmarshal(raw, &policy); err != nil {
			t.Fatal(err)
		}
		if policy.TriggerInputs == nil || len(*policy.TriggerInputs) != 0 {
			t.Fatalf("empty deny-all became undeclared: %s", raw)
		}
		if policy.ExecutionLimits["assemble"].Rounds != 5 {
			t.Fatal("lost budget")
		}
	}
}
func TestInvalidRuntimeContractsAreRejected(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement, code string }{
		{"unknown step", "assemble: {rounds:", "missing: {rounds:", "E_RUNTIME_EXECUTION_LIMIT_INVALID"},
		{"unbounded rounds", "rounds: 5", "rounds: 65", "E_RUNTIME_EXECUTION_LIMIT_INVALID"},
		{"internal input", "trigger_inputs: []", "trigger_inputs: [result]", "E_RUNTIME_TRIGGER_INPUT_INVALID"},
		{"non atomic", "transactional_outputs: true", "transactional_outputs: false", "E_RUNTIME_PUBLICATION_INVALID"},
		{"external output", "required_slots: [result]", "required_slots: [seed]", "E_RUNTIME_PUBLICATION_SLOT_UNKNOWN"},
		{"unpublished alias", "{seed: result}", "{seed: seed}", "E_RUNTIME_INPUT_ALIAS_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := Compile(strings.ReplaceAll(declaredWorkflow, tc.old, tc.replacement), declaredState, "", ProfilePublish)
			for _, d := range result.Diagnostics {
				if d.Code == tc.code {
					return
				}
			}
			t.Fatalf("expected %s: %#v", tc.code, result.Diagnostics)
		})
	}
}
