package currentmemory

import "testing"

func TestPreferenceContextMaxCharsFromEnv(t *testing.T) {
	t.Setenv(PreferenceContextMaxCharsEnv, "4096")
	value, err := PreferenceContextMaxCharsFromEnv()
	if err != nil || value != 4096 {
		t.Fatalf("value=%d err=%v", value, err)
	}
	for _, invalid := range []string{"", "0", "-1", "invalid"} {
		t.Run(invalid, func(t *testing.T) {
			t.Setenv(PreferenceContextMaxCharsEnv, invalid)
			if _, err := PreferenceContextMaxCharsFromEnv(); err == nil {
				t.Fatalf("expected error for %q", invalid)
			}
		})
	}
}
