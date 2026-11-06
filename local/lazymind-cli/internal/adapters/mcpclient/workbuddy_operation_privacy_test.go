package mcpclient

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/agentintegration"
)

func TestWorkBuddyOperationLogOnlyRecordsSafeCorrelationFields(t *testing.T) {
	home := t.TempDir()
	op := newWorkBuddyOperation(home)
	op.enter("login")
	status := agentintegration.Status{
		State:   agentintegration.Failed,
		Message: "fixture-secret-token fixture-private-account fixture-private-path",
		Action:  &agentintegration.Action{Kind: "open_url", URL: "https://fixture.invalid/?token=fixture-secret-token"},
		Login:   &agentintegration.LoginStatus{State: "failed", Message: "fixture-secret-token"},
		Issue:   &agentintegration.Issue{Code: "workbuddy_auth_unreadable"},
	}
	op.finish(&status)
	body, err := os.ReadFile(filepath.Join(home, "logs", "agent-setup.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "fixture-") {
		t.Fatalf("log included private status fields: %s", body)
	}
	allowed := map[string]bool{"time": true, "agent": true, "operation_id": true, "stage": true, "state": true, "code": true, "elapsed_ms": true}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	rows := 0
	for scanner.Scan() {
		var row map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		for key := range row {
			if !allowed[key] {
				t.Fatalf("unapproved log field %q", key)
			}
		}
		if len(row) != len(allowed) || row["operation_id"] != status.Issue.OperationID || row["stage"] != "login" {
			t.Fatalf("incomplete diagnostic: %+v", row)
		}
		rows++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("expected running and terminal records; got %d", rows)
	}
}
