package codexhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackgroundCommandsExcludeAmbientNodeOptions(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "--require=missing-preload.cjs")
	t.Setenv("NODE_PATH", "untrusted-modules")
	for _, mode := range []string{"--probe-only", "--pairing-file", "--recover-intent", "--cleanup-only"} {
		command := backgroundCommand(t.Context(), "node", "dispatcher.mjs", mode)
		if command.Env == nil {
			t.Fatalf("%s inherits ambient Node options", mode)
		}
		for _, entry := range command.Env {
			key, _, _ := strings.Cut(entry, "=")
			if strings.EqualFold(key, "NODE_OPTIONS") || strings.EqualFold(key, "NODE_PATH") {
				t.Fatalf("%s inherits %s", mode, key)
			}
		}
	}
}

// This exercises a real Node process without contacting or restarting Codex.
// CI may use PATH; local runs can select an existing app-managed Node explicitly.
func TestNodeRuntimeIgnoresAmbientPreload(t *testing.T) {
	node := os.Getenv("LAZYMIND_TEST_NODE_BIN")
	if node == "" {
		var err error
		node, err = exec.LookPath("node")
		if err != nil {
			t.Skip("requires a Node executable")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	missing := filepath.Join(t.TempDir(), "missing-preload.cjs")
	t.Setenv("NODE_OPTIONS", "--require="+`"`+filepath.ToSlash(missing)+`"`)
	if output, err := exec.CommandContext(ctx, node, "--version").CombinedOutput(); err != nil {
		t.Fatalf("version check should still succeed: %s %v", output, err)
	}
	args := []string{"--input-type=module", "-e", `console.log("runtime-ready")`}
	if output, err := exec.CommandContext(ctx, node, args...).CombinedOutput(); err == nil || !strings.Contains(string(output), "MODULE_NOT_FOUND") {
		t.Fatalf("ambient preload did not reproduce startup failure: %s %v", output, err)
	}
	output, err := backgroundCommand(ctx, node, args...).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "runtime-ready" {
		t.Fatalf("background runtime failed despite successful version check: %s %v", output, err)
	}
}
