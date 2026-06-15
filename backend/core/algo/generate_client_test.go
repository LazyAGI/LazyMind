package algo

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"

	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
)

func TestGenerateURLUsesChatServiceEndpoint(t *testing.T) {
	t.Setenv("LAZYMIND_ALGO_SERVICE_URL", "http://algo-service.invalid")
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", "http://chat-service:8046")

	got := generateURL(rewritePath)
	want := "http://chat-service:8046/api/chat/rewrite"
	if got != want {
		t.Fatalf("expected generate URL %q, got %q", want, got)
	}
}

func TestGenerateFallsBackToRouterChildWhenRewriteIsNotProxied(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	corestore.Init(db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })

	if err := db.Exec(`
CREATE TABLE router_child_processes (
  id INTEGER PRIMARY KEY,
  algorithm_id TEXT NOT NULL,
  host TEXT NOT NULL,
  port INTEGER NOT NULL,
  status TEXT NOT NULL,
  updated_at DATETIME
)`).Error; err != nil {
		t.Fatalf("create router table: %v", err)
	}

	primaryURL := startGenerateTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", primaryURL)

	var childBody map[string]any
	childURL := startGenerateTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != rewritePath {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&childBody); err != nil {
			t.Fatalf("decode child request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"content": "polished prompt"})
	}))
	host, port := hostPort(t, childURL)
	if err := db.Exec(`
INSERT INTO router_child_processes (id, algorithm_id, host, port, status, updated_at)
VALUES (1, 'default', ?, ?, 'healthy', CURRENT_TIMESTAMP)
`, host, port).Error; err != nil {
		t.Fatalf("insert router child: %v", err)
	}

	got, err := GeneratePolish(context.Background(), PolishGenerateRequest{
		Content:      "raw prompt",
		UserInstruct: "make it clear",
		LLMConfig:    map[string]any{},
	})
	if err != nil {
		t.Fatalf("GeneratePolish() error = %v", err)
	}
	if got != "polished prompt" {
		t.Fatalf("GeneratePolish() = %q, want polished prompt", got)
	}
	if childBody["task_type"] != "polish" {
		t.Fatalf("expected polish task_type, got %#v", childBody["task_type"])
	}
}

func startGenerateTestServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listener unavailable in current test environment: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	return fmt.Sprintf("http://%s", listener.Addr().String())
}

func hostPort(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	hostPort := rawURL[len("http://"):]
	host, portText, err := net.SplitHostPort(hostPort)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return host, port
}
