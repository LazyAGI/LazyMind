package graphengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const sharedWorkflow = `
id: portable
runtime:
  execution:
    version: "1"
    skills: [scenario/shared.md]
    constraints: [Validate the actual deliverable]
    steps:
      write:
        skills: [scenario/write.md, scenario/shared.md]
        tools: [{name: inspect_output, source: package}]
slots:
  - {id: result, type: file}
steps:
  - {id: write}
`

func TestSharedExecutionHasOneBusinessSpecification(t *testing.T) {
	result := Compile(sharedWorkflow, externalState, "", ProfilePublish)
	if !result.Valid {
		t.Fatalf("compile: %+v", result.Diagnostics)
	}
	node := result.Graph.Nodes["write"]
	if node.Execution == nil || node.Execution.Version != "1" || len(node.Execution.Skills) != 2 || len(node.Execution.Constraints) != 1 || result.Graph.Runtime.IsZero() {
		t.Fatalf("shared policy missing: %+v", node)
	}
	if node.ExternalExecution.Prompt != node.Prompt || node.ExternalExecution.Acceptance != nil || node.ExternalExecution.RequiredOutputs != nil {
		t.Fatal("external compatibility projection overrode the shared business specification")
	}
	if !reflect.DeepEqual(node.ExternalExecution.Skills, node.Execution.Skills) || !reflect.DeepEqual(node.ExternalExecution.Constraints, node.Execution.Constraints) || node.ExternalExecution.AdapterInstructions == "" || !node.ToolsOnly {
		t.Fatal("executor adapters lost shared instructions or native bindings")
	}
	var restored CompiledStateGraph
	if err := json.Unmarshal(result.Graph.JSON(), &restored); err != nil || !reflect.DeepEqual(restored.Nodes["write"].Execution, node.Execution) {
		t.Fatalf("compiled round trip: %v", err)
	}
	for _, replacement := range []string{"Keep one page", "Keep the user's full page count"} {
		changed := Compile(sharedWorkflow, strings.Replace(externalState, "Use the native generator", replacement, 1), "", ProfilePublish)
		if !changed.Valid || changed.Graph.Nodes["write"].ExternalExecution.Prompt != replacement {
			t.Fatal("editing the single state prompt did not update the external contract")
		}
	}
}

func TestSharedExecutionRejectsAlternativeBusinessProfiles(t *testing.T) {
	for name, override := range map[string]string{
		"prompt": "prompt: Different task", "acceptance": "acceptance_criteria: [Different requirement]",
		"outputs": "required_outputs: [result]", "typo": "constrants: [Ignored safety rule]",
	} {
		t.Run(name, func(t *testing.T) {
			workflow := strings.Replace(sharedWorkflow, "      write:\n", "      write:\n        "+override+"\n", 1)
			if result := Compile(workflow, externalState, "", ProfilePublish); result.Valid {
				t.Fatal("separate business specification silently accepted")
			}
		})
	}
	for _, pair := range [][2]string{
		{"version: \"1\"", "version: \"2\""}, {"scenario/write.md", "../private.md"},
		{"      write:", "      unknown:"}, {"source: package", "source: shell"},
		{"runtime:\n", "runtime:\n  external_execution: {version: '1', steps: {}}\n"},
	} {
		if Compile(strings.Replace(sharedWorkflow, pair[0], pair[1], 1), externalState, "", ProfilePublish).Valid {
			t.Fatalf("invalid shared policy accepted: %v", pair)
		}
	}
}

func TestAllBuiltinWorkflowsShareBusinessContracts(t *testing.T) {
	paths, err := filepath.Glob("../../../../workflows/*/workflow.yaml")
	if err != nil || len(paths) != 14 {
		t.Fatalf("discover packages: %d %v", len(paths), err)
	}
	steps := 0
	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			workflow, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			state, err := os.ReadFile(filepath.Join(filepath.Dir(path), "scenario/state.yml"))
			if err != nil {
				t.Fatal(err)
			}
			result := Compile(string(workflow), string(state), "", ProfilePublish)
			if !result.Valid || result.Graph.Runtime.Execution == nil || result.Graph.Runtime.ExternalExecution != nil {
				t.Fatalf("shared package compilation: %+v", result.Diagnostics)
			}
			guide, err := os.ReadFile(filepath.Join(filepath.Dir(path), "scenario/execution.md"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(guide), "use the host's own capabilities first") {
				t.Fatal("pinned guide does not prefer the external host's capabilities")
			}
			if filepath.Base(filepath.Dir(path)) == "ppt-workflow" {
				analysis := result.Graph.Nodes["analyze_requirements"]
				acceptance := strings.Join(analysis.Acceptance, "\n")
				if strings.Contains(analysis.Prompt, "deterministic model capability check") ||
					strings.Contains(acceptance, "deterministic check exactly once") {
					t.Fatal("shared PPT contract requires a native post-step check")
				}
				if !strings.Contains(acceptance, "in generate_background_images") ||
					!strings.Contains(acceptance, "exactly when backgrounds are disabled") {
					t.Fatal("PPT acceptance lost the background routing checks")
				}
				for step, required := range map[string][]string{
					"build_outline":     {"runtime/prompts/outline.md"},
					"plan_page_prompts": {"runtime/prompts/style_spec.md", "runtime/references/style_catalog.md"},
					"generate_ppt":      {"runtime/prompts/page_html.md", "runtime/prompts/refine_review.md", "runtime/prompts/refine_apply.md", "runtime/references/html_constraints.md"},
				} {
					node := result.Graph.Nodes[step]
					for _, file := range required {
						found := false
						for _, skill := range node.Execution.Skills {
							if skill == file {
								found = true
							}
						}
						if !found {
							t.Fatalf("%s omitted native presentation instructions %s", step, file)
						}
					}
					if !reflect.DeepEqual(node.Execution.Skills, node.ExternalExecution.Skills) {
						t.Fatalf("%s supplies different Skills to external workers", step)
					}
				}
			}

			for _, node := range result.Graph.Nodes {
				steps++
				if node.Execution == nil || !node.ExternalExecution.Supported() || node.Prompt != node.ExternalExecution.Prompt || node.ExternalExecution.Acceptance != nil || node.ExternalExecution.RequiredOutputs != nil {
					t.Fatalf("divergent business specification: %s", node.ID)
				}
				for _, skill := range node.Execution.Skills {
					if _, err := os.ReadFile(filepath.Join(filepath.Dir(path), skill)); err != nil {
						t.Fatalf("shared Skill unavailable: %s %v", skill, err)
					}
				}
			}
		})
	}
	if steps != 107 {
		t.Fatalf("expected all 107 shared steps, got %d", steps)
	}
}
