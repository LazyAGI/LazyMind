package codexhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlProbeResultPreservesKnownIssue(t *testing.T) {
	for _, code := range []string{"cdp_unavailable", "control_window_unavailable", "capability_unavailable"} {
		t.Run(code, func(t *testing.T) {
			for _, ready := range []bool{false, true} {
				body, err := json.Marshal(map[string]any{"ready": ready, "issue": code, "window_count": 1})
				if err != nil {
					t.Fatal(err)
				}
				accepted, err := decodeProbeResult(body, true)
				if accepted || SetupIssue(err) != code {
					t.Fatalf("probe issue must reject readiness: ready=%v accepted=%v err=%v", ready, accepted, err)
				}
			}
		})
	}
}

func TestProbeResultRejectsMalformedOrUnknownControlEvidence(t *testing.T) {
	for _, body := range []string{`{`, `{"ready":"true"}`, `{}`, `{"ready":null}`, `{"ready":true,"issue":"private-error-content"}`} {
		t.Run(body, func(t *testing.T) {
			accepted, err := decodeProbeResult([]byte(body), true)
			if accepted || SetupIssue(err) != "cdp_unavailable" || strings.Contains(err.Error(), "private-error-content") {
				t.Fatalf("invalid control evidence lost its category or leaked content: accepted=%v err=%v", accepted, err)
			}
		})
	}
	_, err := decodeProbeResult([]byte(`{`), true)
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) {
		t.Fatalf("JSON failure lost its original cause: %v", err)
	}
}

func TestProbeResultSupportsReadyOnlyPresentationResponses(t *testing.T) {
	for _, control := range []bool{false, true} {
		if ready, err := decodeProbeResult([]byte(`{"ready":true}`), control); !ready || err != nil {
			t.Fatalf("ready-only success was rejected: control=%v err=%v", control, err)
		}
	}
	if ready, err := decodeProbeResult([]byte(`{"ready":false}`), false); ready || err != nil {
		t.Fatalf("presentation is allowed to be absent: ready=%v err=%v", ready, err)
	}
	if ready, err := decodeProbeResult([]byte(`{"ready":false}`), true); ready || SetupIssue(err) != "capability_unavailable" {
		t.Fatalf("legacy control not-ready lost its category: ready=%v err=%v", ready, err)
	}
}

func TestConnectionCheckRetainsProbeCauseWithoutSerializingIt(t *testing.T) {
	for _, code := range []string{"cdp_unavailable", "control_window_unavailable", "capability_unavailable", "runtime_unavailable"} {
		t.Run(code, func(t *testing.T) {
			home := connectionFixture(t)
			cause := setupError(code, errors.New("private-probe-detail"))
			controlProbe = func(context.Context, string, Config) (bool, error) { return false, cause }
			err := CheckConnection(context.Background(), home)
			var issue *ConnectionIssue
			if !errors.As(err, &issue) || issue.Code != "CODEX_CONTROL_UNAVAILABLE" || SetupIssue(err) != code || !errors.Is(err, cause) {
				t.Fatalf("connection wrapper lost the probe cause: %v", err)
			}
			body, marshalErr := json.Marshal(issue)
			if marshalErr != nil || strings.Contains(string(body), "private-probe-detail") {
				t.Fatalf("connection JSON must not include the underlying probe cause: %s %v", body, marshalErr)
			}
		})
	}
}

func TestConnectionCheckPreservesProbeCancellationAndStartupIssue(t *testing.T) {
	home := connectionFixture(t)
	controlProbe = func(context.Context, string, Config) (bool, error) {
		return false, setupError("cdp_unavailable", context.Canceled)
	}
	if err := CheckConnection(context.Background(), home); !errors.Is(err, context.Canceled) || SetupIssue(err) != "cdp_unavailable" {
		t.Fatalf("connection wrapper lost probe cancellation: %v", err)
	}
	startupDesktopReady = func(context.Context, int) (bool, error) {
		return false, setupError("control_window_unavailable", errors.New("missing chat window"))
	}
	if err := CheckConnection(context.Background(), home); SetupIssue(err) != "control_window_unavailable" {
		t.Fatalf("connection wrapper lost startup failure: %v", err)
	}
}

func TestRunProbePreservesMissingRuntimeAndCancellation(t *testing.T) {
	home := t.TempDir()
	config := Config{Node: filepath.Join(home, "missing-node"), Port: 9334}
	if ready, err := runProbe(context.Background(), home, config, "--probe-only"); ready || SetupIssue(err) != "runtime_unavailable" || !(errors.Is(err, os.ErrNotExist) || errors.Is(err, exec.ErrNotFound)) {
		t.Fatalf("missing runtime lost its category/cause: ready=%v err=%v", ready, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ready, err := runProbe(ctx, home, config, "--probe-only"); ready || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled probe lost cancellation: ready=%v err=%v", ready, err)
	}
}
