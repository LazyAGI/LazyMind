package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type testSSERecorder struct {
	header  http.Header
	writeCh chan string

	mu   sync.Mutex
	body strings.Builder
}

func newTestSSERecorder() *testSSERecorder {
	return &testSSERecorder{
		header:  make(http.Header),
		writeCh: make(chan string, 16),
	}
}

func (r *testSSERecorder) Header() http.Header {
	return r.header
}

func (r *testSSERecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	n, err := r.body.Write(p)
	r.mu.Unlock()
	select {
	case r.writeCh <- string(p):
	default:
	}
	return n, err
}

func (r *testSSERecorder) WriteHeader(statusCode int) {}

func (r *testSSERecorder) Flush() {}

func (r *testSSERecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

func newAgentTestDB(t *testing.T) *orm.DB {
	t.Helper()

	dsn := fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())
	db, err := orm.Connect(orm.DriverSQLite, dsn)
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.AgentThread{},
		&orm.AgentUserActiveThread{},
		&orm.AgentThreadRecord{},
		&orm.AgentThreadStep{},
		&orm.AgentThreadRound{},
		&orm.UserSelectedModel{},
		&orm.UserModelProviderGroupModel{},
		&orm.UserModelProviderGroup{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func seedAgentRuntimeModelConfig(t *testing.T, db *orm.DB, userID, role string) {
	t.Helper()

	now := time.Now().UTC()
	suffix := strings.ReplaceAll(role, "_", "-")
	group := orm.UserModelProviderGroup{
		ID:                  "group-" + suffix,
		UserModelProviderID: "provider-" + suffix,
		Name:                "Provider " + role,
		BaseURL:             "https://api.example.test/v1",
		APIKey:              "sk-" + suffix,
		IsVerified:          true,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	model := orm.UserModelProviderGroupModel{
		ID:                       "model-" + suffix,
		UserModelProviderID:      group.UserModelProviderID,
		UserModelProviderGroupID: group.ID,
		ProviderName:             "OpenAI",
		Name:                     "gpt-" + suffix,
		ModelType:                "llm",
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	selected := orm.UserSelectedModel{
		UserID:                        userID,
		UserName:                      userID,
		ModelKey:                      role,
		UserModelProviderGroupModelID: model.ID,
		Share:                         false,
		CreatedAt:                     now,
		UpdatedAt:                     now,
	}
	if err := db.DB.Create(&group).Error; err != nil {
		t.Fatalf("create provider group: %v", err)
	}
	if err := db.DB.Create(&model).Error; err != nil {
		t.Fatalf("create provider group model: %v", err)
	}
	if err := db.DB.Create(&selected).Error; err != nil {
		t.Fatalf("create selected model: %v", err)
	}
}

func TestBuildThreadCreateTitleUsesKnowledgeBaseDisplayNameAndDate(t *testing.T) {
	db := newAgentTestDB(t)
	if err := db.DB.AutoMigrate(&orm.Dataset{}); err != nil {
		t.Fatalf("auto migrate dataset: %v", err)
	}

	now := time.Date(2026, 5, 13, 9, 30, 0, 0, time.UTC)
	if err := db.DB.Create(&orm.Dataset{
		ID:                     "dataset-1",
		KbID:                   "kb-1",
		DisplayName:            "产品知识库",
		ResourceUID:            "dataset-1",
		DatasetInfo:            json.RawMessage(`{}`),
		EmbeddingModel:         "default",
		EmbeddingModelProvider: "default",
		Type:                   1,
		Ext:                    json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-1",
			CreateUserName: "tester",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create dataset: %v", err)
	}

	payload := map[string]any{
		"title": "old frontend title",
		"inputs": map[string]any{
			"kb_id": "kb-1",
		},
	}
	applyThreadCreateTitle(context.Background(), db.DB, payload, now)

	if got := payload["title"]; got != "产品知识库-2026-05-13" {
		t.Fatalf("unexpected thread title: %#v", got)
	}
}

func TestBuildThreadCreateTitleFallsBackToPayloadTitle(t *testing.T) {
	now := time.Date(2026, 5, 13, 9, 30, 0, 0, time.UTC)
	payload := map[string]any{
		"title": "前端传入名称",
		"inputs": map[string]any{
			"kb_id": "missing-kb",
		},
	}

	got := buildThreadCreateTitle(context.Background(), nil, payload, now)
	if got != "前端传入名称-2026-05-13" {
		t.Fatalf("unexpected fallback thread title: %q", got)
	}
}

func TestCreateThreadRequiresConfiguredThreadLLMs(t *testing.T) {
	db := newAgentTestDB(t)
	if err := db.DB.AutoMigrate(&orm.Dataset{}); err != nil {
		t.Fatalf("auto migrate dataset: %v", err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", "http://127.0.0.1:1")

	body := []byte(`{
		"mode": "interactive",
		"title": "eval",
		"llm_config": {
			"evo_llm": {"source": "client", "model": "client-supplied"}
		},
		"inputs": {"kb_id": "kb-1", "num_cases": 1}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/core/agent/threads", bytes.NewReader(body))
	req.Header.Set("X-User-Id", "user-1")
	req.Header.Set("X-User-Name", "User One")
	rec := httptest.NewRecorder()

	CreateThread(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected missing thread llm config to return 422, status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "llm") || !strings.Contains(rec.Body.String(), "evo_llm") {
		t.Fatalf("expected response to mention llm and evo_llm, body=%s", rec.Body.String())
	}
	var activeCount int64
	if err := db.DB.Model(&orm.AgentUserActiveThread{}).Count(&activeCount).Error; err != nil {
		t.Fatalf("count active threads: %v", err)
	}
	if activeCount != 0 {
		t.Fatalf("expected validation to happen before active thread reservation, got %d rows", activeCount)
	}
}

func TestAttachThreadModelConfigProvidesRequiredThreadLLMs(t *testing.T) {
	db := newAgentTestDB(t)
	seedAgentRuntimeModelConfig(t, db, "user-1", "llm")
	seedAgentRuntimeModelConfig(t, db, "user-1", "evo_llm")

	payload := map[string]any{}
	if err := attachThreadModelConfig(context.Background(), db.DB, "user-1", payload); err != nil {
		t.Fatalf("attach thread model config: %v", err)
	}
	if !hasThreadRequiredLLMConfig(payload) {
		t.Fatalf("expected attached payload to satisfy thread llm requirement: %#v", payload)
	}
	llmConfig, ok := payload["llm_config"].(map[string]any)
	if !ok {
		t.Fatalf("expected llm_config, got %#v", payload["llm_config"])
	}
	evoConfig, ok := llmConfig["evo_llm"].(map[string]any)
	if !ok || evoConfig["model"] != "gpt-evo-llm" {
		t.Fatalf("expected evo_llm config, got %#v", llmConfig["evo_llm"])
	}
	chatConfig, ok := llmConfig["llm"].(map[string]any)
	if !ok || chatConfig["model"] != "gpt-llm" {
		t.Fatalf("expected llm config, got %#v", llmConfig["llm"])
	}
}

func TestBuildEvoThreadCreatePayloadForwardsRouterTarget(t *testing.T) {
	payload := map[string]any{
		"mode":       "interactive",
		"title":      "eval",
		"llm_config": map[string]any{"llm": map[string]any{}},
		"inputs": map[string]any{
			"kb_id":            "kb-1",
			"router_admin_url": "http://chat:8046",
			"algorithm_id":     "default",
			"num_cases":        2,
		},
	}

	got := buildEvoThreadCreatePayload(payload)
	inputs, ok := got["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("expected inputs map, got %#v", got["inputs"])
	}
	if inputs["router_admin_url"] != "http://chat:8046" || inputs["algorithm_id"] != "default" {
		t.Fatalf("expected router target to be sent to Evo ThreadInputs: %#v", inputs)
	}
	if inputs["router_chat_url"] == "" {
		t.Fatalf("expected router_chat_url to be sent to Evo ThreadInputs: %#v", inputs)
	}
	if fmt.Sprint(inputs["kb_id"]) != "[kb-1]" || inputs["num_case"] != 2 {
		t.Fatalf("unexpected Evo inputs: %#v", inputs)
	}
}

func TestPostThreadActionForwardsOnlyCommandFields(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:     "thr_1",
		Status:       "created",
		CreateUserID: "u1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("seed thread: %v", err)
	}

	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1" {
			_ = json.NewEncoder(w).Encode(evoThread{ThreadID: "thr_1", Status: "running", CurrentStep: "eval"})
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/threads/thr_1/start" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "accepted"})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	body := `{"command_id":"cmd_1","until_step":"eval","llm_config":{"llm":{}},"extra":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/core/agent/threads/thr_1/start", strings.NewReader(body))
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()

	postThreadAction(rec, req, "start")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok, status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(got) != 2 || got["command_id"] != "cmd_1" || got["until_step"] != "eval" {
		t.Fatalf("unexpected upstream command body: %#v", got)
	}
}

func TestPostThreadActionForwardsOnlyEmptyCommandFields(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:     "thr_1",
		Status:       "created",
		CreateUserID: "u1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("seed thread: %v", err)
	}

	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1" {
			_ = json.NewEncoder(w).Encode(evoThread{ThreadID: "thr_1", Status: "paused", CurrentStep: "eval"})
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/threads/thr_1/pause" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "accepted"})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	body := `{"command_id":"cmd_2","llm_config":{"llm":{}},"until_step":"eval","extra":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/core/agent/threads/thr_1/pause", strings.NewReader(body))
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()

	postThreadAction(rec, req, "pause")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok, status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(got) != 1 || got["command_id"] != "cmd_2" {
		t.Fatalf("unexpected upstream empty command body: %#v", got)
	}
}

func TestStreamThreadMessagesProxiesEvoResponse(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:     "thr_1",
		Status:       "created",
		CreateUserID: "u1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("seed thread: %v", err)
	}

	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/threads/thr_1/messages" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"assistant_response\",\"content\":\"ok\"}\n\n")
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	body := `{"message_id":"m1","content":"继续","llm_config":{"llm":{}},"extra":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/core/agent/threads/thr_1/messages", strings.NewReader(body))
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()

	StreamThreadMessages(rec, req)

	if got["message_id"] != "m1" || got["content"] != "继续" || got["extra"] != true {
		t.Fatalf("unexpected upstream message body: %#v", got)
	}
}

func TestGetThreadEvalGateBadCasesProxiesEvoResponse(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:     "thr_1",
		Status:       "created",
		CreateUserID: "u1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("seed thread: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/threads/thr_1/gates/eval/versions/2/bad-cases" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("page_size"); got != "10" {
			t.Fatalf("expected page_size to proxy, got %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"case_id": "case_1"}}})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/core/agent/threads/thr_1/gates/eval/versions/2/bad-cases?page_size=10",
		nil,
	)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1", "version": "2"})
	rec := httptest.NewRecorder()

	GetThreadEvalGateBadCases(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok, status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "case_1") {
		t.Fatalf("expected proxied bad case body, got %s", rec.Body.String())
	}
}

func TestGetThreadTraceDetailProxiesEvoResponse(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:     "thr_1",
		Status:       "created",
		CreateUserID: "u1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("seed thread: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/threads/thr_1/results/traces/trace-1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"trace_id": "trace-1", "trace_status": "success"})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/core/agent/threads/thr_1/results/traces/trace-1", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1", "trace_id": "trace-1"})
	rec := httptest.NewRecorder()

	GetThreadTraceDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok, status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "trace-1") {
		t.Fatalf("expected proxied trace body, got %s", rec.Body.String())
	}
}

func TestCompareThreadTracesProxiesEvoResponse(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:     "thr_1",
		Status:       "created",
		CreateUserID: "u1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("seed thread: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/threads/thr_1/results/traces:compare" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("a"); got != "trace-a" {
			t.Fatalf("expected query a=trace-a, got %q", got)
		}
		if got := r.URL.Query().Get("b"); got != "trace-b" {
			t.Fatalf("expected query b=trace-b, got %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query": "question",
			"a":     map[string]any{"trace_id": "trace-a"},
			"b":     map[string]any{"trace_id": "trace-b"},
		})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/core/agent/threads/thr_1/results/traces:compare?a=trace-a&b=trace-b", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()

	CompareThreadTraces(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok, status=%d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "trace-a") || !strings.Contains(body, "trace-b") {
		t.Fatalf("expected proxied trace compare body, got %s", body)
	}
}

func TestIsThreadFlowRunningKeepsRunningAndPending(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{status: "running", want: true},
		{status: "pending", want: true},
		{status: "paused", want: true},
		{status: "RUNNING", want: true},
		{status: "not_found", want: false},
		{status: "idle", want: false},
		{status: "ended", want: false},
		{status: "failed", want: false},
		{status: "cancelled", want: false},
		{status: "", want: false},
	}

	for _, tc := range cases {
		got := isThreadFlowRunning(&threadFlowStatusResponse{Status: tc.status})
		if got != tc.want {
			t.Fatalf("isThreadFlowRunning(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
	if isThreadFlowRunning(nil) {
		t.Fatalf("nil flow status must not keep stream alive")
	}
}

type testEvoStep struct {
	ThreadID   string `json:"thread_id"`
	StepID     string `json:"step_id"`
	Stage      string `json:"stage"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Active     bool   `json:"active"`
	OrderIndex int    `json:"order_index"`
	EventCount int64  `json:"event_count"`
	NextStepID string `json:"next_step_id"`
	Version    *int   `json:"version"`
}

type testEvoStepList struct {
	ThreadID     string        `json:"thread_id"`
	ActiveStepID string        `json:"active_step_id"`
	Items        []testEvoStep `json:"items"`
	TotalSize    int           `json:"total_size"`
}

func TestListThreadStepsProxiesEvoResponse(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:     "thr_1",
		Status:       "running",
		CreateUserID: "u1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create thread: %v", err)
	}
	stepOneID := "aaaaaaaa-aaaa-5aaa-8aaa-aaaaaaaaaaaa"
	stepTwoID := "bbbbbbbb-bbbb-5bbb-8bbb-bbbbbbbbbbbb"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/threads/thr_1/steps" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(testEvoStepList{
			ThreadID:     "thr_1",
			ActiveStepID: stepTwoID,
			Items: []testEvoStep{
				{ThreadID: "thr_1", StepID: stepOneID, Stage: "dataset", Title: "Dataset", Status: "succeeded", Active: false, OrderIndex: 1, EventCount: 2, NextStepID: stepTwoID},
				{ThreadID: "thr_1", StepID: stepTwoID, Stage: "eval", Title: "Eval", Status: "running", Active: true, OrderIndex: 2, EventCount: 3},
			},
			TotalSize: 2,
		})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/core/agent/threads/thr_1/steps", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	ListThreadSteps(rec, req)

	var response testEvoStepList
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok response, status=%d body=%s", rec.Code, rec.Body.String())
	}
	if response.ActiveStepID != stepTwoID {
		t.Fatalf("expected active_step_id %s, got %q", stepTwoID, response.ActiveStepID)
	}
	if response.TotalSize != 2 || len(response.Items) != 2 {
		t.Fatalf("unexpected step list response: %#v", response)
	}
	if response.Items[0].NextStepID != stepTwoID {
		t.Fatalf("expected first step next_step_id %s, got %q", stepTwoID, response.Items[0].NextStepID)
	}
}

func TestListThreadStepsDoesNotMutateLocalProjectionRows(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:     "thr_1",
		Status:       "running",
		CreateUserID: "u1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create thread: %v", err)
	}
	if err := db.DB.Create(&orm.AgentThreadStep{
		ThreadID:   "thr_1",
		StepID:     "stale",
		Title:      "Stale",
		Status:     "running",
		Active:     true,
		OrderIndex: 9,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error; err != nil {
		t.Fatalf("create stale step: %v", err)
	}

	stepOneID := "aaaaaaaa-aaaa-5aaa-8aaa-aaaaaaaaaaaa"
	stepTwoID := "bbbbbbbb-bbbb-5bbb-8bbb-bbbbbbbbbbbb"
	versionOne := 1
	versionTwo := 2
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/threads/thr_1/steps" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(testEvoStepList{
			ThreadID:     "thr_1",
			ActiveStepID: stepTwoID,
			Items: []testEvoStep{
				{ThreadID: "thr_1", StepID: stepOneID, Stage: "dataset", Title: "dataset", Status: "completed", Active: false, OrderIndex: 0, EventCount: 4, NextStepID: stepTwoID, Version: &versionOne},
				{ThreadID: "thr_1", StepID: stepTwoID, Stage: "eval", Title: "eval", Status: "running", Active: true, OrderIndex: 1, EventCount: 1, Version: &versionTwo},
			},
			TotalSize: 2,
		})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/core/agent/threads/thr_1/steps", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	ListThreadSteps(rec, req)

	var response testEvoStepList
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok response, status=%d body=%s", rec.Code, rec.Body.String())
	}
	if response.ActiveStepID != stepTwoID || response.TotalSize != 2 {
		t.Fatalf("unexpected proxied step list: %#v", response)
	}
	if len(response.Items) != 2 || response.Items[0].StepID != stepOneID ||
		response.Items[0].Stage != "dataset" || response.Items[0].NextStepID != stepTwoID {
		t.Fatalf("unexpected first proxied step: %#v", response.Items)
	}
	if response.Items[0].Version == nil || *response.Items[0].Version != versionOne {
		t.Fatalf("expected first proxied step version %d, got %#v", versionOne, response.Items[0].Version)
	}

	var staleCount int64
	if err := db.DB.Model(&orm.AgentThreadStep{}).Where("thread_id = ? AND step_id = ?", "thr_1", "stale").Count(&staleCount).Error; err != nil {
		t.Fatalf("count stale steps: %v", err)
	}
	if staleCount != 1 {
		t.Fatalf("expected stale local step to be untouched, got %d", staleCount)
	}
}

func TestStreamThreadMessagesReturnsSSEActiveThreadError(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:     "thr_new",
		Status:       "completed",
		CreateUserID: "u1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("seed thread: %v", err)
	}
	if err := db.DB.Create(&orm.AgentUserActiveThread{
		UserID:     "u1",
		ThreadID:   "thr_old",
		Status:     userActiveThreadStatusActive,
		LeaseUntil: now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error; err != nil {
		t.Fatalf("seed active thread: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/threads/thr_old" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(evoThread{ThreadID: "thr_old", Status: "running"})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodPost, "/api/core/agent/threads/thr_new/messages", strings.NewReader(`{"content":"继续"}`))
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_new"})
	req.Header.Set("X-User-Id", "u1")
	rec := newTestSSERecorder()

	StreamThreadMessages(rec, req)

	got := rec.String()
	if strings.Contains(got, "event: USER_ACTIVE_THREAD_EXISTS\n") {
		t.Fatalf("did not expect named USER_ACTIVE_THREAD_EXISTS event, got %q", got)
	}
	wantDataPrefix := `data: {"type":"USER_ACTIVE_THREAD_EXISTS","thread_id":"thr_new","message_id":"msg_thr_new_`
	if !strings.Contains(got, wantDataPrefix) {
		t.Fatalf("expected ordered USER_ACTIVE_THREAD_EXISTS payload, got %q", got)
	}
	wantDataSuffix := `","message":"` + userActiveThreadExistsMessage + `","delta":"` + userActiveThreadExistsMessage + `"}`
	if !strings.Contains(got, wantDataSuffix) {
		t.Fatalf("expected localized active thread message fields, got %q", got)
	}
}

func TestGetThreadRequiresThreadOwner(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:       "thr_a",
		Status:         "completed",
		CreateUserID:   "user-a",
		CreateUserName: "Alice",
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create thread: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/agent/threads/thr_a", nil)
	req.Header.Set("X-User-Id", "user-b")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_a"})
	rec := httptest.NewRecorder()
	GetThread(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected cross-user thread lookup to be hidden, status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStreamThreadEventsDoesNotClaimMissingThreadForCurrentUser(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(http.MethodGet, "/api/core/agent/threads/thr_unknown/events:stream", nil)
	req.Header.Set("X-User-Id", "user-b")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_unknown"})
	rec := httptest.NewRecorder()
	StreamThreadEvents(rec, req)

	var count int64
	if err := db.DB.Model(&orm.AgentThread{}).Where("thread_id = ?", "thr_unknown").Count(&count).Error; err != nil {
		t.Fatalf("count thread: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected missing thread not to be claimed by events stream, found %d rows", count)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected missing thread events request to return 404, status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteThreadRemovesThreadRoundsAndRecords(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()

	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:       "thr_1",
		Status:         "completed",
		CreateUserID:   "u1",
		CreateUserName: "tester",
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create thread: %v", err)
	}
	if err := db.DB.Create(&orm.AgentThreadRound{
		RoundID:          "round_1",
		ThreadID:         "thr_1",
		Status:           "completed",
		UserMessage:      "hello",
		AssistantMessage: "world",
		CreatedAt:        now,
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("create round: %v", err)
	}
	if err := db.DB.Create(&orm.AgentThreadRecord{
		ID:          "record_1",
		ThreadID:    "thr_1",
		RoundID:     "round_1",
		StreamKind:  "message",
		RecordKey:   "rk1",
		EventName:   "message",
		PayloadText: `{"delta":"hi"}`,
		RawFrame:    `data: {"delta":"hi"}`,
		CreatedAt:   now,
		UpdatedAt:   now,
	}).Error; err != nil {
		t.Fatalf("create record: %v", err)
	}
	if err := db.DB.Create(&orm.AgentUserActiveThread{
		UserID:     "u1",
		ThreadID:   "thr_1",
		Status:     userActiveThreadStatusActive,
		LeaseUntil: now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error; err != nil {
		t.Fatalf("create active thread: %v", err)
	}

	result, err := deleteThreadLocalRows(db.DB, "thr_1")
	if err != nil {
		t.Fatalf("deleteThreadLocalRows: %v", err)
	}
	if result["deleted_threads"] != int64(1) {
		t.Fatalf("expected deleted_threads=1, got %#v", result["deleted_threads"])
	}
	if result["deleted_rounds"] != int64(1) {
		t.Fatalf("expected deleted_rounds=1, got %#v", result["deleted_rounds"])
	}
	if result["deleted_records"] != int64(1) {
		t.Fatalf("expected deleted_records=1, got %#v", result["deleted_records"])
	}
	if result["deleted_active_threads"] != int64(1) {
		t.Fatalf("expected deleted_active_threads=1, got %#v", result["deleted_active_threads"])
	}
}

func TestDeleteThreadCancelsRunningFlowBeforeDeleting(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:       "thr_1",
		Status:         "message_streaming",
		CreateUserID:   "u1",
		CreateUserName: "tester",
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create thread: %v", err)
	}

	var mu sync.Mutex
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1":
			_ = json.NewEncoder(w).Encode(evoThread{ThreadID: "thr_1", Status: "running", CurrentStep: "task_1"})
		case r.Method == http.MethodPost && r.URL.Path == "/threads/thr_1/cancel":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "cancelled"})
		case r.Method == http.MethodDelete && r.URL.Path == "/threads/thr_1":
			_ = json.NewEncoder(w).Encode(map[string]any{"deleted_run": true, "deleted_thread": true})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodDelete, "/api/core/agent/threads/thr_1", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	DeleteThread(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected delete ok, status=%d body=%s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	gotCalls := append([]string(nil), calls...)
	mu.Unlock()
	wantCalls := []string{
		"GET /threads/thr_1",
		"POST /threads/thr_1/cancel",
		"DELETE /threads/thr_1",
	}
	if fmt.Sprint(gotCalls) != fmt.Sprint(wantCalls) {
		t.Fatalf("unexpected upstream calls: want %v got %v", wantCalls, gotCalls)
	}
	var count int64
	if err := db.DB.Model(&orm.AgentThread{}).Where("thread_id = ?", "thr_1").Count(&count).Error; err != nil {
		t.Fatalf("count thread: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected thread to be deleted, found %d rows", count)
	}
}

func TestDeleteThreadDoesNotCancelEndedFlow(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:       "thr_1",
		Status:         "completed",
		CreateUserID:   "u1",
		CreateUserName: "tester",
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create thread: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1" {
			_ = json.NewEncoder(w).Encode(evoThread{ThreadID: "thr_1", Status: "ended"})
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/threads/thr_1" {
			_ = json.NewEncoder(w).Encode(map[string]any{"deleted_run": true, "deleted_thread": true})
			return
		}
		http.Error(w, "unexpected request", http.StatusNotFound)
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodDelete, "/api/core/agent/threads/thr_1", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	DeleteThread(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected delete ok, status=%d body=%s", rec.Code, rec.Body.String())
	}
	var count int64
	if err := db.DB.Model(&orm.AgentThread{}).Where("thread_id = ?", "thr_1").Count(&count).Error; err != nil {
		t.Fatalf("count thread: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected thread to be deleted, found %d rows", count)
	}
}

func TestDeleteThreadDeletesLocalRowsWhenUpstreamStatusNotFound(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:       "thr_1",
		Status:         "completed",
		CreateUserID:   "u1",
		CreateUserName: "tester",
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create thread: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1" {
			http.Error(w, `{"detail":"thread not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, "unexpected request", http.StatusNotFound)
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodDelete, "/api/core/agent/threads/thr_1", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	DeleteThread(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected delete ok, status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"missing":true`) {
		t.Fatalf("expected upstream missing marker, body=%s", rec.Body.String())
	}
	var count int64
	if err := db.DB.Model(&orm.AgentThread{}).Where("thread_id = ?", "thr_1").Count(&count).Error; err != nil {
		t.Fatalf("count thread: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected local thread to be deleted, found %d rows", count)
	}
}

func TestDeleteThreadDeletesLocalRowsWhenUpstreamDeleteNotFound(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:       "thr_1",
		Status:         "completed",
		CreateUserID:   "u1",
		CreateUserName: "tester",
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create thread: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1":
			_ = json.NewEncoder(w).Encode(evoThread{ThreadID: "thr_1", Status: "ended"})
		case r.Method == http.MethodDelete && r.URL.Path == "/threads/thr_1":
			http.Error(w, `{"detail":"thread not found"}`, http.StatusNotFound)
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodDelete, "/api/core/agent/threads/thr_1", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	DeleteThread(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected delete ok, status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"missing":true`) {
		t.Fatalf("expected upstream missing marker, body=%s", rec.Body.String())
	}
	var count int64
	if err := db.DB.Model(&orm.AgentThread{}).Where("thread_id = ?", "thr_1").Count(&count).Error; err != nil {
		t.Fatalf("count thread: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected local thread to be deleted, found %d rows", count)
	}
}

func TestDeleteThreadKeepsLocalRowsWhenCancelFails(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:       "thr_1",
		Status:         "message_streaming",
		CreateUserID:   "u1",
		CreateUserName: "tester",
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create thread: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1":
			_ = json.NewEncoder(w).Encode(evoThread{ThreadID: "thr_1", Status: "running"})
		case r.Method == http.MethodPost && r.URL.Path == "/threads/thr_1/cancel":
			http.Error(w, `{"message":"cancel failed"}`, http.StatusInternalServerError)
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodDelete, "/api/core/agent/threads/thr_1", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	DeleteThread(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected bad gateway when cancel fails, status=%d body=%s", rec.Code, rec.Body.String())
	}
	var count int64
	if err := db.DB.Model(&orm.AgentThread{}).Where("thread_id = ?", "thr_1").Count(&count).Error; err != nil {
		t.Fatalf("count thread: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected local thread to remain after cancel failure, found %d rows", count)
	}
}

func TestDeleteThreadKeepsLocalRowsWhenFlowStatusFails(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:       "thr_1",
		Status:         "completed",
		CreateUserID:   "u1",
		CreateUserName: "tester",
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create thread: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"status failed"}`, http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodDelete, "/api/core/agent/threads/thr_1", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	DeleteThread(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected bad gateway when flow status fails, status=%d body=%s", rec.Code, rec.Body.String())
	}
	var count int64
	if err := db.DB.Model(&orm.AgentThread{}).Where("thread_id = ?", "thr_1").Count(&count).Error; err != nil {
		t.Fatalf("count thread: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected local thread to remain after flow status failure, found %d rows", count)
	}
}

func TestListThreadsFiltersByUserAndPaginates(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
	threads := []orm.AgentThread{
		{
			ThreadID:       "thr_old",
			Status:         "completed",
			CreateUserID:   "u1",
			CreateUserName: "tester",
			CreatedAt:      now.Add(-2 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
		},
		{
			ThreadID:       "thr_new",
			Status:         "message_streaming",
			CreateUserID:   "u1",
			CreateUserName: "tester",
			CreatedAt:      now.Add(-1 * time.Hour),
			UpdatedAt:      now.Add(-1 * time.Hour),
		},
		{
			ThreadID:       "thr_other_user",
			Status:         "completed",
			CreateUserID:   "u2",
			CreateUserName: "other",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := db.DB.Create(&threads).Error; err != nil {
		t.Fatalf("create threads: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		switch r.URL.Path {
		case "/threads/thr_new":
			_ = json.NewEncoder(w).Encode(evoThread{ThreadID: "thr_new", Status: "running"})
		case "/threads/thr_old":
			_ = json.NewEncoder(w).Encode(evoThread{ThreadID: "thr_old", Status: "failed"})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/core/agent/threads?page_size=1", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()
	ListThreads(rec, req)

	var firstPage struct {
		Code    int                `json:"code"`
		Message string             `json:"message"`
		Data    threadListResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&firstPage); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if rec.Code != http.StatusOK || firstPage.Code != 0 {
		t.Fatalf("expected ok response, status=%d code=%d message=%s", rec.Code, firstPage.Code, firstPage.Message)
	}
	if firstPage.Data.TotalSize != 2 {
		t.Fatalf("expected total_size=2, got %d", firstPage.Data.TotalSize)
	}
	if firstPage.Data.NextPageToken != "1" {
		t.Fatalf("expected next_page_token=1, got %q", firstPage.Data.NextPageToken)
	}
	if len(firstPage.Data.Threads) != 1 || firstPage.Data.Threads[0].ThreadID != "thr_new" {
		t.Fatalf("unexpected first page threads: %#v", firstPage.Data.Threads)
	}
	if firstPage.Data.Threads[0].Status != "running" {
		t.Fatalf("expected upstream status running, got %q", firstPage.Data.Threads[0].Status)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/core/agent/threads?page_size=10&page_token=1", nil)
	req.Header.Set("X-User-Id", "u1")
	rec = httptest.NewRecorder()
	ListThreads(rec, req)

	var secondPage struct {
		Code    int                `json:"code"`
		Message string             `json:"message"`
		Data    threadListResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&secondPage); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if rec.Code != http.StatusOK || secondPage.Code != 0 {
		t.Fatalf("expected ok second response, status=%d code=%d message=%s", rec.Code, secondPage.Code, secondPage.Message)
	}
	if secondPage.Data.NextPageToken != "" {
		t.Fatalf("expected empty next_page_token, got %q", secondPage.Data.NextPageToken)
	}
	if len(secondPage.Data.Threads) != 1 || secondPage.Data.Threads[0].ThreadID != "thr_old" {
		t.Fatalf("unexpected second page threads: %#v", secondPage.Data.Threads)
	}
	if secondPage.Data.Threads[0].Status != "failed" {
		t.Fatalf("expected upstream status failed, got %q", secondPage.Data.Threads[0].Status)
	}
}

func TestListThreadsFallsBackToLocalStatusWhenStatusesFail(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:       "thr_1",
		Status:         "completed",
		CreateUserID:   "u1",
		CreateUserName: "tester",
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create thread: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "status service unavailable", http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/core/agent/threads?page_size=10", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()
	ListThreads(rec, req)

	var response struct {
		Code    int                `json:"code"`
		Message string             `json:"message"`
		Data    threadListResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rec.Code != http.StatusOK || response.Code != 0 {
		t.Fatalf("expected ok response, status=%d code=%d message=%s", rec.Code, response.Code, response.Message)
	}
	if len(response.Data.Threads) != 1 || response.Data.Threads[0].Status != "completed" {
		t.Fatalf("expected local status fallback, got %#v", response.Data.Threads)
	}
}

func TestReserveUserActiveThreadCreationCreatesPlaceholder(t *testing.T) {
	db := newAgentTestDB(t)
	req := httptest.NewRequest(http.MethodPost, "/api/core/agent/threads", strings.NewReader(`{}`))
	req.Header.Set("X-User-Id", "u1")

	guard, err := reserveUserActiveThreadCreation(context.Background(), db.DB, req)
	if err != nil {
		t.Fatalf("reserveUserActiveThreadCreation returned error: %v", err)
	}
	defer guard.Abort(db.DB)

	var active orm.AgentUserActiveThread
	if err := db.DB.Where("user_id = ?", "u1").First(&active).Error; err != nil {
		t.Fatalf("load active thread placeholder: %v", err)
	}
	if active.Status != userActiveThreadStatusCreating || active.ThreadID != "" || active.CreateToken == "" {
		t.Fatalf("unexpected placeholder: %#v", active)
	}

	if err := guard.Commit(db.DB, "thr_new"); err != nil {
		t.Fatalf("commit active thread: %v", err)
	}
	if err := db.DB.Where("user_id = ?", "u1").First(&active).Error; err != nil {
		t.Fatalf("reload active thread: %v", err)
	}
	if active.Status != userActiveThreadStatusActive || active.ThreadID != "thr_new" || active.CreateToken != "" {
		t.Fatalf("unexpected committed active thread: %#v", active)
	}
}

func TestReserveUserActiveThreadCreationRejectsRunningThread(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentUserActiveThread{
		UserID:     "u1",
		ThreadID:   "thr_old",
		Status:     userActiveThreadStatusActive,
		LeaseUntil: now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error; err != nil {
		t.Fatalf("seed active thread: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/threads/thr_old" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(evoThread{ThreadID: "thr_old", Status: "running"})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodPost, "/api/core/agent/threads", strings.NewReader(`{}`))
	req.Header.Set("X-User-Id", "u1")
	guard, err := reserveUserActiveThreadCreation(context.Background(), db.DB, req)
	if guard != nil {
		t.Fatalf("expected no guard for running thread")
	}
	var activeErr *userActiveThreadError
	if !errors.As(err, &activeErr) || activeErr.statusCode != http.StatusConflict {
		t.Fatalf("expected conflict active thread error, got %T %v", err, err)
	}
}

func TestEnsureUserCanActivateThreadRejectsDifferentRunningThread(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentUserActiveThread{
		UserID:     "u1",
		ThreadID:   "thr_old",
		Status:     userActiveThreadStatusActive,
		LeaseUntil: now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error; err != nil {
		t.Fatalf("seed active thread: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/threads/thr_old" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(evoThread{ThreadID: "thr_old", Status: "running"})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodPost, "/api/core/agent/threads/thr_new/retry", nil)
	req.Header.Set("X-User-Id", "u1")
	err := ensureUserCanActivateThread(context.Background(), db.DB, req, "thr_new")
	var activeErr *userActiveThreadError
	if !errors.As(err, &activeErr) || activeErr.statusCode != http.StatusConflict {
		t.Fatalf("expected conflict active thread error, got %T %v", err, err)
	}
	if activeErr.message != userActiveThreadExistsMessage {
		t.Fatalf("expected localized active thread message, got %q", activeErr.message)
	}
	if activeErr.data["type"] != userActiveThreadExistsType {
		t.Fatalf("expected active thread error type, got %#v", activeErr.data)
	}
}

func TestReserveUserActiveThreadCreationRejectsPausedThread(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentUserActiveThread{
		UserID:     "u1",
		ThreadID:   "thr_old",
		Status:     userActiveThreadStatusActive,
		LeaseUntil: now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error; err != nil {
		t.Fatalf("seed active thread: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/threads/thr_old" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(evoThread{ThreadID: "thr_old", Status: "paused"})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodPost, "/api/core/agent/threads", strings.NewReader(`{}`))
	req.Header.Set("X-User-Id", "u1")
	guard, err := reserveUserActiveThreadCreation(context.Background(), db.DB, req)
	if guard != nil {
		t.Fatalf("expected no guard for paused thread")
	}
	var activeErr *userActiveThreadError
	if !errors.As(err, &activeErr) || activeErr.statusCode != http.StatusConflict {
		t.Fatalf("expected conflict active thread error, got %T %v", err, err)
	}
}

func TestReserveUserActiveThreadCreationReplacesEndedThread(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentUserActiveThread{
		UserID:     "u1",
		ThreadID:   "thr_old",
		Status:     userActiveThreadStatusActive,
		LeaseUntil: now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error; err != nil {
		t.Fatalf("seed active thread: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/threads/thr_old" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(evoThread{ThreadID: "thr_old", Status: "completed"})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodPost, "/api/core/agent/threads", strings.NewReader(`{}`))
	req.Header.Set("X-User-Id", "u1")
	guard, err := reserveUserActiveThreadCreation(context.Background(), db.DB, req)
	if err != nil {
		t.Fatalf("reserveUserActiveThreadCreation returned error: %v", err)
	}
	defer guard.Abort(db.DB)

	var active orm.AgentUserActiveThread
	if err := db.DB.Where("user_id = ?", "u1").First(&active).Error; err != nil {
		t.Fatalf("load active thread placeholder: %v", err)
	}
	if active.Status != userActiveThreadStatusCreating || active.ThreadID != "" {
		t.Fatalf("expected new creating placeholder after ended thread, got %#v", active)
	}
}

func TestReserveUserActiveThreadCreationReplacesMissingThread(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentUserActiveThread{
		UserID:     "u1",
		ThreadID:   "thr_missing",
		Status:     userActiveThreadStatusActive,
		LeaseUntil: now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error; err != nil {
		t.Fatalf("seed active thread: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/threads/thr_missing" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		http.Error(w, `{"detail":"thread thr_missing not found"}`, http.StatusNotFound)
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodPost, "/api/core/agent/threads", strings.NewReader(`{}`))
	req.Header.Set("X-User-Id", "u1")
	guard, err := reserveUserActiveThreadCreation(context.Background(), db.DB, req)
	if err != nil {
		t.Fatalf("reserveUserActiveThreadCreation returned error: %v", err)
	}
	defer guard.Abort(db.DB)

	var active orm.AgentUserActiveThread
	if err := db.DB.Where("user_id = ?", "u1").First(&active).Error; err != nil {
		t.Fatalf("load active thread placeholder: %v", err)
	}
	if active.Status != userActiveThreadStatusCreating || active.ThreadID != "" {
		t.Fatalf("expected new creating placeholder after missing thread, got %#v", active)
	}
}

func TestReserveUserActiveThreadCreationRejectsLiveCreatingLease(t *testing.T) {
	db := newAgentTestDB(t)
	req := httptest.NewRequest(http.MethodPost, "/api/core/agent/threads", strings.NewReader(`{}`))
	req.Header.Set("X-User-Id", "u1")

	guard, err := reserveUserActiveThreadCreation(context.Background(), db.DB, req)
	if err != nil {
		t.Fatalf("first reserve returned error: %v", err)
	}
	defer guard.Abort(db.DB)

	secondGuard, err := reserveUserActiveThreadCreation(context.Background(), db.DB, req)
	if secondGuard != nil {
		t.Fatalf("expected no second guard while first creation lease is live")
	}
	var activeErr *userActiveThreadError
	if !errors.As(err, &activeErr) || activeErr.statusCode != http.StatusConflict {
		t.Fatalf("expected creating conflict, got %T %v", err, err)
	}
}
