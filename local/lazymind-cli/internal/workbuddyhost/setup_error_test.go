package workbuddyhost

import (
	"errors"
	"strings"
	"testing"
)

func TestDesktopLaunchErrorClassifiesWindowsMarkersWithoutRawOutput(t *testing.T) {
	for _, code := range []string{"quit_required", "process_inspection_failed", "launch_failed", "unrecognized"} {
		t.Run(code, func(t *testing.T) {
			cause := errors.New("exit status 1")
			output := "private-profile-path secret-access-token\nLAZYMIND_WB_" + code + ": localized PowerShell output"
			err := desktopLaunchError(output, cause)
			want := code
			if code == "unrecognized" {
				want = "launch_failed"
			}
			if SetupIssue(err) != want || !errors.Is(err, cause) {
				t.Fatalf("issue=%q err=%v", SetupIssue(err), err)
			}
			for _, private := range []string{"private-profile-path", "secret-access-token", "localized PowerShell output"} {
				if strings.Contains(err.Error(), private) {
					t.Fatalf("raw subprocess output leaked: %v", err)
				}
			}
		})
	}
}
