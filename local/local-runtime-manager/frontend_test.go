package main

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	waitCh := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitCh)
	}()
	t.Cleanup(func() {
		_ = signalProcessGroup(cmd.Process.Pid, syscall.SIGKILL)
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
