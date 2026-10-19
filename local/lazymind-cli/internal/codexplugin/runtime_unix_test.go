//go:build !windows

package codexplugin

import (
	"lazymind/agentconnector/internal/executorpolicy"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCLIFlagStopsAndRestartsOnlyOwnedDispatcher(t *testing.T) {
	home, profile, _, binary := fixture(t)
	policy, _ := executorpolicy.New(home)
	t.Setenv("LAZYMIND_CODEX_BIN", binary)
	t.Setenv("LAZYMIND_WORKFLOW_PAIRING_FILE", filepath.Join(home, "pairing.json"))
	t.Setenv("LAZYMIND_NODE_BIN", executable(t, t.TempDir(), "worker", "#!/bin/sh\necho $$ > \"$CODEX_HOME/worker.pid\"\nexec sleep 60\n"))
	stop, err := StartWorker(t.Context(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	pidFile := filepath.Join(profile, "worker.pid")
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatal("disabled CLI started dispatcher")
	}
	for range 2 {
		if _, err := policy.SetEnabled("codex", true); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		pid := 0
		for time.Now().Before(deadline) {
			body, _ := os.ReadFile(pidFile)
			pid, _ = strconv.Atoi(strings.TrimSpace(string(body)))
			if pid > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if pid <= 0 {
			t.Fatal("enabled CLI did not start dispatcher")
		}
		if _, err := policy.SetEnabled("codex", false); err != nil {
			t.Fatal(err)
		}
		deadline = time.Now().Add(7 * time.Second)
		for time.Now().Before(deadline) && syscall.Kill(pid, 0) == nil {
			time.Sleep(20 * time.Millisecond)
		}
		if syscall.Kill(pid, 0) == nil {
			t.Fatal("disabled CLI left dispatcher running")
		}
		if err := os.Remove(pidFile); err != nil {
			t.Fatal(err)
		}
	}
}
