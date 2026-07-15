package chat

import (
	"encoding/json"
	"testing"

	"lazymind/core/common/orm"
)

func TestParseChatMentionsDeduplicatesByTypeAndResource(t *testing.T) {
	raw := map[string]any{"mentions": []any{
		map[string]any{"mention_id": "m1", "type": "tool", "resource_id": "search", "display_name": "Search"},
		map[string]any{"mention_id": "m2", "type": "tool", "resource_id": "search", "display_name": "Search"},
		map[string]any{"mention_id": "m3", "type": "skill", "resource_id": "search", "display_name": "Search skill"},
	}}
	mentions, err := parseChatMentions(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(mentions) != 2 {
		t.Fatalf("len(mentions) = %d, want 2", len(mentions))
	}
}

func TestApplyMentionedToolsOnlyEnablesMentionedNames(t *testing.T) {
	got := applyMentionedTools([]string{"search", "python", "browser"}, []string{"python"})
	want := []string{"search", "browser"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("applyMentionedTools() = %#v, want %#v", got, want)
	}
}

func TestMergeMentionedDatasetsPreservesDefaultsAndDeduplicates(t *testing.T) {
	raw := map[string]any{"conversation": map[string]any{"search_config": map[string]any{
		"dataset_list": []any{map[string]any{"id": "default"}},
	}}}
	mergeMentionedDatasets(raw, []string{"mentioned", "default"})
	conversation := raw["conversation"].(map[string]any)
	search := conversation["search_config"].(map[string]any)
	list := search["dataset_list"].([]any)
	if len(list) != 2 {
		t.Fatalf("dataset_list = %#v, want two unique entries", list)
	}
}

func TestBuildChatHistoryExtPersistsMentions(t *testing.T) {
	raw := map[string]any{
		"input":    []any{map[string]any{"input_type": "text", "text": "use Search"}},
		"mentions": []any{map[string]any{"mention_id": "m1", "type": "tool", "resource_id": "search", "display_name": "Search"}},
	}
	ext := string(buildChatHistoryExt(raw, "use Search"))
	if ext == "" || !containsAll(ext, `"mentions"`, `"resource_id":"search"`) {
		t.Fatalf("history ext did not persist mentions: %s", ext)
	}
}

func TestRecentHistoryMentionsUsesNewestTurnsFirst(t *testing.T) {
	histories := []orm.ChatHistory{
		{Ext: json.RawMessage(`{"mentions":[{"type":"knowledge_base","resource_id":"old","display_name":"Old"}]}`)},
		{Ext: json.RawMessage(`{"mentions":[{"type":"knowledge_base","resource_id":"new","display_name":"New"}]}`)},
	}
	got := recentHistoryMentions(histories, 1)
	if len(got) != 1 || got[0].ResourceID != "new" {
		t.Fatalf("recentHistoryMentions() = %#v, want newest mention", got)
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		found := false
		for i := 0; i+len(part) <= len(value); i++ {
			if value[i:i+len(part)] == part {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
