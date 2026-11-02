package codexhost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDesktopLaunchPreservesCDPAndOriginalConversation(t *testing.T) {
	thread := "11111111-2222-3333-4444-555555555555"
	ctx := context.WithValue(context.Background(), recoveryThreadKey{}, thread)
	want := []string{"--remote-debugging-address=127.0.0.1", "--remote-debugging-port=9334", "codex://threads/" + thread}
	if got := desktopLaunchArguments(ctx, 9334); !reflect.DeepEqual(got, want) {
		t.Fatalf("lost launch arguments: %v", got)
	}
	for _, bad := range []string{"", "other-thread", thread + "?prompt=run", thread + " --remote-debugging-port=1"} {
		if _, err := threadLink(bad); err == nil {
			t.Fatal("accepted arbitrary navigation argument")
		}
	}
}

func TestRecoveryReturnsToOriginalChatAndAcknowledgesOnlyAcceptedHandoff(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "delivery-failed"}[failure], func(t *testing.T) {
			home, _, pair := recoveryFixture(t)
			request := RecoveryRequest{ID: strings.Repeat("a", 32), ThreadID: "11111111-2222-3333-4444-555555555555", ConnectionIssue: ConnectionIssue{CanRestart: true}, Intents: []RecoveryIntent{{ID: strings.Repeat("b", 32), ThreadID: "11111111-2222-3333-4444-555555555555", Tool: "workflow.start", Arguments: json.RawMessage(`{"idempotency_key":"same-stable-key-123456","workflow_id":"ppt"}`)}}}
			acks, completed := 0, false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+pair.Token {
					t.Error("unpaired recovery")
				}
				switch {
				case r.Method == "GET":
					json.NewEncoder(w).Encode(RecoveryResponse{Request: &request})
				case strings.Contains(r.URL.Path, "/accepted/"):
					acks++
					request.Intents[0].Accepted = true
					w.Write([]byte(`{"ok":true}`))
				case strings.HasSuffix(r.URL.Path, "/complete"):
					if acks != 1 {
						t.Error("completed without acceptance")
					}
					completed = true
					w.Write([]byte(`{"ok":true}`))
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_ASSISTANT_BRIDGE_URL", server.URL)
			launch, open, deliver := prepareDesktopLaunch, recoveryOpenThread, recoveryDeliver
			t.Cleanup(func() { prepareDesktopLaunch, recoveryOpenThread, recoveryDeliver = launch, open, deliver })
			ready := false
			startupDesktopReady = func(context.Context, int) (bool, error) { return ready, nil }
			prepareDesktopLaunch = func(ctx context.Context, _ string, port int) error {
				if args := desktopLaunchArguments(ctx, port); args[len(args)-1] != "codex://threads/"+request.ThreadID {
					t.Error("restart lost original chat")
				}
				ready = true
				return nil
			}
			opened := false
			recoveryOpenThread = func(_ context.Context, _ string, thread string) error {
				if thread != request.ThreadID {
					t.Error("wrong thread")
				}
				opened = true
				return nil
			}
			recoveryDeliver = func(_ context.Context, _ string, id string, intent RecoveryIntent) error {
				if !opened || id != request.ID || string(intent.Arguments) != string(request.Intents[0].Arguments) {
					t.Error("lost handoff identity")
				}
				if failure {
					return errors.New("delivery failed")
				}
				return nil
			}
			var stages []string
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := ReconnectRequest(ctx, home, request.ID, func(stage string) { stages = append(stages, stage) })
			if failure {
				if err == nil || acks != 0 || completed {
					t.Fatal("failed handoff reported success")
				}
			} else if err != nil || acks != 1 || !completed || stages[len(stages)-1] != "connected" {
				t.Fatalf("incomplete recovery: %v %v", err, stages)
			}
		})
	}
}

func TestRecoveryRetriesTransientVerificationAfterStartup(t *testing.T) {
	home, _, _ := recoveryFixture(t)
	request := RecoveryRequest{ID: strings.Repeat("c", 32), ThreadID: "11111111-2222-3333-4444-555555555555", ViewOnly: true, ConnectionIssue: ConnectionIssue{CanRestart: true}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(RecoveryResponse{Request: &request})
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_ASSISTANT_BRIDGE_URL", server.URL)
	open, deliver, matches := recoveryOpenThread, recoveryDeliver, startupListenerMatches
	t.Cleanup(func() { recoveryOpenThread, recoveryDeliver, startupListenerMatches = open, deliver, matches })
	calls, opened, delivered := 0, 0, 0
	startupListenerMatches = func(context.Context, string, int) bool {
		calls++
		// PrepareDesktop verifies ownership three times. The following read-only
		// check during restoration must tolerate a transient failed observation.
		return calls != 4
	}
	recoveryOpenThread = func(context.Context, string, string) error { opened++; return nil }
	recoveryDeliver = func(context.Context, string, string, RecoveryIntent) error { delivered++; return nil }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := ReconnectRequest(ctx, home, request.ID, nil); err != nil {
		t.Fatal(err)
	}
	if calls < 6 || opened != 1 || delivered != 1 {
		t.Fatalf("checks=%d opens=%d deliveries=%d", calls, opened, delivered)
	}
}
