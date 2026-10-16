package codexruntime

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEmbeddedRuntimeMaterializesAndRejectsChangedAssets(t *testing.T) {
	if !Supported() {
		t.Skip("desktop installer platform")
	}
	root := t.TempDir()
	if err := makePrivate(root); err != nil {
		t.Fatal(err)
	}
	bundle, err := materialize(root)
	if err != nil {
		t.Fatal(err)
	}
	same, err := materialize(root)
	if err != nil || same != bundle {
		t.Fatalf("repeat: %s %v", same, err)
	}
	for _, name := range []string{"install.mjs", "prepare.mjs", "manage.mjs", "adapter.mjs", "mcp-proxy.mjs"} {
		if _, err := os.Stat(filepath.Join(bundle, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bundle, "adapter.mjs"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := materialize(root); err == nil {
		t.Fatal("modified runtime was accepted")
	}
}

func TestDesktopRuntimeResolvesOuterBundleAndRejectsStandaloneCLI(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS bundle layout")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(root, "Codex.app")
	cli := filepath.Join(app, "Contents", "Resources", "codex-cli", "CodexCLI.app", "Contents", "MacOS", "codex")
	node := filepath.Join(app, "Contents", "Resources", "cua_node", "bin", "node")
	for _, file := range []string{cli, node} {
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	actual, runtime, err := desktopRuntime(cli)
	if err != nil || actual != app || runtime != node {
		t.Fatalf("%s %s %v", actual, runtime, err)
	}
	standalone := filepath.Join(root, "codex")
	_ = os.WriteFile(standalone, []byte("fixture"), 0700)
	if _, _, err := desktopRuntime(standalone); err == nil {
		t.Fatal("accepted standalone CLI")
	}
}
