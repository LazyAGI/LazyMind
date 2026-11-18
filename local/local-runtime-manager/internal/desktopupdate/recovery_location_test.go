package desktopupdate

import (
	"path/filepath"
	"testing"
)

func TestUpdateRecoveryBaseUsesOSAccountDespiteServiceEnvironment(t *testing.T) {
	want := fixtureOSRecoveryBase(t)
	before, err := RecoveryBase()
	if err != nil || before != want {
		t.Fatalf("OS recovery base: %q %v", before, err)
	}
	// These are simulated service environments, not a new product setting.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LAZYMIND_HOST_HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	after, err := RecoveryBase()
	if err != nil || after != want || !filepath.IsAbs(after) {
		t.Fatalf("service environment moved recovery/trust root: %q %v", after, err)
	}
}
