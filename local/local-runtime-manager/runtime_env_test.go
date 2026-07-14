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

func TestInstallerWarmupRuntimeEnvIsOfflineAndAllowsBytecodeCache(t *testing.T) {
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

	env := runtimeCommandEnv(paths, cfg)
	assertEnvContains(t, env, maintenanceModeEnvVar+"="+installerWarmupMaintenanceMode)
	assertEnvContains(t, env, "HF_HUB_OFFLINE=1")
	assertEnvContains(t, env, "TRANSFORMERS_OFFLINE=1")
	assertEnvContains(t, env, "PIP_NO_INDEX=1")
	assertEnvContains(t, env, "PYTHONDONTWRITEBYTECODE=0")
	assertEnvContains(t, env, "LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR="+cfg.FileWatcher.WatchHostDir)
}
