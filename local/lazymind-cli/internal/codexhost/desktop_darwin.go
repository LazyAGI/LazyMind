//go:build darwin

package codexhost

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"lazymind/agentconnector/internal/desktopsetup"
)

var runDarwinDesktopCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, command, args...).CombinedOutput()
}

// Some NSRunningApplication entries have no bundle URL, including applications
// that disappear during shutdown. Check the URL before sending it any messages.
const darwinQuitApplicationHandlers = `use framework "AppKit"
use scripting additions
on closeApplications(runningApps, appPath)
set closingPIDs to {}
repeat with runningApp in runningApps
 set appURL to runningApp's bundleURL()
 if appURL is not missing value then
  set bundlePath to appURL's |path|()
  if bundlePath is not missing value and (bundlePath as text) is appPath then
   set closingPID to runningApp's processIdentifier() as integer
   if closingPID > 0 then
    if not (runningApp's terminate() as boolean) then error "LAZYMIND_AGENT_quit_required: Codex refused to close; finish its pending dialog and retry"
    set end of closingPIDs to closingPID
   end if
  end if
 end if
end repeat
return closingPIDs
end closeApplications
`

func launchDesktop(ctx context.Context, application string, port int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// A normal quit lets Codex finish its own shutdown and save its state.
	// Never force-kill a desktop that refuses to close.
	// Query process objects instead of AppleScript application objects: resolving
	// an application object during shutdown can relaunch it without CDP flags.
	script := darwinQuitApplicationHandlers + `
on run argv
set appPath to item 1 of argv
return closeApplications(current application's NSWorkspace's sharedWorkspace()'s runningApplications(), appPath)
end run`
	output, err := runDarwinDesktopCommand(ctx, "/usr/bin/osascript", "-e", script, application)
	if err != nil {
		if strings.Contains(string(output), "LAZYMIND_AGENT_quit_required:") {
			return fmt.Errorf("%w: %v", desktopsetup.ErrQuitRequired, err)
		}
		return fmt.Errorf("close Codex normally and retry: %s: %w", output, err)
	}
	// NSRunningApplication.isTerminated can remain stale in a script without an
	// AppKit event loop. Check the exact returned PIDs at the OS instead.
	wait, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for _, value := range strings.FieldsFunc(string(output), func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' }) {
		pid, err := strconv.Atoi(value)
		if err != nil || pid <= 0 {
			return fmt.Errorf("invalid Codex process ID: %q", value)
		}
		for {
			err := syscall.Kill(pid, 0)
			if err == syscall.ESRCH {
				break
			}
			if err != nil {
				return fmt.Errorf("check Codex shutdown: %w", err)
			}
			select {
			case <-wait.Done():
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("%w: %v", desktopsetup.ErrQuitRequired, wait.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	reportRecovery(ctx, "starting")
	args := append([]string{"-n", "-a", application, "--args"}, desktopLaunchArguments(ctx, port)...)
	if output, err := runDarwinDesktopCommand(ctx, "/usr/bin/open", args...); err != nil {
		return fmt.Errorf("start Codex: %s: %w", output, err)
	}
	return nil
}

func openDesktopThread(ctx context.Context, application, thread string) error {
	link, err := threadLink(thread)
	if err != nil {
		return err
	}
	if output, err := exec.CommandContext(ctx, "/usr/bin/open", "-a", application, link).CombinedOutput(); err != nil {
		return fmt.Errorf("open original Codex conversation: %s: %w", output, err)
	}
	return nil
}
