package main

import (
	"context"
	"os"
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
	if _, err := os.Stat(filepath.Join(repo, coreSourceDirName, "docs", "swagger.json")); err != nil {
		t.Fatalf("expected local swagger embed placeholder: %v", err)
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
	assertEnvContains(t, env, "ACL_DB_DRIVER=sqlite")
	assertEnvContains(t, env, "ACL_DB_DSN="+sqliteDSN(paths.CoreDBPath))
	assertEnvContains(t, env, "LAZYMIND_CORE_DATABASE_URL="+sqliteURL(paths.CoreDBPath))
	assertEnvContains(t, env, "LAZYMIND_AUTH_SERVICE_URL=http://127.0.0.1:"+strconv.Itoa(cfg.AuthService.Port)+"/api/authservice")
	assertEnvContains(t, env, "LAZYMIND_DOCUMENT_SERVICE_URL=http://127.0.0.1:"+strconv.Itoa(cfg.Algorithm.DocPort))
	assertEnvContains(t, env, "LAZYMIND_PARSING_SERVICE_URL=http://127.0.0.1:"+strconv.Itoa(cfg.Algorithm.ProcessorPort))
	assertEnvContains(t, env, "LAZYMIND_CHAT_SERVICE_URL=http://127.0.0.1:"+strconv.Itoa(cfg.Algorithm.ChatPort))
	assertEnvContains(t, env, "LAZYMIND_OFFICE_CONVERT_URL=http://127.0.0.1:18082/v1/office/to-pdf")
	assertEnvContains(t, env, "LAZYMIND_READONLY_DB_DRIVER=sqlite")
	assertEnvContains(t, env, "LAZYMIND_READONLY_DB_DSN="+paths.LazyLLMDBPath)
}

func TestCoreServiceWaitForDatabasePreparesSQLiteDirs(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runner := &fakeRunner{t: t}
	manager := NewCoreServiceManager(runner)

	if err := manager.waitForCoreDatabase(context.Background(), cfg, paths); err != nil {
		t.Fatalf("wait database: %v", err)
	}
	runner.assertCommandCount(0)
	for _, path := range []string{paths.CoreDBPath, paths.LazyLLMDBPath} {
		if _, err := os.Stat(filepath.Dir(path)); err != nil {
			t.Fatalf("expected sqlite dir for %s: %v", path, err)
		}
	}
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
