package chat

import (
	"net/http/httptest"
	"testing"
)

// --- mergeChunksToFirstChunk ---

// TestMergeChunksToFirstChunk_Empty returns nil for nil input.
func TestMergeChunksToFirstChunk_Empty(t *testing.T) {
	if got := mergeChunksToFirstChunk(nil); got != nil {
		t.Fatal("expected nil for nil input")
	}
	if got := mergeChunksToFirstChunk([]*ChatChunkResponse{}); got != nil {
		t.Fatal("expected nil for empty slice")
	}
}

// TestMergeChunksToFirstChunk_Single returns the chunk with same delta.
func TestMergeChunksToFirstChunk_Single(t *testing.T) {
	chunks := []*ChatChunkResponse{
		{Seq: 1, HistoryID: "h1", Delta: "hello", ReasoningContent: ""},
	}
	got := mergeChunksToFirstChunk(chunks)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.Delta != "hello" {
		t.Fatalf("delta: got %q, want hello", got.Delta)
	}
	if got.Seq != 1 {
		t.Fatalf("seq: got %d, want 1", got.Seq)
	}
	if got.DeltaMode != ChatDeltaModeReplace {
		t.Fatalf("delta mode: got %q, want %q", got.DeltaMode, ChatDeltaModeReplace)
	}
}

// TestMergeChunksToFirstChunk_Multiple concatenates deltas and reasoning from all chunks.
func TestMergeChunksToFirstChunk_Multiple(t *testing.T) {
	chunks := []*ChatChunkResponse{
		{Seq: 1, Delta: "abc"},
		{Seq: 2, Delta: "def", ReasoningContent: "thinking..."},
		{Seq: 3, Delta: "ghi", Sources: []any{map[string]any{"title": "doc1"}}},
	}
	got := mergeChunksToFirstChunk(chunks)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.Delta != "abcdefghi" {
		t.Fatalf("delta: got %q, want abcdefghi", got.Delta)
	}
	if got.ReasoningContent != "thinking..." {
		t.Fatalf("reasoning: got %q, want thinking...", got.ReasoningContent)
	}
	// Last chunk's metadata is preserved.
	if got.Seq != 3 {
		t.Fatalf("seq: got %d, want 3", got.Seq)
	}
	if len(got.Sources) != 1 {
		t.Fatalf("sources: got %d, want 1", len(got.Sources))
	}
	if got.DeltaMode != ChatDeltaModeReplace {
		t.Fatalf("delta mode: got %q, want %q", got.DeltaMode, ChatDeltaModeReplace)
	}
}

func TestMergeChunksToFirstChunk_PreservesLastNonEmptySources(t *testing.T) {
	external := map[string]any{
		"source_type": "external",
		"index":       "1.1",
		"title":       "Example",
		"url":         "https://example.com",
	}
	got := mergeChunksToFirstChunk([]*ChatChunkResponse{
		{Delta: "answer", Sources: []any{external}},
		{},
	})
	if len(got.Sources) != 1 {
		t.Fatalf("sources: got %d, want 1", len(got.Sources))
	}
	if got.Sources[0].(map[string]any)["url"] != "https://example.com" {
		t.Fatalf("unexpected source: %#v", got.Sources[0])
	}
}

func TestRetrievalSourcesPreservesExternalSource(t *testing.T) {
	sources := []any{map[string]any{
		"source_type":  "external",
		"index":        "2.1",
		"title":        "External source",
		"url":          "https://example.com/article",
		"content":      "Evidence",
		"source_roles": []any{"cited", "searched"},
	}}
	got := retrievalSources(marshalRetrievalResult(sources))
	if len(got) != 1 {
		t.Fatalf("sources: got %d, want 1", len(got))
	}
	source := got[0].(map[string]any)
	if source["source_type"] != "external" || source["index"] != "2.1" {
		t.Fatalf("unexpected source: %#v", source)
	}
	roles, ok := source["source_roles"].([]any)
	if !ok || len(roles) != 2 || roles[0] != "cited" || roles[1] != "searched" {
		t.Fatalf("source roles were not preserved: %#v", source["source_roles"])
	}
}

func TestRetrievalSourcesSupportsLegacySourceMap(t *testing.T) {
	raw := []byte(`{"sources":{` +
		`"9.1":{"source_type":"external","title":"Legacy","url":"https://example.com"},` +
		`"1.1":{"file_name":"guide.pdf","index":"existing"}` +
		`}}`)
	got := retrievalSources(raw)
	if len(got) != 2 {
		t.Fatalf("sources: got %d, want 2", len(got))
	}
	first := got[0].(map[string]any)
	if first["index"] != "existing" || first["file_name"] != "guide.pdf" {
		t.Fatalf("existing index should be preserved: %#v", first)
	}
	second := got[1].(map[string]any)
	if second["index"] != "9.1" || second["title"] != "Legacy" {
		t.Fatalf("map key should be restored as index: %#v", second)
	}
}

// TestMergeChunksToFirstChunk_SkipNil skips nil entries in the slice.
func TestMergeChunksToFirstChunk_SkipNil(t *testing.T) {
	chunks := []*ChatChunkResponse{
		nil,
		{Seq: 1, Delta: "a"},
		nil,
		{Seq: 2, Delta: "b"},
	}
	got := mergeChunksToFirstChunk(chunks)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.Delta != "ab" {
		t.Fatalf("delta: got %q, want ab", got.Delta)
	}
}

// TestMergeChunksToFirstChunk_AllNil returns nil when all entries are nil.
func TestMergeChunksToFirstChunk_AllNil(t *testing.T) {
	chunks := []*ChatChunkResponse{nil, nil}
	if got := mergeChunksToFirstChunk(chunks); got != nil {
		t.Fatal("expected nil when all chunks are nil")
	}
}

// TestMergeChunksToFirstChunk_IntentUpdated accumulates the last non-nil intent.
func TestMergeChunksToFirstChunk_IntentUpdated(t *testing.T) {
	evt1 := &IntentUpdatedEvent{Scope: "search"}
	evt2 := &IntentUpdatedEvent{Scope: "chat"}
	chunks := []*ChatChunkResponse{
		{Seq: 1, Delta: "a", IntentUpdated: evt1},
		{Seq: 2, Delta: "b", IntentUpdated: evt2},
	}
	got := mergeChunksToFirstChunk(chunks)
	if got.IntentUpdated == nil {
		t.Fatal("expected non-nil intent")
	}
	if got.IntentUpdated.Scope != "chat" {
		t.Fatalf("intent: got %q, want chat (last non-nil wins)", got.IntentUpdated.Scope)
	}
}

// --- displayChatHistoryContent ---

// TestDisplayChatHistoryContent_Passthrough returns raw if no task tags.
func TestDisplayChatHistoryContent_Passthrough(t *testing.T) {
	raw := "plain text without tags"
	if got := displayChatHistoryContent(raw); got != raw {
		t.Fatalf("got %q, want %q", got, raw)
	}
}

// TestDisplayChatHistoryContent_ExtractsTaskRequestContent extracts text between tags.
func TestDisplayChatHistoryContent_ExtractsTaskRequestContent(t *testing.T) {
	raw := "prefix <current-task-request>do this</current-task-request> suffix"
	want := "do this"
	if got := displayChatHistoryContent(raw); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestDisplayChatHistoryContent_StripsChineseTaskPrefix removes the known task prefix.
func TestDisplayChatHistoryContent_StripsChineseTaskPrefix(t *testing.T) {
	raw := "<current-task-request>这是当前需要执行的任务要求，请使用上方已完成的历史执行结果作答：actual task</current-task-request>"
	want := "actual task"
	if got := displayChatHistoryContent(raw); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestDisplayChatHistoryContent_MissingCloseTag returns raw unchanged.
func TestDisplayChatHistoryContent_MissingCloseTag(t *testing.T) {
	raw := "start <current-task-request>unclosed"
	if got := displayChatHistoryContent(raw); got != raw {
		t.Fatalf("got %q, want %q", got, raw)
	}
}

// TestDisplayChatHistoryContent_OnlyOpenTag returns raw unchanged.
func TestDisplayChatHistoryContent_OnlyOpenTag(t *testing.T) {
	raw := "<current-task-request>"
	if got := displayChatHistoryContent(raw); got != raw {
		t.Fatalf("got %q, want %q", got, raw)
	}
}

// TestDisplayChatHistoryContent_LastTagOnly extracts content from the last occurrence.
func TestDisplayChatHistoryContent_LastTagOnly(t *testing.T) {
	raw := "<current-task-request>first</current-task-request> middle <current-task-request>second</current-task-request>"
	want := "second"
	if got := displayChatHistoryContent(raw); got != want {
		t.Fatalf("got %q, want %q (last occurrence wins)", got, want)
	}
}

// --- parseConversationHistoryPage ---

// TestParseConversationHistoryPage_Defaults returns pageSize 20, offset 0.
func TestParseConversationHistoryPage_Defaults(t *testing.T) {
	req := httptest.NewRequest("GET", "/?page_size=5&page_token=10", nil)
	ps, off := parseConversationHistoryPage(req)
	if ps != 5 {
		t.Fatalf("pageSize: got %d, want 5", ps)
	}
	if off != 10 {
		t.Fatalf("offset: got %d, want 10", off)
	}
}

// TestParseConversationHistoryPage_NoParams returns defaults 20/0.
func TestParseConversationHistoryPage_NoParams(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	ps, off := parseConversationHistoryPage(req)
	if ps != 20 {
		t.Fatalf("pageSize: got %d, want 20", ps)
	}
	if off != 0 {
		t.Fatalf("offset: got %d, want 0", off)
	}
}

// TestParseConversationHistoryPage_MaxPageSize caps at 100.
func TestParseConversationHistoryPage_MaxPageSize(t *testing.T) {
	req := httptest.NewRequest("GET", "/?page_size=200", nil)
	ps, _ := parseConversationHistoryPage(req)
	if ps != 100 {
		t.Fatalf("pageSize: got %d, want 100 (capped)", ps)
	}
}

// TestParseConversationHistoryPage_InvalidParams falls back to defaults.
func TestParseConversationHistoryPage_InvalidParams(t *testing.T) {
	req := httptest.NewRequest("GET", "/?page_size=abc&page_token=-1", nil)
	ps, off := parseConversationHistoryPage(req)
	if ps != 20 {
		t.Fatalf("pageSize: got %d, want 20", ps)
	}
	if off != 0 {
		t.Fatalf("offset: got %d, want 0", off)
	}
}
