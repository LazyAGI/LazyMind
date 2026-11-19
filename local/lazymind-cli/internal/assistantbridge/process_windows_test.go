//go:build windows

package assistantbridge

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestDetachedServiceRequestsBreakawayWhenInAJob(t *testing.T) {
	t.Setenv("LAZYMIND_CONNECTOR_LAUNCHER_JOB", "")
	if got := detachedProcessAttributes().CreationFlags&windows.CREATE_BREAKAWAY_FROM_JOB != 0; got != currentProcessInJob() {
		t.Fatalf("without the launcher marker, breakaway=%t must match Job membership", got)
	}
	t.Setenv("LAZYMIND_CONNECTOR_LAUNCHER_JOB", "1")
	if detachedProcessAttributes().CreationFlags&windows.CREATE_BREAKAWAY_FROM_JOB == 0 {
		t.Fatal("shared Assistant would be killed with foreground MCP")
	}
}
