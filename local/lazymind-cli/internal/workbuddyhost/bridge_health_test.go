package workbuddyhost

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"lazymind/agentconnector/internal/workflowhost"
)

func TestWaitReadyNeverAcceptsBridgeFailureWithHealthyDesktop(t *testing.T) {
	for _, code := range []string{"lazymind_auth_required", "pairing_required", "pairing_disabled", "pairing_account_mismatch", "pairing_credential_mismatch", "pairing_invalid", "pairing_unreadable", "lazymind_unavailable"} {
		t.Run(code, func(t *testing.T) {
			home := t.TempDir()
			pair, err := workflowhost.EnsureForProvider(home, "workbuddy", filepath.Join(home, "profile"), "account")
			if err != nil {
				t.Fatal(err)
			}
			config := Config{PairingFile: filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json"), Node: filepath.Join(home, "node"), CDPPort: 9333}
			body, _ := json.Marshal(config)
			if err = writeAtomic(ConfigPath(home), body); err != nil {
				t.Fatal(err)
			}
			// Even an inconsistent old writer setting ready=true must not mask issue.
			status := Status{ConnectorID: pair.ConnectorID, Version: "5.9.3", Ready: true, Cancellation: "session", Issue: code, CheckedAt: time.Now().UnixMilli(), BridgeURL: "http://127.0.0.1:19091"}
			body, _ = json.Marshal(status)
			if err = writeAtomic(config.PairingFile+".workbuddy-status.json", body); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			err = WaitReady(ctx, home, "account", time.Now().Add(-time.Second))
			var failure *CompatibilityError
			if !errors.As(err, &failure) || failure.Code != code {
				t.Fatalf("want %s, got %v", code, err)
			}
			if (ctx.Err() != nil) != (code == "lazymind_unavailable") {
				t.Fatalf("unexpected retry behavior: %v", ctx.Err())
			}
		})
	}
}
