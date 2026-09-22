package main

import (
	"encoding/json"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func structuredRewriteFixture(t *testing.T, source string, texts, replacements, types []string) (rewriteFixture, *rewriteServer, map[string]any) {
	t.Helper()
	f := newRewriteFixture(t, "markdown")
	f.source, f.candidate = source, source
	items, selections := []any{}, []any{}
	for i, old := range texts {
		start := len([]rune(source[:strings.Index(source, old)]))
		// Focus on part of each block, while the response replaces its complete text.
		quoteStart := len([]rune(old)) / 2
		selections = append(selections, map[string]any{"start": start + quoteStart, "end": start + len([]rune(old)), "selected_text": string([]rune(old)[quoteStart:])})
		items = append(items, map[string]any{
			"target":  map[string]any{"type": "block", "block_type": types[i], "target_start": start, "target_end": start + len([]rune(old))},
			"preview": map[string]any{"old_text": old, "new_text": replacements[i]},
			"patch":   map[string]any{"type": "string_replace_set", "payload": map[string]any{}},
		})
		f.candidate = strings.Replace(f.candidate.(string), old, replacements[i], 1)
	}
	if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("value", json.RawMessage(mustJSONRewrite(source))).Error; err != nil {
		t.Fatal(err)
	}
	server := newRewriteServer(t, f)
	body := f.body("preview", "")
	body["input"] = map[string]any{"instruction": "润色", "type": "markdown", "selection_ranges": selections}
	json.Unmarshal([]byte(mustJSONRewrite(body["input"])), &server.argumentsOverride)
	server.resultOverride = map[string]any{"representation": "markdown", "results": items, "artifact": map[string]any{"content_type": "text", "value": f.candidate}, "commit": map[string]any{"token": "00000000000000000000000000000001"}}
	return f, server, body
}

func TestDocumentRewriteHeadingsAndLists(t *testing.T) {
	for _, tc := range []struct {
		name, source     string
		old, next, types []string
	}{
		{"ordered list", "3. 第一条选中文本。\n4. 第二条。\n\n保留原文。", []string{"第一条选中文本。", "第二条。"}, []string{"第一条更流畅。", "第二条更清晰。"}, []string{"list_item", "list_item"}},
		{"mixed", "## 原标题 ##\n\n原段落。\n\n- 原列表。\n- 保留列表。", []string{"原标题", "原段落。", "原列表。"}, []string{"新标题", "新段落。", "新列表。"}, []string{"heading", "paragraph", "list_item"}},
		{"nested", "3. Parent\n\n   7. Child\n   8. Keep\n4. Last", []string{"Parent", "Child"}, []string{"Clear parent", "Clear child"}, []string{"list_item", "list_item"}},
		{"task", "- [x] First task\n- [ ] Next task", []string{"First task", "Next task"}, []string{"Clear first task", "Clear next task"}, []string{"list_item", "list_item"}},
		{"setext", "Heading\n=======\n\nKeep.", []string{"Heading"}, []string{"Clear heading"}, []string{"heading"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, server, body := structuredRewriteFixture(t, tc.source, tc.old, tc.next, tc.types)
			before := rewriteSnapshot(t, f)
			result := rewriteData(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body))
			if len(result["results"].([]any)) != len(tc.old) || rewriteSnapshot(t, f) != before {
				t.Fatal("preview lost targets or modified the source")
			}
			server.resultOverride = nil
			saved := rewriteData(t, f.post(t.Context(), "execute", "descriptor-artifact", "descriptor-owner", f.body("execute", result["commit"].(map[string]any)["token"].(string))))
			if saved["artifact_id"] == nil {
				t.Fatal("structured rewrite was not saved")
			}
		})
	}
}

func TestDocumentRewriteStructuredRejectsChangedStructure(t *testing.T) {
	for _, mutation := range []string{"block type", "target includes marker", "numbering", "unselected sibling", "split item", "heading level"} {
		t.Run(mutation, func(t *testing.T) {
			f, server, body := structuredRewriteFixture(t, "## Heading\n\n3. First\n4. Keep", []string{"Heading", "First"}, []string{"Clear heading", "Clear first"}, []string{"heading", "list_item"})
			items := server.resultOverride["results"].([]any)
			candidate := f.candidate.(string)
			switch mutation {
			case "block type":
				items[1].(map[string]any)["target"].(map[string]any)["block_type"] = "paragraph"
			case "target includes marker":
				target := items[1].(map[string]any)["target"].(map[string]any)
				target["target_start"] = target["target_start"].(int) - 3
				items[1].(map[string]any)["preview"].(map[string]any)["old_text"] = "3. First"
			case "numbering":
				candidate = strings.Replace(candidate, "3. ", "1. ", 1)
			case "unselected sibling":
				candidate = strings.Replace(candidate, "Keep", "Changed", 1)
			case "split item":
				items[1].(map[string]any)["preview"].(map[string]any)["new_text"] = "Clear first\n4. Injected"
				candidate = strings.Replace(candidate, "Clear first", "Clear first\n4. Injected", 1)
			case "heading level":
				candidate = strings.Replace(candidate, "## ", "### ", 1)
			}
			server.resultOverride["artifact"].(map[string]any)["value"] = candidate
			before := rewriteSnapshot(t, f)
			rewriteError(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", body), 502, "DOCUMENT_ACTION_RESULT_INVALID")
			if rewriteSnapshot(t, f) != before {
				t.Fatal("invalid preview changed the document")
			}
		})
	}
}
