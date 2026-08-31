package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
)

func TestLocalPortAllocatorMovesOccupiedPreferredPort(t *testing.T) {
	allocator := newLocalPortAllocator()
	allocator.available = func(_ string, port int) bool {
		return port != 32000
	}

	port := allocator.resolvedPort("frontend", nil, 32000)
	if port != 32001 {
		t.Fatalf("resolved port = %d, want 32001", port)
	}
	if err := allocator.Err(); err != nil {
		t.Fatalf("unexpected allocation error: %v", err)
	}
	if len(allocator.resolutions) != 1 {
		t.Fatalf("port resolutions = %d, want 1", len(allocator.resolutions))
	}
	resolution := allocator.resolutions[0]
	if resolution.Name != "frontend" || resolution.RequestedPort != 32000 || resolution.ResolvedPort != 32001 {
		t.Fatalf("unexpected port resolution: %+v", resolution)
	}
}

func TestLocalPortAllocatorFailsWhenSearchRangeIsExhausted(t *testing.T) {
	allocator := newLocalPortAllocator()
	allocator.available = func(string, int) bool { return false }

	if port := allocator.resolvedPort("frontend", nil, 32000); port != 0 {
		t.Fatalf("resolved port = %d, want 0", port)
	}
	if allocator.Err() == nil {
		t.Fatal("expected exhausted port search to return an error")
	}
	if !strings.Contains(allocator.Err().Error(), "32000-32499") {
		t.Fatalf("allocation error = %q, want searched range", allocator.Err())
	}
}

func TestRuntimeConfigMovesOccupiedLocalProxyPortAndPropagatesIt(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy preferred local-proxy port: %v", err)
	}
	defer listener.Close()
	requested := listener.Addr().(*net.TCPAddr).Port
	if requested > 65000 {
		t.Skipf("ephemeral port %d leaves too little fallback range", requested)
	}
	t.Setenv(localProxyPortEnvVar, strconv.Itoa(requested))
	t.Setenv(localPortsPinnedEnvVar, "false")

	cfg, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if cfg.LocalProxy.Port == requested {
		t.Fatalf("local-proxy port did not move from occupied preferred port %d", requested)
	}
	wantEnv := localProxyPortEnvVar + "=" + strconv.Itoa(cfg.LocalProxy.Port)
	if !containsEnv(runtimeCommandEnv(paths, cfg), wantEnv) {
		t.Fatalf("runtime command environment does not contain %q", wantEnv)
	}
	recorded := false
	for _, resolution := range cfg.PortResolutions {
		if resolution.Name == "local-proxy" && resolution.RequestedPort == requested && resolution.ResolvedPort == cfg.LocalProxy.Port {
			recorded = true
			break
		}
	}
	if !recorded {
		t.Fatalf("occupied preferred port was not recorded: %+v", cfg.PortResolutions)
	}
}

func containsEnv(env []string, want string) bool {
	for _, item := range env {
		if item == want {
			return true
		}
	}
	return false
}

func TestRuntimeConfigSnapshotPersistsPortResolutions(t *testing.T) {
	cfg := RuntimeConfig{
		PortResolutions: []PortResolution{{
			Name:          "local-proxy",
			EnvName:       localProxyPortEnvVar,
			RequestedPort: 5024,
			ResolvedPort:  5025,
			Reason:        "preferred port unavailable",
		}},
	}
	snapshot := snapshotRuntimeConfig(cfg)
	if len(snapshot.PortResolutions) != 1 {
		t.Fatalf("snapshot port resolutions = %d, want 1", len(snapshot.PortResolutions))
	}

	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal runtime config snapshot: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		`"portResolutions"`,
		`"name":"local-proxy"`,
		`"requestedPort":5024`,
		`"resolvedPort":5025`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("snapshot JSON %s does not contain %s", text, want)
		}
	}
	restored := applyStateConfig(RuntimeConfig{
		PortResolutions: []PortResolution{{Name: "status-probe", RequestedPort: 1, ResolvedPort: 2}},
	}, RuntimeState{Config: snapshot})
	if len(restored.PortResolutions) != 1 || restored.PortResolutions[0].Name != "local-proxy" {
		t.Fatalf("state did not restore authoritative port resolutions: %+v", restored.PortResolutions)
	}
}

func TestRuntimeStartPreflightReportsPortClaimedAfterAllocation(t *testing.T) {
	t.Setenv(localPortsPinnedEnvVar, "false")
	cfg := RuntimeConfig{
		LocalProxy: LocalProxyConfig{Port: 5024},
	}
	err := validateRuntimeStartPortsWith(cfg, func(_ string, port int) bool {
		return port != cfg.LocalProxy.Port
	})
	if !isStartupPortConflict(err) {
		t.Fatalf("preflight error = %v, want startup port conflict", err)
	}
	var conflict *startupPortConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("preflight error type = %T, want startupPortConflictError", err)
	}
	if conflict.Service != "local-proxy" || conflict.Port != 5024 {
		t.Fatalf("unexpected preflight conflict: %+v", conflict)
	}
}

func TestStartupPortConflictCanBeDetectedThroughWrappedErrors(t *testing.T) {
	cause := &startupPortConflictError{
		Service: "core",
		Address: "127.0.0.1",
		Port:    18001,
		Cause:   errors.New("port claimed"),
	}
	wrapped := fmt.Errorf("startup attempt failed: %w", cause)
	if !isStartupPortConflict(wrapped) {
		t.Fatal("wrapped startup port conflict was not detected")
	}
}
