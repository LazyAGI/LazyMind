package graphengine

import (
	"encoding/json"
	"strings"
	"testing"
)

const externalWorkflow = `
id: portable
runtime:
  external_execution:
    version: "1"
    skills: [scenario/external.md]
    constraints: [Validate artifacts before submitting]
    steps:
      write:
        prompt: Generate the output with the external agent
        skills: [scenario/write.md, scenario/external.md]
        acceptance_criteria: [One page is allowed]
        required_outputs: [result]
        tools:
          - {name: inspect_output, source: package}
          - {name: "builtin:image_generator", source: global}
slots:
  - {id: result, type: file}
steps:
  - {id: write}
`

const externalState = `
transitions:
  __start__: [{to: write}]
  write: [{to: __end__}]
steps:
  write:
    prompt: Use the native generator
    tools: [native_generator]
    tools_only: true
    terminal_tools: [native_generator]
    acceptance_criteria: [At least two pages]
    outputs: [{material: result, optional: true}]
`

func TestExternalExecutionCompilePreservesBothModes(t *testing.T) {
	result := Compile(externalWorkflow, externalState, "", ProfilePublish)
	if !result.Valid {
		t.Fatalf("compile: %+v", result.Diagnostics)
	}
	node := result.Graph.Nodes["write"]
	if node.Prompt != "Use the native generator" || !node.ToolsOnly || len(node.LegacyTools) != 1 || len(node.TerminalTools) != 1 || node.Acceptance[0] != "At least two pages" {
		t.Fatalf("native contract changed: %+v", node)
	}
	if !node.ExternalExecution.Supported() || len(node.ExternalExecution.Skills) != 2 || node.ExternalExecution.RequiredOutputs[0] != "result" || len(node.ExternalExecution.Constraints) != 1 {
		t.Fatalf("external contract: %+v", node.ExternalExecution)
	}
	if result.Graph.Runtime.IsZero() {
		t.Fatal("external-only runtime policy was omitted")
	}
	var restored CompiledStateGraph
	if err := json.Unmarshal(result.Graph.JSON(), &restored); err != nil || !restored.Nodes["write"].ExternalExecution.Supported() {
		t.Fatalf("compiled round trip: %v", err)
	}
	if got := Compile(strings.Replace(externalWorkflow, "id: portable", "id: copied", 1), externalState, "", ProfilePublish); !got.Valid || !got.Graph.Nodes["write"].ExternalExecution.Supported() {
		t.Fatal("external policy depended on built-in workflow id")
	}
}

func TestExternalExecutionInvalidDeclarations(t *testing.T) {
	for _, test := range []struct{ name, old, replacement string }{
		{"version", `version: "1"`, `version: "2"`},
		{"missing step", "      write:\n", "      unknown:\n"},
		{"missing prompt", "prompt: Generate the output with the external agent", `prompt: ""`},
		{"traversal", "scenario/write.md", "../secret"},
		{"parent directory", "scenario/write.md", ".."},
		{"absolute", "scenario/write.md", "/tmp/skill.md"},
		{"windows", "scenario/write.md", `C:\skill.md`},
		{"source", "source: package", "source: native"},
		{"duplicate name", `name: "builtin:image_generator"`, "name: inspect_output"},
		{"undeclared output", "required_outputs: [result]", "required_outputs: [private]"},
		{"duplicate output", "required_outputs: [result]", "required_outputs: [result, result]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := Compile(strings.Replace(externalWorkflow, test.old, test.replacement, 1), externalState, "", ProfilePublish)
			if result.Valid {
				t.Fatal("invalid external policy accepted")
			}
		})
	}
}

func TestExternalExecutionEmptyOverridesSurviveSerialization(t *testing.T) {
	workflow := strings.Replace(externalWorkflow, "required_outputs: [result]", "required_outputs: []", 1)
	workflow = strings.Replace(workflow, "acceptance_criteria: [One page is allowed]", "acceptance_criteria: []", 1)
	result := Compile(workflow, externalState, "", ProfilePublish)
	if !result.Valid {
		t.Fatalf("compile: %+v", result.Diagnostics)
	}
	var restored CompiledStateGraph
	if err := json.Unmarshal(result.Graph.JSON(), &restored); err != nil {
		t.Fatal(err)
	}
	policy := restored.Nodes["write"].ExternalExecution
	if policy.RequiredOutputs == nil || policy.Acceptance == nil {
		t.Fatal("explicit empty override became inheritance")
	}
}
