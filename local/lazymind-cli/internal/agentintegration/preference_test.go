package agentintegration

import (
	"lazymind/agentconnector/internal/integrationstate"
	"testing"
)

func TestPreferenceSurvivesMissingInstallAndHealthFailures(t *testing.T) {
	home := t.TempDir()
	if err := integrationstate.Set(home, "workbuddy", true); err != nil {
		t.Fatal(err)
	}
	for _, state := range []State{Ready, Failed, RequirementsMissing, Enabled} {
		status := ApplyPreference(home, "workbuddy", Status{State: state})
		if status.Enabled == nil || !*status.Enabled || !status.CanDisconnect {
			t.Fatalf("lost choice: %+v", status)
		}
	}
	status := ApplyPreference(home, "workbuddy", Status{State: Enabled})
	SetRuntimeIssue(&status, "waiting_for_app")
	if status.Issue != nil || status.Connection.RuntimeState != "waiting" || !*status.Enabled {
		t.Fatal("closed app became an error")
	}
	SetRuntimeIssue(&status, "cdp_unavailable")
	if status.Issue == nil || !*status.Enabled {
		t.Fatal("outage reset preference")
	}
	if err := integrationstate.Set(home, "workbuddy", false); err != nil {
		t.Fatal(err)
	}
	status = ApplyPreference(home, "workbuddy", Status{State: Enabled})
	if status.Enabled == nil || *status.Enabled || status.State != Ready {
		t.Fatalf("disable not reflected: %+v", status)
	}
}
