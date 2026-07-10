package main

import (
	"os"
	"strconv"
	"strings"
	"testing"
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
