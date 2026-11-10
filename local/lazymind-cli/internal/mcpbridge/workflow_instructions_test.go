package mcpbridge

import (
	"strings"
	"testing"
)

func TestSharedWorkflowInstructions(t *testing.T) {
	for _, part := range []string{"workflow.start", "clarification_values", "needs_input", "exact IDs", "revision_id", "new idempotency_key", "unchanged-payload", "start.inputs", "absolute regular-file paths", "imports and binds every supplied file", "do not retry without that source", "idempotency_key", "16–256", "workflow.state", "awaiting_user", "END this turn", "The host displays the Workflow panel automatically", "Native conversation identity belongs to the host connector"} {
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
	for _, part := range []string{"execution_ref=#/execution", "Read every pinned Skill in full", "Subagents are optional", "current external agent generates", "separate verification pass", "same-agent validation", "never claim independent review", "executor_ref is an optional real", "no execution_handle"} {
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
