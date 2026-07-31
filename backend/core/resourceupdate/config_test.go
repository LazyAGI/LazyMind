package resourceupdate

import (
	"testing"
)

// TestDefaultConfig returns non-zero values for all time intervals.
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.SchedulerTickInterval <= 0 {
		t.Fatal("SchedulerTickInterval should be > 0")
	}
	if cfg.SchedulerLockTTL <= 0 {
		t.Fatal("SchedulerLockTTL should be > 0")
	}
	if cfg.WorkerInterval <= 0 {
		t.Fatal("WorkerInterval should be > 0")
	}
	if cfg.SchedulerBatchSize <= 0 {
		t.Fatal("SchedulerBatchSize should be > 0")
	}
	if len(cfg.Stages) == 0 {
		t.Fatal("Stages should be non-empty")
	}
	if !cfg.ConversationIdleEnableExpiredKeyNotify {
		t.Fatal("ConversationIdleEnableExpiredKeyNotify should default to true")
	}
}

// TestNormalizeConfig fills zero values with defaults.
func TestNormalizeConfig(t *testing.T) {
	cfg := normalizeConfig(Config{})
	if cfg.SchedulerTickInterval <= 0 {
		t.Fatal("zero config should use defaults")
	}
}

// TestNormalizeConfig_PreservesOverrides keeps explicitly set values.
func TestNormalizeConfig_PreservesOverrides(t *testing.T) {
	cfg := normalizeConfig(Config{SchedulerBatchSize: 42})
	if cfg.SchedulerBatchSize != 42 {
		t.Fatalf("got %d, want 42", cfg.SchedulerBatchSize)
	}
	// Other fields should still get defaults.
	if cfg.WorkerInterval <= 0 {
		t.Fatal("other fields should fall back to defaults")
	}
}

// TestWithConversationIdleExpiredKeyNotify sets the flag.
func TestWithConversationIdleExpiredKeyNotify(t *testing.T) {
	cfg := DefaultConfig()
	cfg2 := cfg.WithConversationIdleExpiredKeyNotify(false)
	if cfg2.ConversationIdleEnableExpiredKeyNotify {
		t.Fatal("expected false")
	}
	if !cfg.ConversationIdleEnableExpiredKeyNotify {
		t.Fatal("original config should not be mutated")
	}
}
