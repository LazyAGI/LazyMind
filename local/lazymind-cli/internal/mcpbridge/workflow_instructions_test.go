package mcpbridge

import (
	"strings"
	"testing"
)

func TestHostInstructionsScopeCodexPresentation(t *testing.T) {
	for _, provider := range []string{"codex", "workbuddy", "deepseek-harness", "cursor", ""} {
		got := workflowInstructionsForHost(provider)
		for _, shared := range []string{workflowStartInstructions, workflowExecutionInstructions, workflowFinishInstructions} {
			if !strings.Contains(got, shared) {
				t.Fatalf("changed %s execution rules", provider)
			}
		}
		for _, instruction := range []string{codexPanelInstructions, codexIdentityInstructions} {
			if strings.Contains(got, instruction) != (provider == "codex") {
				t.Fatalf("incorrect Codex instruction scope for %q", provider)
			}
		}
		for _, hint := range []string{"interaction_url", "Opening the browser"} {
			if strings.Contains(got, hint) != (provider == "codex") {
				t.Fatalf("incorrect panel URL instruction scope for %q", provider)
			}
		}
	}
}

func TestWorkBuddyReusesExistingExecutionInstructions(t *testing.T) {
	got := workflowInstructionsForHost("workbuddy")
	for _, shared := range []string{workflowStartInstructions, workflowExecutionInstructions, workflowFinishInstructions} {
		if !strings.Contains(got, shared) {
			t.Fatal("WorkBuddy has different workflow rules")
		}
	}
	for _, unwanted := range []string{"open_in_codex", "CODEX_THREAD_ID", "run sleep commands", "keep a tool loop alive", "Do not also call"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("unexpected instruction %q", unwanted)
		}
	}
	if !strings.Contains(got, "The Workflow panel appears automatically") {
		t.Fatal("missing native panel guidance")
	}
}
