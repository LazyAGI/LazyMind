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
