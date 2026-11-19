package workbuddyhost

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lazymind/agentconnector/internal/workflowhost"
)

func TestLoadPreservesPairingRepairClassification(t *testing.T) {
	for _, tc := range []struct{ name, code string }{
		{"missing", "HOST_PAIRING_MISSING"},
		{"invalid", "HOST_PAIRING_INVALID"},
		{"unreadable", "HOST_PAIRING_UNREADABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			pairing := filepath.Join(home, "pairing.json")
			if tc.name == "invalid" {
				if err := os.WriteFile(pairing, []byte(`{"token":"sensitive-fixture",`), 0600); err != nil {
					t.Fatal(err)
				}
			} else if tc.name == "unreadable" {
				if err := os.Mkdir(pairing, 0700); err != nil {
					t.Fatal(err)
				}
			}
			body, _ := json.Marshal(Config{Node: filepath.Join(home, "node"), PairingFile: pairing, CDPPort: 9333})
			if err := writeAtomic(ConfigPath(home), body); err != nil {
				t.Fatal(err)
			}
			_, _, err := Load(home, "account")
			var failure *workflowhost.PairingError
			if !errors.As(err, &failure) || failure.Code != tc.code {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
		})
	}
}
