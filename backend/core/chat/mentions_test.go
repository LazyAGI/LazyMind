package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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

func TestParseChatMentionsAcceptsWorkflow(t *testing.T) {
	raw := map[string]any{"mentions": []any{
		map[string]any{"mention_id": "m1", "type": "workflow", "resource_id": "builtin:test-workflow", "display_name": "Smoke Test"},
	}}
	mentions, err := parseChatMentions(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(mentions) != 1 || mentions[0].Type != "workflow" {
		t.Fatalf("mentions=%#v", mentions)
	}
}

func TestApplyChatMentionsRejectsRemovedPluginWireType(t *testing.T) {
	raw := map[string]any{"mentions": []any{
		map[string]any{"mention_id": "m1", "type": "plugin", "resource_id": "builtin:test-workflow", "display_name": "Smoke Test"},
	}}
	db := orm.MigrateTestDB(t)
	_, _, err := applyChatMentions(
		context.Background(), db.DB, raw, "user-1", "conversation-1", "session-1", "run", nil,
	)
	if err == nil || !strings.Contains(err.Error(), "unsupported mention type") {
		t.Fatalf("expected removed wire type to fail, got %v", err)
	}
}

func TestApplyChatMentionsResolvesWorkflowWireType(t *testing.T) {
	raw := map[string]any{"mentions": []any{
		map[string]any{"mention_id": "m1", "type": "workflow", "resource_id": "builtin:test-workflow", "display_name": "Workflow Runtime End-to-End Self-Test"},
	}}
	db := orm.MigrateTestDB(t, &orm.WorkflowResource{})
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowResource{
		ID: "builtin-test-workflow", WorkflowRef: "builtin:test-workflow", WorkflowID: "test-workflow",
		OwnerScope: "builtin", SourceType: "builtin", RelativeRoot: "workflows/builtin/test-workflow",
		Name: "Workflow Runtime End-to-End Self-Test", Status: "active", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	_, resolved, err := applyChatMentions(
		context.Background(), db.DB, raw, "user-1", "conversation-1", "session-1",
		"帮我执行一下 Workflow Runtime End-to-End Self-Test", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.WorkflowRefs) != 1 || resolved.WorkflowRefs[0] != "builtin:test-workflow" {
		t.Fatalf("workflow refs=%#v", resolved.WorkflowRefs)
	}
	if len(resolved.ResourceMentions) != 1 || resolved.ResourceMentions[0]["resource_type"] != "workflow" {
		t.Fatalf("resource mentions=%#v", resolved.ResourceMentions)
	}
}

func TestConversationWorkflowBindingSurvivesFollowUpAndClearsAtSessionEnd(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.WorkflowSession{})
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{ID: "conversation-1", Ext: json.RawMessage(`{"keep":"value"}`),
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}

	refs, err := resolveConversationWorkflowBinding(context.Background(), db.DB, "conversation-1",
		[]string{"builtin:image-workflow"}, nil, true, true)
	if err != nil || len(refs) != 1 || refs[0] != "builtin:image-workflow" {
		t.Fatalf("initial binding refs=%v err=%v", refs, err)
	}
	refs, err = resolveConversationWorkflowBinding(context.Background(), db.DB, "conversation-1",
		nil, nil, true, true)
	if err != nil || len(refs) != 1 || refs[0] != "builtin:image-workflow" {
		t.Fatalf("follow-up binding refs=%v err=%v", refs, err)
	}

	if err := db.Create(&orm.WorkflowSession{ID: "session-1", ConversationID: "conversation-1",
		WorkflowID: "image-workflow", WorkflowRef: "builtin:image-workflow", Status: "completed",
		CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	refs, err = resolveConversationWorkflowBinding(context.Background(), db.DB, "conversation-1",
		nil, nil, true, true)
	if err != nil || len(refs) != 0 {
		t.Fatalf("terminal session must clear binding refs=%v err=%v", refs, err)
	}
	var conversation orm.Conversation
	if err := db.First(&conversation, "id = ?", "conversation-1").Error; err != nil {
		t.Fatal(err)
	}
	if string(conversation.Ext) != `{"keep":"value"}` {
		t.Fatalf("unrelated conversation ext was not preserved: %s", conversation.Ext)
	}
}

func TestExplicitWorkflowMentionOverridesDisabledConversationToggle(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.WorkflowSession{})
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{ID: "conversation-1",
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}

	refs, err := resolveConversationWorkflowBinding(context.Background(), db.DB, "conversation-1",
		[]string{"builtin:test-workflow"}, nil, false, true)
	if err != nil || len(refs) != 1 || refs[0] != "builtin:test-workflow" {
		t.Fatalf("explicit mention refs=%v err=%v", refs, err)
	}
	bound, err := readConversationWorkflowBinding(context.Background(), db.DB, "conversation-1")
	if err != nil || bound != "builtin:test-workflow" {
		t.Fatalf("persisted binding=%q err=%v", bound, err)
	}

	refs, err = resolveConversationWorkflowBinding(context.Background(), db.DB, "conversation-1",
		nil, nil, false, true)
	if err != nil || len(refs) != 1 || refs[0] != "builtin:test-workflow" {
		t.Fatalf("disabled follow-up must preserve explicit binding refs=%v err=%v", refs, err)
	}
}

func TestConversationWorkflowBindingExplicitCancellationClearsSelection(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.WorkflowSession{})
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{ID: "conversation-1",
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := writeConversationWorkflowBinding(context.Background(), db.DB, "conversation-1",
		"builtin:image-workflow"); err != nil {
		t.Fatal(err)
	}
	refs, err := resolveConversationWorkflowBinding(context.Background(), db.DB, "conversation-1",
		nil, []string{"builtin:image-workflow"}, true, true)
	if err != nil || len(refs) != 0 {
		t.Fatalf("explicit cancellation refs=%v err=%v", refs, err)
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
		mention := chatMention{Type: "workflow", ResourceID: test.name, DisplayName: test.name}
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
		WorkflowRefs:     []string{"video/workflow"},
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
	if got := bindings["workflow_refs"].([]string); len(got) != 1 || got[0] != "video/workflow" {
		t.Fatalf("workflow_refs = %#v", got)
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
			"workflow_refs":      []string{"video/workflow"},
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
	if got := req.ExplicitResources.WorkflowRefs; len(got) != 1 || got[0] != "video/workflow" {
		t.Fatalf("WorkflowRefs = %#v", got)
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

func TestBackendBuildsAndPropagatesWorkflowActivation(t *testing.T) {
	activation := buildWorkflowActivation(map[string]any{
		"workflow_id": "image-workflow", "revision_id": "revision-1", "name": "Image",
	}, "builtin:image-workflow")
	if activation["tool_name"] != "trigger_image_workflow" {
		t.Fatalf("activation = %#v", activation)
	}
	if !strings.Contains(fmt.Sprint(activation["tool_description"]), "executable Workflow") ||
		!strings.Contains(fmt.Sprint(activation["prompt"]), "@workflow") {
		t.Fatalf("workflow execution semantics missing: %#v", activation)
	}
	req := buildLazyChatRequest(map[string]any{
		"workflow_activations": []map[string]any{activation},
	})
	if len(req.Workflow.Activations) != 1 || req.Workflow.Activations[0]["revision_id"] != "revision-1" {
		t.Fatalf("Activations = %#v", req.Workflow.Activations)
	}
}

func TestBuildWorkflowActivationDoesNotSerializeNilRevision(t *testing.T) {
	activation := buildWorkflowActivation(map[string]any{
		"workflow_id": "test-workflow", "revision_id": nil,
	}, "builtin:test-workflow")
	if got := activation["revision_id"]; got != "" {
		t.Fatalf("revision_id = %#v, want empty string", got)
	}
	if buildWorkflowActivation(nil, "builtin:test-workflow") != nil {
		t.Fatal("nil catalog item must not create an activation")
	}
}

func TestMentionResourceContextMarksWorkflowAsExecutable(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.WorkflowResource{})
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowResource{ID: "workflow-1", WorkflowRef: "builtin:test-workflow",
		WorkflowID: "test-workflow", OwnerScope: "builtin", SourceType: "builtin", Status: "active",
		Name: "Workflow Runtime End-to-End Self-Test", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	contextText := buildMentionResourceContext(context.Background(), db.DB, "user-1", nil,
		map[string]any{"mentions": []any{map[string]any{"type": "workflow",
			"resource_id": "builtin:test-workflow", "display_name": "Workflow Runtime End-to-End Self-Test"}}})
	if !strings.Contains(contextText, "semantics=executable_procedure_selected_for_this_turn") ||
		!strings.Contains(contextText, "invoke its bound trigger") {
		t.Fatalf("workflow mention remained ambiguous: %s", contextText)
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
