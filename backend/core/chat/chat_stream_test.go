package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

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
