package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/state"
)

type failNthHSetStore struct {
	state.Store
	failOnCall int
	calls      int
}

func (s *failNthHSetStore) HSet(ctx context.Context, key string, fields map[string]any, ttl time.Duration) error {
	s.calls++
	if s.calls == s.failOnCall {
		return errors.New("injected HSet failure")
	}
	return s.Store.HSet(ctx, key, fields, ttl)
}

func TestUpstreamStreamChunkPreservesToolLimitPending(t *testing.T) {
	pending := &ToolLimitPendingEvent{
		DecisionID:        "decision-1",
		UsedRounds:        21,
		RoundLimit:        21,
		ExpandedMaxRounds: 200,
		TimeoutSeconds:    120,
	}

	chunk := upstreamStreamChunkFromData(LazyChatData{ToolLimitPending: pending})

	if chunk.ToolLimitPending != pending {
		t.Fatalf("tool-limit event was dropped during upstream conversion: %#v", chunk)
	}
}

func TestUpstreamStreamChunkPreservesCapabilityDependency(t *testing.T) {
	dependency := map[string]any{
		"status":  "blocked",
		"missing": []any{map[string]any{"id": "image_generator"}},
	}

	chunk := upstreamStreamChunkFromData(LazyChatData{CapabilityDependency: dependency})

	if chunk.CapabilityDependency["status"] != "blocked" {
		t.Fatalf("capability dependency was dropped during upstream conversion: %#v", chunk)
	}
}

func TestPublishCapabilityDependencyWritesStructuredChatChunk(t *testing.T) {
	recorder := httptest.NewRecorder()
	dependency := map[string]any{
		"status":  "blocked",
		"missing": []any{map[string]any{"id": "image_generator"}},
	}

	publishCapabilityDependency(
		context.Background(), context.Background(), recorder, recorder, nil,
		"conversation-1", "history-1", 1, dependency, true,
	)

	body := recorder.Body.String()
	if !strings.Contains(body, `"capability_dependency":{"missing":[{"id":"image_generator"}],"status":"blocked"}`) {
		t.Fatalf("structured capability dependency missing from chat chunk: %s", body)
	}
}

func TestConsumeRuntimeChunkPrefersError(t *testing.T) {
	terminal := runFinishedEvent("run-1", RunTerminal{
		Status:        "completed",
		Reason:        "normal",
		PartialOutput: false,
	})
	decision, handled := consumeRuntimeChunk(UpstreamStreamChunk{
		RuntimeEvent: terminal,
		Err:          fmt.Errorf("stream failed"),
	}, "run-1", true)

	if !handled || !decision.Stop || decision.Terminal == nil {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Terminal.Status != "failed" || decision.Terminal.Reason != "runtime_failure" || decision.Terminal.Code != "upstream_stream_failed" {
		t.Fatalf("error did not win over terminal: %#v", decision.Terminal)
	}
}

func TestConsumeRuntimeChunkMapsStructuredStreamErrors(t *testing.T) {
	tests := []struct {
		name string
		kind UpstreamStreamErrorKind
		code string
	}{
		{name: "transport", kind: UpstreamStreamErrorTransport, code: "transport_error"},
		{name: "protocol", kind: UpstreamStreamErrorProtocol, code: "protocol_error"},
		{name: "missing terminal", kind: UpstreamStreamErrorMissingTerminal, code: "missing_run_terminal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, handled := consumeRuntimeChunk(UpstreamStreamChunk{
				Err:     fmt.Errorf("stream failed"),
				ErrKind: tt.kind,
			}, "run-1", false)
			if !handled || !decision.Stop || decision.Terminal == nil {
				t.Fatalf("unexpected decision: %#v", decision)
			}
			if decision.Terminal.Code != tt.code {
				t.Fatalf("error code = %q, want %q", decision.Terminal.Code, tt.code)
			}
			if !strings.HasPrefix(decision.Terminal.DiagnosticID, "diag_") {
				t.Fatalf("diagnostic id = %q, want diag_ prefix", decision.Terminal.DiagnosticID)
			}
		})
	}
}

func TestStreamSingleAnswerPersistsFinalAlgorithmID(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/algorithm-attribution.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{
		ID: "conv-1", DisplayName: "test",
		BaseModel: orm.BaseModel{CreateUserID: "u1", CreateUserName: "u1", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	serveAnswer := func(algorithmID string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Algorithm-Id", algorithmID)
			_, _ = fmt.Fprintln(w, `{"code":200,"msg":"success","data":{"text":"answer"}}`)
		}))
	}
	stream := func(serverURL string, target chatPersistTarget) {
		recorder := httptest.NewRecorder()
		streamSingleAnswer(
			context.Background(), context.Background(), recorder, recorder, db.DB, nil,
			serverURL, map[string]any{"query": "question"}, "conv-1", "question", "h1", target, json.RawMessage(`{}`),
		)
	}

	first := serveAnswer("algorithm-a")
	stream(first.URL, chatPersistTarget{HistoryID: "h1", Seq: 1})
	first.Close()
	var history orm.ChatHistory
	if err := db.Where("id = ?", "h1").First(&history).Error; err != nil {
		t.Fatalf("load first history: %v", err)
	}
	if history.AlgorithmID != "algorithm-a" {
		t.Fatalf("first algorithm id: got %q", history.AlgorithmID)
	}
	if err := db.Model(&orm.ChatHistory{}).Where("id = ?", "h1").Updates(map[string]any{
		"feed_back": 1,
	}).Error; err != nil {
		t.Fatalf("like first answer: %v", err)
	}
	if err := db.Where("id = ?", "h1").First(&history).Error; err != nil {
		t.Fatalf("reload liked history: %v", err)
	}

	second := serveAnswer("algorithm-b")
	stream(second.URL, chatPersistTarget{HistoryID: "h1", Seq: 1, IsRegeneration: true, Existing: &history})
	second.Close()
	if err := db.Where("id = ?", "h1").First(&history).Error; err != nil {
		t.Fatalf("load regenerated history: %v", err)
	}
	if history.AlgorithmID != "algorithm-b" {
		t.Fatalf("regenerated algorithm id: got %q", history.AlgorithmID)
	}
	if err := db.Model(&orm.ChatHistory{}).Where("id = ?", "h1").Updates(map[string]any{
		"feed_back": 2,
		"reason":    "slow",
	}).Error; err != nil {
		t.Fatalf("dislike second answer: %v", err)
	}
	if err := db.Where("id = ?", "h1").First(&history).Error; err != nil {
		t.Fatalf("reload disliked history: %v", err)
	}

	third := serveAnswer("algorithm-c")
	stream(third.URL, chatPersistTarget{HistoryID: "h1", Seq: 1, IsRegeneration: true, Existing: &history})
	third.Close()
	if err := db.Where("id = ?", "h1").First(&history).Error; err != nil {
		t.Fatalf("load second regeneration: %v", err)
	}
	if history.AlgorithmID != "algorithm-c" || history.FeedBack != 0 {
		t.Fatalf("unexpected latest answer: %#v", history)
	}
	var ext struct {
		Attempts []routerTrafficAttempt `json:"router_traffic_attempts"`
	}
	if err := json.Unmarshal(history.Ext, &ext); err != nil {
		t.Fatalf("decode traffic attempts: %v", err)
	}
	if len(ext.Attempts) != 2 {
		t.Fatalf("expected two archived attempts, got %#v", ext.Attempts)
	}
	if ext.Attempts[0].AlgorithmID != "algorithm-a" || ext.Attempts[0].FeedBack != 1 {
		t.Fatalf("unexpected first attempt: %#v", ext.Attempts[0])
	}
	if ext.Attempts[1].AlgorithmID != "algorithm-b" || ext.Attempts[1].FeedBack != 2 || ext.Attempts[1].Reason != "slow" {
		t.Fatalf("unexpected second attempt: %#v", ext.Attempts[1])
	}
}

func TestStreamSingleAnswerPersistsAndForwardsPerformanceMetrics(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/performance-stream.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}, &orm.ChatRunPerformance{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{
		ID: "conv-performance", DisplayName: "test",
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "user-1", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, algorithmFrame(t, map[string]any{"text": "answer"}))
		_, _ = fmt.Fprintln(w, algorithmFrame(t, map[string]any{
			"runtime_event": map[string]any{
				"schema_version": 1, "event_id": "evt-performance", "run_id": "run-performance",
				"type": RuntimeEventRunFinished,
				"data": map[string]any{"status": "completed", "reason": "normal", "partial_output": true},
			},
			"performance_metrics": map[string]any{
				"schema_version": 1, "turn_seq": 4, "model_steps": 1, "tool_steps": 0,
				"wall_ms": 1000, "model_ms": 800, "input_tokens": 100, "output_tokens": 20,
			},
		}))
	}))
	defer server.Close()

	recorder := httptest.NewRecorder()
	streamSingleAnswer(
		context.Background(), context.Background(), recorder, recorder, db.DB, nil,
		server.URL, map[string]any{"query": "question", "run_id": "run-performance", "user_id": "user-1"},
		"conv-performance", "question", "history-performance",
		chatPersistTarget{HistoryID: "history-performance", Seq: 4}, json.RawMessage(`{}`),
	)

	if !strings.Contains(recorder.Body.String(), `"performance_metrics":{"schema_version":1`) {
		t.Fatalf("performance metrics were not forwarded to SSE: %s", recorder.Body.String())
	}
	var stored orm.ChatRunPerformance
	if err := db.Where("run_id = ?", "run-performance").Take(&stored).Error; err != nil {
		t.Fatalf("load persisted performance: %v", err)
	}
	if stored.ConversationID != "conv-performance" || stored.HistoryID != "history-performance" || stored.UserID != "user-1" {
		t.Fatalf("unexpected performance ownership: %#v", stored)
	}
	if stored.ModelMS == nil || *stored.ModelMS != 800 {
		t.Fatalf("unexpected persisted metrics: %#v", stored)
	}
}

func TestStreamSingleAnswerPersistsFailureForInvalidTerminal(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/invalid-terminal.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{
		ID: "conv-invalid", DisplayName: "test",
		BaseModel: orm.BaseModel{CreateUserID: "u1", CreateUserName: "u1", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Algorithm-Id", "algorithm-invalid")
		_, _ = fmt.Fprintln(w, runFinishedFrame(t, "wrong-run"))
	}))
	defer server.Close()

	recorder := httptest.NewRecorder()
	streamSingleAnswer(
		context.Background(), context.Background(), recorder, recorder, db.DB, nil,
		server.URL, map[string]any{"query": "question", "run_id": "expected-run"},
		"conv-invalid", "question", "history-invalid", chatPersistTarget{HistoryID: "history-invalid", Seq: 1}, json.RawMessage(`{}`),
	)

	var history orm.ChatHistory
	if err := db.Where("id = ?", "history-invalid").First(&history).Error; err != nil {
		t.Fatalf("load history: %v", err)
	}
	if history.RunStatus != "failed" {
		t.Fatalf("invalid terminal persisted status %q", history.RunStatus)
	}
	terminal, err := parseRunTerminal(history.RunTerminal)
	if err != nil {
		t.Fatalf("parse persisted terminal: %v", err)
	}
	if terminal.Reason != "runtime_failure" || terminal.Code != "protocol_error" {
		t.Fatalf("unexpected persisted terminal: %#v", terminal)
	}
	if strings.Contains(recorder.Body.String(), `"status":"completed"`) {
		t.Fatalf("invalid completed terminal leaked to client: %s", recorder.Body.String())
	}
}

func TestHandleStreamChatEmptyUpstreamReturnsAndPersistsFailure(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/empty-upstream.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}, &orm.TaskCenterTask{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{
		ID: "conv-empty", DisplayName: "test",
		BaseModel: orm.BaseModel{CreateUserID: "u1", CreateUserName: "u1", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/core/conversations:chat", nil)
	handleStreamChat(
		recorder, request, db.DB, nil, upstream.URL,
		map[string]any{"query": "question", "user_id": "u1"},
		"conv-empty", "question", chatPersistTarget{HistoryID: "history-empty", Seq: 1}, false, json.RawMessage(`{}`),
	)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("unexpected response status=%d content-type=%q body=%s", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
	}

	var frames []ChatChunkResponse
	for _, line := range strings.Split(strings.TrimSpace(recorder.Body.String()), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			t.Fatalf("non-SSE response line: %q", line)
		}
		var envelope struct {
			Result ChatChunkResponse `json:"result"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &envelope); err != nil {
			t.Fatalf("decode SSE frame: %v", err)
		}
		frames = append(frames, envelope.Result)
	}
	var event *ChatRuntimeEvent
	for _, frame := range frames {
		if frame.Message != "" || frame.Delta != "" {
			t.Fatalf("empty upstream produced answer content: %#v", frame)
		}
		if frame.RuntimeEvent != nil {
			if event != nil {
				t.Fatalf("multiple runtime events: %#v", frames)
			}
			event = frame.RuntimeEvent
		}
	}
	if event == nil || event.Type != RuntimeEventRunFinished {
		t.Fatalf("missing run_finished frame: %#v", frames)
	}
	if frames[len(frames)-1].RuntimeEvent != event {
		t.Fatalf("run_finished was not the last SSE frame: %#v", frames)
	}
	terminal, err := event.Terminal()
	if err != nil {
		t.Fatalf("parse SSE terminal: %v", err)
	}
	if terminal.Status != "failed" || terminal.Reason != "runtime_failure" || terminal.Code != "missing_run_terminal" || terminal.PartialOutput || !strings.HasPrefix(terminal.DiagnosticID, "diag_") {
		t.Fatalf("unexpected SSE terminal: %#v", terminal)
	}

	var history orm.ChatHistory
	if err := db.Where("id = ?", "history-empty").Take(&history).Error; err != nil {
		t.Fatalf("load persisted history: %v", err)
	}
	stored, err := parseRunTerminal(history.RunTerminal)
	if err != nil {
		t.Fatalf("parse persisted terminal: %v", err)
	}
	if history.RunID != event.RunID || history.RunStatus != terminal.Status || history.Result != "" ||
		stored.Status != terminal.Status || stored.Reason != terminal.Reason || stored.Code != terminal.Code ||
		stored.DiagnosticID != terminal.DiagnosticID || stored.PartialOutput != terminal.PartialOutput {
		t.Fatalf("SSE/history mismatch: event=%#v terminal=%#v history=%#v stored=%#v", event, terminal, history, stored)
	}
}

func assertStoreUnavailableJSON(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("response status = %d, want 503; body=%s", response.StatusCode, recorder.Body.String())
	}
	if got := response.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}
	var payload struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON response: %v; body=%s", err, recorder.Body.String())
	}
	if payload.Code != 2000507 || payload.Message != "Store is not initialized" {
		t.Fatalf("unexpected error payload: %#v", payload)
	}
	if strings.Contains(recorder.Body.String(), "data:") {
		t.Fatalf("error response contains SSE frames: %s", recorder.Body.String())
	}
}

func TestHandleStreamChatNilWorkspaceStoreReturnsHTTP503(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/core/conversations:chat", nil)
	handleStreamChat(
		recorder, request, nil, nil, "",
		map[string]any{"query": "question", "_workspace_bound": true},
		"conv-nil-store", "question", chatPersistTarget{HistoryID: "history-nil-store", Seq: 1}, false, json.RawMessage(`{}`),
	)
	assertStoreUnavailableJSON(t, recorder)
}

func TestHandleStreamChatHSetFailureReturnsHTTP503BeforeUpstream(t *testing.T) {
	for _, tt := range []struct {
		name       string
		dualReply  bool
		failOnCall int
	}{
		{name: "primary run status", failOnCall: 1},
		{name: "primary dual run status", dualReply: true, failOnCall: 1},
		{name: "secondary run status", dualReply: true, failOnCall: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/chat.db")
			if err != nil {
				t.Fatalf("connect db: %v", err)
			}
			if err := db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}, &orm.MultiAnswersChatHistory{}); err != nil {
				t.Fatalf("auto migrate: %v", err)
			}
			now := time.Now().UTC()
			if err := db.Create(&orm.Conversation{
				ID: "conv-hset", DisplayName: "test",
				BaseModel: orm.BaseModel{CreateUserID: "u1", CreateUserName: "u1", CreatedAt: now, UpdatedAt: now},
			}).Error; err != nil {
				t.Fatalf("create conversation: %v", err)
			}
			store, err := state.NewSQLiteStore(t.TempDir() + "/state.db")
			if err != nil {
				t.Fatalf("create state store: %v", err)
			}
			defer store.Close()
			failingStore := &failNthHSetStore{Store: store, failOnCall: tt.failOnCall}

			var upstreamCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				upstreamCalls.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/core/conversations:chat", nil)
			handleStreamChat(
				recorder, request, db.DB, failingStore, upstream.URL,
				map[string]any{"query": "question", "user_id": "u1"},
				"conv-hset", "question", chatPersistTarget{HistoryID: "history-hset", Seq: 1}, tt.dualReply, json.RawMessage(`{}`),
			)
			assertStoreUnavailableJSON(t, recorder)
			if failingStore.calls < tt.failOnCall {
				t.Fatalf("HSet calls = %d, failure was not reached", failingStore.calls)
			}
			if got := upstreamCalls.Load(); got != 0 {
				t.Fatalf("upstream calls = %d, want 0", got)
			}
			assertFailedHistory := func(id, status string, raw json.RawMessage) {
				t.Helper()
				terminal, err := parseRunTerminal(raw)
				if err != nil || status != "failed" || terminal.Status != "failed" || terminal.Code != "state_store_unavailable" {
					t.Fatalf("history %s did not finish: status=%q terminal=%s err=%v", id, status, raw, err)
				}
				cached, err := getChatStatus(request.Context(), store, "conv-hset", id)
				if err != nil || cached.Status != "failed" || cached.RunTerminal == nil || cached.RunTerminal.Code != terminal.Code {
					t.Fatalf("history %s cache did not finish: status=%+v err=%v", id, cached, err)
				}
				if input, err := getChatInput(request.Context(), store, "conv-hset", id); err == nil {
					t.Fatalf("history %s still has active input: %+v", id, input)
				}
			}
			if !tt.dualReply {
				var history orm.ChatHistory
				if err := db.Where("id = ?", "history-hset").Take(&history).Error; err != nil {
					t.Fatalf("load history: %v", err)
				}
				assertFailedHistory(history.ID, history.RunStatus, history.RunTerminal)
			} else {
				var histories []orm.MultiAnswersChatHistory
				if err := db.Where("conversation_id = ?", "conv-hset").Find(&histories).Error; err != nil {
					t.Fatalf("load multi-answer histories: %v", err)
				}
				if len(histories) != 2 {
					t.Fatalf("multi-answer history count = %d, want 2", len(histories))
				}
				for _, history := range histories {
					assertFailedHistory(history.ID, history.RunStatus, history.RunTerminal)
				}
			}
		})
	}
}

func TestHandleStreamChatHistoryClaimFailureRemainsSSE(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/claim-failure.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/core/conversations:chat", nil)
	handleStreamChat(
		recorder, request, db.DB, nil, "",
		map[string]any{"query": "question"},
		"missing-conversation", "question", chatPersistTarget{HistoryID: "history-claim", Seq: 1}, false, json.RawMessage(`{}`),
	)
	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("unexpected response status=%d content-type=%q body=%s", response.StatusCode, response.Header.Get("Content-Type"), recorder.Body.String())
	}
	frame := strings.TrimSpace(recorder.Body.String())
	if !strings.HasPrefix(frame, "data: ") {
		t.Fatalf("missing SSE frame: %s", frame)
	}
	var envelope struct {
		Result ChatChunkResponse `json:"result"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(frame, "data: ")), &envelope); err != nil {
		t.Fatalf("decode SSE frame: %v", err)
	}
	terminal, err := envelope.Result.RuntimeEvent.Terminal()
	if err != nil {
		t.Fatalf("parse run_finished: %v", err)
	}
	if terminal.Status != "failed" || terminal.Code != "history_run_claim_failed" || terminal.PartialOutput {
		t.Fatalf("unexpected terminal: %#v", terminal)
	}
}

func TestStreamChatUpstreamForwardsToolLimitPending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Algorithm-Id", "candidate-a")
		_, _ = fmt.Fprintln(w, `{"code":200,"msg":"success","data":{"tool_limit_pending":{"decision_id":"decision-2","used_rounds":21,"round_limit":21,"expanded_max_rounds":200,"timeout_seconds":120}}}`)
	}))
	defer server.Close()

	stream, algorithmID, err := StreamChatUpstream(context.Background(), server.URL, map[string]any{"query": "test"})
	if err != nil {
		t.Fatalf("start upstream stream: %v", err)
	}
	if algorithmID != "candidate-a" {
		t.Fatalf("unexpected algorithm id %q", algorithmID)
	}
	chunk, ok := <-stream
	if !ok || chunk.ToolLimitPending == nil {
		t.Fatalf("tool-limit event was not forwarded: %#v", chunk)
	}
	if chunk.ToolLimitPending.DecisionID != "decision-2" {
		t.Fatalf("unexpected decision id: %#v", chunk.ToolLimitPending)
	}
}

func TestStreamChatUpstreamForwardsCapabilityDependency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Algorithm-Id", "candidate-a")
		_, _ = fmt.Fprintln(w, `{"code":200,"msg":"success","data":{"capability_dependency":{"status":"blocked","missing":[{"id":"image_generator"}]}}}`)
	}))
	defer server.Close()

	stream, _, err := StreamChatUpstream(context.Background(), server.URL, map[string]any{"query": "test"})
	if err != nil {
		t.Fatalf("start upstream stream: %v", err)
	}
	chunk, ok := <-stream
	if !ok || chunk.CapabilityDependency == nil {
		t.Fatalf("capability dependency was not forwarded: %#v", chunk)
	}
	if chunk.CapabilityDependency["status"] != "blocked" {
		t.Fatalf("unexpected capability dependency: %#v", chunk.CapabilityDependency)
	}
}
