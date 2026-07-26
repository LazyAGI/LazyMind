package main

import "testing"

func TestOpenAPIArtifactExportCanBeDisabledForSignedDesktopBundle(t *testing.T) {
	t.Setenv("LAZYMIND_OPENAPI_ARTIFACT_EXPORT_ENABLED", "false")
	if openAPIArtifactExportEnabled() {
		t.Fatal("OpenAPI artifact export should be disabled")
	}

	t.Setenv("LAZYMIND_OPENAPI_ARTIFACT_EXPORT_ENABLED", "")
	if !openAPIArtifactExportEnabled() {
		t.Fatal("OpenAPI artifact export should remain enabled by default")
	}
}

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

func TestValidateStartupConfigRequiresInternalToken(t *testing.T) {
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "")
	if err := validateStartupConfig(); err == nil {
		t.Fatal("validateStartupConfig() should reject an empty internal token")
	}

	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-secret")
	if err := validateStartupConfig(); err != nil {
		t.Fatalf("validateStartupConfig() error = %v", err)
	}
}

func TestValidateStartupConfigRejectsInvalidPreferenceCapacity(t *testing.T) {
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-secret")
	for _, value := range []string{"", "0", "-1", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("LAZYMIND_PREFERENCE_INDEX_MAX_ITEMS", value)
			if err := validateStartupConfig(); err == nil {
				t.Fatalf("validateStartupConfig() should reject %q", value)
			}
		})
	}
}
