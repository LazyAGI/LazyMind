package currentmemory

import "testing"

func TestPreferenceIndexMaxItemsFromEnv(t *testing.T) {
	t.Run("configured", func(t *testing.T) {
		t.Setenv(PreferenceIndexMaxItemsEnv, "23")
		got, err := PreferenceIndexMaxItemsFromEnv()
		if err != nil || got != 23 {
			t.Fatalf("PreferenceIndexMaxItemsFromEnv() = %d, %v", got, err)
		}
	})

	for _, value := range []string{"", "0", "-1", "invalid"} {
		t.Run("reject "+value, func(t *testing.T) {
			t.Setenv(PreferenceIndexMaxItemsEnv, value)
			if _, err := PreferenceIndexMaxItemsFromEnv(); err == nil {
				t.Fatalf("expected %q to be rejected", value)
			}
		})
	}
}

func TestValidatePreferenceCapacityAllowsNonGrowingOverLimitContent(t *testing.T) {
	if err := ValidatePreferenceCapacity(100, 101, 100); err == nil {
		t.Fatal("growing beyond capacity should fail")
	}
	if err := ValidatePreferenceCapacity(120, 120, 100); err != nil {
		t.Fatalf("equal over-limit content should be allowed: %v", err)
	}
	if err := ValidatePreferenceCapacity(120, 110, 100); err != nil {
		t.Fatalf("shrinking over-limit content should be allowed: %v", err)
	}
}
