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

func TestMentionIsDeniedUsesOnlyTheMentionsLocalClause(t *testing.T) {
	query := "不要使用 paper-search，可以使用 web-search"
	denied := chatMention{Type: "tool", ResourceID: "paper-search", DisplayName: "paper-search"}
	allowed := chatMention{Type: "tool", ResourceID: "web-search", DisplayName: "web-search"}
	if !mentionIsDenied(query, denied) {
		t.Fatal("paper-search should be denied")
	}
	if mentionIsDenied(query, allowed) {
		t.Fatal("the earlier denial must not leak into web-search")
	}
}

func TestMentionIsDeniedHandlesConjunctionsAndCommonDenialWords(t *testing.T) {
	tests := []struct {
		query  string
		name   string
		denied bool
	}{
		{"别用 paper-search", "paper-search", true},
		{"我不想使用 paper-search", "paper-search", true},
		{"不能调用 paper-search", "paper-search", true},
		{"忽略 paper-search", "paper-search", true},
		{"do not use paper-search", "paper-search", true},
		{"不要用 paper-search 但可以用 web-search", "web-search", false},
		{"不要用 paper-search 但请使用 web-search", "web-search", false},
	}
	for _, test := range tests {
		mention := chatMention{Type: "plugin", ResourceID: test.name, DisplayName: test.name}
		if got := mentionIsDenied(test.query, mention); got != test.denied {
			t.Errorf("mentionIsDenied(%q, %q) = %v, want %v", test.query, test.name, got, test.denied)
		}
	}
}

func TestApplyExplicitResourceBindingsIncludesOnlyCurrentMentions(t *testing.T) {
	body := map[string]any{}
	applyExplicitResourceBindings(body, resolvedChatMentions{
		SkillNames:       []string{"video/ai-production"},
		KnowledgeBaseIDs: []string{"kb-video"},
		PluginRefs:       []string{"video/workflow"},
		ResourceMentions: []map[string]string{{
			"resource_type": "knowledge_base", "resource_ref": "kb-video",
			"display_name": "视频资料库",
		}},
	})
	bindings, ok := body["explicit_resource_bindings"].(map[string]any)
	if !ok {
		t.Fatalf("explicit_resource_bindings = %#v", body["explicit_resource_bindings"])
	}
	if got := bindings["skill_names"].([]string); len(got) != 1 || got[0] != "video/ai-production" {
		t.Fatalf("skill_names = %#v", got)
	}
	if got := bindings["knowledge_base_ids"].([]string); len(got) != 1 || got[0] != "kb-video" {
		t.Fatalf("knowledge_base_ids = %#v", got)
	}
	if got := bindings["plugin_refs"].([]string); len(got) != 1 || got[0] != "video/workflow" {
		t.Fatalf("plugin_refs = %#v", got)
	}
	if got := bindings["mentions"].([]map[string]string); len(got) != 1 || got[0]["display_name"] != "视频资料库" {
		t.Fatalf("mentions = %#v", got)
	}
}

func TestBuildLazyChatRequestPropagatesExplicitResourceBindings(t *testing.T) {
	req := buildLazyChatRequest(map[string]any{
		"explicit_resource_bindings": map[string]any{
			"skill_names":        []string{"video/ai-production"},
			"knowledge_base_ids": []string{"kb-video"},
			"plugin_refs":        []string{"video/workflow"},
			"mentions": []any{map[string]any{
				"resource_type": "knowledge_base", "resource_ref": "kb-video",
				"display_name": "视频资料库",
			}},
		},
	})
	if got := req.ExplicitResources.SkillNames; len(got) != 1 || got[0] != "video/ai-production" {
		t.Fatalf("SkillNames = %#v", got)
	}
	if got := req.ExplicitResources.KnowledgeBaseIDs; len(got) != 1 || got[0] != "kb-video" {
		t.Fatalf("KnowledgeBaseIDs = %#v", got)
	}
	if got := req.ExplicitResources.PluginRefs; len(got) != 1 || got[0] != "video/workflow" {
		t.Fatalf("PluginRefs = %#v", got)
	}
	if got := req.ExplicitResources.Mentions; len(got) != 1 || got[0]["resource_ref"] != "kb-video" {
		t.Fatalf("Mentions = %#v", got)
	}
}

func TestBuildLazyChatRequestPropagatesPreviewLLMConfirmation(t *testing.T) {
	req := buildLazyChatRequest(map[string]any{
		"context_usage_preview":             true,
		"context_preview_allow_llm_routing": true,
	})
	if !req.Runtime.ContextUsagePreview || !req.Runtime.ContextPreviewAllowLLMRouting {
		t.Fatalf("runtime preview flags = %#v", req.Runtime)
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
