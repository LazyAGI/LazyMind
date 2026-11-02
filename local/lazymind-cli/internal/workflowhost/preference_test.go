package workflowhost

import (
	"lazymind/agentconnector/internal/integrationstate"
	"testing"
)

func TestExplicitDisableRevokesEvenWhenComponentCleanupHasNotFinished(t *testing.T) {
	for _, provider := range []string{"codex", "workbuddy"} {
		t.Run(provider, func(t *testing.T) {
			home := t.TempDir()
			pair, err := EnsureForProvider(home, provider, t.TempDir(), "account")
			if err != nil {
				t.Fatal(err)
			}
			if err = integrationstate.Set(home, provider, false); err != nil {
				t.Fatal(err)
			}
			if _, err = Verify(home, pair.ConnectorID, pair.Token, "account"); err == nil {
				t.Fatal("disabled preference still authorizes control")
			}
			if Configured(home, pair.ConnectorID, "account") {
				t.Fatal("disabled integration reported configured")
			}
			if err = integrationstate.Set(home, provider, true); err != nil {
				t.Fatal(err)
			}
			if _, err = Verify(home, pair.ConnectorID, pair.Token, "other-account"); err == nil {
				t.Fatal("enable preference bypassed account verification")
			}
		})
	}
}
