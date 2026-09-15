package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestForkPrefixFingerprintTracksContentNotDisplayState(t *testing.T) {
	h := orm.ChatHistory{ID: "h1", Seq: 1, RawContent: "question", Content: "question", Result: "answer", RunStatus: "completed",
		Ext: json.RawMessage(`{"input":[{"input_type":"text","text":"question"}],"conversation_config_snapshot":{"version":1,"thinking_depth":"high"}}`)}
	before, err := forkPrefixRevision([]orm.ChatHistory{h})
	if err != nil {
		t.Fatal(err)
	}
	h.FeedBack = 2
	h.Ext = json.RawMessage(`{"conversation_config_snapshot":{"thinking_depth":"high","version":1},"input":[{"text":"question","input_type":"text"}]}`)
	after, err := forkPrefixRevision([]orm.ChatHistory{h})
	if err != nil || before != after {
		t.Fatalf("equivalent history changed fingerprint: %s %s %v", before, after, err)
	}
	h.Result += " changed"
	after, err = forkPrefixRevision([]orm.ChatHistory{h})
	if err != nil || before == after {
		t.Fatalf("changed answer retained fingerprint: %v", err)
	}
}

func TestForkHistoryCopyIsIndependentAndReadOnly(t *testing.T) {
	source := []orm.ChatHistory{
		{ID: "h1", Seq: 4, ConversationID: "source", RawContent: "q1", Content: "q1", Result: "a1", RunID: "old-run", AlgorithmID: "old-session", RunStatus: "completed", FeedBack: 1,
			Ext: json.RawMessage(`{"conversation_config_snapshot":{"version":1,"thinking_depth":"low"},"ask_pending":{"id":"old-action"},"model_context":{"summary_text":"future"}}`)},
		{ID: "h2", Seq: 8, ConversationID: "source", RawContent: "q2", Content: "q2", RunStatus: "failed", RunTerminal: json.RawMessage(`{"status":"failed","error":"private stack"}`),
			Ext: json.RawMessage(`{"conversation_config_snapshot":{"version":1,"thinking_depth":"high"},"trail":{"parent_history_id":"h1","reference_history_ids":["h1","later"]}}`)},
	}
	copied, err := copyForkHistories(source, "branch")
	if err != nil {
		t.Fatal(err)
	}
	if len(copied) != 2 {
		t.Fatalf("copied %d turns", len(copied))
	}
	for i, h := range copied {
		if h.ID == source[i].ID || h.ConversationID != "branch" || h.Seq != i+1 || h.RunID != "" || h.AlgorithmID != "" || h.FeedBack != 0 {
			t.Fatalf("copied runtime or source identity: %#v", h)
		}
		if strings.Contains(string(h.Ext), "old-action") || strings.Contains(string(h.Ext), "future") || strings.Contains(string(h.RunTerminal), "private stack") {
			t.Fatal("runtime state leaked into fork")
		}
		var ext map[string]any
		if err := json.Unmarshal(h.Ext, &ext); err != nil {
			t.Fatal(err)
		}
		if ext["fork_read_only"] != true {
			t.Fatal("inherited history must be read only")
		}
	}
	if !strings.Contains(string(copied[1].Ext), copied[0].ID) || strings.Contains(string(copied[1].Ext), "later") {
		t.Fatal("history references were not remapped")
	}
	if !strings.Contains(string(copied[0].Ext), `"thinking_depth":"low"`) || !strings.Contains(string(copied[1].Ext), `"thinking_depth":"high"`) {
		t.Fatal("per-node configuration was overwritten")
	}
	if source[0].ID != "h1" || source[0].RunID != "old-run" {
		t.Fatal("source mutated")
	}
}

func TestForkStableHistoryRejectsUnsettledAndAmbiguousPrefix(t *testing.T) {
	for _, status := range []string{"generating", "running", "paused", ""} {
		if err := validateForkPrefix([]orm.ChatHistory{{ID: "h", Seq: 1, RunStatus: status}}); err == nil {
			t.Fatalf("accepted status %q", status)
		}
	}
	for _, status := range []string{"completed", "failed", "cancelled", "interrupted"} {
		if err := validateForkPrefix([]orm.ChatHistory{{ID: "h", Seq: 1, RunStatus: status}}); err != nil {
			t.Fatalf("rejected terminal %q: %v", status, err)
		}
	}
	if err := validateForkPrefix([]orm.ChatHistory{{ID: "h1", Seq: 1, Result: "a"}, {ID: "h2", Seq: 1, Result: "b"}}); err == nil {
		t.Fatal("accepted ambiguous duplicate sequence")
	}
}

func TestForkConfigSnapshotDoesNotPersistCredentials(t *testing.T) {
	body := map[string]any{
		chatModelRouteBodyKey: &chatModelRoute{Mode: "auto", ModelID: "model-a", ProviderID: "provider-a", ModelName: "A"},
		"thinking_depth":      "high", "enable_workflow": true, "enable_subagent": false,
		"agentic_config": map[string]any{"workflow_mode": "dynamic"},
		"model_config":   map[string]any{"api_key": "SECRET", "base_url": "PRIVATE"},
		"Authorization":  "SECRET", "filters": map[string]any{"kb_id": []string{"kb-a"}},
	}
	raw := mergeConversationConfigSnapshot(nil, body)
	if strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "PRIVATE") {
		t.Fatal("credentials leaked")
	}
	if !strings.Contains(string(raw), `"mode":"auto"`) || !strings.Contains(string(raw), "model-a") || !strings.Contains(string(raw), `"thinking_depth":"high"`) {
		t.Fatalf("effective configuration missing: %s", raw)
	}
}
