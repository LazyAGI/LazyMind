package workflow

import "testing"

func TestDiagnoseWorkflowFindsCrossFileErrors(t *testing.T) {
	workflowYAML := "id: demo\nsteps:\n  - id: collect\n    label: Collect\ntool_scripts:\n  - path: scripts/tool.py\n    functions: [run]\n"
	stateYAML := "initial: __start__\nsteps: {}\ntransitions: {}\n"
	diagnostics := diagnoseWorkflow(workflowYAML, stateYAML, "", "{}")
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

func TestDiagnoseWorkflowAcceptsConsistentFiles(t *testing.T) {
	workflowYAML := "id: demo\nslots:\n  - id: result\n    type: text\nsteps:\n  - id: collect\n    label: Collect\nui:\n  tabs:\n    - id: result\n      label: Result\n      layout: vertical\n      slots:\n        - id: result\n"
	stateYAML := "initial: __start__\nsteps:\n  collect:\n    prompt: collect\n    outputs: [result]\ntransitions:\n  __start__:\n    - to: collect\n  collect:\n    - to: __end__\n"
	if diagnostics := diagnoseWorkflow(workflowYAML, stateYAML, "### collect\nDoes work.", "{}"); hasDiagnosticErrors(diagnostics) {
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

func TestPublishDiagnosticsValidateBuiltinArtifactActions(t *testing.T) {
	valid := `id: demo
artifact_actions:
  rewrite_selection:
    preview_tool: builtin:document.rewrite_selection.v1
    execute_tool: builtin:document.rewrite_selection.v1
  save_document:
    execute_tool: builtin:document.save_document.v1
  convert_document:
    preview_tool: builtin:document.convert_document.v1
    execute_tool: builtin:document.convert_document.v1
  write_document:
    execute_tool: builtin:document.write_document.v1
`
	if diagnostics := builtinArtifactActionDiagnostics(valid); len(diagnostics) != 0 {
		t.Fatalf("valid built-ins rejected: %#v", diagnostics)
	}

	invalid := `id: demo
artifact_actions:
  rewrite_selection:
    preview_tool: builtin:document.rewrite_selection.v2
  save_document:
    preview_tool: builtin:document.save_document.v1
  wrong_action:
    execute_tool: builtin:document.sync_document.v1
`
	diagnostics := builtinArtifactActionDiagnostics(invalid)
	if len(diagnostics) != 3 {
		t.Fatalf("expected three diagnostics, got %#v", diagnostics)
	}
	want := map[string]bool{
		"E_DOCUMENT_ACTION_REFERENCE_INVALID": false,
		"E_DOCUMENT_ACTION_PHASE_UNSUPPORTED": false,
	}
	for _, item := range diagnostics {
		if _, exists := want[item.Code]; exists {
			want[item.Code] = true
		}
	}
	for code, found := range want {
		if !found {
			t.Fatalf("missing %s in %#v", code, diagnostics)
		}
	}
}
