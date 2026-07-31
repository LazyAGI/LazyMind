package resourceupdate

import (
	"os"
	"strings"
	"testing"
)

// TestEnabledFromEnvTrue accepts "1", "true", "yes" (case-insensitive).
func TestEnabledFromEnvTrue(t *testing.T) {
	for _, v := range []string{"1", "true", "yes", "TRUE", "YES", "True", "Yes"} {
		os.Setenv("LAZYMIND_RESOURCE_UPDATE_ENABLED", v)
		if !EnabledFromEnv() {
			t.Fatalf("%q should be enabled", v)
		}
	}
}

// TestEnabledFromEnvFalse for unset or non-true values.
func TestEnabledFromEnvFalse(t *testing.T) {
	for _, v := range []string{"0", "false", "no", "", "enabled", " "} {
		if v == "" {
			os.Unsetenv("LAZYMIND_RESOURCE_UPDATE_ENABLED")
		} else {
			os.Setenv("LAZYMIND_RESOURCE_UPDATE_ENABLED", v)
		}
		if EnabledFromEnv() {
			t.Fatalf("%q should be disabled", v)
		}
	}
}

// TestDefaultWorkerID uses prefix and hostname.
func TestDefaultWorkerID(t *testing.T) {
	id := defaultWorkerID("testprefix")
	if !strings.HasPrefix(id, "testprefix-") {
		t.Fatalf("got %q, want prefix testprefix-", id)
	}
	// Different prefix produces different IDs
	id2 := defaultWorkerID("other")
	if !strings.HasPrefix(id2, "other-") {
		t.Fatalf("got %q, want prefix other-", id2)
	}
}
