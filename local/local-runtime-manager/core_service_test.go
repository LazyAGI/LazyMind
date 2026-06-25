package main

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestCoreServiceWaitForDatabaseUsesPgIsReady(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runner := &fakeRunner{t: t}
	manager := NewCoreServiceManager(runner)
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		assertCommand(t, cmd, "docker",
			"compose",
			"-f", repoComposeFileName,
			"-f", localComposeOverrideName,
			"exec",
			"-T",
			"db",
			"pg_isready",
			"-U", "root",
			"-d", "core",
		)
		if cmd.Dir != repo {
			t.Fatalf("unexpected pg_isready dir %q", cmd.Dir)
		}
		return CommandResult{Stdout: "db:5432 - accepting connections\n"}, nil
	})

	if err := manager.waitForCoreDatabase(context.Background(), cfg, paths); err != nil {
		t.Fatalf("wait database: %v", err)
	}
	runner.assertCommandCount(1)
}

func TestCoreServiceWaitForDatabaseReportsLastError(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runner := &fakeRunner{t: t}
	manager := NewCoreServiceManager(runner)
	previousTimeout := coreServiceDBWaitTimeout
	coreServiceDBWaitTimeout = time.Nanosecond
	t.Cleanup(func() { coreServiceDBWaitTimeout = previousTimeout })
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		return CommandResult{Stderr: "database system is starting up"}, errors.New("pg_isready failed")
	})

	err = manager.waitForCoreDatabase(context.Background(), cfg, paths)
	if err == nil {
		t.Fatal("expected wait database error")
	}
	if !strings.Contains(err.Error(), "pg_isready failed") {
		t.Fatalf("expected last runner error in message, got %v", err)
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
