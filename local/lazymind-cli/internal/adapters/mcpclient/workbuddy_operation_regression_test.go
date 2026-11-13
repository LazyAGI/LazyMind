package mcpclient

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"lazymind/agentconnector/internal/agentintegration"
)

func TestWorkBuddyOperationLockFailureClassification(t *testing.T) {
	for _, operation := range []string{"connect", "disconnect"} {
		for _, failure := range []string{"cancelled", "invalid-lock-path"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				root := t.TempDir()
				setTestHome(t, root)
				a := testAdapter(WorkBuddy)
				a.home = filepath.Join(root, "lazy-home")
				ctx := context.Background()
				want := "configuration_failed"
				if failure == "cancelled" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
					want = "operation_cancelled"
				} else if err := os.MkdirAll(filepath.Join(a.home, "workbuddy-workflow", "operation.lock"), 0700); err != nil {
					t.Fatal(err)
				}
				var status agentintegration.Status
				if operation == "connect" {
					status = a.Connect(ctx)
				} else {
					status = a.Disconnect(ctx)
				}
				if status.State != agentintegration.Failed || status.Issue == nil || status.Issue.Code != want {
					t.Fatalf("want %s, got %+v (issue=%+v)", want, status, status.Issue)
				}
				if status.Issue.OperationID == "" || status.Issue.Stage == "" {
					t.Fatalf("missing operation correlation: %+v", status.Issue)
				}
			})
		}
	}
}
