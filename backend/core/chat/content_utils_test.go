package chat

import (
	"testing"

	"lazymind/core/common/orm"
)

func TestStripToolTagsPreservesDisplayTags(t *testing.T) {
	raw := `before<tool_call>{"name":"kb"}</tool_call><tp>thinking</tp><tool_result>{"ok":true}</tool_result>after<trp>done</trp>`

	got := stripToolTags(raw)

	want := `before<tp>thinking</tp>after<trp>done</trp>`
	if got != want {
		t.Fatalf("unexpected stripped content: got %q want %q", got, want)
	}
}

func TestStripThinkTags(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", ""},
		{"no think tags", "hello world", "hello world"},
		{"think tag stripped", "before<think>reasoning content</think>after", "beforeafter"},
		{"multiline think", "<think>step1\nstep2</think>result", "result"},
		{"think only", "<think>just thinking</think>", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripThinkTags(tt.raw)
			if got != tt.want {
				t.Fatalf("stripThinkTags: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestBuildAssistantHistoryContentPassthrough(t *testing.T) {
	// Result now contains think tags directly; buildAssistantHistoryContent passes it through.
	history := orm.ChatHistory{
		Result: "<think>先想一步</think>最终答案",
	}

	got := buildAssistantHistoryContent(history)
	want := "<think>先想一步</think>最终答案"
	if got != want {
		t.Fatalf("unexpected assistant history content: got %q want %q", got, want)
	}
}
