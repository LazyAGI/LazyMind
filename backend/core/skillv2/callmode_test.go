package skillv2

import "testing"

func TestManualModeNeverAutomaticallyEnabled(t *testing.T) {
	for _, mode := range []string{"manual", "disabled"} {
		if got := NormalizeCallMode(mode, true); got != "manual" {
			t.Errorf("normalize %s = %s", mode, got)
		}
		if CallModeEnabled(mode) {
			t.Errorf("%s automatically enabled", mode)
		}
	}
	if !ValidCallMode("manual") {
		t.Error("manual must be valid")
	}
}
