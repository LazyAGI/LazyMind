package chat

import (
	"testing"

	"lazymind/core/common/orm"
)

func TestApplyCatalogWindowIfMissingKeepsPythonBudget(t *testing.T) {
	pythonBudget := int64(64000)
	report := &ContextUsageResponse{
		EstimatedTokens:              3200,
		MaxInputTokens:               &pythonBudget,
		CompressionApplied:           true,
		CompressionCoveredThroughSeq: 14,
	}
	applyCatalogWindowIfMissing(t.Context(), nil, "user-1", report)
	if report.MaxInputTokens == nil || *report.MaxInputTokens != 64000 {
		t.Fatalf("MaxInputTokens = %v, want 64000 from python", report.MaxInputTokens)
	}
	if !report.CompressionApplied || report.CompressionCoveredThroughSeq != 14 {
		t.Fatalf("compression fields mutated: %#v", report)
	}
	if report.EstimatedRatio == nil {
		t.Fatal("EstimatedRatio is nil")
	}
	if got := *report.EstimatedRatio; got < 0.049 || got > 0.051 {
		t.Fatalf("EstimatedRatio = %v, want ~0.05", got)
	}
}

func TestParseMaxInputTokens(t *testing.T) {
	tests := map[string]int64{
		"128K": 128000,
		"200k": 200000,
		"1M":   1000000,
		"32":   32,
	}
	for input, expected := range tests {
		got := parseMaxInputTokens(input)
		if got == nil || *got != expected {
			t.Fatalf("parseMaxInputTokens(%q) = %v, want %d", input, got, expected)
		}
	}
	for _, input := range []string{"", "nope", "0"} {
		if got := parseMaxInputTokens(input); got != nil {
			t.Fatalf("parseMaxInputTokens(%q) = %d, want nil", input, *got)
		}
	}
}

func TestPreviewQueryReadsTextInput(t *testing.T) {
	raw := map[string]any{
		"input": []any{
			map[string]any{"input_type": "text", "text": "  hello  "},
			map[string]any{"input_type": "file", "uri": "/tmp/a.txt"},
		},
	}
	if got := previewQuery(raw); got != "hello" {
		t.Fatalf("previewQuery() = %q, want hello", got)
	}
}

func TestMentionedBuiltinWorkflowReplacesDefaultCatalog(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserWorkflowSetting{})
	catalog := []map[string]any{{"workflow_ref": "plugin:default", "workflow_id": "default"}}
	selected, builtins, err := mergeMentionedWorkflows(
		t.Context(), db.DB, "user-1", []string{"builtin:image-workflow"}, catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 0 {
		t.Fatalf("selected catalog = %#v, want no default workflows", selected)
	}
	if len(builtins) != 1 || builtins[0] != "image-workflow" {
		t.Fatalf("builtins = %#v, want image-workflow", builtins)
	}
}

// TestParseMaxInputTokens_EdgeCases covers more input variants.
func TestParseMaxInputTokens_EdgeCases(t *testing.T) {
	tests := []struct {
		input string
		want  *int64
	}{
		{"0.5K", int64Ptr(500)},
		{"1.5M", int64Ptr(1500000)},
		{"1k", int64Ptr(1000)},
		{"1m", int64Ptr(1000000)},
		{"2048", int64Ptr(2048)},
		{" 128k ", int64Ptr(128000)},
		{"", nil},
		{"nope", nil},
		{"0", nil},
		{"-1K", nil},
		{"K", nil},
		{"M", nil},
	}
	for _, tt := range tests {
		got := parseMaxInputTokens(tt.input)
		if tt.want == nil {
			if got != nil {
				t.Fatalf("parseMaxInputTokens(%q) = %d, want nil", tt.input, *got)
			}
		} else {
			if got == nil || *got != *tt.want {
				t.Fatalf("parseMaxInputTokens(%q) = %v, want %d", tt.input, got, *tt.want)
			}
		}
	}
}

func int64Ptr(v int64) *int64 { return &v }

// TestPreviewQuery_QueryKey extracts the "query" key from raw map.
func TestPreviewQuery_QueryKey(t *testing.T) {
	raw := map[string]any{"query": "  what is AI?  "}
	if got := previewQuery(raw); got != "what is AI?" {
		t.Fatalf("previewQuery(query) = %q, want 'what is AI?'", got)
	}
}

// TestPreviewQuery_ContentKey falls back to "content" when "query" is missing.
func TestPreviewQuery_ContentKey(t *testing.T) {
	raw := map[string]any{"content": "  summarize this  "}
	if got := previewQuery(raw); got != "summarize this" {
		t.Fatalf("previewQuery(content) = %q, want 'summarize this'", got)
	}
}

// TestPreviewQuery_QueryOverContent prefers "query" over "content" when both present.
func TestPreviewQuery_QueryOverContent(t *testing.T) {
	raw := map[string]any{"query": "Q", "content": "C"}
	if got := previewQuery(raw); got != "Q" {
		t.Fatalf("previewQuery = %q, want Q (query wins)", got)
	}
}

// TestPreviewQuery_EmptyQuery uses content when query is whitespace.
func TestPreviewQuery_EmptyQuery(t *testing.T) {
	raw := map[string]any{"query": "   ", "content": "ok"}
	if got := previewQuery(raw); got != "ok" {
		t.Fatalf("previewQuery = %q, want ok", got)
	}
}

// TestPreviewQuery_InputTextItem extracts text from input array items.
func TestPreviewQuery_InputTextItem(t *testing.T) {
	raw := map[string]any{
		"input": []any{
			map[string]any{"input_type": "file", "uri": "/a.txt"},
			map[string]any{"input_type": "text", "text": " extracted text "},
		},
	}
	if got := previewQuery(raw); got != "extracted text" {
		t.Fatalf("previewQuery(input) = %q, want 'extracted text'", got)
	}
}

// TestPreviewQuery_NoMatch returns empty when nothing matches.
func TestPreviewQuery_NoMatch(t *testing.T) {
	raw := map[string]any{"unknown": "value"}
	if got := previewQuery(raw); got != "" {
		t.Fatalf("previewQuery = %q, want empty", got)
	}
}

// TestPreviewQuery_InputWithoutText ignores input items that are not text type.
func TestPreviewQuery_InputWithoutText(t *testing.T) {
	raw := map[string]any{
		"input": []any{
			map[string]any{"input_type": "file", "uri": "/a.txt"},
		},
	}
	if got := previewQuery(raw); got != "" {
		t.Fatalf("previewQuery(file-only input) = %q, want empty", got)
	}
}

// TestPreviewQuery_InputParsingGraceful handles malformed input entries.
func TestPreviewQuery_InputParsingGraceful(t *testing.T) {
	raw := map[string]any{
		"input": []any{
			"not-a-map",
			map[string]any{"input_type": "text", "text": "valid"},
		},
	}
	if got := previewQuery(raw); got != "valid" {
		t.Fatalf("previewQuery(mixed input) = %q, want valid", got)
	}
}
