package agentintegration

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"lazymind/agentconnector/internal/desktopsetup"
)

func TestRequireQuitPreservesConfigurationAndRetryDirection(t *testing.T) {
	for _, agent := range []string{"workbuddy", "codex", "deepseek-harness"} {
		for _, action := range []string{"connect", "disconnect"} {
			t.Run(agent+"/"+action, func(t *testing.T) {
				enabled := true
				connection := &ConnectionStatus{MCPConfigured: true, MCPTrusted: true}
				status := RequireQuit(Status{Agent: agent, DisplayName: agent, Enabled: &enabled,
					Connection: connection, CanConnect: true, CanDisconnect: true}, action)
				if status.State != ActionRequired || status.Action == nil || status.Action.Kind != "quit_agent" || status.Issue.Code != "quit_required" || status.RetryAction != action {
					t.Fatalf("unexpected action: %+v", status)
				}
				if status.Enabled == nil || !*status.Enabled || status.Connection != connection || !status.CanConnect || !status.CanDisconnect {
					t.Fatalf("lost actual configuration: %+v", status)
				}
			})
		}
	}
}

func TestFailErrorOnlyTreatsExplicitQuitRequirementAsUserAction(t *testing.T) {
	for _, test := range []struct {
		err   error
		state State
	}{
		{fmt.Errorf("wrapped: %w", desktopsetup.ErrQuitRequired), ActionRequired},
		{os.ErrPermission, Failed},
		{errors.New("could not inspect or launch desktop"), Failed},
	} {
		status := FailError(Status{Agent: "codex", DisplayName: "Codex"}, test.err)
		if status.State != test.state {
			t.Fatalf("error %v: %+v", test.err, status)
		}
	}
}
