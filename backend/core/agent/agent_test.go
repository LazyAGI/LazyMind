package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
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

func TestStreamThreadMessagesForwardsOnlyMessageFields(t *testing.T) {
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

	if got["message_id"] != "m1" || got["content"] != "继续" || len(got) != 2 {
		t.Fatalf("unexpected upstream message body: %#v", got)
	}
}

func TestDecodeJSONArrayObjectsSupportsNestedEnvelope(t *testing.T) {
	body := []byte(`{"data":{"items":[{"seq":1,"kind":"user.message"},{"seq":2,"kind":"assistant.reply"}]}}`)

	items, err := decodeJSONArrayObjects(body)
	if err != nil {
		t.Fatalf("decodeJSONArrayObjects returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if got := extractStringByKeys(items[1], "kind"); got != "assistant.reply" {
		t.Fatalf("unexpected second item kind: %q", got)
	}
}

func TestDecodeJSONArrayObjectsAllowsEmptyBody(t *testing.T) {
	items, err := decodeJSONArrayObjects([]byte(""))
	if err != nil {
		t.Fatalf("decodeJSONArrayObjects returned error for empty body: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected empty slice for empty body, got %d items", len(items))
	}
}

func TestEvoClientEventsStreamURLDoesNotForceSince(t *testing.T) {
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", "http://evo-service:8048/")

	got := newEvoClient(nil).EventsStreamURL("thr/1", "")
	want := "http://evo-service:8048/threads/thr%2F1/events:stream"
	if got != want {
		t.Fatalf("unexpected thread events URL:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestEvoClientEventsStreamURLUsesStepQuery(t *testing.T) {
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", "http://evo-service:8048/")

	got := newEvoClient(nil).EventsStreamURL("thr/1", "step/collect")
	want := "http://evo-service:8048/threads/thr%2F1/events:stream?step_id=step%2Fcollect"
	if got != want {
		t.Fatalf("unexpected thread step events URL:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestParseArtifactRefSupportsVersionOnly(t *testing.T) {
	ref := parseArtifactRef("eval.dataset@v7")
	if ref.Base != "eval.dataset" || ref.Version != 7 {
		t.Fatalf("unexpected parsed artifact ref: %#v", ref)
	}
	encoded := parseArtifactRef("analysis.summary%40v3")
	if encoded.Base != "analysis.summary" || encoded.Version != 3 {
		t.Fatalf("unexpected parsed encoded artifact ref: %#v", encoded)
	}
	legacyCase := parseArtifactRef("eval.dataset[case_0001]@v7")
	if legacyCase.Base != "eval.dataset[case_0001]" || legacyCase.Version != 7 {
		t.Fatalf("case selector should remain part of unsupported artifact id: %#v", legacyCase)
	}
}

func TestFetchThreadArtifactProxyReturnsGateContentDirectly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1/gates":
			_ = json.NewEncoder(w).Encode(evoGateList{
				ThreadID: "thr_1",
				Gates: []evoGate{{
					Step:       "dataset",
					ArtifactID: "eval.dataset",
					Versions:   []int{1},
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1/gates/dataset/versions/1":
			_ = json.NewEncoder(w).Encode(evoGateContent{
				ThreadID: "thr_1",
				Step:     "dataset",
				Version:  1,
				Content: map[string]any{
					"cases": []any{
						map[string]any{"case_id": "case_0001", "question": "q1"},
						map[string]any{"case_id": "case_0002", "question": "q2"},
					},
				},
			})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodGet, "/agent/threads/thr_1/artifacts/eval.dataset@v1", nil)
	proxy, statusCode, err := fetchThreadArtifactProxy(context.Background(), req, "thr_1", "eval.dataset@v1")
	if err != nil {
		t.Fatalf("fetchThreadArtifactProxy returned error: %v", err)
	}
	if statusCode != http.StatusOK {
		t.Fatalf("unexpected status code: %d", statusCode)
	}
	body, ok := proxy.Body.(map[string]any)
	if !ok {
		t.Fatalf("expected map body, got %#v", proxy.Body)
	}
	cases, ok := body["cases"].([]any)
	if !ok || len(cases) != 2 {
		t.Fatalf("expected full evo artifact content, got %#v", body)
	}
	for _, forbidden := range []string{"data", "runtime_artifact_id", "source_artifact_id", "artifact_id", "schema"} {
		if _, ok := body[forbidden]; ok {
			t.Fatalf("artifact response should not include old envelope field %q: %#v", forbidden, body)
		}
	}
}

func TestFetchThreadArtifactProxyRejectsCaseSelector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unsupported artifact selector should not call evo, got %s %s", r.Method, r.URL.RequestURI())
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodGet, "/agent/threads/thr_1/artifacts/eval.dataset%5Bcase_0002%5D@v1", nil)
	proxy, statusCode, err := fetchThreadArtifactProxy(context.Background(), req, "thr_1", "eval.dataset[case_0002]@v1")
	if err == nil {
		t.Fatalf("expected unsupported artifact selector error")
	}
	if proxy != nil {
		t.Fatalf("expected nil proxy, got %#v", proxy)
	}
	if statusCode != http.StatusNotFound {
		t.Fatalf("unexpected status code: %d", statusCode)
	}
}

func TestFetchThreadArtifactProxyRejectsResultKindAlias(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unsupported artifact id should not call evo, got %s %s", r.Method, r.URL.RequestURI())
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodGet, "/agent/threads/thr_1/artifacts/datasets", nil)
	proxy, statusCode, err := fetchThreadArtifactProxy(context.Background(), req, "thr_1", "datasets")
	if err == nil {
		t.Fatalf("expected unsupported artifact error")
	}
	if proxy != nil {
		t.Fatalf("expected nil proxy, got %#v", proxy)
	}
	if statusCode != http.StatusNotFound {
		t.Fatalf("unexpected status code: %d", statusCode)
	}
}

func TestFetchThreadResultProxyReturnsGateContentDirectly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1/gates":
			latest := 2
			_ = json.NewEncoder(w).Encode(evoGateList{
				ThreadID: "thr_1",
				Gates: []evoGate{{
					Step:             "eval",
					ArtifactID:       "eval.summary",
					Versions:         []int{1, 2},
					EffectiveVersion: &latest,
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1/gates/eval/versions/2":
			_ = json.NewEncoder(w).Encode(evoGateContent{
				ThreadID: "thr_1",
				Step:     "eval",
				Version:  2,
				Content: map[string]any{
					"correct_rate": 0.5,
					"cases":        []any{map[string]any{"case_id": "case_1"}},
				},
			})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodGet, "/agent/threads/thr_1/results/eval-reports", nil)
	proxy, statusCode, err := fetchThreadResultProxy(context.Background(), req, "thr_1", "eval-reports", 0)
	if err != nil {
		t.Fatalf("fetchThreadResultProxy returned error: %v", err)
	}
	if statusCode != http.StatusOK {
		t.Fatalf("unexpected status code: %d", statusCode)
	}
	body, ok := proxy.Body.(map[string]any)
	if !ok {
		t.Fatalf("expected direct content map, got %#v", proxy.Body)
	}
	if body["correct_rate"] != 0.5 {
		t.Fatalf("expected evo content metrics to be returned directly: %#v", body)
	}
	for _, forbidden := range []string{"artifact_id", "runtime_artifact_id", "source_artifact_id", "schema", "data", "file_url"} {
		if _, ok := body[forbidden]; ok {
			t.Fatalf("result response should not include old envelope field %q: %#v", forbidden, body)
		}
	}
}

func TestFetchThreadResultProxyReturnsNotFoundWhenGateHasNoContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1/gates":
			_ = json.NewEncoder(w).Encode(evoGateList{
				ThreadID: "thr_1",
				Gates: []evoGate{{
					Step:       "analysis",
					ArtifactID: "analysis.summary",
				}},
			})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodGet, "/agent/threads/thr_1/results/analysis-reports", nil)
	proxy, statusCode, err := fetchThreadResultProxy(context.Background(), req, "thr_1", "analysis-reports", 0)
	if err == nil {
		t.Fatalf("expected missing gate content error")
	}
	if proxy != nil {
		t.Fatalf("expected nil proxy, got %#v", proxy)
	}
	if statusCode != http.StatusNotFound {
		t.Fatalf("unexpected status code: %d", statusCode)
	}
}

func TestGetThreadResultReturnsNotFoundWhenGateHasNoContent(t *testing.T) {
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
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1/gates":
			_ = json.NewEncoder(w).Encode(evoGateList{
				ThreadID: "thr_1",
				Gates: []evoGate{{
					Step:       "analysis",
					ArtifactID: "analysis.summary",
				}},
			})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/core/agent/threads/thr_1/results/analysis-reports", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()

	GetThreadResultAnalysisReports(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected result endpoint to return 404, status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetThreadResultReturnsUpstreamNotFoundForBadVersion(t *testing.T) {
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
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1/gates":
			_ = json.NewEncoder(w).Encode(evoGateList{
				ThreadID: "thr_1",
				Gates: []evoGate{{
					Step:       "dataset",
					ArtifactID: "eval.dataset",
					Versions:   []int{1},
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1/gates/dataset/versions/999":
			http.Error(w, `{"detail":"version not found"}`, http.StatusNotFound)
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/core/agent/threads/thr_1/results/datasets?version=999", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()

	GetThreadResultDatasets(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected upstream version 404 to stay 404, status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDownloadThreadResultCSVUsesGateContentOnlyOnDownloadPath(t *testing.T) {
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
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1/gates":
			_ = json.NewEncoder(w).Encode(evoGateList{
				ThreadID: "thr_1",
				Gates: []evoGate{{
					Step:       "abtest",
					ArtifactID: "abtest.comparison",
					Versions:   []int{3},
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1/gates/abtest/versions/3":
			_ = json.NewEncoder(w).Encode(evoGateContent{
				ThreadID: "thr_1",
				Step:     "abtest",
				Version:  3,
				Content: map[string]any{
					"case_deltas": []any{
						map[string]any{
							"case_id": "case_1",
							"outcome": "improved",
							"before":  0.2,
							"after":   0.8,
							"delta":   0.6,
						},
					},
				},
			})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/core/agent/threads/thr_1/results/abtests:download", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1", "kind": "abtests"})
	rec := httptest.NewRecorder()

	DownloadThreadResult(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected download ok, status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/csv") {
		t.Fatalf("expected csv content type, got %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") || !strings.Contains(got, "thr_1_abtests_v3.csv") {
		t.Fatalf("unexpected content disposition: %q", got)
	}
	raw := rec.Body.Bytes()
	if !bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatalf("expected utf-8 bom")
	}
	reader := csv.NewReader(bytes.NewReader(raw[3:]))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(records) != 2 || strings.Join(records[0], ",") != "after,before,case_id,delta,outcome" || records[1][2] != "case_1" {
		t.Fatalf("unexpected csv records: %#v", records)
	}
}

func TestFrontendMessageStreamDataAdaptsAssistantResponse(t *testing.T) {
	raw := `{"type":"assistant_response","thread_id":"thr_1","content":"继续执行已提交"}`

	got := frontendMessageStreamData("assistant_response", raw)
	payload := parseJSONValue(got)
	if extractStringByExactKeys(payload, "type") != "message.assistant" {
		t.Fatalf("expected frontend assistant message payload, got %s", got)
	}
	if extractStringByExactKeys(payload, "original_type") != "assistant_response" {
		t.Fatalf("expected original_type to preserve evo event type, got %s", got)
	}
	if extractStringByExactKeys(payload, "role") != "assistant" || extractStringByExactKeys(payload, "content") != "继续执行已提交" {
		t.Fatalf("expected assistant role/content fields, got %s", got)
	}
}

func TestFrontendMessageStreamDataLeavesRuntimeEventsUntouched(t *testing.T) {
	raw := `{"type":"command_applied","kind":"continue_flow"}`

	got := frontendMessageStreamData("command_applied", raw)
	if got != raw {
		t.Fatalf("expected non-display runtime event to remain unchanged:\nwant: %s\ngot:  %s", raw, got)
	}
}

func TestBuildFetchedThreadEventsPreservesRawFrames(t *testing.T) {
	events := []map[string]any{
		{"kind": "user.message", "payload": map[string]any{"content": "a"}},
		{"kind": "assistant.reply", "payload": map[string]any{"content": "b"}},
	}

	result, err := buildFetchedThreadEvents(events)
	if err != nil {
		t.Fatalf("buildFetchedThreadEvents returned error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 events, got %d", len(result))
	}
	if strings.Contains(result[0].RawFrame, `"seq"`) || strings.Contains(result[1].RawFrame, `"seq"`) {
		t.Fatalf("expected backend not to inject seq into raw frames: %#v", result)
	}
}

func TestFetchedThreadEventFromSSEFrameUsesFrameData(t *testing.T) {
	event, ok := fetchedThreadEventFromSSEFrame(&sseFrame{
		Event: "message",
		Data:  `{"kind":"task.running","payload":{"task_id":"task_1"}}`,
		Raw:   `id: 1\nevent: message\ndata: {"kind":"task.running","payload":{"task_id":"task_1"}}`,
	})
	if !ok {
		t.Fatalf("expected SSE frame to produce a fetched event")
	}
	if event.EventName != "task.running" {
		t.Fatalf("expected event name task.running, got %q", event.EventName)
	}
	if event.TaskID != "task_1" {
		t.Fatalf("expected task id task_1, got %q", event.TaskID)
	}
	if event.RawFrame != `{"kind":"task.running","payload":{"task_id":"task_1"}}` {
		t.Fatalf("expected raw frame to use data JSON, got %q", event.RawFrame)
	}
}

func TestFetchedThreadEventFromSSEFrameSkipsHeartbeatAndEmptyData(t *testing.T) {
	cases := []*sseFrame{
		{Event: "heartbeat", Data: `{}`, Raw: "event: heartbeat\ndata: {}"},
		{Event: "message", Data: `{}`, Raw: "data: {}"},
		{Event: "message", Data: `{"event":"heartbeat","ts":"2026-04-29T09:32:55Z"}`, Raw: `data: {"event":"heartbeat"}`},
	}

	for _, frame := range cases {
		if event, ok := fetchedThreadEventFromSSEFrame(frame); ok {
			t.Fatalf("expected heartbeat/empty frame to be skipped, got %#v", event)
		}
	}
}

func TestBuildFetchedThreadEventsSkipsHeartbeatAndEmptyItems(t *testing.T) {
	events := []map[string]any{
		{},
		{"event": "heartbeat"},
		{"kind": "dataset_gen.start", "task_id": "task_1"},
	}

	result, err := buildFetchedThreadEvents(events)
	if err != nil {
		t.Fatalf("buildFetchedThreadEvents returned error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected only one valid event, got %#v", result)
	}
	if result[0].EventName != "dataset_gen.start" || result[0].TaskID != "task_1" {
		t.Fatalf("unexpected valid event: %#v", result[0])
	}
}

func TestShouldKeepThreadFlowStreamAliveKeepsRunningAndPending(t *testing.T) {
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
		got := shouldKeepThreadFlowStreamAlive(&threadFlowStatusResponse{Status: tc.status})
		if got != tc.want {
			t.Fatalf("shouldKeepThreadFlowStreamAlive(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
	if shouldKeepThreadFlowStreamAlive(nil) {
		t.Fatalf("nil flow status must not keep stream alive")
	}
}

func TestReadSSEFrameParsesMultilineData(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("event: answer\ndata: {\"delta\":\"hello\"}\ndata: {\"delta\":\"world\"}\n\n"))

	frame, err := readSSEFrame(reader)
	if err != nil {
		t.Fatalf("readSSEFrame returned error: %v", err)
	}
	if frame.Event != "answer" {
		t.Fatalf("expected event answer, got %q", frame.Event)
	}
	if frame.Data != "{\"delta\":\"hello\"}\n{\"delta\":\"world\"}" {
		t.Fatalf("unexpected frame data: %q", frame.Data)
	}
}

func TestReadThreadEventSSEFrameAcceptsLineDelimitedData(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(
		"data: {\"kind\":\"task.running\",\"task_id\":\"task_1\"}\n" +
			"data: {\"kind\":\"task.done\",\"task_id\":\"task_1\"}\n",
	))

	first, err := readThreadEventSSEFrame(reader)
	if err != nil {
		t.Fatalf("read first thread event frame: %v", err)
	}
	if first.Data != "{\"kind\":\"task.running\",\"task_id\":\"task_1\"}" {
		t.Fatalf("unexpected first frame data: %q", first.Data)
	}

	second, err := readThreadEventSSEFrame(reader)
	if err != nil {
		t.Fatalf("read second thread event frame: %v", err)
	}
	if second.Data != "{\"kind\":\"task.done\",\"task_id\":\"task_1\"}" {
		t.Fatalf("unexpected second frame data: %q", second.Data)
	}
}

func TestBuildGateCSVUsesKnownRowsAndStableHeaders(t *testing.T) {
	csvBytes, rowCount, err := buildGateCSV("datasets", map[string]any{
		"cases": []any{
			map[string]any{
				"question":      "q1",
				"reference_doc": []any{"a.pdf", "b.pdf"},
				"score":         1.5,
				"meta":          map[string]any{"source": "doc"},
			},
			map[string]any{
				"question":      "q2",
				"reference_doc": []any{"c.pdf"},
				"score":         2,
				"extra":         true,
			},
		},
	})
	if err != nil {
		t.Fatalf("buildGateCSV returned error: %v", err)
	}
	if rowCount != 2 {
		t.Fatalf("expected row count 2, got %d", rowCount)
	}
	if !bytes.HasPrefix(csvBytes, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatalf("expected utf-8 bom")
	}

	reader := csv.NewReader(bytes.NewReader(csvBytes[3:]))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected header plus 2 rows, got %d", len(records))
	}
	expectedHeader := []string{"extra", "meta", "question", "reference_doc", "score"}
	if strings.Join(records[0], ",") != strings.Join(expectedHeader, ",") {
		t.Fatalf("unexpected header: %#v", records[0])
	}
	if records[1][3] != "a.pdf; b.pdf" {
		t.Fatalf("expected list cell to be joined inline, got %q", records[1][3])
	}
	if records[1][1] != `{"source":"doc"}` {
		t.Fatalf("expected object cell to be json encoded, got %q", records[1][1])
	}
}

func TestBuildGateCSVNormalizesMultilineCells(t *testing.T) {
	csvBytes, rowCount, err := buildGateCSV("eval-reports", map[string]any{
		"rows": []any{
			map[string]any{
				"answer":   "line 1\r\nline 2\n\nline 3\x00\x01",
				"segments": []any{"chunk 1\nchunk 2", "chunk 3"},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildGateCSV returned error: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected row count 1, got %d", rowCount)
	}
	if bytes.ContainsAny(csvBytes, "\r\x00\x01") || bytes.Count(csvBytes, []byte("\n")) != 2 {
		t.Fatalf("expected csv to contain record separators only and no control characters, got %q", string(csvBytes))
	}

	reader := csv.NewReader(bytes.NewReader(csvBytes[3:]))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if records[1][0] != "line 1 line 2 line 3" {
		t.Fatalf("expected multiline string to be normalized, got %q", records[1][0])
	}
	if records[1][1] != "chunk 1 chunk 2; chunk 3" {
		t.Fatalf("expected multiline list values to be normalized, got %q", records[1][1])
	}
}

func TestBuildGateCSVProtectsFormulaCells(t *testing.T) {
	csvBytes, rowCount, err := buildGateCSV("datasets", map[string]any{
		"cases": []any{
			map[string]any{
				"answer":  "=HYPERLINK(\"http://example.com\")",
				"comment": "  @SUM(1,2)",
				"score":   -1,
			},
		},
	})
	if err != nil {
		t.Fatalf("buildGateCSV returned error: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected row count 1, got %d", rowCount)
	}
	reader := csv.NewReader(bytes.NewReader(csvBytes[3:]))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if records[1][0] != `'=HYPERLINK("http://example.com")` {
		t.Fatalf("expected formula cell to be prefixed, got %#v", records)
	}
	if records[1][1] != "'@SUM(1,2)" {
		t.Fatalf("expected trimmed formula cell to be prefixed, got %#v", records)
	}
	if records[1][2] != "'-1" {
		t.Fatalf("expected numeric formula prefix to be prefixed, got %#v", records)
	}
}

func TestBuildGateCSVUsesAbtestCaseDeltas(t *testing.T) {
	csvBytes, rowCount, err := buildGateCSV("abtests", map[string]any{
		"case_deltas": []any{
			map[string]any{
				"case_id":           "case_1",
				"outcome":           "improved",
				"before":            0.2,
				"after":             0.7,
				"delta":             0.5,
				"baseline_quality":  "bad",
				"candidate_quality": "good",
			},
		},
	})
	if err != nil {
		t.Fatalf("buildGateCSV returned error: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected row count 1, got %d", rowCount)
	}
	reader := csv.NewReader(bytes.NewReader(csvBytes[3:]))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	wantHeader := "after,baseline_quality,before,candidate_quality,case_id,delta,outcome"
	if strings.Join(records[0], ",") != wantHeader {
		t.Fatalf("unexpected abtest header: %#v", records[0])
	}
	if strings.Join(records[1], ",") != "0.7,bad,0.2,good,case_1,0.5,improved" {
		t.Fatalf("unexpected abtest row: %#v", records[1])
	}
}

func TestBuildGateCSVUsesRepairDiffMap(t *testing.T) {
	csvBytes, rowCount, err := buildGateCSV("diffs", map[string]any{
		"run_id":              "run_1",
		"algo_id":             "base",
		"candidate_algo_id":   "candidate",
		"status":              "verified",
		"diff":                map[string]any{"b.go": "patch b", "a.go": "patch a"},
		"ignored_for_csv_row": true,
	})
	if err != nil {
		t.Fatalf("buildGateCSV returned error: %v", err)
	}
	if rowCount != 2 {
		t.Fatalf("expected row count 2, got %d", rowCount)
	}
	reader := csv.NewReader(bytes.NewReader(csvBytes[3:]))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	wantHeader := "algo_id,candidate_algo_id,diff,file,run_id,status"
	if strings.Join(records[0], ",") != wantHeader {
		t.Fatalf("unexpected repair diff header: %#v", records[0])
	}
	if strings.Join(records[1], ",") != "base,candidate,patch a,a.go,run_1,verified" {
		t.Fatalf("unexpected first repair diff row: %#v", records[1])
	}
}

func TestBuildGateCSVFallsBackToTopLevelObject(t *testing.T) {
	csvBytes, rowCount, err := buildGateCSV("diffs", map[string]any{
		"patch":  "diff --git a/a b/a",
		"status": "verified",
	})
	if err != nil {
		t.Fatalf("buildGateCSV returned error: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected row count 1, got %d", rowCount)
	}
	reader := csv.NewReader(bytes.NewReader(csvBytes[3:]))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if strings.Join(records[0], ",") != "patch,status" {
		t.Fatalf("unexpected fallback header: %#v", records[0])
	}
}

func TestBuildGateCSVPreservesHeaderWhitespaceForCellLookup(t *testing.T) {
	csvBytes, rowCount, err := buildGateCSV("datasets", map[string]any{
		"cases": []any{
			map[string]any{" case_id ": "case_1", "question": "q1"},
		},
	})
	if err != nil {
		t.Fatalf("buildGateCSV returned error: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected row count 1, got %d", rowCount)
	}
	reader := csv.NewReader(bytes.NewReader(csvBytes[3:]))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if records[0][0] != " case_id " || records[1][0] != "case_1" {
		t.Fatalf("expected spaced header to preserve cell value, got %#v", records)
	}
}

func TestBuildGateCSVSkipsEmptyObjectArrayFallback(t *testing.T) {
	csvBytes, rowCount, err := buildGateCSV("analysis-reports", map[string]any{
		"empty": []any{},
		"items": []any{
			map[string]any{"case_id": "case_1", "label": "hard"},
		},
	})
	if err != nil {
		t.Fatalf("buildGateCSV returned error: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected row count 1, got %d", rowCount)
	}
	reader := csv.NewReader(bytes.NewReader(csvBytes[3:]))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if strings.Join(records[0], ",") != "empty,items" || !strings.Contains(records[1][1], "case_1") {
		t.Fatalf("unexpected contract fallback records: %#v", records)
	}
}

func TestSaveThreadRecordKeepsDuplicateThreadEventFrames(t *testing.T) {
	db := newAgentTestDB(t)

	first, created, err := saveThreadRecord(db.DB, "thr_1", "round_1", "task_1", streamKindThreadEvent, "dataset.complete", `{"seq":1}`, `{"seq":1}`)
	if err != nil {
		t.Fatalf("first save returned error: %v", err)
	}
	if !created {
		t.Fatalf("expected first save to create record")
	}

	second, created, err := saveThreadRecord(db.DB, "thr_1", "round_1", "task_1", streamKindThreadEvent, "dataset.complete", `{"seq":1}`, `{"seq":1}`)
	if err != nil {
		t.Fatalf("second save returned error: %v", err)
	}
	if !created {
		t.Fatalf("expected duplicate thread event frame to be preserved")
	}
	if first.ID == second.ID {
		t.Fatalf("expected duplicate thread event frame to get a new record id")
	}
}

func TestSaveStepThreadEventRecordUsesStepAndStableRecordKey(t *testing.T) {
	db := newAgentTestDB(t)

	first, created, err := saveThreadRecordWithOptions(db.DB, "thr_1", "", "task_1", streamKindThreadEvent, "dataset.complete", `{"seq":1}`, `{"seq":1}`, saveThreadRecordOptions{
		StepID:    "collect_material",
		RecordKey: sha256Hex("collect_material\x00evt_1"),
	})
	if err != nil {
		t.Fatalf("first save returned error: %v", err)
	}
	if !created {
		t.Fatalf("expected first save to create record")
	}
	if first.StepID != "collect_material" {
		t.Fatalf("expected step_id to be persisted, got %q", first.StepID)
	}

	second, created, err := saveThreadRecordWithOptions(db.DB, "thr_1", "", "task_1", streamKindThreadEvent, "dataset.complete", `{"seq":1}`, `{"seq":1}`, saveThreadRecordOptions{
		StepID:    "collect_material",
		RecordKey: sha256Hex("collect_material\x00evt_1"),
	})
	if err != nil {
		t.Fatalf("second save returned error: %v", err)
	}
	if created {
		t.Fatalf("expected replayed step event frame to reuse existing record")
	}
	if second.ID != first.ID {
		t.Fatalf("expected existing record id %q, got %q", first.ID, second.ID)
	}
}

func TestSaveThreadRecordKeepsDuplicateMessageFrames(t *testing.T) {
	db := newAgentTestDB(t)

	first, created, err := saveThreadRecord(db.DB, "thr_1", "round_1", "task_1", streamKindMessage, "message", `{"delta":"same"}`, `data: {"delta":"same"}`)
	if err != nil {
		t.Fatalf("first save returned error: %v", err)
	}
	if !created {
		t.Fatalf("expected first save to create record")
	}

	second, created, err := saveThreadRecord(db.DB, "thr_1", "round_1", "task_1", streamKindMessage, "message", `{"delta":"same"}`, `data: {"delta":"same"}`)
	if err != nil {
		t.Fatalf("second save returned error: %v", err)
	}
	if !created {
		t.Fatalf("expected duplicate message frame to be preserved")
	}
	if first.ID == second.ID {
		t.Fatalf("expected duplicate message frame to get a new record id")
	}
}

func TestUpdateThreadStepFromEventMaintainsSummary(t *testing.T) {
	db := newAgentTestDB(t)

	if err := updateThreadStepFromEvent(db.DB, "thr_1", "collect_material", fetchedThreadEvent{
		TaskID:    "task_1",
		EventName: "step.started",
		RawFrame:  `{"step_title":"Collect material","step_order":2,"status":"running"}`,
	}); err != nil {
		t.Fatalf("update running step returned error: %v", err)
	}
	if err := updateThreadStepFromEvent(db.DB, "thr_1", "collect_material", fetchedThreadEvent{
		TaskID:    "task_1",
		EventName: "step.completed",
		RawFrame:  `{"status":"completed"}`,
	}); err != nil {
		t.Fatalf("update completed step returned error: %v", err)
	}

	var step orm.AgentThreadStep
	if err := db.DB.Where("thread_id = ? AND step_id = ?", "thr_1", "collect_material").First(&step).Error; err != nil {
		t.Fatalf("load step: %v", err)
	}
	if step.Title != "Collect material" {
		t.Fatalf("expected title to be preserved, got %q", step.Title)
	}
	if step.Status != "succeeded" || step.Active {
		t.Fatalf("expected succeeded inactive step, got status=%q active=%v", step.Status, step.Active)
	}
	if step.EventCount != 2 {
		t.Fatalf("expected event_count=2, got %d", step.EventCount)
	}
	if step.OrderIndex != 2 {
		t.Fatalf("expected order_index=2, got %d", step.OrderIndex)
	}
	if step.EndedAt == nil {
		t.Fatalf("expected ended_at to be set")
	}
}

func TestUpdateThreadStepFromEventDoneCompletesRunningStep(t *testing.T) {
	db := newAgentTestDB(t)

	if err := updateThreadStepFromEvent(db.DB, "thr_1", "step_1", fetchedThreadEvent{
		EventName: "dataset.start",
		RawFrame:  `{"status":"running","step_run_id":"step_1"}`,
	}); err != nil {
		t.Fatalf("update running step returned error: %v", err)
	}
	if err := updateThreadStepFromEvent(db.DB, "thr_1", "step_1", fetchedThreadEvent{
		EventName: "done",
		RawFrame:  `{"type":"done","status":"running","step_run_id":"step_1","next_step_run_id":"step_2"}`,
	}); err != nil {
		t.Fatalf("update done step returned error: %v", err)
	}

	var step orm.AgentThreadStep
	if err := db.DB.Where("thread_id = ? AND step_id = ?", "thr_1", "step_1").First(&step).Error; err != nil {
		t.Fatalf("load step: %v", err)
	}
	if step.Status != "succeeded" || step.Active {
		t.Fatalf("expected done event to complete step, got status=%q active=%v", step.Status, step.Active)
	}
	if step.EventCount != 2 {
		t.Fatalf("expected event_count=2, got %d", step.EventCount)
	}
	if step.NextStepRunID != "step_2" {
		t.Fatalf("expected next_step_run_id step_2, got %q", step.NextStepRunID)
	}
	if err := updateThreadStepFromEvent(db.DB, "thr_1", "step_1", fetchedThreadEvent{
		EventName: "done",
		RawFrame:  `{"type":"done","status":"running","step_run_id":"step_1","next_step_run_id":"step_3"}`,
	}); err != nil {
		t.Fatalf("update duplicate done step returned error: %v", err)
	}
	if err := db.DB.Where("thread_id = ? AND step_id = ?", "thr_1", "step_1").First(&step).Error; err != nil {
		t.Fatalf("reload step: %v", err)
	}
	if step.NextStepRunID != "step_2" {
		t.Fatalf("expected first next_step_run_id to be preserved, got %q", step.NextStepRunID)
	}
}

func TestUpdateThreadStepFromEventKeepsOnlyLatestRunningStepActive(t *testing.T) {
	db := newAgentTestDB(t)

	if err := updateThreadStepFromEvent(db.DB, "thr_1", "step_1", fetchedThreadEvent{
		EventName: "dataset.start",
		RawFrame:  `{"status":"running","step_run_id":"step_1"}`,
	}); err != nil {
		t.Fatalf("update first running step returned error: %v", err)
	}
	if err := updateThreadStepFromEvent(db.DB, "thr_1", "step_2", fetchedThreadEvent{
		EventName: "eval.start",
		RawFrame:  `{"status":"running","step_run_id":"step_2"}`,
	}); err != nil {
		t.Fatalf("update second running step returned error: %v", err)
	}

	var steps []orm.AgentThreadStep
	if err := db.DB.Where("thread_id = ?", "thr_1").Order("step_id").Find(&steps).Error; err != nil {
		t.Fatalf("load steps: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if steps[0].StepID != "step_1" || steps[0].Status != "succeeded" || steps[0].Active {
		t.Fatalf("expected first step to be inactive succeeded, got %#v", steps[0])
	}
	if steps[1].StepID != "step_2" || steps[1].Status != "running" || !steps[1].Active {
		t.Fatalf("expected second step to be active running, got %#v", steps[1])
	}
}

func TestListThreadStepsReturnsActiveStep(t *testing.T) {
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
	if err := db.DB.Create(&[]orm.AgentThreadStep{
		{ThreadID: "thr_1", StepID: "collect_material", Title: "Collect", Status: "succeeded", Active: false, OrderIndex: 1, EventCount: 2, NextStepRunID: "generate_image", CreatedAt: now, UpdatedAt: now},
		{ThreadID: "thr_1", StepID: "generate_image", Title: "Generate", Status: "running", Active: true, OrderIndex: 2, EventCount: 3, CreatedAt: now, UpdatedAt: now.Add(time.Second)},
	}).Error; err != nil {
		t.Fatalf("create steps: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/agent/threads/thr_1/steps", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	ListThreadSteps(rec, req)

	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			ThreadID     string               `json:"thread_id"`
			ActiveStepID string               `json:"active_step_id"`
			Items        []threadStepResponse `json:"items"`
			TotalSize    int                  `json:"total_size"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rec.Code != http.StatusOK || response.Code != 0 {
		t.Fatalf("expected ok response, status=%d code=%d message=%s", rec.Code, response.Code, response.Message)
	}
	if response.Data.ActiveStepID != "generate_image" {
		t.Fatalf("expected active_step_id generate_image, got %q", response.Data.ActiveStepID)
	}
	if response.Data.TotalSize != 2 || len(response.Data.Items) != 2 {
		t.Fatalf("unexpected step list response: %#v", response.Data)
	}
	if response.Data.Items[0].NextStepRunID != "generate_image" {
		t.Fatalf("expected first step next_step_run_id generate_image, got %q", response.Data.Items[0].NextStepRunID)
	}
}

func TestListThreadStepRecordsFiltersStepThreadEvents(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:     "thr_1",
		Status:       "completed",
		CreateUserID: "u1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create thread: %v", err)
	}
	records := []orm.AgentThreadRecord{
		{ID: "record_1", ThreadID: "thr_1", StepID: "collect_material", StreamKind: streamKindThreadEvent, RecordKey: "rk1", EventName: "step.started", PayloadText: `{"seq":1}`, RawFrame: `{"seq":1}`, CreatedAt: now, UpdatedAt: now},
		{ID: "record_2", ThreadID: "thr_1", StepID: "collect_material", StreamKind: streamKindMessage, RecordKey: "rk2", EventName: "message", PayloadText: `{"seq":2}`, RawFrame: `data: {"seq":2}`, CreatedAt: now, UpdatedAt: now},
		{ID: "record_3", ThreadID: "thr_1", StepID: "generate_image", StreamKind: streamKindThreadEvent, RecordKey: "rk3", EventName: "step.started", PayloadText: `{"seq":3}`, RawFrame: `{"seq":3}`, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.DB.Create(&records).Error; err != nil {
		t.Fatalf("create records: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/agent/threads/thr_1/steps/collect_material/records", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1", "step_id": "collect_material"})
	rec := httptest.NewRecorder()
	ListThreadStepRecords(rec, req)

	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			ThreadID string           `json:"thread_id"`
			StepID   string           `json:"step_id"`
			Items    []recordResponse `json:"items"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rec.Code != http.StatusOK || response.Code != 0 {
		t.Fatalf("expected ok response, status=%d code=%d message=%s", rec.Code, response.Code, response.Message)
	}
	if response.Data.StepID != "collect_material" {
		t.Fatalf("expected step_id collect_material, got %q", response.Data.StepID)
	}
	if len(response.Data.Items) != 1 || response.Data.Items[0].ID != "record_1" {
		t.Fatalf("unexpected step records: %#v", response.Data.Items)
	}
	if response.Data.Items[0].StreamKind != streamKindThreadEvent {
		t.Fatalf("expected only thread_event records, got %q", response.Data.Items[0].StreamKind)
	}
}

func TestBuildReplayFrameForMessageOmitsSSEIDAndUsesDataOnly(t *testing.T) {
	record := orm.AgentThreadRecord{
		ID:          "0001",
		ThreadID:    "thr_1",
		RoundID:     "round_1",
		StreamKind:  streamKindMessage,
		EventName:   "message",
		PayloadText: `{"delta":"hi"}`,
		RawFrame:    "id: upstream-1\nevent: message\ndata: {\"delta\":\"hi\"}",
		CreatedAt:   time.Now().UTC(),
	}

	frame := buildReplayFrame(record)
	expected := "data: {\"delta\":\"hi\"}\n\n"
	if frame != expected {
		t.Fatalf("unexpected message replay frame:\nwant: %q\ngot:  %q", expected, frame)
	}
	if strings.Contains(frame, "\nid:") || strings.HasPrefix(frame, "id:") || strings.Contains(frame, "\nevent:") || strings.HasPrefix(frame, "event:") {
		t.Fatalf("message replay frame must only include data: %q", frame)
	}
}

func TestShouldSkipStreamRecordSkipsMessageHeartbeatAndEmptyData(t *testing.T) {
	cases := []orm.AgentThreadRecord{
		{StreamKind: streamKindMessage, EventName: "heartbeat", PayloadText: `{}`, RawFrame: "event: heartbeat\ndata: {}"},
		{StreamKind: streamKindMessage, EventName: "message", PayloadText: `{}`, RawFrame: "data: {}"},
		{StreamKind: streamKindMessage, EventName: "message", PayloadText: `[]`, RawFrame: "data: []"},
		{StreamKind: streamKindMessage, EventName: "message", PayloadText: `[DONE]`, RawFrame: "data: [DONE]"},
	}

	for _, record := range cases {
		if !shouldSkipStreamRecord(record) {
			t.Fatalf("expected message stream record to be skipped: %#v", record)
		}
	}

	valid := orm.AgentThreadRecord{
		StreamKind:  streamKindMessage,
		EventName:   "message",
		PayloadText: `{"delta":"hi"}`,
		RawFrame:    `data: {"delta":"hi"}`,
	}
	if shouldSkipStreamRecord(valid) {
		t.Fatalf("expected valid message stream record to be returned")
	}
}

func TestBuildReplayFrameForThreadEventUsesJSONLineData(t *testing.T) {
	record := orm.AgentThreadRecord{
		ID:         "0001",
		ThreadID:   "thr_1",
		TaskID:     "task_1",
		StreamKind: streamKindThreadEvent,
		RawFrame:   `{"seq":1,"kind":"user.message"}`,
		CreatedAt:  time.Now().UTC(),
	}

	frame := buildReplayFrame(record)
	expected := "data: {\"seq\":1,\"kind\":\"user.message\"}\n\n"
	if frame != expected {
		t.Fatalf("unexpected task event replay frame:\nwant: %q\ngot:  %q", expected, frame)
	}
	if strings.Contains(frame, "\nid:") || strings.HasPrefix(frame, "id:") {
		t.Fatalf("thread event replay frame must not include SSE id: %q", frame)
	}
}

func TestBuildThreadEventFrameOmitsSSEID(t *testing.T) {
	frame := buildThreadEventFrame(`{"seq":1,"kind":"dataset_gen.start"}`)
	expected := "data: {\"seq\":1,\"kind\":\"dataset_gen.start\"}\n\n"
	if frame != expected {
		t.Fatalf("unexpected thread event frame:\nwant: %q\ngot:  %q", expected, frame)
	}
	if strings.Contains(frame, "\nid:") || strings.HasPrefix(frame, "id:") {
		t.Fatalf("thread event frame must not include SSE id: %q", frame)
	}
}

func TestStreamUpstreamThreadEventsForwardsDuplicateFrames(t *testing.T) {
	db := newAgentTestDB(t)
	rec := httptest.NewRecorder()
	body := strings.NewReader(strings.Join([]string{
		"event: message\ndata: {\"kind\":\"task.running\",\"task_id\":\"task_1\"}\n\n",
		"event: message\ndata: {\"kind\":\"task.running\",\"task_id\":\"task_1\"}\n\n",
	}, ""))

	var lastUpstreamEventID string
	if err := streamUpstreamThreadEvents(context.Background(), rec, rec, db.DB, "thr_1", "", body, &lastUpstreamEventID, nil); err != nil {
		t.Fatalf("streamUpstreamThreadEvents returned error: %v", err)
	}

	want := "data: {\"kind\":\"task.running\",\"task_id\":\"task_1\"}\n\n" +
		"data: {\"kind\":\"task.running\",\"task_id\":\"task_1\"}\n\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("unexpected forwarded stream:\nwant: %q\ngot:  %q", want, got)
	}

	var count int64
	if err := db.DB.Model(&orm.AgentThreadRecord{}).
		Where("thread_id = ? AND stream_kind = ?", "thr_1", streamKindThreadEvent).
		Count(&count).Error; err != nil {
		t.Fatalf("count saved records: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected both duplicate thread event frames to be saved, got %d", count)
	}
}

func TestStreamUpstreamThreadEventsTracksUpstreamIDWithoutForwarding(t *testing.T) {
	db := newAgentTestDB(t)
	rec := httptest.NewRecorder()
	body := strings.NewReader("id: 339\nevent: message\ndata: {\"kind\":\"task.running\",\"task_id\":\"task_1\"}\n\n")

	var lastUpstreamEventID string
	if err := streamUpstreamThreadEvents(context.Background(), rec, rec, db.DB, "thr_1", "", body, &lastUpstreamEventID, nil); err != nil {
		t.Fatalf("streamUpstreamThreadEvents returned error: %v", err)
	}

	want := "data: {\"kind\":\"task.running\",\"task_id\":\"task_1\"}\n\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("unexpected forwarded stream:\nwant: %q\ngot:  %q", want, got)
	}
	if lastUpstreamEventID != "339" {
		t.Fatalf("unexpected last upstream event id: %q", lastUpstreamEventID)
	}
}

func TestStreamUpstreamThreadEventsFiltersRequestedStep(t *testing.T) {
	db := newAgentTestDB(t)
	rec := httptest.NewRecorder()
	stepOne := `{"type":"dataset.start","status":"running","step_run_id":"step_1"}`
	stepTwo := `{"type":"eval.start","status":"running","step_run_id":"step_2"}`
	stepTwoDone := `{"type":"done","status":"running","step_run_id":"step_2","next_step_run_id":"step_3"}`
	body := strings.NewReader(strings.Join([]string{
		"id: 1\nevent: message\ndata: " + stepOne + "\n\n",
		"id: 2\nevent: message\ndata: " + stepTwo + "\n\n",
		"id: 3\nevent: message\ndata: " + stepTwoDone + "\n\n",
	}, ""))

	var lastUpstreamEventID string
	err := streamUpstreamThreadEvents(context.Background(), rec, rec, db.DB, "thr_1", "step_2", body, &lastUpstreamEventID, nil)
	if !errors.Is(err, errThreadEventsDone) {
		t.Fatalf("expected done stop error, got %v", err)
	}

	want := "data: " + stepTwo + "\n\n" +
		"data: " + stepTwoDone + "\n\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("unexpected forwarded stream:\nwant: %q\ngot:  %q", want, got)
	}
	if strings.Contains(rec.Body.String(), "step_1") {
		t.Fatalf("expected step_1 frame to be filtered, got %q", rec.Body.String())
	}
	if lastUpstreamEventID != "3" {
		t.Fatalf("unexpected last upstream event id: %q", lastUpstreamEventID)
	}

	var count int64
	if err := db.DB.Model(&orm.AgentThreadRecord{}).
		Where("thread_id = ? AND step_id = ?", "thr_1", "step_1").
		Count(&count).Error; err != nil {
		t.Fatalf("count step_1 records: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no step_1 records, got %d", count)
	}
	if err := db.DB.Model(&orm.AgentThreadRecord{}).
		Where("thread_id = ? AND step_id = ?", "thr_1", "step_2").
		Count(&count).Error; err != nil {
		t.Fatalf("count step_2 records: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 step_2 records, got %d", count)
	}

	var step orm.AgentThreadStep
	if err := db.DB.Where("thread_id = ? AND step_id = ?", "thr_1", "step_2").First(&step).Error; err != nil {
		t.Fatalf("load step_2: %v", err)
	}
	if step.Status != "succeeded" || step.Active || step.EventCount != 2 {
		t.Fatalf("expected step_2 to be completed from filtered stream, got %#v", step)
	}
	if step.NextStepRunID != "step_3" {
		t.Fatalf("expected step_2 next_step_run_id step_3, got %q", step.NextStepRunID)
	}
}

func TestStreamUpstreamThreadEventsAssignsRequestedStepWhenFrameOmitsStep(t *testing.T) {
	db := newAgentTestDB(t)
	rec := httptest.NewRecorder()
	body := strings.NewReader("id: 2\nevent: message\ndata: {\"type\":\"eval.start\",\"status\":\"running\"}\n\n")

	var lastUpstreamEventID string
	if err := streamUpstreamThreadEvents(context.Background(), rec, rec, db.DB, "thr_1", "step_2", body, &lastUpstreamEventID, nil); err != nil {
		t.Fatalf("streamUpstreamThreadEvents returned error: %v", err)
	}
	if lastUpstreamEventID != "2" {
		t.Fatalf("unexpected last upstream event id: %q", lastUpstreamEventID)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"step_id":"step_2"`) || !strings.Contains(got, `"step_run_id":"step_2"`) {
		t.Fatalf("expected requested step id to be injected into downstream event, got %q", got)
	}

	var record orm.AgentThreadRecord
	if err := db.DB.Where("thread_id = ? AND step_id = ?", "thr_1", "step_2").First(&record).Error; err != nil {
		t.Fatalf("load step_2 record: %v", err)
	}
	if !strings.Contains(record.PayloadText, `"step_id":"step_2"`) {
		t.Fatalf("expected persisted payload to include step_id, got %q", record.PayloadText)
	}
}

func TestStreamThreadStepEventsDoesNotCreateStepBeforeEvents(t *testing.T) {
	db := newAgentTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	if err := db.DB.Create(&orm.AgentThread{
		ThreadID:     "thr_1",
		Status:       "completed",
		CreateUserID: "u1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create thread: %v", err)
	}

	var mu sync.Mutex
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1/events:stream" && r.URL.Query().Get("step_id") == "step_1":
			http.Error(w, `{"detail":"closed"}`, http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1":
			_ = json.NewEncoder(w).Encode(evoThread{ThreadID: "thr_1", Status: "ended"})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/core/agent/threads/thr_1/events/step_1", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1", "step_id": "step_1"})
	rec := httptest.NewRecorder()
	StreamThreadStepEvents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected stream response header, status=%d body=%s", rec.Code, rec.Body.String())
	}
	var count int64
	if err := db.DB.Model(&orm.AgentThreadStep{}).Where("thread_id = ?", "thr_1").Count(&count).Error; err != nil {
		t.Fatalf("count steps: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected opening step events not to create step rows, got %d", count)
	}

	mu.Lock()
	gotCalls := append([]string(nil), calls...)
	mu.Unlock()
	wantCalls := []string{
		"GET /threads/thr_1/events:stream?step_id=step_1",
		"GET /threads/thr_1",
	}
	if fmt.Sprint(gotCalls) != fmt.Sprint(wantCalls) {
		t.Fatalf("unexpected upstream calls: want %v got %v", wantCalls, gotCalls)
	}
}

func TestStreamUpstreamThreadEventsStopsAfterDoneType(t *testing.T) {
	db := newAgentTestDB(t)
	rec := httptest.NewRecorder()
	done := `{"type":"done","status":"success"}`
	body := strings.NewReader(strings.Join([]string{
		"id: 41\nevent: message\ndata: {\"kind\":\"task.running\",\"task_id\":\"task_1\"}\n\n",
		"id: 42\nevent: message\ndata: " + done + "\n\n",
		"id: 43\nevent: message\ndata: {\"kind\":\"task.after\",\"task_id\":\"task_1\"}\n\n",
	}, ""))

	var lastUpstreamEventID string
	err := streamUpstreamThreadEvents(context.Background(), rec, rec, db.DB, "thr_1", "", body, &lastUpstreamEventID, nil)
	if !errors.Is(err, errThreadEventsDone) {
		t.Fatalf("expected done stop error, got %v", err)
	}

	want := "data: {\"kind\":\"task.running\",\"task_id\":\"task_1\"}\n\n" +
		"data: " + done + "\n\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("unexpected forwarded stream:\nwant: %q\ngot:  %q", want, got)
	}
	if strings.Contains(rec.Body.String(), "task.after") {
		t.Fatalf("expected stream to stop before later frames, got %q", rec.Body.String())
	}
	if lastUpstreamEventID != "42" {
		t.Fatalf("unexpected last upstream event id: %q", lastUpstreamEventID)
	}
}

func TestStreamUpstreamThreadEventsContinuesAfterRunCompletedUntilDone(t *testing.T) {
	db := newAgentTestDB(t)
	rec := httptest.NewRecorder()
	completed := `{"type":"artifact.run.completed","event_type":"run.completed","payload":{"event_type":"run.completed","raw_event":{"event_type":"run.completed"}}}`
	normalizedCompleted := `{"event":"run.completed","event_type":"run.completed","flow_kind":"run.completed","payload":{"event_type":"run.completed","raw_event":{"event_type":"run.completed"}},"type":"artifact.run.completed"}`
	done := `{"type":"done","status":"success"}`
	body := strings.NewReader(strings.Join([]string{
		"id: 41\nevent: message\ndata: {\"kind\":\"task.running\",\"task_id\":\"task_1\"}\n\n",
		"id: 42\nevent: message\ndata: " + completed + "\n\n",
		"id: 43\nevent: message\ndata: {\"kind\":\"task.after\",\"task_id\":\"task_1\"}\n\n",
		"id: 44\nevent: message\ndata: " + done + "\n\n",
		"id: 45\nevent: message\ndata: {\"kind\":\"task.later\",\"task_id\":\"task_1\"}\n\n",
	}, ""))

	var lastUpstreamEventID string
	err := streamUpstreamThreadEvents(context.Background(), rec, rec, db.DB, "thr_1", "", body, &lastUpstreamEventID, nil)
	if !errors.Is(err, errThreadEventsDone) {
		t.Fatalf("expected done stop error, got %v", err)
	}

	want := "data: {\"kind\":\"task.running\",\"task_id\":\"task_1\"}\n\n" +
		"data: " + normalizedCompleted + "\n\n" +
		"data: {\"kind\":\"task.after\",\"task_id\":\"task_1\"}\n\n" +
		"data: " + done + "\n\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("unexpected forwarded stream:\nwant: %q\ngot:  %q", want, got)
	}
	if strings.Contains(rec.Body.String(), "task.later") {
		t.Fatalf("expected stream to stop after done, got %q", rec.Body.String())
	}
	if lastUpstreamEventID != "44" {
		t.Fatalf("unexpected last upstream event id: %q", lastUpstreamEventID)
	}
}

func TestStreamUpstreamThreadEventsForwardsLineDelimitedFrames(t *testing.T) {
	db := newAgentTestDB(t)
	rec := httptest.NewRecorder()
	body := strings.NewReader(strings.Join([]string{
		"data: {\"kind\":\"task.running\",\"task_id\":\"task_1\"}\n",
		"data: {\"kind\":\"task.done\",\"task_id\":\"task_1\"}\n",
	}, ""))

	var lastUpstreamEventID string
	if err := streamUpstreamThreadEvents(context.Background(), rec, rec, db.DB, "thr_1", "", body, &lastUpstreamEventID, nil); err != nil {
		t.Fatalf("streamUpstreamThreadEvents returned error: %v", err)
	}

	want := "data: {\"kind\":\"task.running\",\"task_id\":\"task_1\"}\n\n" +
		"data: {\"kind\":\"task.done\",\"task_id\":\"task_1\"}\n\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("unexpected forwarded stream:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestStreamUpstreamThreadEventsForwardsKeepalive(t *testing.T) {
	db := newAgentTestDB(t)
	rec := httptest.NewRecorder()
	body := strings.NewReader(strings.Join([]string{
		": upstream heartbeat\n\n",
		"data: {\"kind\":\"task.running\",\"task_id\":\"task_1\"}\n\n",
	}, ""))

	var lastUpstreamEventID string
	if err := streamUpstreamThreadEvents(context.Background(), rec, rec, db.DB, "thr_1", "", body, &lastUpstreamEventID, nil); err != nil {
		t.Fatalf("streamUpstreamThreadEvents returned error: %v", err)
	}

	want := ": keepalive\n\n" +
		"data: {\"kind\":\"task.running\",\"task_id\":\"task_1\"}\n\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("unexpected forwarded stream:\nwant: %q\ngot:  %q", want, got)
	}

	var count int64
	if err := db.DB.Model(&orm.AgentThreadRecord{}).
		Where("thread_id = ? AND stream_kind = ?", "thr_1", streamKindThreadEvent).
		Count(&count).Error; err != nil {
		t.Fatalf("count saved records: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected keepalive to stay unpersisted and one thread event to be saved, got %d", count)
	}
}

func TestStreamUpstreamThreadEventsSendsKeepaliveWhenUpstreamIdle(t *testing.T) {
	db := newAgentTestDB(t)
	rec := newTestSSERecorder()
	previousInterval := threadEventsKeepaliveInterval
	threadEventsKeepaliveInterval = 20 * time.Millisecond
	t.Cleanup(func() { threadEventsKeepaliveInterval = previousInterval })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bodyReader, bodyWriter := io.Pipe()
	defer bodyReader.Close()
	defer bodyWriter.Close()

	done := make(chan error, 1)
	go func() {
		var lastUpstreamEventID string
		done <- streamUpstreamThreadEvents(ctx, rec, rec, db.DB, "thr_1", "", bodyReader, &lastUpstreamEventID, nil)
	}()

	select {
	case chunk := <-rec.writeCh:
		if chunk != ": keepalive\n\n" {
			t.Fatalf("unexpected keepalive frame: %q", chunk)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for idle keepalive frame")
	}

	cancel()
	_ = bodyWriter.Close()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("streamUpstreamThreadEvents returned unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("streamUpstreamThreadEvents did not stop after cancellation")
	}

	if got := rec.String(); !strings.Contains(got, ": keepalive\n\n") {
		t.Fatalf("expected idle keepalive in response body, got %q", got)
	}
}

func TestStreamMessageRecordsForwardsPublishedKeepalive(t *testing.T) {
	db := newAgentTestDB(t)
	session := &activeMessageStream{
		threadID:    "thr_1",
		roundID:     "round_1",
		done:        make(chan struct{}),
		subscribers: make(map[*messageStreamSubscription]struct{}),
	}
	req := httptest.NewRequest(http.MethodGet, "/agent/threads/thr_1:messages", nil)
	rec := newTestSSERecorder()
	done := make(chan struct{})
	go func() {
		streamMessageRecords(req, rec, rec, db.DB, "thr_1", "", session)
		close(done)
	}()

	deadline := time.After(time.Second)
	for {
		session.mu.RLock()
		subscriberCount := len(session.subscribers)
		session.mu.RUnlock()
		if subscriberCount > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("message stream did not subscribe")
		case <-time.After(10 * time.Millisecond):
		}
	}

	session.publishHeartbeat()
	select {
	case chunk := <-rec.writeCh:
		if chunk != ": keepalive\n\n" {
			t.Fatalf("unexpected keepalive frame: %q", chunk)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for keepalive frame")
	}

	close(session.done)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("message stream did not stop")
	}
	if got := rec.String(); got != ": keepalive\n\n" {
		t.Fatalf("unexpected message stream body: %q", got)
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

	req := httptest.NewRequest(http.MethodPost, "/api/core/agent/threads/thr_new:messages", strings.NewReader(`{"content":"继续"}`))
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

func TestStreamMessageRecordsReplaysOnlyActiveRound(t *testing.T) {
	db := newAgentTestDB(t)
	now := time.Now().UTC()
	records := []orm.AgentThreadRecord{
		{
			ID:          "0001",
			ThreadID:    "thr_1",
			RoundID:     "round_old",
			StreamKind:  streamKindMessage,
			RecordKey:   "rk_old",
			EventName:   "message",
			PayloadText: `{"delta":"old"}`,
			RawFrame:    `data: {"delta":"old"}`,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          "0002",
			ThreadID:    "thr_1",
			RoundID:     "round_current",
			StreamKind:  streamKindMessage,
			RecordKey:   "rk_current",
			EventName:   "message",
			PayloadText: `{"delta":"current"}`,
			RawFrame:    `data: {"delta":"current"}`,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}
	if err := db.DB.Create(&records).Error; err != nil {
		t.Fatalf("create records: %v", err)
	}

	done := make(chan struct{})
	close(done)
	session := &activeMessageStream{
		threadID:    "thr_1",
		roundID:     "round_current",
		done:        done,
		subscribers: make(map[*messageStreamSubscription]struct{}),
	}
	req := httptest.NewRequest(http.MethodGet, "/agent/threads/thr_1:messages", nil)
	rec := newTestSSERecorder()

	streamMessageRecords(req, rec, rec, db.DB, "thr_1", "", session)

	want := "data: {\"delta\":\"current\"}\n\n"
	if got := rec.String(); got != want {
		t.Fatalf("unexpected message replay:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestBuildThreadRoundResponsesOmitsHistoryInternalsAndBuildsAssistantMessage(t *testing.T) {
	now := time.Now().UTC()
	rounds := []orm.AgentThreadRound{
		{
			RoundID:          "round_1",
			ThreadID:         "thr_1",
			Status:           "completed",
			UserMessage:      "hello",
			AssistantMessage: "stored assistant message",
			RequestPayload:   `{"message":"hello"}`,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}
	recordsByRound := map[string][]orm.AgentThreadRecord{
		"round_1": {
			{ID: "0001", RoundID: "round_1", EventName: "answer_delta", PayloadText: `{"delta":"answer-1"}`},
			{ID: "0002", RoundID: "round_1", EventName: "thinking_delta", PayloadText: `{"delta":"think-1"}`},
			{ID: "0003", RoundID: "round_1", EventName: "thinking_delta", PayloadText: `{"delta":"think-2"}`},
			{ID: "0004", RoundID: "round_1", EventName: "answer_delta", PayloadText: `{"delta":"answer-2"}`},
			{ID: "0005", RoundID: "round_1", EventName: "other", PayloadText: `{"delta":"ignored"}`},
		},
	}

	items := buildThreadRoundResponses(rounds, recordsByRound)
	if len(items) != 1 {
		t.Fatalf("expected one round response, got %d", len(items))
	}
	if got, want := items[0].AssistantMessage, "think-1think-2answer-1answer-2"; got != want {
		t.Fatalf("unexpected assistant_message: want %q, got %q", want, got)
	}

	raw, err := json.Marshal(threadHistoryResponse{ThreadID: "thr_1", Rounds: items})
	if err != nil {
		t.Fatalf("marshal history response: %v", err)
	}
	for _, forbidden := range []string{"thread_events", "request_payload", "records"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("history response must not include %q: %s", forbidden, raw)
		}
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

	req := httptest.NewRequest(http.MethodGet, "/api/core/agent/threads/thr_unknown:events", nil)
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

func TestDeleteThreadHistoryRemovesThreadRoundsAndRecords(t *testing.T) {
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
		StreamKind:  streamKindMessage,
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

	result, err := deleteThreadHistory(db.DB, "thr_1")
	if err != nil {
		t.Fatalf("deleteThreadHistory: %v", err)
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

func TestDeleteThreadHistoryCancelsRunningFlowBeforeDeleting(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodDelete, "/api/core/agent/threads/thr_1:history", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	DeleteThreadHistory(rec, req)

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

func TestDeleteThreadHistoryDoesNotCancelEndedFlow(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodDelete, "/api/core/agent/threads/thr_1:history", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	DeleteThreadHistory(rec, req)

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

func TestDeleteThreadHistoryDeletesLocalRowsWhenUpstreamStatusNotFound(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodDelete, "/api/core/agent/threads/thr_1:history", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	DeleteThreadHistory(rec, req)

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

func TestDeleteThreadHistoryDeletesLocalRowsWhenUpstreamDeleteNotFound(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodDelete, "/api/core/agent/threads/thr_1:history", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	DeleteThreadHistory(rec, req)

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

func TestDeleteThreadHistoryCancelsRunningFlowBeforeActiveStreamConflict(t *testing.T) {
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

	session := &activeMessageStream{
		threadID:    "thr_1",
		done:        make(chan struct{}),
		subscribers: make(map[*messageStreamSubscription]struct{}),
	}
	if !activeStreams.put("thr_1", session) {
		t.Fatalf("seed active stream")
	}
	t.Cleanup(func() {
		activeStreams.delete("thr_1", session)
		close(session.done)
	})

	var mu sync.Mutex
	cancelCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/threads/thr_1":
			_ = json.NewEncoder(w).Encode(evoThread{ThreadID: "thr_1", Status: "running"})
		case r.Method == http.MethodPost && r.URL.Path == "/threads/thr_1/cancel":
			mu.Lock()
			cancelCalled = true
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "cancelled"})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_EVO_SERVICE_URL", server.URL)

	req := httptest.NewRequest(http.MethodDelete, "/api/core/agent/threads/thr_1:history", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	DeleteThreadHistory(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected active stream conflict, status=%d body=%s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	gotCancelCalled := cancelCalled
	mu.Unlock()
	if !gotCancelCalled {
		t.Fatalf("expected delete to request cancel before returning active stream conflict")
	}
	var count int64
	if err := db.DB.Model(&orm.AgentThread{}).Where("thread_id = ?", "thr_1").Count(&count).Error; err != nil {
		t.Fatalf("count thread: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected local thread to remain while active stream is open, found %d rows", count)
	}
}

func TestDeleteThreadHistoryKeepsLocalRowsWhenCancelFails(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodDelete, "/api/core/agent/threads/thr_1:history", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	DeleteThreadHistory(rec, req)

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

func TestDeleteThreadHistoryKeepsLocalRowsWhenFlowStatusFails(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodDelete, "/api/core/agent/threads/thr_1:history", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"thread_id": "thr_1"})
	rec := httptest.NewRecorder()
	DeleteThreadHistory(rec, req)

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

	req := httptest.NewRequest(http.MethodPost, "/api/core/agent/threads/thr_new:retry", nil)
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
