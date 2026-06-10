package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
)

func newPromptTestDB(t *testing.T) *orm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := orm.Connect(orm.DriverSQLite, dbPath)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(orm.AllModelsForDDL()...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func TestPolishPromptCallsRewrite(t *testing.T) {
	db := newPromptTestDB(t)
	corestore.Init(db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })

	var algoBody map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/rewrite" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&algoBody); err != nil {
			t.Fatalf("decode algorithm request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"content": "polished prompt"})
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listener unavailable in current test environment: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Shutdown(context.Background()) }()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", fmt.Sprintf("http://%s", listener.Addr().String()))

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/core/prompts:polish",
		strings.NewReader(`{"content":"raw prompt","user_instruct":"make it clear"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()

	PolishPrompt(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if algoBody["task_type"] != "polish" {
		t.Fatalf("expected polish task_type, got %#v", algoBody["task_type"])
	}
	if algoBody["content"] != "raw prompt" {
		t.Fatalf("expected content forwarded, got %#v", algoBody["content"])
	}
	if algoBody["user_instruct"] != "make it clear" {
		t.Fatalf("expected user_instruct forwarded, got %#v", algoBody["user_instruct"])
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["content"] != "polished prompt" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}
