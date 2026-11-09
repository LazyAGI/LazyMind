package workflowhost

import (
	"testing"
)

func TestPairingDisableRevokesUntilExplicitlyRepaired(t *testing.T) {
	for _, provider := range []string{"codex", "workbuddy"} {
		t.Run(provider, func(t *testing.T) {
			home := t.TempDir()
			pair, err := EnsureForProvider(home, provider, t.TempDir(), "account")
			if err != nil {
				t.Fatal(err)
			}
			if err = Disable(home, pair.ConnectorID); err != nil {
				t.Fatal(err)
			}
			if _, err = Verify(home, pair.ConnectorID, pair.Token, "account"); err == nil {
				t.Fatal("disabled pairing still authorizes control")
			}
			if Configured(home, pair.ConnectorID, "account") {
				t.Fatal("disabled integration reported configured")
			}
			if _, err = EnsureForProvider(home, provider, pair.Profile, "account"); err != nil {
				t.Fatal(err)
			}
			if _, err = Verify(home, pair.ConnectorID, pair.Token, "other-account"); err == nil {
				t.Fatal("pairing bypassed account verification")
			}
		})
	}
}
