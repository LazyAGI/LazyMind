package plugin

import "testing"

func TestDiagnosePluginFindsCrossFileErrors(t *testing.T) {
	pluginYAML := "id: demo\nsteps:\n  - id: collect\n    label: Collect\ntool_scripts:\n  - path: scripts/tool.py\n    functions: [run]\n"
	stateYAML := "initial: __start__\nsteps: {}\ntransitions: {}\n"
	diagnostics := diagnosePlugin(pluginYAML, stateYAML, "", "{}")
	if !hasDiagnosticErrors(diagnostics) {
		t.Fatal("expected blocking diagnostics")
	}
	want := map[string]bool{"E_STATE_STEP_MISSING": false, "E_START_MISSING": false, "W_TOOL_SCRIPT_MISSING": false}
	for _, item := range diagnostics {
		if _, ok := want[item.Code]; ok {
			want[item.Code] = true
		}
	}
	for code, found := range want {
		if !found {
			t.Fatalf("missing diagnostic %s: %#v", code, diagnostics)
		}
	}
}

func TestDiagnosePluginAcceptsConsistentFiles(t *testing.T) {
	pluginYAML := "id: demo\nslots:\n  - id: result\n    type: text\nsteps:\n  - id: collect\n    label: Collect\nui:\n  tabs:\n    - id: result\n      label: Result\n      layout: vertical\n      slots:\n        - id: result\n"
	stateYAML := "initial: __start__\nsteps:\n  collect:\n    prompt: collect\n    outputs: [result]\ntransitions:\n  __start__:\n    - to: collect\n  collect:\n    - to: __end__\n"
	if diagnostics := diagnosePlugin(pluginYAML, stateYAML, "### collect\nDoes work.", "{}"); hasDiagnosticErrors(diagnostics) {
		t.Fatalf("unexpected diagnostics: %#v", diagnostics)
	}
}

func TestDiagnosticsForTargetExcludesUnrelatedAreas(t *testing.T) {
	items := []repairDiagnostic{
		{Code: "E_START_MISSING", Path: "scenario/state.yml.transitions.__start__", Severity: "error"},
		{Code: "E_UI_TAB_EMPTY", Severity: "error"},
		{Code: "W_SCENARIO_STEP_MISSING", Severity: "warning"},
	}
	filtered := diagnosticsForTarget(items, "statemachine")
	if len(filtered) != 1 || filtered[0].Code != "E_START_MISSING" {
		t.Fatalf("unexpected statemachine diagnostics: %#v", filtered)
	}
}
