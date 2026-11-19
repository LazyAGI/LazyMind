package workbuddyhost

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lazymind/agentconnector/internal/workflowhost"
)

func TestReconnectKeepsCustomControlPortFromDisabledPairing(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, "workbuddy")
	pair, err := workflowhost.EnsureForProvider(home, "workbuddy", profile, "fixture-account")
	if err != nil {
		t.Fatal(err)
	}
	config := Config{PairingFile: filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json"), Node: filepath.Join(home, "node"), CDPPort: 19333}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(ConfigPath(home), body); err != nil {
		t.Fatal(err)
	}
	if err := workflowhost.Disable(home, pair.ConnectorID); err != nil {
		t.Fatal(err)
	}
	_, _, err = Load(home, "fixture-account")
	var pairingErr *workflowhost.PairingError
	if !errors.As(err, &pairingErr) || pairingErr.Code != "HOST_PAIRING_DISABLED" {
		t.Fatalf("expected disabled pairing, got %v", err)
	}
	if port := configuredControlPort(home); port != config.CDPPort {
		t.Fatalf("disabled pairing reset port to %d", port)
	}
	after, err := os.ReadFile(ConfigPath(home))
	if err != nil || string(after) != string(body) {
		t.Fatalf("reading endpoint mutated configuration: %v", err)
	}
	if _, err := workflowhost.EnsureForProvider(home, "workbuddy", profile, "fixture-account"); err != nil {
		t.Fatal(err)
	}
	if port := configuredControlPort(home); port != config.CDPPort {
		t.Fatalf("reconnected pairing reset port to %d", port)
	}
}

func TestConfiguredControlPortFallsBackOnlyForMissingOrInvalidPort(t *testing.T) {
	for _, body := range []string{"", "invalid", `{}`, `{"cdp_port":0}`, `{"cdp_port":65536}`, `{"cdp_port":-1}`} {
		home := t.TempDir()
		if body != "" {
			if err := writeAtomic(ConfigPath(home), []byte(body)); err != nil {
				t.Fatal(err)
			}
		}
		if got := configuredControlPort(home); got != 9333 {
			t.Fatalf("body=%s got=%d", body, got)
		}
	}
}
