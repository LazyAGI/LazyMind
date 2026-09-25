package workflowhost

import (
	"encoding/json"
	"os"
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

func TestProviderIdentityAndLegacyPairingCompatibility(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, "shared-profile")
	legacy, err := Ensure(home, profile, "account")
	if err != nil {
		t.Fatal(err)
	}
	path, _ := pairingPath(home, legacy.ConnectorID)
	// Simulate an actual old file, without the new provider field.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "provider")
	body, _ = json.Marshal(fields)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	verified, err := Verify(home, legacy.ConnectorID, legacy.Token, "account")
	if err != nil || verified.Provider != DSHProvider {
		t.Fatalf("legacy verification: %v", err)
	}
	// Read-only verification must not rewrite the legacy file.
	after, _ := os.ReadFile(path)
	if string(after) != string(body) {
		t.Fatal("verification rewrote pairing")
	}
	upgraded, err := EnsureForProvider(home, DSHProvider, profile, "account")
	if err != nil || upgraded.Token != legacy.Token || upgraded.ConnectorID != legacy.ConnectorID {
		t.Fatalf("legacy authority changed: %v", err)
	}
	seen := map[string]bool{legacy.ConnectorID: true}
	for _, provider := range []string{"fake-agent", "another-agent"} {
		pair, err := EnsureForProvider(home, provider, profile, "account")
		if err != nil {
			t.Fatal(err)
		}
		if seen[pair.ConnectorID] {
			t.Fatal("providers share connector identity")
		}
		seen[pair.ConnectorID] = true
		verified, err := Verify(home, pair.ConnectorID, pair.Token, "account")
		if err != nil || verified.Provider != provider {
			t.Fatalf("provider verification: %v", err)
		}
		if !Configured(home, pair.ConnectorID, "account") {
			t.Fatal("new provider is not configured")
		}
		if _, err := Verify(home, pair.ConnectorID, legacy.Token, "account"); err == nil {
			t.Fatal("accepted another provider credential")
		}
		if err := Disable(home, pair.ConnectorID); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(home, pair.ConnectorID, pair.Token, "account"); err == nil {
			t.Fatal("disabled provider verified")
		}
		again, err := EnsureForProvider(home, provider, profile, "account")
		if err != nil || again.Token != pair.Token {
			t.Fatalf("provider reconnect changed credential: %v", err)
		}
	}
}

func TestNewProviderRequiresExplicitTrustedIdentity(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, "profile")
	for _, provider := range []string{"", "../dsh", "Host With Spaces"} {
		if _, err := EnsureForProvider(home, provider, profile, "account"); err == nil {
			t.Fatalf("accepted invalid provider %q", provider)
		}
	}
	pair, err := EnsureForProvider(home, "fake-agent", profile, "account")
	if err != nil {
		t.Fatal(err)
	}
	pair.Provider = ""
	path, _ := pairingPath(home, pair.ConnectorID)
	if err := save(path, pair); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(home, pair.ConnectorID, pair.Token, "account"); err == nil {
		t.Fatal("new host inferred a missing provider")
	}
	if Configured(home, pair.ConnectorID, "account") {
		t.Fatal("invalid provider considered configured")
	}
	if _, err := EnsureForProvider(home, "fake-agent", profile, "account"); err == nil {
		t.Fatal("reconnect silently repaired missing provider")
	}
}
