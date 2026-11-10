package mcpclient

import (
	"context"
	"errors"
	"os"
	"testing"

	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/workflowhost"
)

func TestDSHPendingDisconnectSurvivesRefreshUntilRetried(t *testing.T) {
	for _, live := range []bool{false, true} {
		name := "without heartbeat"
		if live {
			name = "with heartbeat"
		}
		t.Run(name, func(t *testing.T) {
			a, pair, _ := connectedDSHFixture(t)
			running := true
			a.dshRunning = func(context.Context, string) (bool, error) { return running, nil }
			if live {
				if err := workflowhost.RecordRuntimeStatus(a.home, pair, "", true); err != nil {
					t.Fatal(err)
				}
			}
			assertPending := func(s agentintegration.Status, issue string) {
				t.Helper()
				if s.State != agentintegration.ActionRequired || s.Issue == nil || s.Issue.Code != issue ||
					s.RetryAction != "disconnect" || s.Enabled == nil || !*s.Enabled || !s.CanDisconnect {
					t.Fatalf("lost pending disconnect (%s): %+v", issue, s)
				}
			}
			assertPending(a.Disconnect(context.Background()), "quit_required")
			before, _ := os.ReadFile(a.dshRecordPath())
			patchBefore, err := os.ReadFile(a.forDSHProfile("desktop", "").dshConfigPath())
			if err != nil {
				t.Fatal(err)
			}
			record, err := a.dshRecord()
			if err != nil || !record.PendingDisconnect {
				t.Fatalf("pending operation was not persisted: %+v, %v", record, err)
			}
			restarted := *a
			for i := 0; i < 2; i++ {
				assertPending(restarted.Status(context.Background()), "quit_required")
			}
			running = false
			assertPending(restarted.Status(context.Background()), "disconnect_pending")
			after, _ := os.ReadFile(a.dshRecordPath())
			if string(before) != string(after) {
				t.Fatal("status refresh changed the installation record")
			}
			patchAfter, err := os.ReadFile(a.forDSHProfile("desktop", "").dshConfigPath())
			if err != nil || string(patchBefore) != string(patchAfter) {
				t.Fatal("status refresh changed connection configuration")
			}
			for _, s := range []agentintegration.Status{restarted.Disconnect(context.Background()), restarted.Status(context.Background())} {
				if s.State != agentintegration.Ready || s.Enabled == nil || *s.Enabled || s.CanDisconnect || s.RetryAction != "" || s.Issue != nil || s.Action != nil {
					t.Fatalf("successful disconnect retained a pending operation: %+v", s)
				}
			}
			record, err = restarted.dshRecord()
			if err != nil || record.PendingDisconnect {
				t.Fatalf("successful disconnect did not clear pending operation: %+v, %v", record, err)
			}
		})
	}
}

func TestDSHPendingDisconnectRecoversInspectionFailureAndReconnect(t *testing.T) {
	a, _, _ := connectedDSHFixture(t)
	a.dshRunning = func(context.Context, string) (bool, error) { return true, nil }
	a.Disconnect(context.Background())
	a.dshRunning = func(context.Context, string) (bool, error) { return false, errors.New("process inspection failed") }
	s := a.Status(context.Background())
	if s.Issue == nil || s.Issue.Code != "process_inspection_failed" || s.RetryAction != "disconnect" {
		t.Fatalf("inspection error lost pending disconnect: %+v", s)
	}
	a.dshRunning = func(context.Context, string) (bool, error) { return false, nil }
	s = a.Status(context.Background())
	if s.Issue == nil || s.Issue.Code != "disconnect_pending" {
		t.Fatalf("inspection recovery lost pending action: %+v", s)
	}
	// An explicit connection request replaces a pending disconnect; merely polling does not.
	a.Connect(context.Background())
	s = a.Status(context.Background())
	if s.State != agentintegration.Enabled || s.RetryAction != "" || s.Issue != nil {
		t.Fatalf("reconnect retained pending disconnect: %+v", s)
	}
}
