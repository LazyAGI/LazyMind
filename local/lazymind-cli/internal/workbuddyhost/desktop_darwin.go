package workbuddyhost

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func launchDesktop(ctx context.Context, application string, port int) error {
	// A normal quit lets WorkBuddy finish its own shutdown and save its state.
	// Never force-kill a desktop that refuses to close.
	// Query process objects instead of AppleScript application objects: resolving
	// an application object during shutdown can relaunch it without CDP flags.
	script := `use framework "AppKit"
use scripting additions
on run argv
set appPath to item 1 of argv
set closingPIDs to {}
repeat with runningApp in (current application's NSWorkspace's sharedWorkspace()'s runningApplications())
 set bundlePath to runningApp's bundleURL()'s |path|()
 if bundlePath is not missing value and (bundlePath as text) is appPath then
  if not (runningApp's terminate() as boolean) then error "WorkBuddy refused to close; finish its pending dialog and retry"
  set end of closingPIDs to runningApp's processIdentifier() as integer
 end if
end repeat
return closingPIDs
end run`
	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script, application).CombinedOutput()
	if err != nil {
		return fmt.Errorf("close WorkBuddy normally and retry: %s: %w", output, err)
	}
	// NSRunningApplication.isTerminated can remain stale in a script without an
	// AppKit event loop. Check the exact returned PIDs at the OS instead.
	wait, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for _, value := range strings.FieldsFunc(string(output), func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' }) {
		pid, err := strconv.Atoi(value)
		if err != nil || pid <= 0 {
			return fmt.Errorf("invalid WorkBuddy process ID: %q", value)
		}
		for {
			err := syscall.Kill(pid, 0)
			if err == syscall.ESRCH {
				break
			}
			if err != nil {
				return fmt.Errorf("check WorkBuddy shutdown: %w", err)
			}
			select {
			case <-wait.Done():
				return fmt.Errorf("WorkBuddy did not close; finish its pending dialog and retry: %w", wait.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	if output, err := exec.CommandContext(ctx, "/usr/bin/open", "-n", "-a", application, "--args", "--remote-debugging-address=127.0.0.1", "--remote-debugging-port="+strconv.Itoa(port)).CombinedOutput(); err != nil {
		return fmt.Errorf("start WorkBuddy: %s: %w", output, err)
	}
	return nil
}
