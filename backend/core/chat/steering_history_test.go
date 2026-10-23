package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestSteeringBoundarySurvivesAlgorithmStreamDecoder(t *testing.T) {
	frame := algorithmFrame(t, map[string]any{"steering_applied": []steeringApplied{{ID: "g", Text: "收窄范围", AcceptedAt: 123}}})
	response := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(frame + "\n"))}
	count := 0
	for data := range lazyStreamHandler(context.Background(), response) {
		if data.Err != nil {
			t.Fatal(data.Err)
		}
		chunk := upstreamStreamChunkFromData(data.Resp.Data)
		if !hasBusinessStreamPayload(chunk) || len(chunk.SteeringApplied) != 1 || chunk.SteeringApplied[0].Text != "收窄范围" {
			t.Fatalf("chunk=%+v", chunk)
		}
		count++
	}
	if count != 1 {
		t.Fatalf("frames=%d", count)
	}
}

func TestSteeringStreamPersistsOrderedPartialRepliesAndRestoresContext(t *testing.T) {
	db, cache := newRunDecisionStreamHarness(t)
	if err := db.AutoMigrate(&orm.ConversationFollowUp{}); err != nil {
		t.Fatal(err)
	}
	const runID, historyID = "steered-run", "steered-history"
	for _, row := range []orm.ConversationFollowUp{
		{ID: "guide1", SteerID: "runtime1", Text: "只比较天津和济南", Content: json.RawMessage(`{"text":"只比较天津和济南"}`)},
		{ID: "guide2", SteerID: "runtime2", Text: "两人不超过2000元", Content: json.RawMessage(`{"text":"两人不超过2000元"}`)},
	} {
		row.ConversationID, row.TargetHistoryID, row.TargetRunID = "conv-stream-decision", historyID, runID
		// The stream must not depend on the HTTP handler updating accepted first.
		row.Mode, row.Status = followUpModeSteer, followUpSteering
		row.IdempotencyKey = row.ID
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	server := streamServer(t, runID,
		algorithmFrame(t, map[string]any{"text": "北京出发，先比较交通。"}),
		algorithmFrame(t, map[string]any{"steering_applied": []steeringApplied{{ID: "runtime1", AcceptedAt: 1}}}),
		algorithmFrame(t, map[string]any{"text": "天津更近，济南也方便。"}),
		algorithmFrame(t, map[string]any{"steering_applied": []steeringApplied{{ID: "runtime2", AcceptedAt: 2}}}),
		algorithmFrame(t, map[string]any{"text": "最终推荐天津，预算1800元。"}),
		algorithmFrame(t, map[string]any{"runtime_event": completedRunEvent(runID, true)}),
	)
	defer server.Close()
	recorder := httptest.NewRecorder()
	streamSingleAnswer(context.Background(), context.Background(), recorder, recorder, db, cache,
		server.URL, map[string]any{"query": "周末旅行", "run_id": runID}, "conv-stream-decision", "周末旅行", historyID,
		chatPersistTarget{HistoryID: historyID, Seq: 1}, json.RawMessage(`{"other":"keep"}`))
	var stored orm.ChatHistory
	if err := db.First(&stored, "id = ?", historyID).Error; err != nil {
		t.Fatal(err)
	}
	timeline := steeringTimelineFromHistory(stored)
	if len(timeline.Items) != 2 || stored.RunStatus != "completed" {
		t.Fatalf("history=%+v", stored)
	}
	if strings.Contains(string(stored.Ext), "北京出发") || !strings.Contains(string(stored.Ext), `"other":"keep"`) {
		t.Fatalf("body duplicated in metadata or unrelated metadata lost: %s", stored.Ext)
	}
	got := []string{}
	for _, msg := range buildHistoryMessages([]orm.ChatHistory{stored}, nil) {
		got = append(got, msg["role"].(string)+":"+msg["content"].(string))
	}
	want := []string{"user:周末旅行", "assistant:北京出发，先比较交通。", "user:只比较天津和济南", "assistant:天津更近，济南也方便。", "user:两人不超过2000元", "assistant:最终推荐天津，预算1800元。"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("context=%q", got)
	}
	item := chatHistoryToResponseItem(stored)
	if err := hydrateConversationSteeringHistory(context.Background(), db, stored.ConversationID, "0", []map[string]any{item}); err != nil {
		t.Fatal(err)
	}
	messages := item["steering_messages"].([]conversationSteeringHistoryDTO)
	if len(messages) != 2 || *messages[0].AssistantSegment != "北京出发，先比较交通。" || *messages[1].AssistantSegment != "天津更近，济南也方便。" || item["assistant_remainder"] != "最终推荐天津，预算1800元。" {
		t.Fatalf("reload=%+v", item)
	}
	chunks := decodeChatChunkSSE(t, recorder.Body.String())
	chunkPointers := make([]*ChatChunkResponse, len(chunks))
	for i := range chunks {
		chunkPointers[i] = &chunks[i]
	}
	replay := steeringResumeChunk(chunkPointers, mergeChunksToFirstChunk(chunkPointers))
	if len(replay.SteeringReplay) != 2 || replay.SteeringReplay[0].ID != "guide1" || replay.Delta != item["assistant_remainder"] {
		t.Fatalf("replay=%+v", replay)
	}
}

func TestSteeringTimelineBurstDeduplicationAndRunIsolation(t *testing.T) {
	items := []steeringApplied{{ID: "a", Text: "天津", AcceptedAt: 2}, {ID: "b", Text: "预算", AcceptedAt: 1}}
	ext := withSteeringBoundary(nil, "run", 0, items)
	ext = withSteeringBoundary(ext, "run", 0, items)
	h := orm.ChatHistory{RunID: "run", Ext: ext, Result: "final"}
	if got := steeringTimelineFromHistory(h).Items; len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("timeline=%+v", got)
	}
	h.RunID = "new-run"
	if len(steeringTimelineFromHistory(h).Items) != 0 {
		t.Fatal("regeneration inherited previous run boundaries")
	}
	h.RunID, h.Result = "run", ""
	h.Ext = withSteeringBoundary(nil, "run", 100, items)
	if len(steeringTimelineFromHistory(h).Items) != 0 {
		t.Fatal("invalid offset accepted")
	}
}

func TestSteeringDatabaseReloadKeepsPartsAndUnconsumedAcceptedGuide(t *testing.T) {
	db := followUpTestDB(t)
	part1, part2 := "北京出发。", "天津美食丰富。"
	h := orm.ChatHistory{ID: "h", ConversationID: "conversation", RunID: "run", Seq: 1,
		RawContent: "周末旅行", Result: part1 + part2, RunStatus: "interrupted",
		Ext: withSteeringBoundary(nil, "run", len(part1), []steeringApplied{{ID: "g1", Text: "只比较天津济南", AcceptedAt: 1}})}
	if err := db.Create(&h).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []orm.ConversationFollowUp{
		{ID: "g1", Text: "只比较天津济南"}, {ID: "g2", Text: "预算2000元"},
	} {
		row.ConversationID, row.TargetHistoryID, row.TargetRunID = "conversation", "h", "run"
		row.Mode, row.Status = followUpModeSteer, followUpAccepted
		seedFollowUp(t, db, row)
		if err := db.Model(&orm.ConversationFollowUp{}).Where("id = ?", row.ID).Update("text", row.Text).Error; err != nil {
			t.Fatal(err)
		}
	}
	var restored orm.ChatHistory
	if err := db.First(&restored, "id = ?", "h").Error; err != nil {
		t.Fatal(err)
	}
	messages := injectConversationSteeringMessages(context.Background(), db.DB, "conversation", "u1", []orm.ChatHistory{restored}, buildHistoryMessages([]orm.ChatHistory{restored}, nil))
	got := []string{}
	for _, message := range messages {
		got = append(got, message["content"].(string))
	}
	if !reflect.DeepEqual(got, []string{"周末旅行", part1, "只比较天津济南", part2, "预算2000元"}) {
		t.Fatalf("context=%q", got)
	}
	item := chatHistoryToResponseItem(restored)
	if err := hydrateConversationSteeringHistory(context.Background(), db.DB, "conversation", "u1", []map[string]any{item}); err != nil {
		t.Fatal(err)
	}
	guides := item["steering_messages"].([]conversationSteeringHistoryDTO)
	if len(guides) != 2 || *guides[0].AssistantSegment != part1 || *guides[1].AssistantSegment != part2 || item["assistant_remainder"] != "" {
		t.Fatalf("history=%+v", item)
	}
	// New runs must not accidentally inherit old accepted-row guidance.
	restored.RunID = "new-run"
	messages = injectConversationSteeringMessages(context.Background(), db.DB, "conversation", "u1", []orm.ChatHistory{restored}, buildHistoryMessages([]orm.ChatHistory{restored}, nil))
	if len(messages) != 2 {
		t.Fatal("old guides injected into regenerated run")
	}
}

func TestSteeringReplayAndForkRetainOrderedMessages(t *testing.T) {
	chunks := []*ChatChunkResponse{
		{Delta: "之前的部分回答"},
		{SteeringApplied: []steeringApplied{{ID: "a", Text: "收窄范围"}, {ID: "b", Text: "限定预算"}}},
		{Delta: "最终回答"},
	}
	replay := steeringResumeChunk(chunks, mergeChunksToFirstChunk(chunks))
	if len(replay.SteeringReplay) != 2 || replay.SteeringReplay[0].AssistantSegment != "之前的部分回答" || replay.SteeringReplay[1].AssistantSegment != "" || replay.Delta != "最终回答" {
		t.Fatalf("replay=%+v", replay)
	}
	h := orm.ChatHistory{ID: "h", RunID: "r", Result: "部分回答", Ext: withSteeringBoundary(nil, "r", 0, []steeringApplied{{ID: "a", Text: "收窄范围"}})}
	fork, err := copyForkHistories([]orm.ChatHistory{h}, "new-conversation")
	if err != nil {
		t.Fatal(err)
	}
	if len(steeringTimelineFromHistory(fork[0]).Items) != 1 {
		t.Fatal("fork lost its inherited guidance")
	}
}

func TestSteeringPreservesToolPairsThinkingAndTailExports(t *testing.T) {
	partial := `<think>规划</think>部分回答<tool_call>{"id":"t"}</tool_call><tool_result>{"id":"t","content":"天津"}</tool_result>`
	h := orm.ChatHistory{ID: "h", RunID: "run", RawContent: "原问题", Result: partial + "草稿", Ext: withSteeringBoundary(nil, "run", len(partial), []steeringApplied{{ID: "g", Text: "调整"}})}
	messages := buildHistoryMessages([]orm.ChatHistory{h}, nil)
	if messages[1]["content"] != partial {
		t.Fatal("tool history changed")
	}
	result := replaceSteeringTailExport(h.Result, h.Ext, h.RunID, "完整导出")
	if result != partial+"完整导出" {
		t.Fatalf("export overwrote earlier segment: %q", result)
	}
	h.Result = result
	item := chatHistoryToResponseItem(h)
	segment := item["steering_messages"].([]conversationSteeringHistoryDTO)[0]
	if *segment.AssistantSegment != "部分回答" || segment.ReasoningContent != "规划" || item["assistant_remainder"] != "完整导出" {
		t.Fatalf("item=%+v", item)
	}
}

func TestSteeringPersistenceFailureDoesNotPublishAcceptedBoundary(t *testing.T) {
	db, cache := newRunDecisionStreamHarness(t)
	if err := db.AutoMigrate(&orm.ConversationFollowUp{}); err != nil {
		t.Fatal(err)
	}
	server := streamServer(t, "run", algorithmFrame(t, map[string]any{"steering_applied": []steeringApplied{{ID: "not-owned"}}}))
	defer server.Close()
	w := httptest.NewRecorder()
	streamSingleAnswer(context.Background(), context.Background(), w, w, db, cache, server.URL,
		map[string]any{"query": "q", "run_id": "run"}, "conv-stream-decision", "q", "h",
		chatPersistTarget{HistoryID: "h", Seq: 1}, nil)
	if strings.Contains(w.Body.String(), `"steering_applied"`) || !strings.Contains(w.Body.String(), "steering_persistence_failed") {
		t.Fatalf("unexpected acknowledgement: %s", w.Body.String())
	}
}
