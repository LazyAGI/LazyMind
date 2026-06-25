package main

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
)

func TestCoreServiceBuildUsesBackendCore(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	runner := &fakeRunner{t: t}
	manager := NewCoreServiceManager(runner)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		assertCommand(t, cmd, "go", "build", "-buildvcs=false", "-o", paths.CoreBin, ".")
		if cmd.Dir != filepath.Join(repo, coreSourceDirName) {
			t.Fatalf("unexpected core build dir %q", cmd.Dir)
		}
		return CommandResult{}, nil
	})

	if err := manager.buildCore(context.Background(), paths); err != nil {
		t.Fatalf("build core: %v", err)
	}
	runner.assertCommandCount(1)
}

func TestCoreServiceEnvUsesLocalEndpoints(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	env := coreServiceEnv(cfg, paths)

	assertEnvContains(t, env, "LAZYMIND_CORE_HOST=127.0.0.1")
	assertEnvContains(t, env, "LAZYMIND_CORE_PORT="+strconv.Itoa(cfg.LocalProxy.CoreHostPort))
	assertEnvContains(t, env, "ACL_DB_DSN=host=127.0.0.1 user=root password=123456 dbname=core port="+strconv.Itoa(cfg.Algorithm.PostgresPort)+" sslmode=disable TimeZone=UTC")
	assertEnvContains(t, env, "LAZYMIND_AUTH_SERVICE_URL=http://127.0.0.1:"+strconv.Itoa(cfg.AuthService.Port)+"/api/authservice")
	assertEnvContains(t, env, "LAZYMIND_DOCUMENT_SERVICE_URL=http://127.0.0.1:"+strconv.Itoa(cfg.Algorithm.DocPort))
	assertEnvContains(t, env, "LAZYMIND_PARSING_SERVICE_URL=http://127.0.0.1:"+strconv.Itoa(cfg.Algorithm.ProcessorPort))
	assertEnvContains(t, env, "LAZYMIND_CHAT_SERVICE_URL=http://127.0.0.1:"+strconv.Itoa(cfg.Algorithm.ChatPort))
	assertEnvContains(t, env, "LAZYMIND_OFFICE_CONVERT_URL=http://127.0.0.1:18082/v1/office/to-pdf")
}

func assertEnvContains(t *testing.T, env []string, want string) {
	t.Helper()
	for _, item := range env {
		if item == want {
			return
		}
	}
	t.Fatalf("missing env %q in %#v", want, env)
}
