package plugin

import "testing"

func TestDiagnosePluginFindsCrossFileErrors(t *testing.T) {
	pluginYAML := "id: demo\nsteps:\n  - id: collect\n    label: Collect\ntool_scripts:\n  - path: scripts/tool.py\n    functions: [run]\n"
	stateYAML := "initial: __start__\nsteps: {}\ntransitions: {}\n"
	diagnostics := diagnosePlugin(pluginYAML, stateYAML, "", "{}")
	if !hasDiagnosticErrors(diagnostics) {
		t.Fatal("expected blocking diagnostics")
	}
	want := map[string]bool{"state_step_missing": false, "state_start_missing": false, "state_transition_missing": false, "tool_script_missing": false}
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
	pluginYAML := "id: demo\nsteps:\n  - id: collect\n    label: Collect\n"
	stateYAML := "initial: __start__\nsteps:\n  collect:\n    prompt: collect\ntransitions:\n  __start__:\n    - to: collect\n  collect:\n    - to: __end__\n"
	if diagnostics := diagnosePlugin(pluginYAML, stateYAML, "### collect\nDoes work.", "{}"); hasDiagnosticErrors(diagnostics) {
		t.Fatalf("unexpected diagnostics: %#v", diagnostics)
	}
}
