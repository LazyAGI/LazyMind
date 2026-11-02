package workbuddyhost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/integrationstate"
)

func TestLoginEnvironmentPersistsWithoutLazyMindAndHonorsExplicitDisable(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "state with ' quote")
	oldHome, oldRun := startupUserHome, startupLoginRun
	startupUserHome = func() (string, error) { return root, nil }
	startupLoginRun = func(context.Context, ...string) ([]byte, error) { return nil, fmt.Errorf("no loaded job in fixture") }
	t.Cleanup(func() { startupUserHome = oldHome; startupLoginRun = oldRun })
	if err := integrationstate.Set(home, "workbuddy", true); err != nil {
		t.Fatal(err)
	}
	if err := configureLoginEnvironment(context.Background(), home, 9333, true); err != nil {
		t.Fatal(err)
	}
	path, _, _ := loginEnvironmentPaths(home)
	if output, err := exec.Command("/usr/bin/plutil", "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("invalid login job %s %v", output, err)
	}
	body, err := exec.Command("/usr/bin/plutil", "-extract", "ProgramArguments.2", "raw", "-o", "-", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	// Substitute only the effect, executing the actual generated preference gate.
	marker := filepath.Join(root, "restored")
	script := strings.Replace(string(body), "exec /bin/launchctl setenv WORKBUDDY_REMOTE_DEBUGGING_PORT 9333", "touch '"+marker+"'", 1)
	if output, err := exec.Command("/bin/sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("login script %s %v", output, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("enabled preference did not restore")
	}
	_ = os.Remove(marker)
	if err := integrationstate.Set(home, "workbuddy", false); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/bin/sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("disabled login script %s %v", output, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("login job re-enabled a disabled integration")
	}
	if err := configureLoginEnvironment(context.Background(), home, 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("login job not removed")
	}
}
