package evalset

import (
	"os"
	"testing"
	"time"
)

// TestEnvString returns env value when set, fallback otherwise.
func TestEnvString(t *testing.T) {
	os.Unsetenv("TEST_ENV_STRING")
	if got := envString("TEST_ENV_STRING", "default"); got != "default" {
		t.Fatalf("got %q, want default", got)
	}
	os.Setenv("TEST_ENV_STRING", "custom")
	if got := envString("TEST_ENV_STRING", "default"); got != "custom" {
		t.Fatalf("got %q, want custom", got)
	}
}

// TestEnvDuration parses duration or returns fallback for invalid values.
func TestEnvDuration(t *testing.T) {
	os.Unsetenv("TEST_ENV_DUR")
	if got := envDuration("TEST_ENV_DUR", 5*time.Minute); got != 5*time.Minute {
		t.Fatalf("got %v, want 5m", got)
	}
	os.Setenv("TEST_ENV_DUR", "10m")
	if got := envDuration("TEST_ENV_DUR", 5*time.Minute); got != 10*time.Minute {
		t.Fatalf("got %v, want 10m", got)
	}
	// Invalid format falls back
	os.Setenv("TEST_ENV_DUR", "bad")
	if got := envDuration("TEST_ENV_DUR", 5*time.Minute); got != 5*time.Minute {
		t.Fatalf("invalid got %v, want 5m fallback", got)
	}
}

// TestEnvInt parses int or returns fallback.
func TestEnvInt(t *testing.T) {
	os.Unsetenv("TEST_ENV_INT")
	if got := envInt("TEST_ENV_INT", 42); got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
	os.Setenv("TEST_ENV_INT", "100")
	if got := envInt("TEST_ENV_INT", 42); got != 100 {
		t.Fatalf("got %d, want 100", got)
	}
	// Invalid format falls back
	os.Setenv("TEST_ENV_INT", "bad")
	if got := envInt("TEST_ENV_INT", 42); got != 42 {
		t.Fatalf("invalid got %d, want 42", got)
	}
}

// TestEnvBytes parses int64 or returns fallback.
func TestEnvBytes(t *testing.T) {
	os.Unsetenv("TEST_ENV_BYTES")
	if got := envBytes("TEST_ENV_BYTES", 1024); got != 1024 {
		t.Fatalf("got %d, want 1024", got)
	}
	os.Setenv("TEST_ENV_BYTES", "2048")
	if got := envBytes("TEST_ENV_BYTES", 1024); got != 2048 {
		t.Fatalf("got %d, want 2048", got)
	}
	// Invalid format falls back
	os.Setenv("TEST_ENV_BYTES", "bad")
	if got := envBytes("TEST_ENV_BYTES", 1024); got != 1024 {
		t.Fatalf("invalid got %d, want 1024", got)
	}
}

// TestEnvDurationWithAliases checks primary env first, then aliases.
func TestEnvDurationWithAliases(t *testing.T) {
	os.Unsetenv("EVAL_SET_IMPORT_CLEANUP_INTERVAL")
	os.Unsetenv("EVAL_SET_IMPORT_CLEAN_INTERVAL")

	// No env set → fallback
	got := envDurationWithAliases(time.Hour, "EVAL_SET_IMPORT_CLEANUP_INTERVAL", "EVAL_SET_IMPORT_CLEAN_INTERVAL")
	if got != time.Hour {
		t.Fatalf("got %v, want 1h", got)
	}

	// Primary env set
	os.Setenv("EVAL_SET_IMPORT_CLEANUP_INTERVAL", "30m")
	got2 := envDurationWithAliases(time.Hour, "EVAL_SET_IMPORT_CLEANUP_INTERVAL", "EVAL_SET_IMPORT_CLEAN_INTERVAL")
	if got2 != 30*time.Minute {
		t.Fatalf("got %v, want 30m", got2)
	}
	os.Unsetenv("EVAL_SET_IMPORT_CLEANUP_INTERVAL")

	// Alias env set
	os.Setenv("EVAL_SET_IMPORT_CLEAN_INTERVAL", "45m")
	got3 := envDurationWithAliases(time.Hour, "EVAL_SET_IMPORT_CLEANUP_INTERVAL", "EVAL_SET_IMPORT_CLEAN_INTERVAL")
	if got3 != 45*time.Minute {
		t.Fatalf("got %v, want 45m", got3)
	}
}

// TestDefaultImportTempDir returns a path under os.TempDir.
func TestDefaultImportTempDir(t *testing.T) {
	dir := defaultImportTempDir()
	if dir == "" {
		t.Fatal("expected non-empty temp dir")
	}
}

// TestLoadImportRuntimeConfigFromEnv returns config with defaults.
func TestLoadImportRuntimeConfigFromEnv(t *testing.T) {
	os.Unsetenv("EVAL_SET_IMPORT_PREVIEW_TTL")
	cfg := LoadImportRuntimeConfigFromEnv()
	if cfg.MaxRows != defaultImportMaxRows {
		t.Fatalf("max_rows = %d, want %d", cfg.MaxRows, defaultImportMaxRows)
	}
	if cfg.MaxFileSize != defaultImportMaxFileSize {
		t.Fatalf("max_file_size = %d, want %d", cfg.MaxFileSize, defaultImportMaxFileSize)
	}
}

// TestLoadAsyncJobRuntimeConfigFromEnv returns config with defaults.
func TestLoadAsyncJobRuntimeConfigFromEnv(t *testing.T) {
	os.Unsetenv("ASYNC_JOB_CONCURRENCY")
	cfg := LoadAsyncJobRuntimeConfigFromEnv()
	if cfg.Concurrency != defaultAsyncJobConcurrency {
		t.Fatalf("concurrency = %d, want %d", cfg.Concurrency, defaultAsyncJobConcurrency)
	}
}
