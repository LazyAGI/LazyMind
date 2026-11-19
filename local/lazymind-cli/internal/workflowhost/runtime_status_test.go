package workflowhost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeStatusRequiresFreshMatchingPairing(t *testing.T) {
	home := t.TempDir()
	pair, err := Ensure(home, filepath.Join(home, "desktop"), "account")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRuntimeStatus(home, pair); err == nil {
		t.Fatal("configuration was accepted as a live dispatcher")
	}
	if err := RecordRuntimeStatus(home, pair, "", true); err != nil {
		t.Fatal(err)
	}
	value, err := ReadRuntimeStatus(home, pair)
	if err != nil || value.Issue != "" {
		t.Fatalf("fresh dispatcher was rejected: %+v %v", value, err)
	}
	path := filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json.runtime-status.json")
	body, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(body), pair.Token) || strings.Contains(string(body), pair.AccountScope) {
		t.Fatal("runtime status contains a credential or account")
	}
	if err := RecordRuntimeStatus(home, pair, "", true); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(path)
	if string(again) != string(body) {
		t.Fatal("healthy polling was not coalesced")
	}
	rotated := pair
	rotated.Token = strings.Repeat("f", 64)
	if _, err := ReadRuntimeStatus(home, rotated); err == nil {
		t.Fatal("old status survived credential rotation")
	}
	disabled := pair
	disabled.Enabled = false
	if _, err := ReadRuntimeStatus(home, disabled); err == nil {
		t.Fatal("disabled pairing accepted runtime evidence")
	}
	for _, age := range []time.Duration{13 * time.Second, -time.Second} {
		value.CheckedAt = time.Now().Add(-age).UnixMilli()
		body, _ := json.Marshal(value)
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadRuntimeStatus(home, pair); err == nil {
			t.Fatalf("invalid timestamp accepted: age=%v", age)
		}
	}
}

func TestRuntimeFailureAndRecoveryAreNotThrottled(t *testing.T) {
	home := t.TempDir()
	pair, err := Ensure(home, t.TempDir(), "account")
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range []string{"", "lazymind_unavailable", "lazymind_auth_required", ""} {
		if err := RecordRuntimeStatus(home, pair, issue, true); err != nil {
			t.Fatal(err)
		}
		value, err := ReadRuntimeStatus(home, pair)
		if err != nil || value.Issue != issue {
			t.Fatalf("issue=%q status=%+v err=%v", issue, value, err)
		}
	}
	if err := RecordRuntimeStatus(home, pair, "raw confidential error", true); err == nil {
		t.Fatal("unclassified error persisted")
	}
}

func TestReenableRequiresANewPollDespitePreservedCredential(t *testing.T) {
	home, profile := t.TempDir(), t.TempDir()
	before, err := Ensure(home, profile, "account")
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordRuntimeStatus(home, before, "", true); err != nil {
		t.Fatal(err)
	}
	if err := Disable(home, before.ConnectorID); err != nil {
		t.Fatal(err)
	}
	after, err := Ensure(home, profile, "account")
	if err != nil {
		t.Fatal(err)
	}
	if before.Token != after.Token || before.ConnectorID != after.ConnectorID || before.RuntimeRevision == after.RuntimeRevision {
		t.Fatal("re-enable must preserve business identity and change the runtime revision")
	}
	if _, err := ReadRuntimeStatus(home, after); err == nil {
		t.Fatal("re-enabled host reused a preceding heartbeat")
	}
	// Simulate an already-authenticated request completing after re-enablement.
	if err := RecordRuntimeStatus(home, before, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRuntimeStatus(home, after); err == nil {
		t.Fatal("late old poll made the new connection ready")
	}
	if err := RecordRuntimeStatus(home, after, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRuntimeStatus(home, after); err != nil {
		t.Fatalf("fresh poll did not restore readiness: %v", err)
	}
}
