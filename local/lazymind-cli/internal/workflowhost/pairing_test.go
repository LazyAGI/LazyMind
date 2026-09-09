package workflowhost

import (
	"path/filepath"
	"testing"
)

func TestPairingSeparatesProfilesAccountsAndDisconnect(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, "dsh", "web")
	pair, err := Ensure(home, profile, "account-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(home, pair.ConnectorID, pair.Token, "account-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(home, pair.ConnectorID, pair.Token, "account-b"); err == nil {
		t.Fatal("account switch retained host authority")
	}
	if _, err := Verify(home, pair.ConnectorID, "wrong", "account-a"); err == nil {
		t.Fatal("invalid credential accepted")
	}
	other, err := Ensure(home, filepath.Join(home, "dsh", "other"), "account-a")
	if err != nil || other.ConnectorID == pair.ConnectorID {
		t.Fatal("profiles shared a controller identity")
	}
	if err := Disable(home, pair.ConnectorID); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(home, pair.ConnectorID, pair.Token, "account-a"); err == nil {
		t.Fatal("disconnected host retained authority")
	}
	reconnected, err := Ensure(home, profile, "account-a")
	if err != nil || reconnected.Token != pair.Token {
		t.Fatal("reconnect lost access to existing bound workflows")
	}
}
