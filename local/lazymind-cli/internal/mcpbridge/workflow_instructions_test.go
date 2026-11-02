package mcpbridge

import (
	"strings"
	"testing"
)

func TestSharedWorkflowInstructions(t *testing.T) {
	for _, part := range []string{"workflow.start", "idempotency_key", "16–256", "workflow.state", "awaiting_user", "END this turn", "The host displays the Workflow panel automatically", "Native conversation identity belongs to the host connector"} {
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
