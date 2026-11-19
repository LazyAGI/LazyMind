package agentintegration

import "testing"

func TestRuntimeHealthKeepsConfigurationState(t *testing.T) {
	enabled := true
	status := Status{State: Enabled, Enabled: &enabled}

	SetRuntimeIssue(&status, "waiting_for_app")
	if status.Issue != nil || status.Connection.RuntimeState != "waiting" || !*status.Enabled {
		t.Fatal("closed app became an error")
	}
	SetRuntimeIssue(&status, "connection_required")
	if status.Issue != nil || status.Connection.RuntimeState != "connection_required" || status.Connection.ControlReady || !*status.Enabled {
		t.Fatal("ordinary disconnected launch became a configuration error")
	}
	SetRuntimeIssue(&status, "cdp_unavailable")
	if status.Issue == nil || !*status.Enabled {
		t.Fatal("outage reset preference")
	}
}
