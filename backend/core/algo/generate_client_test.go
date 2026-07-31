package algo

import (
	"context"
	"errors"

	"encoding/json"
	"fmt"
	"lazymind/core/common"
	"net"
	"net/http"
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
	db := orm.OpenTestDB(t)
	corestore.Init(db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })

	if err := db.Exec(`
CREATE TABLE router_child_processes (
  id INTEGER PRIMARY KEY,
  algorithm_id TEXT NOT NULL,
  host TEXT NOT NULL,
  port INTEGER NOT NULL,
  status TEXT NOT NULL,
  updated_at TIMESTAMP
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

// TestChatEndpointScheme returns http for unparseable endpoint, scheme otherwise.
func TestChatEndpointScheme(t *testing.T) {
	got := chatEndpointScheme()
	if got != "http" && got != "https" {
		t.Fatalf("got %q, want http or https", got)
	}
}

// TestBaseURLForHostPort builds URL from scheme + host + port.
func TestBaseURLForHostPort(t *testing.T) {
	// Valid inputs
	got := baseURLForHostPort("https", "localhost", 8080)
	if got != "https://localhost:8080" {
		t.Fatalf("got %q", got)
	}

	// Empty host returns empty
	if got := baseURLForHostPort("https", "", 8080); got != "" {
		t.Fatalf("empty host got %q, want empty", got)
	}

	// Zero port returns empty
	if got := baseURLForHostPort("https", "localhost", 0); got != "" {
		t.Fatalf("zero port got %q, want empty", got)
	}

	// Empty scheme defaults to http
	got2 := baseURLForHostPort("", "localhost", 3000)
	if got2 != "http://localhost:3000" {
		t.Fatalf("empty scheme got %q", got2)
	}
}

// TestIsNotFound returns true for 404 HTTPError, false otherwise.
func TestIsNotFound(t *testing.T) {
	// nil
	if isNotFound(nil) {
		t.Fatal("nil should not be not-found")
	}
	// 404 error
	if !isNotFound(&common.HTTPError{StatusCode: 404}) {
		t.Fatal("404 should be not-found")
	}
	// 500 error
	if isNotFound(&common.HTTPError{StatusCode: 500}) {
		t.Fatal("500 should not be not-found")
	}
	// generic error
	if isNotFound(errors.New("generic")) {
		t.Fatal("generic error should not be not-found")
	}
}

// TestRewritePayload builds RewriteRequest with nil-safe llmConfig.
func TestRewritePayload(t *testing.T) {
	req := rewritePayload("skill", "content", "  instruct  ", nil)
	if req.TaskType != "skill" {
		t.Fatalf("task_type = %q", req.TaskType)
	}
	if req.UserInstruct != "instruct" {
		t.Fatalf("user_instruct = %q, want instruct", req.UserInstruct)
	}
	if req.LLMConfig == nil {
		t.Fatal("llmConfig should be non-nil empty map")
	}

	// Provided config is preserved
	cfg := map[string]any{"model": "gpt-4"}
	req2 := rewritePayload("memory", "content", "instruct", cfg)
	if req2.LLMConfig["model"] != "gpt-4" {
		t.Fatalf("config not preserved")
	}
}

// TestExtractGeneratedContent extracts content from nested map structure.
func TestExtractGeneratedContent(t *testing.T) {
	// Direct string returns trimmed value
	if got := extractGeneratedContent("  hello  "); got != "hello" {
		t.Fatalf("string got %q, want hello", got)
	}

	// map with data.content
	payload := map[string]any{
		"data": map[string]any{
			"content": "  generated text  ",
		},
	}
	if got := extractGeneratedContent(payload); got != "generated text" {
		t.Fatalf("got %q, want generated text", got)
	}

	// nil
	if got := extractGeneratedContent(nil); got != "" {
		t.Fatalf("nil got %q, want empty", got)
	}

	// empty map
	if got := extractGeneratedContent(map[string]any{}); got != "" {
		t.Fatalf("empty map got %q, want empty", got)
	}
}
