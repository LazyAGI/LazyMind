package codexhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Opt in against an already running original app. This test never launches or
// quits it and stores generated probe/config files only in its temporary home.
func TestLiveConnectionPreflight(t *testing.T) {
	if os.Getenv("LAZYMIND_CODEX_LIVE_PROBE") != "1" {
		t.Skip("requires an existing Codex CDP connection")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := Configure(home, node, filepath.Join(home, "unused-pairing.json")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := CheckConnection(ctx, home); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Join(home, "codex-workflow"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("unexpected persistence outside config and probe script: %v", files)
	}
}
