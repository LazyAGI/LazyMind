package codexplugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkerKeepsOutputInLogAndStopsWithMCP(t *testing.T) {
	home, profile, _, binary := fixture(t)
	node := executable(t, t.TempDir(), "worker", "#!/bin/sh\necho 'worker stdout'\necho 'worker stderr' >&2\nexec sleep 60\n")
	t.Setenv("LAZYMIND_NODE_BIN", node)
	t.Setenv("LAZYMIND_CODEX_BIN", binary)
	t.Setenv("LAZYMIND_WORKFLOW_PAIRING_FILE", filepath.Join(home, "pairing.json"))
	t.Setenv("CODEX_HOME", profile)
	stop, err := StartWorker(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			stop()
		}
	}()
	logPath := filepath.Join(home, "logs", "codex-workflow.log")
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(logPath)
		if strings.Contains(string(data), "worker stdout") && strings.Contains(string(data), "worker stderr") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker output missing: %s", data)
		}
		time.Sleep(10 * time.Millisecond)
	}
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
		stopped = true
	case <-time.After(7 * time.Second):
		t.Fatal("MCP shutdown did not stop worker")
	}
}

func TestWorkerRequiresInstallation(t *testing.T) {
	home, _, _, _ := fixture(t)
	t.Setenv("LAZYMIND_WORKFLOW_PAIRING_FILE", "")
	if _, err := StartWorker(context.Background(), home); err == nil {
		t.Fatal("started unconfigured worker")
	}
}
