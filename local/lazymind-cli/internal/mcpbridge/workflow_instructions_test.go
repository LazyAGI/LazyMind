package mcpbridge

import (
	"strings"
	"testing"
)

func TestSharedWorkflowInstructions(t *testing.T) {
	for _, part := range []string{"workflow.list", "workflow.get", "workflow.start", "workflow.state", "agent_instruction", "execution_handle", "admission"} {
		if !strings.Contains(workflowInstructions, part) {
			t.Fatalf("missing shared guidance %q", part)
		}
	}
	for _, obsolete := range []string{"Codex", "WorkBuddy", "CODEX_THREAD_ID", "open_in_codex", "interaction_url", "placement=bottom"} {
		if strings.Contains(workflowInstructions, obsolete) {
			t.Fatalf("obsolete host-specific guidance %q", obsolete)
		}
	}
}

func TestExternalExecutionSupportsHostsWithoutSubagents(t *testing.T) {
	for _, part := range []string{"Read the supplied Skills in full", "Subagents are optional", "execute directly", "same-agent validation", "no execution_handle", "workflow.tool.list", "workflow.tool.call"} {
		if !strings.Contains(workflowInstructions, part) {
			t.Fatalf("missing portable execution guidance %q", part)
		}
	}
	for _, obsolete := range []string{"Delegate generation and validation to native host subagents", "completes with executor_ref identifying the worker", "follow its original LazyMind Skills with a native worker", "Delegate each exact LazyMind generation_request to a native worker"} {
		if strings.Contains(workflowInstructions, obsolete) {
			t.Fatalf("mandatory subagent guidance remains: %q", obsolete)
		}
	}
}
