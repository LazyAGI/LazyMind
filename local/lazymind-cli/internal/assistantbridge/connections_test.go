package assistantbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"lazymind/agentconnector/internal/agentintegration"
)

func TestDSHInstallOutlivesRequestAndRepeatedClicksShareJob(t *testing.T) {
	home := t.TempDir()
	server := newTestServer(t, home)
	t.Cleanup(server.cancelAgentConnections)
	t.Setenv("DSH_HOME", filepath.Join(home, "dsh-home"))
	binary := filepath.Join(home, "existing-dsh")
	if err := os.WriteFile(binary, []byte("existing executable"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_DSH_PATH", binary)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server.connectOverride = func(ctx context.Context, agent string) agentintegration.Status {
		calls.Add(1)
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return agentintegration.Status{Agent: agent, State: agentintegration.Failed, Message: ctx.Err().Error()}
		}
		return agentintegration.Status{Agent: agent, DisplayName: "DeepSeek Harness", State: agentintegration.Failed, Message: "package download failed"}
	}
	handler := server.routes()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/agents/deepseek-harness/connect", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusAccepted {
		t.Fatalf("installer blocked request: %d %s", response.Code, response.Body.String())
	}
	cancel()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("installation did not start")
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		url := "/v1/agents/deepseek-harness"
		if method == http.MethodPost {
			url += "/connect"
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, url, nil))
		var status agentintegration.Status
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || status.State != agentintegration.Connecting {
			t.Fatalf("pending status missing: %s %v", w.Body.String(), err)
		}
	}
	server.connectionMu.Lock()
	job := server.connections["deepseek-harness"]
	server.connectionMu.Unlock()
	close(release)
	select {
	case <-job.done:
	case <-time.After(time.Second):
		t.Fatal("installation did not finish")
	}
	if calls.Load() != 1 {
		t.Fatal("repeated click started another installation")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/agents/deepseek-harness", nil))
	var final agentintegration.Status
	if err := json.Unmarshal(w.Body.Bytes(), &final); err != nil || final.State != agentintegration.Failed || final.Message != "package download failed" {
		t.Fatalf("installation failure not visible after reload: %s %v", w.Body.String(), err)
	}
}
