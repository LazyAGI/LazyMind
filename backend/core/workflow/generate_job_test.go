package workflow

import (
	"testing"

	"gopkg.in/yaml.v3"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func TestValidateGeneratedWorkflowSkeletonRejectsPlaceholderOutput(t *testing.T) {
	err := validateGeneratedWorkflowSkeleton(`
id: ""
name: ""
slots:
  - id: research_topic
steps: []
`)
	if err == nil {
		t.Fatal("expected placeholder skeleton to be rejected")
	}
}

func TestValidateGeneratedWorkflowSkeletonAcceptsConcreteOutput(t *testing.T) {
	err := validateGeneratedWorkflowSkeleton(`
id: deep_research
name: Deep Research
slots:
  - id: research_topic
    type: text
    external: true
  - id: final_report
    type: text
steps:
  - id: synthesize_report
    label: Synthesize report
`)
	if err != nil {
		t.Fatalf("expected concrete skeleton to pass: %v", err)
	}
}

func TestValidateGenerateResumePointRequiresPriorArtifacts(t *testing.T) {
	if err := validateGenerateResumePoint(orm.WorkflowDraft{}, generatePhaseDesignBrief); err != nil {
		t.Fatalf("design_brief start should always be allowed: %v", err)
	}
	if err := validateGenerateResumePoint(orm.WorkflowDraft{}, generatePhaseSkeleton); err == nil {
		t.Fatal("skeleton start should require design brief")
	}
	workflowYAML := `
id: demo
name: Demo
slots:
  - id: user_input
    external: true
  - id: result
steps:
  - id: summarize
`
	stateYAML := `
transitions:
  __start__: [{to: summarize}]
  summarize: [{to: __end__}]
steps:
  summarize:
    inputs: [{slot: user_input, required: true}]
    outputs: [result]
`
	draft := orm.WorkflowDraft{
		DesignBriefContent:  "brief",
		WorkflowYAMLContent: workflowYAML,
		StateYAMLContent:    stateYAML,
	}
	if err := validateGenerateResumePoint(draft, generatePhaseSkeleton); err != nil {
		t.Fatalf("skeleton start should accept design brief: %v", err)
	}
	if err := validateGenerateResumePoint(draft, generatePhaseStateMachine); err != nil {
		t.Fatalf("state_machine start should accept workflow yaml: %v", err)
	}
	if err := validateGenerateResumePoint(draft, generatePhaseScenarioScripts); err != nil {
		t.Fatalf("scenario_scripts start should accept valid workflow+state: %v; diagnostics=%#v", err, diagnoseWorkflowWithProfile(workflowYAML, stateYAML, "", "{}", graphengine.ProfileGenerationPhase))
	}
}

func TestValidateGenerateResumePointAllowsScenarioResumeWithOnlyUIErrors(t *testing.T) {
	workflowYAML := `
id: demo
name: Demo
slots:
  - id: user_input
    external: true
  - id: result
    exposed: true
steps:
  - id: summarize
ui:
  tabs:
    - key: result
      label: Result
      contents:
        - slot: result
`
	stateYAML := `
transitions:
  __start__:
    - to: summarize
  summarize:
    - to: __end__
steps:
  summarize:
    inputs:
      - slot: user_input
        required: true
    outputs:
      - result
`
	draft := orm.WorkflowDraft{
		WorkflowYAMLContent: workflowYAML,
		StateYAMLContent:    stateYAML,
	}
	diagnostics := diagnoseWorkflowWithProfile(workflowYAML, stateYAML, "", "{}", graphengine.ProfileGenerationPhase)
	if !hasDiagnosticErrorsForTarget(diagnostics, "ui") {
		t.Fatalf("fixture should have UI errors: %#v", diagnostics)
	}
	if hasDiagnosticErrorsForTarget(diagnostics, "statemachine") {
		t.Fatalf("fixture should not have state machine errors: %#v", diagnostics)
	}
	if err := validateGenerateResumePoint(draft, generatePhaseScenarioScripts); err != nil {
		t.Fatalf("scenario_scripts resume should not be blocked by UI-only errors: %v", err)
	}
}

func TestValidateGeneratedScenarioContentRejectsPlaceholders(t *testing.T) {
	stateYAML := `
steps:
  summarize:
    outputs:
      - result
transitions:
  __start__:
    - to: summarize
  summarize:
    - to: __end__
`
	scenarioMD := `
# Scenario

### summarize (summarize)

（暂无描述）
`
	if err := validateGeneratedScenarioContent(scenarioMD, stateYAML); err == nil {
		t.Fatal("expected placeholder scenario content to be rejected")
	}
}

func TestValidateGeneratedScenarioContentAcceptsSubstantiveSections(t *testing.T) {
	stateYAML := `
steps:
  summarize:
    outputs:
      - result
transitions:
  __start__:
    - to: summarize
  summarize:
    - to: __end__
`
	scenarioMD := `
# Scenario

### summarize (summarize)

The summarize step reads the user input and upstream source material, identifies
the important findings, and produces the final result material for the user.
`
	if err := validateGeneratedScenarioContent(scenarioMD, stateYAML); err != nil {
		t.Fatalf("expected substantive scenario content to pass: %v", err)
	}
}

func TestReusableSkillScriptsKeepsOnlyOriginalScriptFiles(t *testing.T) {
	pkg := map[string]any{
		"files": []any{
			map[string]any{"path": "scripts/tools/check.py", "content": "def check():\n    return True\n"},
			map[string]any{"path": "scripts/helpers/format.py", "content": "def format_value(v):\n    return str(v)\n"},
			map[string]any{"path": "SKILL.md", "content": "# Skill"},
		},
	}
	report := map[string]any{
		"scripts/tools/check.py": map[string]any{"classification": "importable_tool"},
		"scripts/helpers/format.py": map[string]any{
			"classification": "wrappable_command",
		},
		"synthesis_checklist": "function runSynthesisCheck() { return true; }",
	}
	scripts := reusableSkillScripts(pkg, report)
	if scripts["scripts/tools/check.py"] == "" {
		t.Fatalf("expected original script path to be preserved: %#v", scripts)
	}
	if scripts["scripts/helpers/format.py"] == "" {
		t.Fatalf("expected nested script path to be preserved: %#v", scripts)
	}
	if _, ok := scripts["scripts/generated/synthesis_checklist.js"]; ok {
		t.Fatalf("inline analysis code should not be stored as an original script: %#v", scripts)
	}
}

func TestEnsureGeneratedStepTabsExpandsSingleResultsTab(t *testing.T) {
	input := `id: demo
name: Demo
slots:
  - id: parsed
    type: text
    exposed: false
  - id: result
    type: text
    exposed: true
steps:
  - id: parse
    label: Parse
    outputs: [parsed]
  - id: finish
    label: Finish
    outputs: [result]
ui:
  tabs:
    - id: results
      label: Results
      slots: [{id: result}]
`
	output := ensureGeneratedStepTabs(input)
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(output), &doc); err != nil {
		t.Fatal(err)
	}
	ui := doc["ui"].(map[string]any)
	tabs := ui["tabs"].([]any)
	if len(tabs) != 2 {
		t.Fatalf("tabs = %#v", tabs)
	}
	first := tabs[0].(map[string]any)
	if first["id"] != "parse" || first["step_id"] != "parse" {
		t.Fatalf("first tab = %#v", first)
	}
	slots := doc["slots"].([]any)
	if slots[0].(map[string]any)["exposed"] != true {
		t.Fatalf("intermediate output was not exposed: %#v", slots[0])
	}
}
