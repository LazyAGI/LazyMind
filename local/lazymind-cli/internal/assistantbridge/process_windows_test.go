//go:build windows

package assistantbridge

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestBridgeBootstrapInWindowsPowerShell(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
		fail bool
	}{
		{name: "valid environment and literal path", want: "bootstrap environment verified"},
		{name: "missing executable reports its stage", want: "bootstrap failed during prepare executable", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "O'Brien [资料]")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(dir, "launch.json")
			logPath := filepath.Join(dir, "bridge.log")
			executable := filepath.Join(dir, "missing.exe")
			if !tc.fail {
				staged, err := stageBridgeExecutable(dir)
				if err != nil {
					t.Fatal(err)
				}
				executable = staged
			}
			payload, err := json.Marshal(map[string]any{
				"executable":  executable,
				"arguments":   []string{"-test.run=^TestBridgeBootstrapFixture$", "-test.v"},
				"environment": []string{"LAZYMIND_BRIDGE_BOOTSTRAP_TEST=one=two", "=C:=C:\\temp"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, payload, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive",
				"-EncodedCommand", encodePowerShell(bridgeBootstrapCommand(configPath, logPath)))
			output, err := cmd.CombinedOutput()
			if (err != nil) != tc.fail {
				t.Fatalf("PowerShell exit = %v, output: %s", err, output)
			}
			log, err := os.ReadFile(logPath)
			if err != nil || !strings.Contains(string(log), tc.want) {
				t.Fatalf("bootstrap log = %q, err = %v; shell output: %s", log, err, output)
			}
			_, err = os.Stat(configPath)
			if tc.fail && err != nil || !tc.fail && !os.IsNotExist(err) {
				t.Fatalf("unexpected handoff receipt: config stat = %v", err)
			}
		})
	}
}

func TestBridgeBootstrapFixture(t *testing.T) {
	value := os.Getenv("LAZYMIND_BRIDGE_BOOTSTRAP_TEST")
	if value == "" {
		t.Skip("only run as a Windows PowerShell bootstrap child")
	}
	if value != "one=two" {
		t.Fatalf("bootstrap did not preserve an environment value containing '='")
	}
	t.Log("bootstrap environment verified")
}

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
