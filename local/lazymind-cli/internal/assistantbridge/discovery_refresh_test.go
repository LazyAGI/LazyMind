package assistantbridge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestAgentConnectContinuesAfterCacheRefreshFailure(t *testing.T) {
	transport := http.DefaultTransport
	http.DefaultTransport = discoveryOfflineTransport{}
	t.Cleanup(func() { http.DefaultTransport = transport })
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("LAZYMIND_CONNECTOR_CONFIG", "")
	t.Setenv("LAZYMIND_RUNTIME_PROFILE", "")
	t.Setenv("LAZYMIND_MCP_URL", "")
	server := newTestServer(t, filepath.Join(root, "lazymind"))
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
		status, err := server.agentAction(context.Background(), "cursor", "connect")
		if err != nil || status.Agent != "cursor" || agentintegration.MissingRequirement(status.Requirements) {
			t.Fatalf("cache maintenance blocked the bound desktop: status=%+v err=%v", status, err)
		}
		if status.State != agentintegration.Failed || !strings.Contains(status.Message, "not logged in") {
			t.Fatalf("connection did not reach its normal authentication check: %+v", status)
		}
	}
}

func TestAgentStatusRefreshContinuesAfterCacheFailure(t *testing.T) {
	transport := http.DefaultTransport
	http.DefaultTransport = discoveryOfflineTransport{}
	t.Cleanup(func() { http.DefaultTransport = transport })
	server := newTestServer(t, t.TempDir())
	previous := invalidateDesktopApplications
	t.Cleanup(func() { invalidateDesktopApplications = previous })
	calls := 0
	invalidateDesktopApplications = func(context.Context) error { calls++; return os.ErrPermission }
	request := httptest.NewRequest(http.MethodGet, "/v1/agents?refresh_installations=true", nil)
	response := httptest.NewRecorder()
	server.handleAgentStatuses(response, request)
	if calls != 1 || response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"agents"`) {
		t.Fatalf("cache refresh discarded agent statuses: calls=%d code=%d body=%s", calls, response.Code, response.Body.String())
	}
}

func TestAgentCacheRefreshPreservesCancellation(t *testing.T) {
	previous := invalidateDesktopApplications
	t.Cleanup(func() { invalidateDesktopApplications = previous })
	invalidateDesktopApplications = func(ctx context.Context) error { return ctx.Err() }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Server{}).agentAction(ctx, "cursor", "connect"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled connection continued: %v", err)
	}
}
