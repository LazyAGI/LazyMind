package workflowhost

import (
	"lazymind/agentconnector/internal/integrationstate"
	"testing"
)

func TestExplicitDisableRevokesEvenWhenComponentCleanupHasNotFinished(t *testing.T) {
	home := t.TempDir()
	pair, err := EnsureForProvider(home, "workbuddy", t.TempDir(), "account")
	if err != nil {
		t.Fatal(err)
	}
	if err = integrationstate.Set(home, "workbuddy", false); err != nil {
		t.Fatal(err)
	}
	if _, err = Verify(home, pair.ConnectorID, pair.Token, "account"); err == nil {
		t.Fatal("disabled preference still authorizes control")
	}
	if Configured(home, pair.ConnectorID, "account") {
		t.Fatal("disabled integration reported configured")
	}
	if err = integrationstate.Set(home, "workbuddy", true); err != nil {
		t.Fatal(err)
	}
	if _, err = Verify(home, pair.ConnectorID, pair.Token, "other-account"); err == nil {
		t.Fatal("enable preference bypassed account verification")
	}
}
