package desktopupdate

import "testing"

func TestNoNewerReleaseDoesNotRequireMigrationState(t *testing.T) {
	f := newPolicyFixture(t)
	f.policy["releases"] = []any{fixtureRelease("r1", "1.0.0")}
	f.policy["edges"] = []any{}
	f.state.DataProfile = ""
	f.state.Dirty = true
	d, err := f.check(t)
	if err != nil || d.Status != "up_to_date" || len(d.Route) != 0 {
		t.Fatalf("no update was reported as a migration blocker: %+v %v", d, err)
	}
}

func TestUnpublishedClientAheadDoesNotSuggestInstallingAnOlderBootstrap(t *testing.T) {
	f := newPolicyFixture(t)
	f.config.ReleaseID = "unpublished-client"
	f.config.Version = "3.0.0"
	f.state.DataProfile = ""
	d, err := f.check(t)
	if err != nil || d.Status != "up_to_date" || d.Reason != "client_ahead" || len(d.Route) != 0 {
		t.Fatalf("newer client was sent to an older bootstrap: %+v %v", d, err)
	}
}
