package workflow

import (
	"reflect"
	"strings"
	"testing"
)

func TestRewriteMarkdownSourceBoundaries(t *testing.T) {
	for _, source := range []string{"😀 First.\n\nLast.", "😀 First.\r\n\r\nLast.", "😀 First.\n  \nLast."} {
		selections := []map[string]any{{"start": 0, "end": len([]rune(source)), "selected_text": source}}
		blocks, ok := selectedRewriteMarkdownBlocks(source, selections)
		if !ok || len(blocks) != 2 || blocks[0].raw != "😀 First." || blocks[1].raw != "Last." {
			t.Fatalf("bad paragraph boundaries for %q: %#v", source, blocks)
		}
		for _, block := range blocks {
			if string([]rune(source)[block.start:block.end]) != block.raw {
				t.Fatal("offsets are not source code points")
			}
		}
	}
}
func TestRewriteMarkdownSourceRejectsUnsupportedOrAmbiguousQuotes(t *testing.T) {
	for _, source := range []string{"First.\n\n> Quote\n\nLast.", "First.\n\n```\nCode\n\nStill code\n```\n\nLast.", "First.\n\n| A |\n| - |\n| B |\n\nLast."} {
		if _, ok := selectedRewriteMarkdownBlocks(source, []map[string]any{{"start": 0, "end": len([]rune(source)), "selected_text": source}}); ok {
			t.Fatalf("accepted unsupported structure: %q", source)
		}
	}
	if _, ok := selectedRewriteMarkdownBlocks("Same.\n\nSame.", []map[string]any{{"selected_text": "Same"}}); ok {
		t.Fatal("accepted ambiguous rendered quote")
	}
}

func TestRewriteMarkdownHeadingAndListBoundaries(t *testing.T) {
	for _, tc := range []struct {
		source string
		texts  []string
	}{
		{"前言\n## 标题 **重点** ##\n3. 第一条\n4. 第二条\n\n结尾", []string{"前言", "标题 **重点**", "第一条", "第二条", "结尾"}},
		{"Heading\n=======\n\n正文", []string{"Heading", "正文"}},
		{"3. Parent\n   1. Child\n   2. Next\n4. Last", []string{"Parent", "Child", "Next", "Last"}},
		{"3. Parent\n\n   7. Child\n   8. Next\n4. Last", []string{"Parent", "Child", "Next", "Last"}},
		{"3. First\n   continuation\n\n   Second paragraph\n4. Last", []string{"First\n   continuation", "Second paragraph", "Last"}},
		{"- [x] Task\n- [ ] Next", []string{"Task", "Next"}},
		{"  😀 text  \r\n   next  \r\n", []string{"😀 text  \r\n   next"}},
	} {
		t.Run(tc.source, func(t *testing.T) {
			for _, offsets := range []bool{true, false} {
				selections := []map[string]any{}
				for _, old := range tc.texts {
					selected := map[string]any{"selected_text": old}
					if offsets {
						start := len([]rune(tc.source[:strings.Index(tc.source, old)]))
						selected["start"], selected["end"] = start, start+len([]rune(old))
					}
					selections = append(selections, selected)
				}
				blocks, ok := selectedRewriteMarkdownBlocks(tc.source, selections)
				if !ok || len(blocks) != len(tc.texts) {
					t.Fatalf("offsets=%v: missing text blocks: %#v", offsets, blocks)
				}
				for i, block := range blocks {
					if block.raw != tc.texts[i] || string([]rune(tc.source)[block.start:block.end]) != tc.texts[i] {
						t.Fatalf("offsets=%v: block %d includes markers or adjacent content: %#v", offsets, i, block)
					}
				}
			}
		})
	}
}
func TestRewriteMarkdownFormattedAndEscapedQuotes(t *testing.T) {
	source := "前缀**原文**后缀。\n\nA &amp; B with `x_y` and [link](https://example.test)."
	selected := []map[string]any{{"selected_text": "前缀原文"}, {"selected_text": "后缀"}, {"selected_text": "A &amp; B"}, {"selected_text": "x_y"}, {"selected_text": "link"}}
	blocks, ok := selectedRewriteMarkdownBlocks(source, selected)
	if !ok || len(blocks) != 2 {
		t.Fatalf("rendered quotes failed or were not deduplicated: %#v", blocks)
	}
}

func TestRewriteIRSpanDefaultsNested(t *testing.T) {
	source := map[string]any{"blocks": []any{map[string]any{"node_id": "parent", "children": []any{map[string]any{"node_id": "child", "spans": []any{map[string]any{"text": "", "style": map[string]any{}}}}}}}}
	candidate := map[string]any{"blocks": []any{map[string]any{"node_id": "parent", "children": []any{map[string]any{"node_id": "child", "spans": []any{map[string]any{}}}}}}}
	normalizeRewriteIRDefaults(source, true)
	normalizeRewriteIRDefaults(candidate, true)
	if !reflect.DeepEqual(source, candidate) {
		t.Fatal("nested span defaults were treated as content changes")
	}
}
