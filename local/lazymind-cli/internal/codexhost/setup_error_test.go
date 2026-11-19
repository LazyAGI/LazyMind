package codexhost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/desktopsetup"
)

func TestDesktopLaunchErrorPreservesCategoriesAndCause(t *testing.T) {
	for _, code := range []string{"quit_required", "process_inspection_failed", "launch_failed", "unknown"} {
		t.Run(code, func(t *testing.T) {
			cause := errors.New("exit status 1")
			output := "private-profile-path secret-access-token\nLAZYMIND_AGENT_" + code + ": localized output"
			err := desktopLaunchError(output, cause)
			want := code
			if code == "unknown" {
				want = "launch_failed"
			}
			if SetupIssue(fmt.Errorf("setup: %w", err)) != want || !errors.Is(err, cause) {
				t.Fatalf("issue=%q err=%v", SetupIssue(err), err)
			}
			if errors.Is(err, desktopsetup.ErrQuitRequired) != (want == "quit_required") {
				t.Fatalf("incorrect shutdown sentinel: %v", err)
			}
			for _, private := range []string{"private-profile-path", "secret-access-token", "localized output"} {
				if strings.Contains(err.Error(), private) {
					t.Fatalf("raw subprocess output leaked: %v", err)
				}
			}
		})
	}
}

func TestSetupIssuePreservesExistingErrors(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		err := setupError("launch_failed", cause)
		if !errors.Is(err, cause) {
			t.Fatalf("lost context cause: %v", err)
		}
	}
	if SetupIssue(fmt.Errorf("shutdown: %w", desktopsetup.ErrQuitRequired)) != "quit_required" {
		t.Fatal("unrecognized existing shutdown sentinel")
	}
	if SetupIssue(nil) != "" || SetupIssue(errors.New("other failure")) != "" {
		t.Fatal("classified an unrelated error")
	}
}
