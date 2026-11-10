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

// Keep enumeration separate so the same script can be exercised with processes
// that have no application bundle, without quitting the user's applications.
const darwinQuitApplications = `use framework "AppKit"
use scripting additions
on quitApplications(appPath, runningApps)
set closingPIDs to {}
repeat with runningApp in runningApps
 set bundleURL to runningApp's bundleURL()
 if bundleURL is not missing value then
  set bundlePath to bundleURL's |path|()
  if bundlePath is not missing value then
   if (bundlePath as text) is appPath then
    if not (runningApp's terminate() as boolean) then error "LAZYMIND_WB_quit_required"
    set end of closingPIDs to runningApp's processIdentifier() as integer
   end if
  end if
 end if
end repeat
return closingPIDs
end quitApplications
`

const darwinQuitScript = darwinQuitApplications + `
on run argv
return my quitApplications(item 1 of argv, current application's NSWorkspace's sharedWorkspace()'s runningApplications())
end run`

func darwinQuitError(output string, err error) error {
	// Only our explicit refusal marker means the user needs to quit WorkBuddy.
	// Do not expose raw AppleScript output, which may contain private paths.
	code := "process_inspection_failed"
	if strings.Contains(output, "LAZYMIND_WB_quit_required") {
		code = "quit_required"
	}
	return setupError(code, fmt.Errorf("request WorkBuddy shutdown: %w", err))
}

func launchDesktop(ctx context.Context, application string, port int) error {
	// A normal quit lets WorkBuddy save its state. Never force-kill it.
	// Query process objects: resolving an AppleScript application object during
	// shutdown can relaunch it without CDP flags.
	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", darwinQuitScript, application).CombinedOutput()
	if err != nil {
		return darwinQuitError(string(output), err)
	}
	// NSRunningApplication.isTerminated can remain stale in a script without an
	// AppKit event loop. Check the exact returned PIDs at the OS instead.
	wait, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for _, value := range strings.FieldsFunc(string(output), func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' }) {
		pid, err := strconv.Atoi(value)
		if err != nil || pid <= 0 {
			return setupError("process_inspection_failed", fmt.Errorf("invalid WorkBuddy process ID: %q", value))
		}
		for {
			err := syscall.Kill(pid, 0)
			if err == syscall.ESRCH {
				break
			}
			if err != nil {
				return setupError("process_inspection_failed", fmt.Errorf("check WorkBuddy shutdown: %w", err))
			}
			select {
			case <-wait.Done():
				return setupError("quit_required", fmt.Errorf("WorkBuddy did not close: %w", wait.Err()))
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	if output, err := exec.CommandContext(ctx, "/usr/bin/open", "-n", "-a", application, "--args", "--remote-debugging-address=127.0.0.1", "--remote-debugging-port="+strconv.Itoa(port)).CombinedOutput(); err != nil {
		return setupError("launch_failed", fmt.Errorf("start WorkBuddy: %s: %w", output, err))
	}
	return nil
}
