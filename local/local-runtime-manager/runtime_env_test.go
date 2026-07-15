package main

import "testing"

func TestServiceRuntimeEnvDisablesPythonBytecodeWrites(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}

	assertEnvContains(t, serviceRuntimeEnv(paths), "PYTHONDONTWRITEBYTECODE=1")
	assertEnvContains(t, runtimeCommandEnv(paths, cfg), "PYTHONDONTWRITEBYTECODE=1")
}

func TestRuntimeEnvCarriesLocalAutoLoginLANFlag(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}

	assertEnvContains(t, localRuntimeEnv(cfg), localAutoLoginAllowLANEnvVar+"=false")
	assertEnvContains(t, runtimeCommandEnv(paths, cfg), localAutoLoginAllowLANEnvVar+"=false")

	t.Setenv(localAutoLoginAllowLANEnvVar, "true")
	assertEnvContains(t, localRuntimeEnv(cfg), localAutoLoginAllowLANEnvVar+"=true")
	assertEnvContains(t, runtimeCommandEnv(paths, cfg), localAutoLoginAllowLANEnvVar+"=true")
}

func TestInstallerWarmupUsesPerProcessCapabilities(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfigWithOptions(RuntimeConfigOptions{
		Profile:         defaultProfileValue(),
		RepoRoot:        repo,
		MaintenanceMode: installerWarmupMaintenanceMode,
	})
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}

	base := runtimeCommandEnv(paths, cfg)
	plan := buildRuntimeProcessPlan(cfg)
	assertEnvContains(t, base, "PYTHONDONTWRITEBYTECODE=1")
	assertEnvMissing(t, base, "LAZYMIND_MAINTENANCE_MODE")

	authEnv := runtimeProcessEnvironment(base, cfg, plan, authServiceProcessName)
	assertEnvContains(t, authEnv, "HF_HUB_OFFLINE=1")
	assertEnvContains(t, authEnv, "TRANSFORMERS_OFFLINE=1")
	assertEnvContains(t, authEnv, "PIP_NO_INDEX=1")
	assertEnvContains(t, authEnv, "PYTHONDONTWRITEBYTECODE=0")
	assertEnvContains(t, authEnv, "LAZYMIND_CLOUD_AUTH_HEALTH_CHECK_ENABLED=false")

	coreEnv := runtimeProcessEnvironment(base, cfg, plan, coreProcessName)
	assertEnvContains(t, coreEnv, "LAZYMIND_BACKGROUND_JOBS_ENABLED=false")
	assertEnvContains(t, coreEnv, "PYTHONDONTWRITEBYTECODE=1")
	assertEnvMissing(t, coreEnv, "HF_HUB_OFFLINE")

	chatEnv := runtimeProcessEnvironment(base, cfg, plan, chatProcessName)
	assertEnvContains(t, chatEnv, "LAZYMIND_BACKGROUND_JOBS_ENABLED=false")
	assertEnvContains(t, chatEnv, "LAZYMIND_ROUTER_CHILD_PROCESSES_ENABLED=false")
	assertEnvContains(t, chatEnv, "PYTHONDONTWRITEBYTECODE=0")
}

func TestMaintenanceModeIsAcceptedOnlyFromTypedOptions(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	t.Setenv("LAZYMIND_MAINTENANCE_MODE", installerWarmupMaintenanceMode)
	cfg, _, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if cfg.MaintenanceMode != "" {
		t.Fatalf("maintenance mode leaked from environment: %q", cfg.MaintenanceMode)
	}
}

func assertEnvMissing(t *testing.T, env []string, key string) {
	t.Helper()
	prefix := key + "="
	for _, item := range env {
		if item == key || len(item) >= len(prefix) && item[:len(prefix)] == prefix {
			t.Fatalf("environment unexpectedly contains %q: %v", key, env)
		}
	}
}
