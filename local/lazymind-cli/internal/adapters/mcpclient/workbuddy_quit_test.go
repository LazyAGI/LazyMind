package mcpclient

import (
	"errors"
	"testing"

	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/workbuddyhost"
)

func TestWorkBuddyQuitRequiredIsRecoverableUserAction(t *testing.T) {
	for _, code := range []string{"quit_required", "process_inspection_failed", "launch_failed"} {
		status := workBuddyFailure(agentintegration.Status{Agent: "workbuddy", DisplayName: "WorkBuddy", CanConnect: true},
			&workbuddyhost.SetupError{Code: code, Err: errors.New("fixture")})
		want := agentintegration.Failed
		if code == "quit_required" {
			want = agentintegration.ActionRequired
			if status.Action == nil || status.Action.Kind != "quit_agent" || status.RetryAction != "connect" {
				t.Fatalf("lost manual retry action: %+v", status)
			}
		}
		if status.State != want || status.Issue == nil || status.Issue.Code != code {
			t.Fatalf("code=%s status=%+v", code, status)
		}
	}
}
