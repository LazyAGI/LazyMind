package chat

import (
	"encoding/json"
	"testing"

	"lazymind/core/common/orm"
)

func TestFilterHistoriesAfterCovered(t *testing.T) {
	histories := []orm.ChatHistory{
		{ID: "a", Seq: 1},
		{ID: "b", Seq: 2},
		{ID: "c", Seq: 3},
	}
	got := filterHistoriesAfterCovered(histories, 2)
	if len(got) != 1 || got[0].Seq != 3 {
		t.Fatalf("expected only seq=3, got %#v", got)
	}
}

func TestBuildModelHistoryMessagesPrependsSummary(t *testing.T) {
	histories := []orm.ChatHistory{
		{ID: "a", Seq: 1, RawContent: "old", Result: "old-ans"},
		{ID: "b", Seq: 2, RawContent: "new", Result: "new-ans"},
	}
	modelCtx := &modelContextState{
		SummaryText:       "## Current task\nShip it",
		CoveredThroughSeq: 1,
		Version:           1,
	}
	msgs := buildModelHistoryMessages(histories, nil, modelCtx)
	if len(msgs) != 3 {
		t.Fatalf("expected summary + user/assistant for seq2, got %d msgs: %#v", len(msgs), msgs)
	}
	if msgs[0]["role"] != "user" {
		t.Fatalf("summary role: %#v", msgs[0]["role"])
	}
	content, _ := msgs[0]["content"].(string)
	if content == "" || content[:10] != "The follow" {
		t.Fatalf("summary disclaimer missing: %q", content)
	}
	if msgs[1]["history_seq"] != 2 || msgs[1]["content"] != "new" {
		t.Fatalf("unexpected tail user msg: %#v", msgs[1])
	}
}

func TestBuildModelHistoryMessagesIgnoresOutOfRangeCoverage(t *testing.T) {
	histories := []orm.ChatHistory{
		{ID: "a", Seq: 1, RawContent: "old", Result: "old-ans"},
		{ID: "b", Seq: 2, RawContent: "new", Result: "new-ans"},
	}
	modelCtx := &modelContextState{
		SummaryText:       "## Current task\nShip it",
		CoveredThroughSeq: 9,
		Version:           1,
	}
	msgs := buildModelHistoryMessages(histories, nil, modelCtx)
	if len(msgs) != 4 {
		t.Fatalf("expected full history without summary-only truncation, got %d msgs: %#v", len(msgs), msgs)
	}
	if msgs[0]["content"] != "old" || msgs[3]["content"] != "new-ans" {
		t.Fatalf("unexpected history replay after invalid coverage: %#v", msgs)
	}
}

func TestBuildHistoryMessagesIncludesHistorySeq(t *testing.T) {
	histories := []orm.ChatHistory{
		{ID: "a", Seq: 7, RawContent: "q", Result: "a"},
	}
	msgs := buildHistoryMessages(histories, nil)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0]["history_seq"] != 7 || msgs[1]["history_seq"] != 7 {
		t.Fatalf("history_seq missing: %#v", msgs)
	}
}

func TestModelContextStateJSONRoundTrip(t *testing.T) {
	raw := []byte(`{"model_context":{"summary_text":"hello","covered_through_seq":9,"version":1}}`)
	ext := map[string]any{}
	if err := json.Unmarshal(raw, &ext); err != nil {
		t.Fatal(err)
	}
	mc, _ := ext["model_context"].(map[string]any)
	if mc["summary_text"] != "hello" {
		t.Fatalf("unexpected %#v", mc)
	}
}

func TestClearModelContextPreservesOtherConversationExt(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{})
	ext := json.RawMessage(`{
		"model_context":{"summary_text":"stale","covered_through_seq":9,"version":1},
		"workflow_preflight":{"step":"outline"}
	}`)
	if err := db.Create(&orm.Conversation{ID: "conv-1", Ext: ext}).Error; err != nil {
		t.Fatal(err)
	}

	if err := clearModelContext(t.Context(), db.DB, "conv-1"); err != nil {
		t.Fatal(err)
	}

	var conv orm.Conversation
	if err := db.Where("id = ?", "conv-1").First(&conv).Error; err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	if err := json.Unmarshal(conv.Ext, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["model_context"]; ok {
		t.Fatalf("model_context was not cleared: %#v", got)
	}
	if _, ok := got["workflow_preflight"]; !ok {
		t.Fatalf("unrelated ext was removed: %#v", got)
	}
}

func TestHandleModelContextUpdatedMergesSidecarWithoutDroppingSummary(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{})
	ext := json.RawMessage(`{"model_context":{"summary_text":"keep me","covered_through_seq":4,"version":1}}`)
	if err := db.Create(&orm.Conversation{ID: "conv-sidecar", Ext: ext}).Error; err != nil {
		t.Fatal(err)
	}
	handleModelContextUpdated(t.Context(), db.DB, "conv-sidecar", &ModelContextUpdatedEvent{
		ActiveSkills: json.RawMessage(`[{"name":"demo","path":"/skills/demo/SKILL.md","hash":"abc"}]`),
		TaskGoal:     json.RawMessage(`"keep the bid outline on track"`),
	})
	var conv orm.Conversation
	if err := db.Where("id = ?", "conv-sidecar").First(&conv).Error; err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	if err := json.Unmarshal(conv.Ext, &got); err != nil {
		t.Fatal(err)
	}
	mc, _ := got["model_context"].(map[string]any)
	if mc["summary_text"] != "keep me" {
		t.Fatalf("summary dropped: %#v", mc)
	}
	if mc["covered_through_seq"] != float64(4) {
		t.Fatalf("covered changed: %#v", mc["covered_through_seq"])
	}
	skills, _ := mc["active_skills"].([]any)
	if len(skills) != 1 {
		t.Fatalf("active_skills missing: %#v", mc)
	}
	if mc["task_goal"] != "keep the bid outline on track" {
		t.Fatalf("task_goal missing: %#v", mc)
	}
}
