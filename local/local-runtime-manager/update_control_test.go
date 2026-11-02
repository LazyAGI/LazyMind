package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDesktopUpdateControlUsesPersistedOwnershipAndPorts(t *testing.T) {
	f := newUpdateStateFixture(t)
	s, err := readRuntimeState(f.paths.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	s.Config.LocalProxy.CoreHostPort = 23456
	if writeRuntimeState(f.paths.StateFile, s) != nil {
		t.Fatal("write fixture")
	}
	cfg, _, err := desktopUpdateRuntime(f.cfg, f.paths)
	if err != nil || cfg.LocalProxy.CoreHostPort != 23456 {
		t.Fatal(cfg.LocalProxy.CoreHostPort, err)
	}
	foreign := f.cfg
	foreign.OwnerToken = "foreign-owner"
	if _, _, err := desktopUpdateRuntime(foreign, f.paths); err == nil {
		t.Fatal("foreign controller accepted")
	}
	s.OverallStatus = "failed"
	_ = writeRuntimeState(f.paths.StateFile, s)
	if _, _, err := desktopUpdateRuntime(f.cfg, f.paths); err == nil {
		t.Fatal("failed runtime accepted")
	}
}
func TestDesktopUpdateControlAggregatesPeersAndResumesAmbiguousPause(t *testing.T) {
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "fixture-token")
	var mu sync.Mutex
	calls := map[string][]string{}
	failCore := false
	server := func(name string, active int64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/internal/desktop-update" || r.Header.Get("X-LazyMind-Internal-Token") != "fixture-token" {
				t.Error("unexpected control endpoint/token")
				w.WriteHeader(403)
				return
			}
			var body struct {
				Action string
				Epoch  uint64
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			calls[name] = append(calls[name], body.Action)
			failed := failCore && name == "core" && body.Action == "pause"
			mu.Unlock()
			if failed {
				w.WriteHeader(503)
				return
			}
			paused := body.Action == "pause"
			epoch := body.Epoch
			if !paused {
				epoch = 0
			}
			_ = json.NewEncoder(w).Encode(updateWorkState{Active: active, Paused: paused, Epoch: epoch})
		}))
	}
	core, scan := server("core", 2), server("scan", 3)
	defer core.Close()
	defer scan.Close()
	cfg := RuntimeConfig{LocalProxy: LocalProxyConfig{CoreHostPort: core.Listener.Addr().(*net.TCPAddr).Port, ScanHostPort: scan.Listener.Addr().(*net.TCPAddr).Port}}
	s, err := controlDesktopUpdate(context.Background(), cfg, "status", 0)
	if err != nil || s.Active != 5 || s.Paused {
		t.Fatal(s, err)
	}
	mu.Lock()
	failCore = true
	mu.Unlock()
	if _, err := controlDesktopUpdate(context.Background(), cfg, "pause", 7); err == nil {
		t.Fatal("ambiguous pause accepted")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, name := range []string{"core", "scan"} {
		if strings.Join(calls[name], ",") != "status,pause,resume" {
			t.Fatal(name, calls[name])
		}
	}
}

type updateControlTransport func(*http.Request) (*http.Response, error)

func (f updateControlTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDesktopUpdateControlRejectsMalformedSuccessfulReplies(t *testing.T) {
	for _, body := range []string{`{}`, `{"active":0,"paused":false}`, `{"active":-1,"paused":true,"epoch":1}`, `{"active":0,"paused":true,"epoch":2}`, `{"active":0,"paused":true,"epoch":1}{}`} {
		client := &http.Client{Transport: updateControlTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		if _, err := requestDesktopUpdate(context.Background(), client, 1234, "fixture-token", "pause", 1); err == nil {
			t.Fatal("accepted", body)
		}
	}
}
func TestDesktopUpdateStopCLIRequiresIdleAndConfirmedShutdown(t *testing.T) {
	for _, scenario := range []string{"success", "busy", "supervisor-failure", "census-failure"} {
		t.Run(scenario, func(t *testing.T) {
			f := newUpdateStateFixture(t)
			t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "fixture-token")
			original := resolveRuntimeRecoveryRoot
			root := t.TempDir()
			resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
			defer func() { resolveRuntimeRecoveryRoot = original }()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Action string
					Epoch  uint64
				}
				_ = json.NewDecoder(r.Body).Decode(&request)
				active := int64(0)
				if scenario == "busy" {
					active = 1
				}
				_ = json.NewEncoder(w).Encode(updateWorkState{Active: active, Paused: true, Epoch: request.Epoch})
			}))
			defer server.Close()
			state, _ := readRuntimeState(f.paths.StateFile)
			port := server.Listener.Addr().(*net.TCPAddr).Port
			state.Config.LocalProxy.CoreHostPort = port
			state.Config.LocalProxy.ScanHostPort = port
			_ = writeRuntimeState(f.paths.StateFile, state)
			runner := &fakeRunner{t: t}
			if scenario == "supervisor-failure" {
				runner.handlers = []func(Command) (CommandResult, error){func(Command) (CommandResult, error) { return CommandResult{}, errors.New("fixture stop failed") }}
			}
			manager := NewRuntimeManager(runner, "fixture-runtime")
			manager.probeAPI = func(int, time.Duration) bool { return false }
			manager.processScanner = func(RuntimePaths) ([]LocalProcessRecord, error) {
				if scenario == "census-failure" {
					return nil, errors.New("fixture census failed")
				}
				return nil, nil
			}
			var output bytes.Buffer
			cli := NewCLI(&output, io.Discard, runner, "fixture-runtime")
			cli.newManager = func(CommandRunner, string) *RuntimeManager { return manager }
			args := []string{"update-control", "--action", "stop", "--epoch", "7", "--profile", "desktop", "--owner-token", f.cfg.OwnerToken, "--repo-root", f.paths.RepoRoot, "--runtime-root", f.paths.RuntimeRoot, "--resources-root", f.paths.ResourcesRoot}
			err := cli.Run(context.Background(), args)
			after, _ := readRuntimeState(f.paths.StateFile)
			if scenario == "success" {
				if err != nil || after.OverallStatus != "stopped" || !strings.Contains(output.String(), `"status":"stopped"`) {
					t.Fatal(err, after.OverallStatus, output.String())
				}
			} else if err == nil || after.OverallStatus == "stopped" {
				t.Fatal("failed stop authorized install", err, after.OverallStatus)
			}
			want := 1
			if scenario == "busy" {
				want = 0
			}
			if len(runner.calls) != want {
				t.Fatal("unexpected fallback command", runner.calls)
			}
		})
	}
}
