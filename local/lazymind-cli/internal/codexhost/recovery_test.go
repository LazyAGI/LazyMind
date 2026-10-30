package codexhost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/integrationstate"
	"lazymind/agentconnector/internal/workflowhost"
)

func recoveryFixture(t *testing.T) (string, *credentials.Store, workflowhost.Pairing) {
	t.Helper()
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	home := connectionFixture(t)
	store, err := credentials.NewStore(home, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credentials.Credentials{ServerURL: "http://127.0.0.1:8090", Username: "alice", AccessToken: "access", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	scope, err := store.AccountScope()
	if err != nil {
		t.Fatal(err)
	}
	pair, err := workflowhost.EnsureForProvider(home, "codex", filepath.Join(home, "profile"), scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := Configure(home, filepath.Join(home, "node"), filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json")); err != nil {
		t.Fatal(err)
	}
	return home, store, pair
}

func TestExecutionRequestsRecoveryButTransientAndDisabledConnectionsDoNot(t *testing.T) {
	for _, scenario := range []string{"offline", "transient", "disabled", "ready"} {
		t.Run(scenario, func(t *testing.T) {
			home, _, pair := recoveryFixture(t)
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != "POST" || r.URL.Path != "/v1/codex/recovery" || r.Header.Get("Authorization") != "Bearer "+pair.Token || r.Header.Get("X-LazyMind-Connector-Id") != pair.ConnectorID {
					t.Error("unpaired recovery request")
				}
				var issue ConnectionIssue
				if json.NewDecoder(r.Body).Decode(&issue) != nil || !issue.CanRestart {
					t.Error("missing restart issue")
				}
				_, _ = w.Write([]byte(`{"request":null}`))
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_ASSISTANT_BRIDGE_URL", server.URL)
			checks := 0
			startupDesktopReady = func(context.Context, int) (bool, error) {
				checks++
				return scenario == "ready" || scenario == "transient" && checks > 1, nil
			}
			if scenario == "disabled" {
				if err := integrationstate.Set(home, "codex", false); err != nil {
					t.Fatal(err)
				}
			}
			err := EnsureConnection(context.Background(), home)
			if scenario == "offline" && (err == nil || requests != 1) {
				t.Fatalf("offline: err=%v requests=%d", err, requests)
			}
			if (scenario == "transient" || scenario == "ready") && (err != nil || requests != 0) {
				t.Fatalf("healthy: err=%v requests=%d", err, requests)
			}
			if scenario == "disabled" && (err == nil || requests != 0) {
				t.Fatal("disabled integration requested recovery")
			}
		})
	}
}

func TestReconnectReusesInstallationAndStopsOnShutdownFailure(t *testing.T) {
	for _, scenario := range []string{"ready", "restart", "refused", "foreign-port", "disabled"} {
		t.Run(scenario, func(t *testing.T) {
			home, _, _ := recoveryFixture(t)
			before, _ := os.ReadFile(configPath(home))
			launch := prepareDesktopLaunch
			t.Cleanup(func() { prepareDesktopLaunch = launch })
			ready := scenario == "ready"
			startupDesktopReady = func(context.Context, int) (bool, error) {
				if scenario == "foreign-port" {
					return false, errors.New("foreign service")
				}
				return ready, nil
			}
			launched := 0
			prepareDesktopLaunch = func(ctx context.Context, app string, port int) error {
				launched++
				if scenario == "refused" {
					return errors.New("Codex refused to close")
				}
				reportRecovery(ctx, "starting")
				ready = true
				return nil
			}
			if scenario == "disabled" {
				if err := integrationstate.Set(home, "codex", false); err != nil {
					t.Fatal(err)
				}
			}
			var stages []string
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := Reconnect(ctx, home, func(stage string) { stages = append(stages, stage) })
			if scenario == "ready" || scenario == "restart" {
				if err != nil {
					t.Fatal(err)
				}
				want := []string{"connecting", "connected"}
				if scenario == "restart" {
					want = []string{"closing", "starting", "connecting", "connected"}
				}
				if !reflect.DeepEqual(stages, want) {
					t.Fatalf("stages=%v", stages)
				}
			} else if err == nil {
				t.Fatal("failed recovery reported success")
			}
			if (scenario == "ready" || scenario == "foreign-port" || scenario == "disabled") && launched != 0 {
				t.Fatal("unnecessary restart")
			}
			after, _ := os.ReadFile(configPath(home))
			if string(before) != string(after) {
				t.Fatal("recovery rewrote installation")
			}
		})
	}
}

func TestReconnectIsRevokedWhenAccountChanges(t *testing.T) {
	home, store, _ := recoveryFixture(t)
	launch := prepareDesktopLaunch
	t.Cleanup(func() { prepareDesktopLaunch = launch })
	startupDesktopReady = func(context.Context, int) (bool, error) { return false, nil }
	prepareDesktopLaunch = func(ctx context.Context, _ string, _ int) error {
		if err := store.Save(credentials.Credentials{ServerURL: "http://127.0.0.1:8090", Username: "bob", AccessToken: "bob", RefreshToken: "bob-refresh"}); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := Reconnect(ctx, home, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("recovery survived account change: %v", err)
	}
}

func TestOfflineExecutionSavesItsExactRecoveryIntentBeforeReturning(t *testing.T) {
	home, _, _ := recoveryFixture(t)
	startupDesktopReady = func(context.Context, int) (bool, error) { return false, nil }
	intent := RecoveryIntent{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ThreadID: "11111111-2222-3333-4444-555555555555", Tool: "workflow.start", Arguments: json.RawMessage(`{"workflow_id":"ppt","idempotency_key":"same-start-key-123456"}`)}
	var received RecoverySubmission
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.Write([]byte(`{"request":{"id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_ASSISTANT_BRIDGE_URL", server.URL)
	ctx := WithRecoveryIntent(context.Background(), func() (*RecoveryIntent, error) { return &intent, nil })
	if err := EnsureConnection(ctx, home); err == nil {
		t.Fatal("disconnected operation was admitted")
	}
	if received.Intent == nil || received.ThreadID != intent.ThreadID || !reflect.DeepEqual(*received.Intent, intent) {
		t.Fatal("original operation was not handed to LazyMind")
	}
}

func TestCompletedResultPresentationRequestsNoExecutionReplay(t *testing.T) {
	for _, scenario := range []string{"completed", "unbound", "denied", "connected"} {
		t.Run(scenario, func(t *testing.T) {
			home, _, _ := recoveryFixture(t)
			startupDesktopReady = func(context.Context, int) (bool, error) { return scenario == "connected", nil }
			posts, gets := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					gets++
					if scenario == "denied" {
						w.WriteHeader(403)
						return
					}
					if scenario == "unbound" {
						w.Write([]byte(`{"run_id":""}`))
						return
					}
					w.Write([]byte(`{"run_id":"completed-run"}`))
					return
				}
				posts++
				var saved RecoverySubmission
				json.NewDecoder(r.Body).Decode(&saved)
				if !saved.ViewOnly || saved.Intent != nil || saved.ThreadID == "" {
					t.Error("result viewing queued an execution")
				}
				w.Write([]byte(`{"request":null}`))
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_ASSISTANT_BRIDGE_URL", server.URL)
			requested, err := EnsurePresentation(context.Background(), home, "11111111-2222-3333-4444-555555555555")
			if scenario == "completed" {
				if !requested || err != nil || posts != 1 {
					t.Fatal("completed results lost their Panel recovery")
				}
			} else if requested || posts != 0 {
				t.Fatal("unexpected recovery")
			}
			if scenario == "connected" && gets != 0 {
				t.Fatal("healthy view made extra binding requests")
			}
		})
	}
}
