package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWriteCaddyfileProxiesLocalEndpoints(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	if err := writeCaddyfile(paths, cfg); err != nil {
		t.Fatalf("write Caddyfile: %v", err)
	}
	raw, err := os.ReadFile(paths.CaddyConfig)
	if err != nil {
		t.Fatalf("read Caddyfile: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "handle /_local/*") {
		t.Fatalf("Caddyfile missing /_local proxy:\n%s", content)
	}
	if !strings.Contains(content, "reverse_proxy http://127.0.0.1:"+strconv.Itoa(cfg.LocalProxy.Port)) {
		t.Fatalf("Caddyfile missing local-proxy reverse proxy:\n%s", content)
	}
	expectedAssets := filepath.Join(paths.RepoRoot, "skills", ".runtime", "featured-skills", "assets")
	if !strings.Contains(content, "handle_path /showcase-assets/*") || !strings.Contains(content, filepath.ToSlash(expectedAssets)) {
		t.Fatalf("Caddyfile missing featured asset route:\n%s", content)
	}

	cfg.Profile = "desktop"
	paths.ResourcesRoot = t.TempDir()
	if err := writeCaddyfile(paths, cfg); err != nil {
		t.Fatalf("write desktop Caddyfile: %v", err)
	}
	raw, err = os.ReadFile(paths.CaddyConfig)
	if err != nil {
		t.Fatal(err)
	}
	desktopAssets := filepath.Join(paths.ResourcesRoot, "featured-skills", "assets")
	if !strings.Contains(string(raw), filepath.ToSlash(desktopAssets)) {
		t.Fatalf("desktop Caddyfile missing default featured assets path:\n%s", raw)
	}
	if err := os.MkdirAll(filepath.Join(paths.ResourcesRoot, "featured-skills"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.ResourcesRoot, "featured-skills", "downloads.json"), []byte(`{"schemaVersion":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeCaddyfile(paths, cfg); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(paths.CaddyConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "handle /showcase-assets/*") || !strings.Contains(string(raw), "reverse_proxy http://127.0.0.1:"+strconv.Itoa(cfg.LocalProxy.CoreHostPort)) {
		t.Fatalf("deferred assets must use Core without stripping their URL prefix: %s", raw)
	}

}

func TestFrontendDownStopsPIDFileProcess(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	cmd := exec.Command("sleep", "60")
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell.exe", "-NoProfile", "-Command", "Start-Sleep -Seconds 60")
	}
	configureChildProcess(cmd, false)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	waitCh := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitCh)
	}()
	t.Cleanup(func() {
		_ = forceKillProcessTree(cmd.Process.Pid)
		_ = cmd.Process.Kill()
		select {
		case <-waitCh:
		case <-time.After(2 * time.Second):
		}
	})
	if err := os.WriteFile(paths.FrontendPIDFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatalf("write frontend pid file: %v", err)
	}

	manager := NewFrontendManager(&fakeRunner{t: t})
	if err := manager.Down(context.Background(), cfg, paths); err != nil {
		t.Fatalf("frontend down: %v", err)
	}
	select {
	case <-waitCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("frontend pid %d still alive", cmd.Process.Pid)
	}
	if _, err := os.Stat(paths.FrontendPIDFile); !os.IsNotExist(err) {
		t.Fatalf("frontend pid file still exists: %v", err)
	}
}

func TestFrontendNodeModulesReadyLayouts(t *testing.T) {
	for _, tc := range []struct {
		name, metadata         string
		missingVite, wantReady bool
	}{
		{"hoisted", "nodeLinker: hoisted\n", false, true},
		{"isolated", "nodeLinker: isolated\nvirtualStoreDir: .pnpm\n", false, true},
		{"wrong isolated store", "nodeLinker: isolated\nvirtualStoreDir: /wrong/store\n", false, false},
		{"hoisted missing vite", "nodeLinker: hoisted\n", true, false},
		{"comment is not linker", "# nodeLinker: hoisted\nvirtualStoreDir: /wrong/store\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			nm := filepath.Join(root, "node_modules")
			if err := os.MkdirAll(filepath.Join(nm, ".bin"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(nm, "vite", "bin"), 0755); err != nil {
				t.Fatal(err)
			}
			files := map[string]string{filepath.Join(nm, ".modules.yaml"): tc.metadata, frontendToolPath(nm, "vite"): ""}
			if !tc.missingVite {
				files[filepath.Join(nm, "vite", "bin", "vite.js")] = ""
			}
			for path, content := range files {
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			ready, reason, err := frontendNodeModulesReady(RuntimePaths{FrontendNodeModules: nm}, root)
			if err != nil || ready != tc.wantReady {
				t.Fatalf("ready=%v reason=%q err=%v", ready, reason, err)
			}
		})
	}
}

func TestPrepareFrontendNodeModulesPreservesHoistedRuntime(t *testing.T) {
	root := t.TempDir()
	runtimeModules := filepath.Join(root, "runtime", "node_modules")
	frontend := filepath.Join(root, "frontend")
	if err := os.MkdirAll(filepath.Join(runtimeModules, "vite", "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runtimeModules, ".bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(frontend, 0755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(runtimeModules, "keep")
	for path, content := range map[string]string{filepath.Join(runtimeModules, ".modules.yaml"): "nodeLinker: hoisted\n", frontendToolPath(runtimeModules, "vite"): "", filepath.Join(runtimeModules, "vite", "bin", "vite.js"): "", marker: "keep"} {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := createDirectoryLink(runtimeModules, filepath.Join(frontend, "node_modules")); err != nil {
		t.Fatal(err)
	}
	if err := prepareFrontendNodeModules(RuntimePaths{FrontendNodeModules: runtimeModules}, frontend); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("valid hoisted dependencies were cleared: %v", err)
	}
}
