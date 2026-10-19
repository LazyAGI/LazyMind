package codexruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeEndpointValidation(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if err := validateEndpoint("ws://127.0.0.1:49152", token); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"ws://0.0.0.0:1", "ws://localhost:1", "ws://127.0.0.1:65536", "ws://user@127.0.0.1:1", "ws://127.0.0.1:123/rpc", "ws://127.0.0.1:123?token=secret"} {
		if validateEndpoint(endpoint, token) == nil {
			t.Fatal("accepted", endpoint)
		}
	}
	if validateEndpoint("ws://127.0.0.1:49152", "") == nil {
		t.Fatal("accepted unauthenticated TCP")
	}
}
func TestNativeBackendArgumentsPreservePolicy(t *testing.T) {
	c := &runtimeConfig{RuntimeDir: t.TempDir(), Node: "original-node", Helper: "fixture-helper", BundleDir: "bundle", Endpoint: "ws://127.0.0.1:49152", TokenFile: "token-file", Plugins: map[string]map[string]any{}}
	for _, kind := range []string{"codex_app", "cua_repl"} {
		c.Plugins[kind] = map[string]any{"command": "original-plugin", "args": []any{"entry"}, "startup_timeout_sec": float64(20), "approval_policy": "on-request"}
	}
	args, err := backendArgs(c, "runtime-config")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--ws-auth capability-token --ws-token-file token-file", `"approval_policy"="on-request"`, `"startup_timeout_sec"=20`, "mcp-proxy.mjs", `"LAZYMIND_RUNTIME_HELPER"="fixture-helper"`} {
		if !strings.Contains(joined, want) {
			t.Fatal("missing", want, joined)
		}
	}
	if c.Plugins["codex_app"]["command"] != "original-plugin" {
		t.Fatal("mutated original plugin configuration")
	}
}
func TestWindowsLayoutRequiresBundledResources(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(root, "resources", "codex-cli", "codex.exe")
	node := filepath.Join(root, "resources", "cua_node", "node.exe")
	for _, p := range []string{cli, node, filepath.Join(root, "Codex.exe")} {
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	actual, found, err := windowsDesktopLayout(cli)
	if err != nil || actual != root || found != node {
		t.Fatalf("%s %s %v", actual, found, err)
	}
	_ = os.WriteFile(filepath.Join(root, "ChatGPT.exe"), []byte("ambiguous"), 0600)
	if _, _, err = windowsDesktopLayout(cli); err == nil {
		t.Fatal("accepted ambiguous app layout without a package manifest")
	}
}
func TestPrivateFilesAndProcessIdentity(t *testing.T) {
	root := t.TempDir()
	if err := makePrivate(root); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "state.json")
	if err := writePrivateJSON(file, map[string]string{"kind": "test"}); err != nil {
		t.Fatal(err)
	}
	if err := checkPrivate(file); err != nil {
		t.Fatal(err)
	}
	info, err := platformProcess(os.Getpid())
	if err != nil || info == nil || !sameProcess(info) {
		t.Fatalf("process: %#v %v", info, err)
	}
}
