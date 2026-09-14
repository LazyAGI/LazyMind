package workflow

import (
	"strings"
	"testing"
)

func TestWorkflowStepObjectiveUsesPromptAndFirstTurnInput(t *testing.T) {
	got := workflowStepObjective("Summarize {{user_input}} and save it.", "", "hello")
	if got != "Summarize hello and save it." {
		t.Fatalf("unexpected objective: %q", got)
	}
}

func TestWorkflowStepObjectiveKeepsRuntimeRefinement(t *testing.T) {
	got := workflowStepObjective("Create the artifact.", "Use the concise format.", "")
	want := "Create the artifact.\n\nRuntime objective:\nUse the concise format."
	if got != want {
		t.Fatalf("unexpected objective: %q", got)
	}
}

func TestWorkflowStepObjectiveWithRuntimeBoundariesForOpenToolStep(t *testing.T) {
	got := workflowStepObjectiveWithRuntimeBoundaries(
		"Search the API and save results.",
		"Find five useful candidates.",
		"",
		[]string{"http_request"},
		[]string{"url_fetch"},
		nil,
	)
	if !containsAll(got, workflowExecutionBoundaryMarker, "HTTP/API budget", "Stop as soon as") {
		t.Fatalf("runtime boundaries missing: %q", got)
	}
}

func TestWorkflowStepObjectiveWithRuntimeBoundariesDoesNotDuplicate(t *testing.T) {
	prompt := "Search the API.\n\n" + workflowExecutionBoundaryPrompt([]workflowExecutionBoundaryKind{workflowBoundaryHTTP})
	got := workflowStepObjectiveWithRuntimeBoundaries(prompt, "", "", []string{"http_request"}, []string{"url_fetch"}, nil)
	if strings.Count(got, workflowExecutionBoundaryMarker) != 1 {
		t.Fatalf("runtime boundaries duplicated: %q", got)
	}
}

func TestSessionIntentTextReadsPersistedTriggerContext(t *testing.T) {
	got := sessionIntentText(`{"text":"run this workflow"}`)
	if got != "run this workflow" {
		t.Fatalf("unexpected session intent: %q", got)
	}
}

func TestApplyRecoveryIntentPreservesLaunchRequest(t *testing.T) {
	target := transitionTarget{
		UserInput:          "请重新执行步骤 collect_materials",
		RuntimeInstruction: "Replace only the first item.",
	}

	applyRecoveryIntent(`{"text":"搜索一张哈兰德照片并在球衣上写必胜"}`, &target)

	if target.UserInput != "搜索一张哈兰德照片并在球衣上写必胜" {
		t.Fatalf("recovery replaced launch request: %q", target.UserInput)
	}
	want := "Replace only the first item.\n\nRecovery request for this rerun only: 请重新执行步骤 collect_materials"
	if target.RuntimeInstruction != want {
		t.Fatalf("unexpected runtime instruction: %q", target.RuntimeInstruction)
	}
}

func TestApplyRecoveryIntentDoesNotDuplicateLaunchRequest(t *testing.T) {
	target := transitionTarget{UserInput: "原始请求"}

	applyRecoveryIntent(`{"text":"原始请求"}`, &target)

	if target.UserInput != "原始请求" || target.RuntimeInstruction != "" {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func containsAll(text string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(text, needle) {
			return false
		}
	}
	return true
}
