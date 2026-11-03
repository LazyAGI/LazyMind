package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
)

type discoveryOfflineTransport struct{}

func (discoveryOfflineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("fixture Core unavailable")
}

func TestInternalAgentContinuesAfterCacheRefreshFailure(t *testing.T) {
	transport := http.DefaultTransport
	http.DefaultTransport = discoveryOfflineTransport{}
	t.Cleanup(func() { http.DefaultTransport = transport })
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("LAZYMIND_HOME", filepath.Join(root, "lazymind"))
	t.Setenv("LAZYMIND_CONNECTOR_CONFIG", "")
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	t.Setenv("LAZYMIND_MCP_URL", "")
	if err := os.MkdirAll(filepath.Join(root, ".cursor"), 0700); err != nil {
		t.Fatal(err)
	}
	desktop := filepath.Join(root, "Cursor.exe")
	if err := os.WriteFile(desktop, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := agentexec.SetExecutableBinding(agentexec.CursorDesktop, desktop); err != nil {
		t.Fatal(err)
	}
	previous := invalidateDesktopApplications
	t.Cleanup(func() { invalidateDesktopApplications = previous })
	for _, cacheErr := range []error{os.ErrPermission, context.DeadlineExceeded} {
		invalidateDesktopApplications = func(context.Context) error { return cacheErr }
		for _, action := range []string{"status", "connect"} {
			args := []string{"agent", "cursor", action}
			if action == "status" {
				args = append(args, "--refresh-installations")
			}
			var stdout, stderr bytes.Buffer
			if err := runInternal(context.Background(), args, &stdout, &stderr); err != nil {
				t.Fatalf("%s failed on cache maintenance: %v", action, err)
			}
			var status agentintegration.Status
			if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
				t.Fatalf("invalid status JSON: %s: %v", &stdout, err)
			}
			if status.Agent != "cursor" || agentintegration.MissingRequirement(status.Requirements) {
				t.Fatalf("explicit desktop binding was ignored: %+v", status)
			}
			if action == "status" && status.State != agentintegration.Ready {
				t.Fatalf("status did not use bound installation: %+v", status)
			}
			// Connect must reach its normal authentication check. This fixture has
			// no credentials and must neither contact Core nor launch the desktop.
			if action == "connect" && (status.State != agentintegration.Failed || !strings.Contains(status.Message, "not logged in")) {
				t.Fatalf("connection did not reach authentication: %+v", status)
			}
			if !strings.Contains(stderr.String(), cacheErr.Error()) {
				t.Fatalf("cache failure was not diagnosed: %s", &stderr)
			}
		}
	}
}

func TestInternalAgentCacheRefreshPreservesCancellation(t *testing.T) {
	previous := invalidateDesktopApplications
	t.Cleanup(func() { invalidateDesktopApplications = previous })
	invalidateDesktopApplications = func(ctx context.Context) error { return ctx.Err() }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	err := runInternal(ctx, []string{"agent", "cursor", "connect"}, &output, &output)
	if !errors.Is(err, context.Canceled) || output.Len() != 0 {
		t.Fatalf("canceled operation continued: output=%s err=%v", &output, err)
	}
}
