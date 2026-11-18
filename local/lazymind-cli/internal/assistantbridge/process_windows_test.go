//go:build windows

package assistantbridge

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestDetachedServiceEscapesOnlyLauncherJob(t *testing.T) {
	t.Setenv("LAZYMIND_CONNECTOR_LAUNCHER_JOB", "")
	if detachedProcessAttributes().CreationFlags&windows.CREATE_BREAKAWAY_FROM_JOB != 0 {
		t.Fatal("requested breakaway outside launcher job")
	}
	t.Setenv("LAZYMIND_CONNECTOR_LAUNCHER_JOB", "1")
	if detachedProcessAttributes().CreationFlags&windows.CREATE_BREAKAWAY_FROM_JOB == 0 {
		t.Fatal("shared Assistant would be killed with foreground MCP")
	}
}
