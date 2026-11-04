package graphengine

import (
	"os"
	"path/filepath"
	"testing"

	"lazymind/core/common/askuser"
)

func TestClarificationDefaultsConditionsAndFalse(t *testing.T) {
	fields := []ClarificationField{
		{ID: "format", Type: "single", ChoicePolicy: "fixed", Choices: []string{"docx", "md"}, Default: "docx"},
		{ID: "style", Type: "boolean", Required: true, Reason: "Formatting", AppliesWhen: &ClarificationCondition{Field: "format", Equals: "docx"}},
		{ID: "count", Type: "text", Required: true, Reason: "Size", Validation: &askuser.Validation{Format: "positive_integer"}},
	}
	result, err := ResolveClarifications(fields, map[string]any{"style": false, "count": "5"})
	if err != nil || len(result.Missing) != 0 || result.Values["style"] != false || result.Values["format"] != "docx" {
		t.Fatalf("defaults/false were lost: %#v, %v", result, err)
	}
	result, err = ResolveClarifications(fields, map[string]any{"format": "md", "count": "5", "style": "ignored"})
	if err != nil || len(result.Missing) != 0 {
		t.Fatalf("inapplicable style blocked Markdown: %#v %v", result, err)
	}
	if _, exists := result.Values["style"]; exists {
		t.Fatal("inapplicable answer leaked into execution")
	}
	if _, err := ResolveClarifications(fields, map[string]any{"count": "0"}); err == nil {
		t.Fatal("invalid number accepted")
	}
}

func TestClarificationBatchesPreserveFixedEnums(t *testing.T) {
	fields := make([]ClarificationField, 5)
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		fields[i] = ClarificationField{ID: id, Question: id, Reason: "Required", Required: true, Type: "text"}
	}
	fields[0].Type, fields[0].ChoicePolicy = "single", "fixed"
	fields[0].Choices = []string{"one", "two", "three", "four", "five", "six", "seven"}
	result, err := ResolveClarifications(fields, nil)
	if err != nil || len(result.Missing) != 5 || len(result.Questions) != 3 || len(result.Questions[0].Choices) != 7 {
		t.Fatalf("bad first batch: %#v %v", result, err)
	}
	result, err = ResolveClarifications(fields, map[string]any{"a": "seven", "b": "answer", "c": "answer"})
	if err != nil || len(result.Questions) != 2 || result.Questions[0].ID != "d" {
		t.Fatalf("answered fields were asked again: %#v %v", result, err)
	}
}

func TestClarificationMultipleAllowsOneAndRejectsUnknown(t *testing.T) {
	field := ClarificationField{ID: "formats", Type: "multiple", Required: true,
		ChoicePolicy: "fixed", Choices: []string{"md", "docx"}}
	if _, err := ResolveClarifications([]ClarificationField{field}, map[string]any{"formats": []any{"md"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveClarifications([]ClarificationField{field}, map[string]any{"formats": []any{"pdf"}}); err == nil {
		t.Fatal("unknown format accepted")
	}
}

func TestCompileBuiltinClarificationPolicies(t *testing.T) {
	for _, id := range []string{"ppt-workflow", "writer-workflow", "image-workflow", "image-workflow-v2",
		"academic_research_pipeline", "bid_tech_proposal_writer", "product_solution_delivery"} {
		t.Run(id, func(t *testing.T) {
			root := filepath.Join("../../../../workflows", id)
			workflow, err := os.ReadFile(filepath.Join(root, "workflow.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			state, err := os.ReadFile(filepath.Join(root, "scenario/state.yml"))
			if err != nil {
				t.Fatal(err)
			}
			compiled := Compile(string(workflow), string(state), "", ProfileRuntimeLoad)
			for _, diagnostic := range compiled.Diagnostics {
				if diagnostic.Severity == "error" && len(diagnostic.Code) >= len("E_RUNTIME_CLARIFICATION") &&
					diagnostic.Code[:len("E_RUNTIME_CLARIFICATION")] == "E_RUNTIME_CLARIFICATION" {
					t.Fatalf("invalid field contract: %#v", diagnostic)
				}
			}
		})
	}
}
