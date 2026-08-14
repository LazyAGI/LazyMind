package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/state"
)

func intPtr(value int) *int          { return &value }
func stringPtr(value string) *string { return &value }

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

func TestUpstreamStreamChunkPreservesProviderEvents(t *testing.T) {
	status := &ProviderStatusEvent{
		ModelCallID: "call-1", HTTPStatus: intPtr(429), FinishReason: nil, ErrorBody: `{"error":"limited"}`,
	}
	retry := &ModelRetryEvent{ModelCallID: "call-1", RetryIndex: 1, MaxRetries: 5, DelayMS: 1100}
	transport := &ModelTransportErrorEvent{ModelCallID: "call-2", ErrorType: "ReadTimeout", ErrorMessage: "timed out"}

	chunk := upstreamStreamChunkFromData(LazyChatData{
		ProviderStatus: status, ModelRetry: retry, ModelTransportError: transport,
	})

	if chunk.ProviderStatus != status || chunk.ModelRetry != retry || chunk.ModelTransportError != transport {
		t.Fatalf("provider events were dropped during upstream conversion: %#v", chunk)
	}
}

func TestForwardProviderEventKeepsLiveBodyAndStoresOnlyLatestRedactedStatus(t *testing.T) {
	ctx := context.Background()
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("create state store: %v", err)
	}
	defer stateStore.Close()
	recorder := httptest.NewRecorder()
	status := &ProviderStatusEvent{
		ModelCallID: "call-1", HTTPStatus: intPtr(429), FinishReason: nil, ErrorBody: "raw provider body",
	}

	got, handled := forwardProviderEvent(
		ctx, recorder, recorder, stateStore, "conv-1", "history-1", 2,
		UpstreamStreamChunk{ProviderStatus: status}, true,
	)

	if !handled || got != status {
		t.Fatalf("provider status was not handled: handled=%v status=%#v", handled, got)
	}
	if !strings.Contains(recorder.Body.String(), "raw provider body") {
		t.Fatalf("live SSE lost provider body: %s", recorder.Body.String())
	}
	snapshot, err := getLatestProviderStatus(ctx, stateStore, "conv-1", "history-1")
	if err != nil {
		t.Fatalf("read provider snapshot: %v", err)
	}
	if snapshot == nil || snapshot.ModelCallID != "call-1" || snapshot.ErrorBody != "" {
		t.Fatalf("unexpected stored provider snapshot: %#v", snapshot)
	}
	chunks, err := getChatChunks(ctx, stateStore, "conv-1", "history-1")
	if err != nil {
		t.Fatalf("read replay chunks: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("raw provider event leaked into replay list: %#v", chunks)
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

func TestStreamChatUpstreamForwardsProviderStatusBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(w, `{"code":200,"msg":"success","data":{"provider_status":{"model_call_id":"call-1","http_status":429,"finish_reason":null,"error_body":"raw upstream body"}}}`)
		_, _ = fmt.Fprintln(w, `{"code":500,"msg":"chat service failed","data":{"status":"FAILED","tool_call_turns":3}}`)
	}))
	defer server.Close()

	stream, _, err := StreamChatUpstream(context.Background(), server.URL, map[string]any{"query": "test"})
	if err != nil {
		t.Fatalf("start upstream stream: %v", err)
	}
	chunk, ok := <-stream
	if !ok || chunk.ProviderStatus == nil {
		t.Fatalf("provider status was not forwarded: %#v", chunk)
	}
	if chunk.ProviderStatus.HTTPStatus == nil || *chunk.ProviderStatus.HTTPStatus != 429 ||
		chunk.ProviderStatus.ErrorBody != "raw upstream body" {
		t.Fatalf("unexpected provider status: %#v", chunk.ProviderStatus)
	}
	lifecycle, ok := <-stream
	if !ok || lifecycle.Status != "FAILED" || lifecycle.ToolCallTurns != 3 {
		t.Fatalf("failed lifecycle status was not forwarded after provider status: %#v", lifecycle)
	}
}
