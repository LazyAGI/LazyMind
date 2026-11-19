//go:build darwin

package codexhost

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/desktopsetup"
)

// Exercise the actual AppleScript with fake application objects. No real
// application is inspected, closed or launched by these regression tests.
func TestDarwinQuitSkipsMissingBundleURLAndCapturesPIDBeforeTermination(t *testing.T) {
	script := darwinQuitApplicationHandlers + `
on run argv
 script noBundle
  on bundleURL()
   return missing value
  end bundleURL
  on terminate()
   error "must not terminate an app without a bundle URL"
  end terminate
 end script
 script otherApp
  script appURL
   on |path|()
    return "/Other.app"
   end |path|
  end script
  on bundleURL()
   return appURL
  end bundleURL
  on terminate()
   error "must not terminate an unrelated application"
  end terminate
 end script
 script targetApp
  property currentPID : 12345
  script appURL
   on |path|()
    return "/Test Codex.app"
   end |path|
  end script
  on bundleURL()
   return appURL
  end bundleURL
  on processIdentifier()
   return currentPID
  end processIdentifier
  on terminate()
   set currentPID to 0
   return true
  end terminate
 end script
 return closeApplications({noBundle, otherApp, targetApp}, "/Test Codex.app")
end run`
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "12345" {
		t.Fatalf("quit script: %v, %s", err, output)
	}
}

func TestDarwinLaunchRetriesAfterUserManuallyClosesCodex(t *testing.T) {
	original := runDarwinDesktopCommand
	t.Cleanup(func() { runDarwinDesktopCommand = original })
	app := "/Applications/测试 Codex.app"
	closeAttempts, launches := 0, 0
	runDarwinDesktopCommand = func(_ context.Context, command string, args ...string) ([]byte, error) {
		switch command {
		case "/usr/bin/osascript":
			closeAttempts++
			if args[len(args)-1] != app {
				t.Fatal("lost exact application path")
			}
			if closeAttempts == 1 {
				return []byte("LAZYMIND_AGENT_quit_required: Codex refused to close"), errors.New("pending quit confirmation")
			}
			// The user closed Codex after the failed attempt: no PIDs remain.
			return nil, nil
		case "/usr/bin/open":
			launches++
			want := []string{"-n", "-a", app, "--args", "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=9334"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("launch arguments: %q", args)
			}
			return nil, nil
		default:
			t.Fatalf("unexpected command: %s", command)
			return nil, nil
		}
	}
	if err := launchDesktop(context.Background(), app, 9334); err == nil || launches != 0 {
		t.Fatalf("launched despite a refused shutdown: %v", err)
	}
	if err := launchDesktop(context.Background(), app, 9334); err != nil || launches != 1 {
		t.Fatalf("retry failed after manual shutdown: %v", err)
	}
}

func TestDarwinQuitCanInspectWorkspaceWhenTargetIsAlreadyAbsent(t *testing.T) {
	// The nonexistent temporary path cannot match a real running application.
	application := t.TempDir() + "/Absent Codex.app"
	script := darwinQuitApplicationHandlers + `
on run argv
return closeApplications(current application's NSWorkspace's sharedWorkspace()'s runningApplications(), item 1 of argv)
end run`
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script, application).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "" {
		t.Fatalf("already-closed application blocked startup: %v, %s", err, output)
	}
}

func TestDarwinCancelledLaunchDoesNotCloseOrStartCodex(t *testing.T) {
	original := runDarwinDesktopCommand
	t.Cleanup(func() { runDarwinDesktopCommand = original })
	runDarwinDesktopCommand = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("cancelled launch changed the desktop")
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := launchDesktop(ctx, "/Applications/Codex.app", 9334); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestDarwinLaunchPreservesShutdownFailureCause(t *testing.T) {
	original := runDarwinDesktopCommand
	t.Cleanup(func() { runDarwinDesktopCommand = original })
	cause := errors.New("pending quit confirmation")
	runDarwinDesktopCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("LAZYMIND_AGENT_quit_required: Codex refused to close"), cause
	}
	err := launchDesktop(context.Background(), "/Applications/Codex.app", 9334)
	if !errors.Is(err, desktopsetup.ErrQuitRequired) || !errors.Is(err, cause) {
		t.Fatalf("shutdown error lost its category or cause: %v", err)
	}
}

func TestDarwinLaunchPreservesCancellationDuringCommand(t *testing.T) {
	for _, phase := range []string{"shutdown", "start"} {
		t.Run(phase, func(t *testing.T) {
			original := runDarwinDesktopCommand
			t.Cleanup(func() { runDarwinDesktopCommand = original })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cause := errors.New("subprocess interrupted")
			runDarwinDesktopCommand = func(_ context.Context, command string, _ ...string) ([]byte, error) {
				if phase == "start" && command == "/usr/bin/osascript" {
					return nil, nil
				}
				cancel()
				return nil, cause
			}
			err := launchDesktop(ctx, "/Applications/Codex.app", 9334)
			if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
				t.Fatalf("command error lost cancellation or cause: %v", err)
			}
		})
	}
}

func TestDarwinLaunchClassifiesFailuresAtTheirActualStage(t *testing.T) {
	for _, scenario := range []struct {
		name, output, want string
	}{
		{"inspection", "private-profile-path", "process_inspection_failed"},
		{"refused", "private-profile-path\nLAZYMIND_AGENT_quit_required: pending dialog", "quit_required"},
		{"invalid-pid", "private-profile-path", "process_inspection_failed"},
		{"launch", "private-profile-path", "launch_failed"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			original := runDarwinDesktopCommand
			t.Cleanup(func() { runDarwinDesktopCommand = original })
			cause := errors.New("exit status 1")
			runDarwinDesktopCommand = func(_ context.Context, command string, _ ...string) ([]byte, error) {
				if command == "/usr/bin/open" && scenario.name != "launch" {
					t.Fatal("launched despite shutdown inspection failure")
				}
				if command == "/usr/bin/osascript" && scenario.name == "launch" {
					return nil, nil
				}
				if scenario.name == "invalid-pid" {
					return []byte(scenario.output), nil
				}
				return []byte(scenario.output), cause
			}
			err := launchDesktop(context.Background(), "/Applications/Codex.app", 9334)
			if SetupIssue(err) != scenario.want {
				t.Fatalf("issue=%q err=%v", SetupIssue(err), err)
			}
			if scenario.name != "invalid-pid" && !errors.Is(err, cause) {
				t.Fatalf("lost subprocess cause: %v", err)
			}
			if strings.Contains(err.Error(), "private-profile-path") {
				t.Fatalf("raw subprocess output leaked: %v", err)
			}
		})
	}
}
