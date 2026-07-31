package resourceupdate

import (
	"testing"
)

// TestNormalizeStringIDs deduplicates, trims and sorts string IDs.
func TestNormalizeStringIDs(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{"no_dups", []string{"c", "a", "b"}, []string{"a", "b", "c"}},
		{"with_dups", []string{"a", "b", "a", "c"}, []string{"a", "b", "c"}},
		{"with_empty", []string{"", "a", "  ", "b", ""}, []string{"a", "b"}},
		{"all_empty", []string{"", "  "}, []string{}},
		{"nil", nil, []string{}},
		{"single", []string{"z"}, []string{"z"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeStringIDs(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len: got %d, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("[%d]: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestCountHistoryResultToolTurns counts tool_call and tool_result tags in result XML.
func TestCountHistoryResultToolTurns(t *testing.T) {
	tests := []struct {
		name   string
		result string
		known  map[string]struct{}
		want   int
	}{
		{
			name:   "empty",
			result: "",
			known:  map[string]struct{}{},
			want:   0,
		},
		{
			name:   "single_tool_call",
			result: `<tool_call>{"name":"search","id":"t1"}</tool_call>`,
			known:  map[string]struct{}{},
			want:   1,
		},
		{
			name:   "tool_call_with_result",
			result: `<tool_call>{"name":"search","id":"t1"}</tool_call><tool_result>{"id":"t1"}</tool_result>`,
			known:  map[string]struct{}{},
			want:   1, // tool_result with known id is not counted
		},
		{
			name:   "unknown_tool_result_counts",
			result: `<tool_result>{"id":"unknown"}</tool_result>`,
			known:  map[string]struct{}{},
			want:   1,
		},
		{
			name:   "multiple_tool_calls",
			result: `<tool_call>{"name":"a","id":"x1"}</tool_call><tool_call>{"name":"b","id":"x2"}</tool_call>`,
			known:  map[string]struct{}{},
			want:   2,
		},
		{
			name:   "invalid_json_skipped",
			result: `<tool_call>not json</tool_call>`,
			known:  map[string]struct{}{},
			want:   0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countHistoryResultToolTurns(tt.result, tt.known)
			if got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

// TestParseHistoryToolTagPayload parses JSON payload from regex match.
func TestParseHistoryToolTagPayload(t *testing.T) {
	tests := []struct {
		name  string
		match []string
		isNil bool
	}{
		{"valid_json", []string{"full", `{"name":"search","id":"t1"}`}, false},
		{"short_match", []string{"one"}, true},
		{"nil_match", nil, true},
		{"empty_match", []string{}, true},
		{"invalid_json", []string{"full", "not json"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseHistoryToolTagPayload(tt.match)
			if tt.isNil && got != nil {
				t.Fatalf("expected nil, got %#v", got)
			}
			if !tt.isNil && got == nil {
				t.Fatal("expected non-nil")
			}
		})
	}

	// Verify parsed values for valid payload
	payload := parseHistoryToolTagPayload([]string{"full", `{"name":"search","id":"t1"}`})
	if payload["name"] != "search" {
		t.Fatalf("name = %v, want search", payload["name"])
	}
	if payload["id"] != "t1" {
		t.Fatalf("id = %v, want t1", payload["id"])
	}
}
