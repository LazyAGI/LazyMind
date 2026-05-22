package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadResolvesRelativeBaseRootFromConfigDir(t *testing.T) {
	root := t.TempDir()
	cfgPath := writeTestConfig(t, root, `../../../data/scan`)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := filepath.Join(root, "data", "scan")
	if cfg.BaseRoot != want {
		t.Fatalf("BaseRoot = %q, want %q", cfg.BaseRoot, want)
	}
	if cfg.Staging.HostRoot != filepath.Join(want, "staging") {
		t.Fatalf("Staging.HostRoot = %q", cfg.Staging.HostRoot)
	}
	if cfg.Snapshot.HostRoot != filepath.Join(want, "snapshots") {
		t.Fatalf("Snapshot.HostRoot = %q", cfg.Snapshot.HostRoot)
	}
}

func TestLoadAllowsEnvOverrideForBaseRoot(t *testing.T) {
	root := t.TempDir()
	override := filepath.Join(root, "custom-scan-root")
	t.Setenv("LAZYMIND_FILE_WATCHER_BASE_ROOT", override)
	cfgPath := writeTestConfig(t, root, `${LAZYMIND_FILE_WATCHER_BASE_ROOT:-../../../data/scan}`)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.BaseRoot != override {
		t.Fatalf("BaseRoot = %q, want %q", cfg.BaseRoot, override)
	}
}

func TestLoadExpandsAgentEndpointsAndPathMappingsFromEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_FILE_WATCHER_LISTEN_ADDR", "0.0.0.0:19090")
	t.Setenv("LAZYMIND_FILE_WATCHER_CONTROL_PLANE_BASE_URL", "http://scan-control-plane:18080")
	t.Setenv("LAZYMIND_FILE_WATCHER_HOST_PATH_STYLE", "windows")
	t.Setenv("LAZYMIND_FILE_WATCHER_PATH_MAPPINGS", "C:/Users/alice/Documents=/watch/documents,D:/Data=/watch/data")
	cfgPath := writeTestConfig(t, root, `${LAZYMIND_FILE_WATCHER_BASE_ROOT:-../../../data/scan}`)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.ListenAddr != "0.0.0.0:19090" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.ControlPlaneBaseURL != "http://scan-control-plane:18080" {
		t.Fatalf("ControlPlaneBaseURL = %q", cfg.ControlPlaneBaseURL)
	}
	if cfg.HostPathStyle != "windows" {
		t.Fatalf("HostPathStyle = %q", cfg.HostPathStyle)
	}
	if len(cfg.PathMappings) != 2 {
		t.Fatalf("expected 2 path mappings, got %d", len(cfg.PathMappings))
	}
	if cfg.PathMappings[0].PublicRoot != "C:/Users/alice/Documents" || cfg.PathMappings[0].RuntimeRoot != "/watch/documents" {
		t.Fatalf("unexpected first mapping: %#v", cfg.PathMappings[0])
	}
}

func TestLoadDerivesPathMappingFromWatchVolumeEnv(t *testing.T) {
	root := t.TempDir()
	hostRoot := filepath.Join(root, "watch-root")
	t.Setenv("LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR", hostRoot)
	t.Setenv("LAZYMIND_FILE_WATCHER_WATCH_CONTAINER_DIR", "/watch/docs")
	cfgPath := writeTestConfig(t, root, `${LAZYMIND_FILE_WATCHER_BASE_ROOT:-../../../data/scan}`)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(cfg.PathMappings) != 1 {
		t.Fatalf("expected 1 path mapping, got %d", len(cfg.PathMappings))
	}
	if cfg.PathMappings[0].PublicRoot != hostRoot || cfg.PathMappings[0].RuntimeRoot != "/watch/docs" {
		t.Fatalf("unexpected mapping: %#v", cfg.PathMappings[0])
	}
}

func writeTestConfig(t *testing.T, root, baseRoot string) string {
	t.Helper()

	cfgDir := filepath.Join(root, "backend", "file-watcher", "configs")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	cfgPath := filepath.Join(cfgDir, "agent.yaml")
	data := []byte(`agent_id: "agent-test"
tenant_id: "tenant-test"
listen_addr: "${LAZYMIND_FILE_WATCHER_LISTEN_ADDR:-127.0.0.1:19090}"
control_plane_base_url: "${LAZYMIND_FILE_WATCHER_CONTROL_PLANE_BASE_URL:-http://127.0.0.1:18080}"
base_root: "` + baseRoot + `"
`)
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return cfgPath
}
