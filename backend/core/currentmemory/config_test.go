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

func TestPreferenceContextMaxCharsFromEnv(t *testing.T) {
	t.Setenv(PreferenceContextMaxCharsEnv, "4096")
	value, err := PreferenceContextMaxCharsFromEnv()
	if err != nil || value != 4096 {
		t.Fatalf("value=%d err=%v", value, err)
	}
	for _, invalid := range []string{"", "0", "invalid"} {
		t.Run(invalid, func(t *testing.T) {
			t.Setenv(PreferenceContextMaxCharsEnv, invalid)
			if _, err := PreferenceContextMaxCharsFromEnv(); err == nil {
				t.Fatalf("expected error for %q", invalid)
			}
		})
	}
}
