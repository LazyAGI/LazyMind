package codexhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"lazymind/agentconnector/internal/workflowconnector"
)

// Opt in against an already running original app. This test never launches or
// quits it and copies its sidecar into a temporary installation.
func TestLiveConnectionPreflight(t *testing.T) {
	if os.Getenv("LAZYMIND_CODEX_LIVE_PROBE") != "1" {
		t.Skip("requires an existing Codex CDP connection")
	}
	executable := os.Getenv("LAZYMIND_TEST_CONNECTOR_BIN")
	if executable == "" {
		t.Fatal("set LAZYMIND_TEST_CONNECTOR_BIN to the built CLI with its adjacent Workflow Connector package")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := Configure(home, node, filepath.Join(home, "unused-pairing.json")); err != nil {
		t.Fatal(err)
	}
	if err := workflowconnector.Ensure(t.Context(), home, executable, "codex"); err != nil {
		t.Fatal(err)
	}
	installed, err := workflowconnector.Resolve(home, "codex")
	if err != nil {
		t.Fatal(err)
	}
	revision, err := installed.Revision()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := CheckConnection(ctx, home); err != nil {
		t.Fatal(err)
	}
	if after, err := installed.Revision(); err != nil || after != revision {
		t.Fatalf("read-only preflight changed installed Connector modules: %s %v", after, err)
	}
}
