package state

import "testing"

// TestStateBackendFromEnv reads LAZYMIND_STATE_BACKEND and returns the backend name,
// defaulting to "redis" when unset and lowercasing/trimming the value.
func TestStateBackendFromEnv(t *testing.T) {
	tests := []struct {
		name   string
		envVal string
		setEnv bool
		want   string
	}{
		{"default empty", "", false, StateBackendRedis},
		{"explicit redis", "redis", true, StateBackendRedis},
		{"explicit sqlite", "sqlite", true, StateBackendSQLite},
		{"uppercase SQLITE", "SQLITE", true, StateBackendSQLite},
		{"with spaces", "  sqlite  ", true, StateBackendSQLite},
		{"unknown value", "mysql", true, "mysql"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv("LAZYMIND_STATE_BACKEND", tt.envVal)
			}
			got := StateBackendFromEnv()
			if got != tt.want {
				t.Fatalf("StateBackendFromEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIsSQLiteMode checks the convenience helper against the env-var state.
func TestIsSQLiteMode(t *testing.T) {
	t.Run("sqlite mode", func(t *testing.T) {
		t.Setenv("LAZYMIND_STATE_BACKEND", "sqlite")
		if !IsSQLiteMode() {
			t.Fatal("expected IsSQLiteMode() = true")
		}
	})
	t.Run("redis mode", func(t *testing.T) {
		t.Setenv("LAZYMIND_STATE_BACKEND", "redis")
		if IsSQLiteMode() {
			t.Fatal("expected IsSQLiteMode() = false")
		}
	})
	t.Run("default mode", func(t *testing.T) {
		if IsSQLiteMode() {
			t.Fatal("expected IsSQLiteMode() = false by default")
		}
	})
}
