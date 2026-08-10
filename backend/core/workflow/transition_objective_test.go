package workflow

import "testing"

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

func TestSessionIntentTextReadsPersistedTriggerContext(t *testing.T) {
	got := sessionIntentText(`{"text":"run this workflow"}`)
	if got != "run this workflow" {
		t.Fatalf("unexpected session intent: %q", got)
	}
}
