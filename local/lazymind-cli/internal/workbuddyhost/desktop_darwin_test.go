package workbuddyhost

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runDarwinQuitScript(t *testing.T, script string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "/usr/bin/osascript", append([]string{"-e", script}, args...)...).CombinedOutput()
}

func TestDarwinQuitSkipsProcessesWithoutBundles(t *testing.T) {
	// Execute the production handler against deterministic AppleScript objects.
	// A missing bundle or an unrelated app must never receive terminate().
	fixture := `
script unbundled
 on bundleURL()
  return missing value
 end bundleURL
 on terminate()
  error "terminated unbundled process"
 end terminate
end script
script unrelated
 on bundleURL()
  return current application's NSURL's fileURLWithPath:"/Applications/Other.app"
 end bundleURL
 on terminate()
  error "terminated unrelated application"
 end terminate
end script
script targetApp
 on bundleURL()
  return current application's NSURL's fileURLWithPath:"/Applications/WorkBuddy.app"
 end bundleURL
 on terminate()
  return true
 end terminate
 on processIdentifier()
  return 12345
 end processIdentifier
end script
return my quitApplications("/Applications/WorkBuddy.app", {unbundled, unrelated, targetApp})
`
	output, err := runDarwinQuitScript(t, darwinQuitApplications+fixture)
	if err != nil || strings.TrimSpace(string(output)) != "12345" {
		t.Fatalf("quit enumeration: %v %s", err, output)
	}
}

func TestDarwinQuitRealProcessEnumerationWithAbsentTarget(t *testing.T) {
	// Read-only integration check: this nonexistent path cannot match a running
	// application, even on a developer's machine with WorkBuddy open.
	output, err := runDarwinQuitScript(t, darwinQuitScript, filepath.Join(t.TempDir(), "Absent.app"))
	if err != nil || strings.TrimSpace(string(output)) != "" {
		t.Fatalf("absent target: %v %s", err, output)
	}
}

func TestDarwinQuitClassifiesRefusalAndInspectionFailure(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"refused", `return false`, "quit_required"},
		{"inspection-error", `error "private-profile-path secret-access-token"`, "process_inspection_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := `
script targetApp
 on bundleURL()
  return current application's NSURL's fileURLWithPath:"/Applications/WorkBuddy.app"
 end bundleURL
 on terminate()
 ` + tc.body + `
 end terminate
end script
return my quitApplications("/Applications/WorkBuddy.app", {targetApp})
`
			output, cause := runDarwinQuitScript(t, darwinQuitApplications+fixture)
			if cause == nil {
				t.Fatal("expected script failure")
			}
			err := darwinQuitError(string(output), cause)
			if SetupIssue(err) != tc.want || !errors.Is(err, cause) {
				t.Fatalf("issue=%s err=%v output=%s", SetupIssue(err), err, output)
			}
			if strings.Contains(err.Error(), "private-profile-path") || strings.Contains(err.Error(), "secret-access-token") {
				t.Fatalf("raw output leaked: %v", err)
			}
		})
	}
}

func TestFindWorkBuddyDesktopBundles(t *testing.T) {
	for _, test := range []struct {
		name string
		want bool
	}{
		{"WorkBuddy.app", true},
		{"WorkBuddy CN.app", true},
		{"WorkBuddy AI.app", true},
		{"WorkBuddy AI Toolkit.app", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			applications := t.TempDir()
			t.Setenv("LAZYMIND_HOME", t.TempDir())
			t.Setenv("LAZYMIND_CONNECTOR_CONFIG", "")
			t.Setenv("LAZYMIND_DESKTOP_APPLICATION_DIRS", applications)
			bundle := filepath.Join(applications, test.name)
			if err := os.Mkdir(bundle, 0700); err != nil {
				t.Fatal(err)
			}
			found, err := desktopApplication()
			if test.want {
				if err != nil || found != bundle {
					t.Fatalf("automatic discovery = %q, %v; want %q", found, err, bundle)
				}
			} else if err == nil || found != "" {
				t.Fatalf("automatic discovery accepted an unrelated app: %q, %v", found, err)
			}
		})
	}
}
