package vocabulary

import "testing"

func TestEnabled(t *testing.T) {
	t.Setenv("LAZYMIND_VOCABULARY_ENABLED", "")
	if Enabled() {
		t.Fatal("vocabulary must be disabled by default")
	}

	for _, value := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("LAZYMIND_VOCABULARY_ENABLED", value)
			if !Enabled() {
				t.Fatalf("expected %q to enable vocabulary", value)
			}
		})
	}
}

func TestRequireLocalRuntimeUsesFeatureFlag(t *testing.T) {
	t.Setenv("LAZYMIND_VOCABULARY_ENABLED", "false")
	if err := requireLocalRuntime(); err == nil {
		t.Fatal("expected disabled vocabulary to be rejected")
	}
	t.Setenv("LAZYMIND_VOCABULARY_ENABLED", "true")
	if err := requireLocalRuntime(); err != nil {
		t.Fatalf("expected enabled vocabulary to be accepted: %v", err)
	}
}
