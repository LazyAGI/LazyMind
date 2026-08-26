package workbuddy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAvailabilityRequiresCodeBuddyAuthenticationFile(t *testing.T) {
	auth := filepath.Join(t.TempDir(), "Tencent-Cloud.coding-copilot.info")
	runner := &ChatRunner{auth: auth}
	if ready, reason := runner.Availability(); ready || reason == "" {
		t.Fatalf("signed-out availability=(%v, %q)", ready, reason)
	}
	if err := os.WriteFile(auth, []byte("authenticated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ready, reason := runner.Availability(); !ready || reason != "" {
		t.Fatalf("signed-in availability=(%v, %q)", ready, reason)
	}
}
