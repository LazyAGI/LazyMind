package assistantbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/codexhost"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/integrationstate"
	"lazymind/agentconnector/internal/workflowhost"
)

func TestCodexRecoveryRequiresCurrentPairingAndCoalescesUntilDismissed(t *testing.T) {
	server := newTestServer(t, t.TempDir())
	if err := server.store.Save(credentials.Credentials{ServerURL: "http://127.0.0.1:8090", Username: "alice", AccessToken: "access", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	scope, err := server.store.AccountScope()
	if err != nil {
		t.Fatal(err)
	}
	pair, err := workflowhost.EnsureForProvider(server.store.Directory(), "codex", filepath.Join(server.store.Directory(), "codex"), scope)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-LazyMind-Connector-Id", pair.ConnectorID)
		w := httptest.NewRecorder()
		server.routes().ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/v1/codex/recovery", "wrong", `{"code":"CODEX_CONNECTION_REQUIRED"}`); w.Code != 403 {
		t.Fatal(w.Code)
	}
	create := func() codexhost.RecoveryResponse {
		w := call("POST", "/v1/codex/recovery", pair.Token, `{"code":"CODEX_CONNECTION_REQUIRED","can_restart":false}`)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var result codexhost.RecoveryResponse
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := create()
	second := create()
	if first.Request == nil || !first.Request.CanRestart || first.Request.ID != second.Request.ID {
		t.Fatal("not coalesced")
	}
	if w := call("POST", "/v1/codex/recovery", pair.Token, `{"code":"run-arbitrary-command"}`); w.Code != 422 {
		t.Fatal("unsupported action accepted")
	}
	if w := call("DELETE", "/v1/codex/recovery/wrong-id", pair.Token, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if current, _ := server.codexRecovery.current(pair.ConnectorID); current == nil {
		t.Fatal("stale dismissal removed request")
	}
	call("DELETE", "/v1/codex/recovery/"+first.Request.ID, pair.Token, "")
	if current, _ := server.codexRecovery.current(pair.ConnectorID); current != nil {
		t.Fatal("dismissal ignored")
	}
	third := create()
	if third.Request.ID == first.Request.ID {
		t.Fatal("new operation did not get a new request")
	}
	if err := integrationstate.Set(server.store.Directory(), "codex", false); err != nil {
		t.Fatal(err)
	}
	if w := call("GET", "/v1/codex/recovery?wait=0", pair.Token, ""); w.Code != 403 {
		t.Fatal("disabled pairing still served")
	}
}

func TestCodexRecoveryPersistsOperationsAndCompletesOnlyAfterEveryHandoff(t *testing.T) {
	home := t.TempDir()
	var broker codexRecoveryBroker
	if err := broker.bindStorage(home, "alice:pair-token"); err != nil {
		t.Fatal(err)
	}
	thread := "11111111-2222-3333-4444-555555555555"
	intent := codexhost.RecoveryIntent{ID: strings.Repeat("a", 32), ThreadID: thread, Tool: "workflow.start", Fingerprint: strings.Repeat("b", 64), Arguments: json.RawMessage(`{"workflow_id":"ppt","driver_session_id":"11111111-2222-3333-4444-555555555555","idempotency_key":"stable-request-123456"}`)}
	input := codexhost.RecoverySubmission{ThreadID: thread, Intent: &intent}
	first, err := broker.request("connector", codexhost.ConnectionIssue{Code: "CODEX_CONNECTION_REQUIRED", CanRestart: true}, input)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := intent
	duplicate.ID = strings.Repeat("c", 32)
	if _, err := broker.request("connector", first.ConnectionIssue, codexhost.RecoverySubmission{ThreadID: thread, Intent: &duplicate}); err != nil {
		t.Fatal(err)
	}
	var restarted codexRecoveryBroker
	if err := restarted.bindStorage(home, "alice:pair-token"); err != nil {
		t.Fatal(err)
	}
	current, _ := restarted.current("connector")
	if current == nil || current.ID != first.ID || len(current.Intents) != 1 || string(current.Intents[0].Arguments) != string(intent.Arguments) {
		t.Fatal("blocked request did not survive MCP/Bridge restart")
	}
	if err := restarted.complete("connector", first.ID); err == nil {
		t.Fatal("discarded an undelivered operation")
	}
	if err := restarted.accepted("connector", first.ID, intent.ID); err != nil {
		t.Fatal(err)
	}
	// A request arriving during recovery is retained, even after the first ack.
	later := intent
	later.ID = strings.Repeat("d", 32)
	later.Fingerprint = strings.Repeat("e", 64)
	later.ThreadID = "22222222-2222-3333-4444-555555555555"
	if _, err := restarted.request("connector", first.ConnectionIssue, codexhost.RecoverySubmission{ThreadID: later.ThreadID, Intent: &later}); err != nil {
		t.Fatal(err)
	}
	if err := restarted.complete("connector", first.ID); err == nil {
		t.Fatal("lost concurrently queued operation")
	}
	if err := restarted.accepted("connector", first.ID, later.ID); err != nil {
		t.Fatal(err)
	}
	if err := restarted.complete("connector", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(restarted.storage); !os.IsNotExist(err) {
		t.Fatal("finished request retained")
	}
	if err := broker.bindStorage(home, "bob:other-token"); err != nil {
		t.Fatal(err)
	}
	if current, _ := broker.current("connector"); current != nil {
		t.Fatal("another account inherited saved request")
	}
}

func TestCodexRecoveryRefreshesIssueWithoutLosingSavedOperations(t *testing.T) {
	issues := []codexhost.ConnectionIssue{
		{Code: "CODEX_CONTROL_UNAVAILABLE", Message: "still loading", CanRestart: false},
		{Code: "CODEX_CONNECTION_REQUIRED", Message: "restart to connect", CanRestart: true},
	}
	for _, reverse := range []bool{false, true} {
		firstIssue, latestIssue := issues[0], issues[1]
		if reverse {
			firstIssue, latestIssue = latestIssue, firstIssue
		}
		t.Run(latestIssue.Code, func(t *testing.T) {
			home := t.TempDir()
			var broker codexRecoveryBroker
			if err := broker.bindStorage(home, "connector:token"); err != nil {
				t.Fatal(err)
			}
			intent := codexhost.RecoveryIntent{ID: strings.Repeat("a", 32), Fingerprint: strings.Repeat("b", 64), ThreadID: "11111111-2222-3333-4444-555555555555"}
			first, err := broker.request("connector", firstIssue, codexhost.RecoverySubmission{ThreadID: intent.ThreadID, Intent: &intent})
			if err != nil {
				t.Fatal(err)
			}
			_, changed := broker.current("connector")
			latest, err := broker.request("connector", latestIssue)
			if err != nil {
				t.Fatal(err)
			}
			if latest.ID != first.ID || latest.ThreadID != first.ThreadID || len(latest.Intents) != 1 || latest.Intents[0].ID != intent.ID {
				t.Fatalf("lost saved request: %+v", latest)
			}
			select {
			case <-changed:
			default:
				t.Fatal("listeners were not notified")
			}
			var restored codexRecoveryBroker
			if err := restored.bindStorage(home, "connector:token"); err != nil {
				t.Fatal(err)
			}
			saved, _ := restored.current("connector")
			if latest.ConnectionIssue != latestIssue || saved == nil || saved.ConnectionIssue != latestIssue {
				t.Fatalf("latest diagnosis was not returned and persisted: latest=%+v saved=%+v", latest, saved)
			}
		})
	}
}

func TestViewCompletionCannotConsumeAnExecutionQueuedDuringItsProbe(t *testing.T) {
	var broker codexRecoveryBroker
	view, err := broker.request("connector", codexhost.ConnectionIssue{Code: "CODEX_CONNECTION_REQUIRED"}, codexhost.RecoverySubmission{ViewOnly: true, ThreadID: "11111111-2222-3333-4444-555555555555"})
	if err != nil {
		t.Fatal(err)
	}
	intent := codexhost.RecoveryIntent{ID: strings.Repeat("a", 32), Fingerprint: strings.Repeat("b", 64), ThreadID: view.ThreadID}
	if _, err := broker.request("connector", view.ConnectionIssue, codexhost.RecoverySubmission{Intent: &intent}); err != nil {
		t.Fatal(err)
	}
	if err := broker.complete("connector", view.ID, true); err == nil {
		t.Fatal("view watcher consumed a queued execution")
	}
	current, _ := broker.current("connector")
	if current == nil || len(current.Intents) != 1 {
		t.Fatal("execution lost")
	}
}

func TestRecoveryLongPollWakesAndKeepsAccountsSeparate(t *testing.T) {
	var broker codexRecoveryBroker
	_, changed := broker.current("alice")
	woken := make(chan struct{})
	go func() { <-changed; close(woken) }()
	first, err := broker.request("alice", codexhost.ConnectionIssue{Code: "CODEX_CONNECTION_REQUIRED"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-woken:
	case <-time.After(time.Second):
		t.Fatal("listener was not woken")
	}
	if current, _ := broker.current("bob"); current != nil {
		t.Fatal("cross-account event")
	}
	broker.dismiss("bob", first.ID)
	if current, _ := broker.current("alice"); current == nil {
		t.Fatal("another account dismissed event")
	}
	second, err := broker.request("bob", codexhost.ConnectionIssue{Code: "CODEX_CONNECTION_REQUIRED"})
	if err != nil {
		t.Fatal(err)
	}
	broker.dismiss("alice", first.ID)
	if current, _ := broker.current("bob"); current == nil || current.ID != second.ID {
		t.Fatal("stale account dismissed new request")
	}
}

func TestCodexRecoveryLongPollHonorsCancellation(t *testing.T) {
	server := newTestServer(t, t.TempDir())
	if err := server.store.Save(credentials.Credentials{ServerURL: "http://127.0.0.1:8090", Username: "alice", AccessToken: "access", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	scope, _ := server.store.AccountScope()
	pair, err := workflowhost.EnsureForProvider(server.store.Directory(), "codex", filepath.Join(server.store.Directory(), "codex"), scope)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodGet, "/v1/codex/recovery", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+pair.Token)
	r.Header.Set("X-LazyMind-Connector-Id", pair.ConnectorID)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { server.routes().ServeHTTP(w, r); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled poll remained blocked")
	}
}
