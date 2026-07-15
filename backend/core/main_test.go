package main

import "testing"

func TestCoreListenAddrDefaultsToCloudPort(t *testing.T) {
	t.Setenv("LAZYMIND_CORE_HOST", "")
	t.Setenv("LAZYMIND_CORE_PORT", "")

	if got := coreListenAddr(); got != ":8000" {
		t.Fatalf("coreListenAddr() = %q, want :8000", got)
	}
}

func TestCoreListenAddrUsesLocalHostAndPort(t *testing.T) {
	t.Setenv("LAZYMIND_CORE_HOST", "127.0.0.1")
	t.Setenv("LAZYMIND_CORE_PORT", "18001")

	if got := coreListenAddr(); got != "127.0.0.1:18001" {
		t.Fatalf("coreListenAddr() = %q, want 127.0.0.1:18001", got)
	}
}

func TestBackgroundJobsEnabledDefaultsTrue(t *testing.T) {
	t.Setenv("LAZYMIND_BACKGROUND_JOBS_ENABLED", "")
	if !backgroundJobsEnabled() {
		t.Fatal("background jobs should be enabled by default")
	}
}

func TestBackgroundJobsEnabledAcceptsFalseValues(t *testing.T) {
	for _, value := range []string{"0", "false", "no", "off", " FALSE "} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("LAZYMIND_BACKGROUND_JOBS_ENABLED", value)
			if backgroundJobsEnabled() {
				t.Fatalf("background jobs should be disabled for %q", value)
			}
		})
	}
}
