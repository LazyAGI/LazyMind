package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/state"
	"lazymind/core/store"
)

func TestBuildChatRequestBodyUsesConversationIDDerivedSessionID(t *testing.T) {
	body := buildChatRequestBody(context.TODO(), nil, "conv-1", "", "hello", nil, map[string]any{}, nil, "", 1)
	sessionID, ok := body["session_id"].(string)
	if !ok {
		t.Fatalf("expected session_id string, got %T", body["session_id"])
	}
	if !strings.HasPrefix(sessionID, "conv-1_") {
		t.Fatalf("expected session_id to start with conversation id, got %q", sessionID)
	}
	suffix := strings.TrimPrefix(sessionID, "conv-1_")
	if suffix == "" {
		t.Fatalf("expected timestamp suffix in session_id, got %q", sessionID)
	}
	if _, err := strconv.ParseInt(suffix, 10, 64); err != nil {
		t.Fatalf("expected millisecond timestamp suffix, got %q: %v", suffix, err)
	}
}

func TestConversationListSeparatesAssistantOwnershipFromExecutionEngine(t *testing.T) {
	database := newPromptTestDB(t)
	db := database.DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	for _, conversation := range []orm.Conversation{
		{ID: "native", DisplayName: "LazyMind native", ChatExecutor: ChatExecutorLazyMind,
			BaseModel: orm.BaseModel{CreateUserID: "u1", CreatedAt: now, UpdatedAt: now}},
		{ID: "managed", DisplayName: "LazyMind managed Codex", ChatExecutor: ChatExecutorCodex,
			BaseModel: orm.BaseModel{CreateUserID: "u1", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}},
		{ID: "external", DisplayName: "Codex native", ChatExecutor: ChatExecutorCodex,
			BaseModel: orm.BaseModel{CreateUserID: "u1", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)}},
		{ID: "archived-external", DisplayName: "Archived Codex native", ChatExecutor: ChatExecutorCodex,
			ArchivedAt: &now, BaseModel: orm.BaseModel{CreateUserID: "u1", CreatedAt: now.Add(3 * time.Second), UpdatedAt: now.Add(3 * time.Second)}},
	} {
		if err := db.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, binding := range []orm.ExternalAgentBinding{
		{ID: "managed-binding", ConversationID: "managed", Provider: ChatExecutorCodex,
			HostID: "host-1", ProviderThreadID: "managed-thread", CreatedByUserID: "u1", CreatedAt: now, UpdatedAt: now},
		{ID: "external-binding", ConversationID: "external", Provider: ChatExecutorCodex,
			HostID: "host-1", ProviderThreadID: "external-thread",
			CreatedByUserID: "u1", CreatedAt: now, UpdatedAt: now},
		{ID: "archived-external-binding", ConversationID: "archived-external", Provider: ChatExecutorCodex,
			HostID: "host-1", ProviderThreadID: "archived-external-thread", CreatedByUserID: "u1", CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(&binding).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, session := range []orm.ExternalAgentSession{
		{ID: "managed-session", OwnerUserID: "u1", Provider: ChatExecutorCodex, HostID: "host-1",
			ProviderThreadID: "managed-thread", ProjectKey: "codex-project-1", ProjectName: "DataAnnotation",
			DisplayName: "Managed Codex", Active: true, LastSeenAt: now, CreatedAt: now, UpdatedAt: now},
		{ID: "external-session", OwnerUserID: "u1", Provider: ChatExecutorCodex, HostID: "host-1",
			ProviderThreadID: "external-thread", ProjectKey: "codex-project-1", ProjectName: "DataAnnotation",
			DisplayName: "Codex native", TurnCount: 1,
			Active: true, LastSeenAt: now, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(&session).Error; err != nil {
			t.Fatal(err)
		}
	}

	list := func(assistant string) []struct {
		ID           string `json:"conversation_id"`
		Assistant    string `json:"assistant"`
		ChatExecutor string `json:"chat_executor"`
		ProjectKey   string `json:"project_key"`
		ProjectName  string `json:"project_name"`
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/core/conversations?assistant="+assistant, nil)
		req.Header.Set("X-User-Id", "u1")
		rec := httptest.NewRecorder()
		ListConversations(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("assistant=%s status=%d body=%s", assistant, rec.Code, rec.Body.String())
		}
		var response struct {
			Conversations []struct {
				ID           string `json:"conversation_id"`
				Assistant    string `json:"assistant"`
				ChatExecutor string `json:"chat_executor"`
				ProjectKey   string `json:"project_key"`
				ProjectName  string `json:"project_name"`
			} `json:"conversations"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Conversations
	}

	lazyMind := list(ChatExecutorLazyMind)
	if len(lazyMind) != 1 || lazyMind[0].ID != "native" || lazyMind[0].Assistant != ChatExecutorLazyMind {
		t.Fatalf("LazyMind assistant conversations=%#v", lazyMind)
	}
	codex := list(ChatExecutorCodex)
	if len(codex) != 2 || codex[0].ID != "external" || codex[0].Assistant != ChatExecutorCodex ||
		codex[0].ChatExecutor != ChatExecutorCodex || codex[0].ProjectKey != "codex-project-1" ||
		codex[0].ProjectName != "DataAnnotation" || codex[1].ID != "managed" {
		t.Fatalf("Codex assistant conversations=%#v", codex)
	}
}

func TestWorkflowSessionAvailableForRequest(t *testing.T) {
	tests := []struct {
		name    string
		session *orm.WorkflowSession
		raw     map[string]any
		want    bool
	}{
		{
			name:    "active session is injected without frontend context",
			session: &orm.WorkflowSession{ID: "session-1", Status: "active"},
			raw:     map[string]any{},
			want:    true,
		},
		{
			name:    "failed session remains available for recovery",
			session: &orm.WorkflowSession{ID: "session-1", Status: "failed"},
			raw:     map[string]any{},
			want:    true,
		},
		{
			name:    "completed session is available when explicitly focused",
			session: &orm.WorkflowSession{ID: "session-1", Status: "completed"},
			raw: map[string]any{
				"workflow_context": map[string]any{"session_id": "session-1"},
			},
			want: true,
		},
		{
			name:    "completed session is not sticky without explicit context",
			session: &orm.WorkflowSession{ID: "session-1", Status: "completed"},
			raw:     map[string]any{},
			want:    false,
		},
		{
			name:    "completed session rejects a stale frontend id",
			session: &orm.WorkflowSession{ID: "session-1", Status: "completed"},
			raw: map[string]any{
				"workflow_context": map[string]any{"session_id": "session-old"},
			},
			want: false,
		},
		{
			name:    "dismissed failed session is unavailable",
			session: &orm.WorkflowSession{ID: "session-1", Status: "failed", Dismissed: true},
			raw:     map[string]any{},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workflowSessionAvailableForRequest(tt.session, tt.raw); got != tt.want {
				t.Fatalf("workflowSessionAvailableForRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPromoteAgentRuntimeFlagsPrefersExplicitRequest(t *testing.T) {
	body := map[string]any{
		"agentic_config": map[string]any{
			"enable_workflow": true,
			"enable_subagent": false,
		},
	}
	promoteAgentRuntimeFlags(map[string]any{
		"enable_workflow": false,
	}, body)
	if enabled, _ := body["enable_workflow"].(bool); enabled {
		t.Fatalf("explicit enable_workflow=false was overwritten: %#v", body)
	}
	if enabled, _ := body["enable_subagent"].(bool); enabled {
		t.Fatalf("expected persisted enable_subagent=false: %#v", body)
	}
}

func TestApplyChatFeatureControlsOverridesConversationRuntimeFlags(t *testing.T) {
	db := newPromptTestDB(t)
	now := time.Now().UTC()
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{
		"user_id": "user-1", "task_center_enabled": false, "skills_enabled": true,
		"workflows_enabled": true, "mcp_enabled": true, "document_parsing_enabled": true,
		"created_at": now, "updated_at": now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"enable_workflow": true,
		"enable_subagent": true,
		"agentic_config": map[string]any{
			"enable_workflow": true,
			"enable_subagent": true,
		},
	}

	if err := applyChatFeatureControls(t.Context(), db.DB, "user-1", body); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := body["enable_workflow"].(bool); enabled {
		t.Fatalf("workflow must be disabled by the task center master control: %#v", body)
	}
	if enabled, _ := body["enable_subagent"].(bool); enabled {
		t.Fatalf("subagents must be disabled by the task center master control: %#v", body)
	}
	agentConfig := body["agentic_config"].(map[string]any)
	if agentConfig["enable_workflow"] != false || agentConfig["enable_subagent"] != false {
		t.Fatalf("agentic config must use the same effective controls: %#v", agentConfig)
	}
}

func TestBuildChatRequestBodyPropagatesSensitiveFilterBypass(t *testing.T) {
	body := buildChatRequestBody(context.TODO(), nil, "conv-1", "", "hello", nil, map[string]any{"skip_sensitive_filter": true}, nil, "", 1)
	if skip, _ := body["skip_sensitive_filter"].(bool); !skip {
		t.Fatalf("expected skip_sensitive_filter=true, got %#v", body["skip_sensitive_filter"])
	}
	req := buildLazyChatRequest(body)
	if !req.Runtime.SkipSensitiveFilter {
		t.Fatal("expected upstream runtime to skip repeated sensitive filtering")
	}
}

func TestApplyIntentOperationsPreservesUnchangedFields(t *testing.T) {
	doc := map[string]any{
		"version":        2,
		"revision":       3,
		"goal":           "总结经验",
		"execution_mode": "analysis_only",
		"constraints":    []any{"不要执行原任务"},
	}

	updated, err := applyIntentOperations(doc, []IntentOperation{
		{Op: "add", Field: "corrections", Value: "必须检查 GitHub", Evidence: "检查 GitHub"},
	})
	if err != nil {
		t.Fatalf("apply intent operations: %v", err)
	}
	if updated["goal"] != "总结经验" || updated["execution_mode"] != "analysis_only" {
		t.Fatalf("unchanged intent fields were lost: %#v", updated)
	}
	if intentRevision(updated) != 4 {
		t.Fatalf("expected revision 4, got %#v", updated["revision"])
	}
}

func TestApplyIntentOperationsRejectsInvalidBatch(t *testing.T) {
	_, err := applyIntentOperations(map[string]any{}, []IntentOperation{
		{Op: "set", Field: "constraints", Value: "invalid"},
	})
	if err == nil {
		t.Fatal("expected invalid scalar/list operation to fail")
	}
}

func TestMergeIntentUpdatedIntoExtPreservesExistingFields(t *testing.T) {
	ext := json.RawMessage(`{"mentions":[{"id":"m1"}]}`)
	intent := &IntentUpdatedEvent{
		Scope:         "conversation",
		IntentContext: map[string]any{"goal": "总结经验", "revision": 2},
	}

	merged := mergeIntentUpdatedIntoExt(ext, intent)
	var got map[string]any
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("unmarshal merged ext: %v", err)
	}
	if got["mentions"] == nil {
		t.Fatalf("existing ext field was lost: %#v", got)
	}
	updated, ok := got["intent_updated"].(map[string]any)
	if !ok || updated["scope"] != "conversation" {
		t.Fatalf("unexpected intent update: %#v", got["intent_updated"])
	}
}

func TestMergeChunksRetainsConversationIntentUpdate(t *testing.T) {
	intent := &IntentUpdatedEvent{Scope: "conversation", IntentContext: map[string]any{"goal": "新目标"}}
	merged := mergeChunksToFirstChunk([]*ChatChunkResponse{
		{Delta: "前", IntentUpdated: intent},
		{Delta: "后"},
	})
	if merged.Delta != "前后" || merged.IntentUpdated != intent {
		t.Fatalf("intent update was not retained: %#v", merged)
	}
}

func TestBuildLazyChatRequestIncludesConversationIntent(t *testing.T) {
	req := buildLazyChatRequest(map[string]any{
		"conversation_id": "conv-1",
		"intent_context": map[string]any{
			"version": 2,
			"goal":    "总结经验",
		},
	})
	if req.Conversation.IntentContext["goal"] != "总结经验" {
		t.Fatalf("unexpected intent context: %#v", req.Conversation.IntentContext)
	}
}

func TestWorkflowStepParamsFromEventParamsPreservesChatSessionID(t *testing.T) {
	params := workflowStepParamsFromEventParams(map[string]any{
		"workflow_id":            "writer-workflow",
		"step_id":                "generate_outline",
		"session_id":             "ps-1",
		"chat_session_id":        "conv-1_123",
		"user_input":             "go",
		"is_cold_start":          false,
		"retry_hint":             "retry",
		"partial_indices":        map[string]any{"outline": []any{float64(1), float64(3)}},
		"history_files_per_turn": map[string]any{"2": []any{"a.png", "b.pdf"}},
		"filters":                map[string]any{"kb_id": "kb-1"},
		"user_id":                "user-1",
	})

	if params.WorkflowID != "writer-workflow" || params.StepID != "generate_outline" || params.SessionID != "ps-1" {
		t.Fatalf("unexpected basic params: %+v", params)
	}
	if params.ChatSessionID != "conv-1_123" {
		t.Fatalf("expected chat_session_id to be preserved, got %q", params.ChatSessionID)
	}
	if got := params.PartialIndices["outline"]; len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("unexpected partial_indices: %#v", params.PartialIndices)
	}
	if got := params.HistoryFilesPerTurn["2"]; len(got) != 2 || got[0] != "a.png" || got[1] != "b.pdf" {
		t.Fatalf("unexpected history_files_per_turn: %#v", params.HistoryFilesPerTurn)
	}
	if params.Filters["kb_id"] != "kb-1" || params.UserID != "user-1" || params.RetryHint != "retry" {
		t.Fatalf("unexpected remaining params: %+v", params)
	}
}

func TestBuildChatRequestBodyUsesDatasetListFilters(t *testing.T) {
	body := buildChatRequestBody(context.TODO(), nil, "conv-1", "", "hello", nil, map[string]any{
		"conversation": map[string]any{
			"search_config": map[string]any{
				"dataset_list": []any{
					map[string]any{"id": "ds_1"},
					map[string]any{"id": "ds_2"},
				},
				"creators": []any{"user_a"},
				"tags":     []any{"tag_a", "tag_b"},
			},
		},
	}, nil, "", 1)

	filters, ok := body["filters"].(map[string]any)
	if !ok {
		t.Fatalf("expected filters map, got %T", body["filters"])
	}

	kbIDs, ok := filters["kb_id"].([]string)
	if !ok {
		t.Fatalf("expected kb_id []string, got %T", filters["kb_id"])
	}
	if len(kbIDs) != 2 || kbIDs[0] != "ds_1" || kbIDs[1] != "ds_2" {
		t.Fatalf("unexpected kb_id: %#v", kbIDs)
	}

	creators, ok := filters["creator"].([]string)
	if !ok {
		t.Fatalf("expected creator []string, got %T", filters["creator"])
	}
	if len(creators) != 1 || creators[0] != "user_a" {
		t.Fatalf("unexpected creator: %#v", creators)
	}

	tags, ok := filters["tags"].([]string)
	if !ok {
		t.Fatalf("expected tags []string, got %T", filters["tags"])
	}
	if len(tags) != 2 || tags[0] != "tag_a" || tags[1] != "tag_b" {
		t.Fatalf("unexpected tags: %#v", tags)
	}
}

func TestBuildLazyChatRequestPreservesDatasetListFilters(t *testing.T) {
	body := buildChatRequestBody(context.TODO(), nil, "conv-1", "", "hello", nil, map[string]any{
		"conversation": map[string]any{
			"search_config": map[string]any{
				"dataset_list": []any{
					map[string]any{"id": "ds_1"},
				},
			},
		},
	}, nil, "", 1)

	req := buildLazyChatRequest(body)

	if req.Retrieval.Filters == nil || len(req.Retrieval.Filters.DatasetIDs) != 1 || req.Retrieval.Filters.DatasetIDs[0] != "ds_1" {
		t.Fatalf("unexpected retrieval filters: %#v", req.Retrieval.Filters)
	}
}

func TestBuildChatRequestBodyLoadsFiltersFromConversationDB(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{})
	now := time.Now()
	searchConfig := json.RawMessage(`{"dataset_list":[{"id":"ds_db_1"},{"id":"ds_db_2"}],"creators":["u1"]}`)
	if err := db.Create(&orm.Conversation{
		ID:           "conv-db",
		DisplayName:  "test",
		ChannelID:    "default",
		SearchConfig: searchConfig,
		BaseModel: orm.BaseModel{
			CreateUserID: "u1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	body := buildChatRequestBody(t.Context(), db.DB, "conv-db", "", "hello", nil, map[string]any{}, nil, "", 2)
	filters, ok := body["filters"].(map[string]any)
	if !ok {
		t.Fatalf("expected filters map from DB search_config, got %T", body["filters"])
	}
	kbIDs, ok := filters["kb_id"].([]string)
	if !ok || len(kbIDs) != 2 || kbIDs[0] != "ds_db_1" || kbIDs[1] != "ds_db_2" {
		t.Fatalf("unexpected kb_id from DB: %#v", filters["kb_id"])
	}
}

func TestBuildChatRequestBodyKeepsExistingFilters(t *testing.T) {
	existing := map[string]any{"kb_id": []string{"manual"}}
	body := buildChatRequestBody(context.TODO(), nil, "conv-1", "", "hello", nil, map[string]any{
		"filters": existing,
		"conversation": map[string]any{
			"search_config": map[string]any{
				"dataset_list": []any{map[string]any{"id": "ds_1"}},
			},
		},
	}, nil, "", 1)

	filters, ok := body["filters"].(map[string]any)
	if !ok {
		t.Fatalf("expected filters map, got %T", body["filters"])
	}

	kbIDs, ok := filters["kb_id"].([]string)
	if !ok {
		t.Fatalf("expected kb_id []string, got %T", filters["kb_id"])
	}
	if len(kbIDs) != 1 || kbIDs[0] != "manual" {
		t.Fatalf("expected existing filters to be preserved, got %#v", kbIDs)
	}
}

func TestBuildChatRequestBodyAddsResourceContextWithoutLegacyMemory(t *testing.T) {
	ctx := &evolution.ChatResourceContext{
		DisabledTools:      []string{"bing"},
		AvailableSkills:    []string{"coding/git-workflow"},
		UsePersonalization: true,
	}
	body := buildChatRequestBody(context.TODO(), nil, "conv-1", "session-1", "hello", nil, map[string]any{}, ctx, "user-1", 1)

	if got := body["session_id"]; got != "session-1" {
		t.Fatalf("expected session_id to be preserved, got %#v", got)
	}
	if got := body["user_id"]; got != "user-1" {
		t.Fatalf("expected user_id to be forwarded, got %#v", got)
	}
	if got, ok := body["disabled_tools"].([]string); !ok || len(got) != 1 || got[0] != "bing" {
		t.Fatalf("unexpected disabled_tools: %#v", body["disabled_tools"])
	}
	if got, ok := body["available_skills"].([]string); !ok || len(got) != 1 || got[0] != "coding/git-workflow" {
		t.Fatalf("unexpected available_skills: %#v", body["available_skills"])
	}
	if _, ok := body["skill_fs_url"]; ok {
		t.Fatalf("expected skill_fs_url to be omitted")
	}
	if _, ok := body["memory"]; ok {
		t.Fatalf("legacy memory content must not be sent")
	}
	if _, ok := body["user_preference"]; ok {
		t.Fatalf("legacy user_preference content must not be sent")
	}
	if got, ok := body["use_memory"].(bool); !ok || !got {
		t.Fatalf("expected use_memory default true, got %#v", body["use_memory"])
	}
	if got, ok := body["reasoning"].(bool); !ok || !got {
		t.Fatalf("expected reasoning default true, got %#v", body["reasoning"])
	}
}

func TestBuildChatRequestBodyMergesRequestDisabledTools(t *testing.T) {
	ctx := &evolution.ChatResourceContext{DisabledTools: []string{"bing"}}
	body := buildChatRequestBody(
		context.TODO(), nil, "conv-1", "session-1", "hello", nil,
		map[string]any{"disabled_tools": []any{"ask_user"}}, ctx, "user-1", 1,
	)

	disabled, ok := body["disabled_tools"].([]string)
	if !ok || len(disabled) != 2 || disabled[0] != "ask_user" || disabled[1] != "bing" {
		t.Fatalf("expected request and persisted disabled tools to merge, got %#v", body["disabled_tools"])
	}
}

func TestReplaceAskUserToolResultSupportsJSONCarrier(t *testing.T) {
	content := `before<tool_result>{"id":"call-1","name":"ask_user","result":"Question sent"}</tool_result>after`
	replaced := replaceAskUserToolResult(content, "Q1: Purpose\n  Answer: Personal use")

	if strings.Contains(replaced, `"result":"Question sent"`) {
		t.Fatalf("expected placeholder result to be replaced, got %s", replaced)
	}
	if !strings.Contains(replaced, `"result":"Q1: Purpose\n  Answer: Personal use"`) {
		t.Fatalf("expected structured answer context, got %s", replaced)
	}
}

func TestBuildChatRequestBodySkipsMemoryAndPreferenceWhenPersonalizationDisabled(t *testing.T) {
	ctx := &evolution.ChatResourceContext{
		DisabledTools:      []string{},
		AvailableSkills:    []string{"coding/git-workflow"},
		UsePersonalization: false,
	}
	body := buildChatRequestBody(context.TODO(), nil, "conv-1", "session-1", "hello", nil, map[string]any{}, ctx, "", 1)

	if got, ok := body["use_memory"].(bool); !ok || got {
		t.Fatalf("expected use_memory false, got %#v", body["use_memory"])
	}
	if _, ok := body["memory"]; ok {
		t.Fatalf("expected memory to be omitted when personalization is disabled")
	}
	if _, ok := body["user_preference"]; ok {
		t.Fatalf("expected user_preference to be omitted when personalization is disabled")
	}
}

func TestBuildChatRequestBodyPreservesExplicitReasoningFalse(t *testing.T) {
	body := buildChatRequestBody(context.TODO(), nil, "conv-1", "", "hello", nil, map[string]any{
		"reasoning": false,
	}, nil, "", 1)

	if got, ok := body["reasoning"].(bool); !ok || got {
		t.Fatalf("expected reasoning false, got %#v", body["reasoning"])
	}
}

func TestBuildChatRequestBodyForwardsThinkingDepth(t *testing.T) {
	body := buildChatRequestBody(context.TODO(), nil, "conv-1", "", "hello", nil, map[string]any{
		"thinking_depth": "low",
	}, nil, "", 1)

	if got := body["thinking_depth"]; got != "low" {
		t.Fatalf("expected low thinking depth, got %#v", got)
	}
	req := buildLazyChatRequest(body)
	if req.Runtime.ThinkingDepth != "low" {
		t.Fatalf("expected upstream low thinking depth, got %q", req.Runtime.ThinkingDepth)
	}
}

func TestBuildChatRequestBodyDefaultsInvalidThinkingDepth(t *testing.T) {
	body := buildChatRequestBody(context.TODO(), nil, "conv-1", "", "hello", nil, map[string]any{
		"thinking_depth": "turbo",
	}, nil, "", 1)
	if got := body["thinking_depth"]; got != "medium" {
		t.Fatalf("expected medium thinking depth, got %#v", got)
	}
}

func TestBuildChatRequestBodyAcceptsMaxThinkingDepth(t *testing.T) {
	body := buildChatRequestBody(context.TODO(), nil, "conv-1", "", "hello", nil, map[string]any{
		"thinking_depth": "MAX",
	}, nil, "", 1)
	if got := body["thinking_depth"]; got != "max" {
		t.Fatalf("expected max thinking depth, got %#v", got)
	}
}

func TestBuildChatHistoryExtPreservesMultimodalInput(t *testing.T) {
	ext := buildChatHistoryExt(map[string]any{
		"input": []any{
			map[string]any{"input_type": "text", "text": "记住这个是王牌超"},
			map[string]any{
				"input_type":   "image",
				"uri":          "/var/lib/lazymind/uploads/tmp/users/u1/files/upload_a.jpg",
				"input_base64": "data:image/jpeg;base64,/9j/abc",
			},
		},
	}, "记住这个是王牌超")

	var payload struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(ext, &payload); err != nil {
		t.Fatalf("unmarshal ext: %v", err)
	}
	if len(payload.Input) != 2 {
		t.Fatalf("expected 2 input items, got %#v", payload.Input)
	}
	if got := payload.Input[1]["input_type"]; got != "image" {
		t.Fatalf("expected image item to be preserved, got %#v", got)
	}
	if got := payload.Input[1]["input_base64"]; got != "data:image/jpeg;base64,/9j/abc" {
		t.Fatalf("expected image base64 to be preserved, got %#v", got)
	}
}

func TestBuildChatHistoryExtUsesDisplayQueryForAutomatedContext(t *testing.T) {
	ext := buildChatHistoryExt(map[string]any{
		"input": []any{
			map[string]any{"input_type": "text", "text": "large internal model context"},
			map[string]any{"input_type": "image", "uri": "/uploads/dog.jpg"},
		},
		"display_query": "用户任务描述",
	}, "用户任务描述")
	var payload struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(ext, &payload); err != nil {
		t.Fatalf("unmarshal ext: %v", err)
	}
	if len(payload.Input) != 2 {
		t.Fatalf("expected text+image input items, got %#v", payload.Input)
	}
	if got := payload.Input[0]["text"]; got != "用户任务描述" {
		t.Fatalf("expected display query text, got %#v", got)
	}
	if strings.Contains(string(ext), "large internal model context") {
		t.Fatalf("history ext must not keep internal model text: %s", ext)
	}
	if got := payload.Input[1]["uri"]; got != "/uploads/dog.jpg" {
		t.Fatalf("expected image uri to be preserved, got %#v", got)
	}
}

func TestCollectedInputsForConversationReturnsSnapshotAndSummary(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskCenterTask{}, &orm.TaskRunInput{}, &orm.TaskRunOutput{})
	now := time.Now().UTC()
	if err := db.Create(&orm.TaskCenterTask{ID: "downstream", UserID: "u", ConversationID: "weekly-conv", TaskType: "scheduled", Status: "succeeded", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.TaskRunOutput{ID: "output", TaskID: "upstream", ConversationID: "daily-conv", SummaryText: "日报摘要", OutputStatus: "ready", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(map[string]any{"source_name": "Github调研", "executed_at": now, "mode": "摘要"})
	if err := db.Create(&orm.TaskRunInput{ID: "input", DownstreamTaskID: "downstream", UpstreamTaskID: "upstream", DependencyID: "dep", OutputID: "output", Position: 0, SnapshotJSON: snapshot, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	items := collectedInputsForConversation(context.Background(), db.DB, "weekly-conv")
	if len(items) != 1 || items[0]["summary"] != "日报摘要" || items[0]["source_name"] != "Github调研" || items[0]["conversation_id"] != "daily-conv" {
		t.Fatalf("unexpected collected inputs: %#v", items)
	}
}

func TestGetConversationDetailReturnsStoredMultimodalInput(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ChatHistory{}, &orm.ExternalAgentBinding{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	ext := buildChatHistoryExt(map[string]any{
		"input": []any{
			map[string]any{"input_type": "text", "text": "记住这个是王牌超"},
			map[string]any{
				"input_type":   "image",
				"uri":          "/var/lib/lazymind/uploads/tmp/users/u1/files/upload_a.jpg",
				"input_base64": "data:image/jpeg;base64,/9j/abc",
			},
		},
	}, "记住这个是王牌超")
	if err := db.Create(&orm.Conversation{
		ID:           "conv-1",
		DisplayName:  "记住这个是王牌超",
		ChannelID:    "default",
		SearchConfig: json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "u1",
			CreateUserName: "User 1",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&orm.ChatHistory{
		ID:             "h_1",
		Seq:            1,
		ConversationID: "conv-1",
		RawContent:     "记住这个是王牌超",
		Content:        "记住这个是王牌超",
		Result:         "好的",
		Ext:            ext,
		TimeMixin:      orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error; err != nil {
		t.Fatalf("create history: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/conversations/conv-1:detail", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"name": "conv-1:detail"})
	rec := httptest.NewRecorder()

	GetConversationDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Conversation struct {
			ConversationID string `json:"conversation_id"`
			DisplayName    string `json:"display_name"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Conversation.ConversationID != "conv-1" {
		t.Fatalf("expected conversation_id conv-1, got %q", resp.Conversation.ConversationID)
	}
	if resp.Conversation.DisplayName != "记住这个是王牌超" {
		t.Fatalf("expected display_name preserved, got %q", resp.Conversation.DisplayName)
	}
}

func TestChatHistoryResponseIncludesMentions(t *testing.T) {
	item := chatHistoryToResponseItem(orm.ChatHistory{
		RawContent: "查看知识库1",
		Ext:        json.RawMessage(`{"input":[{"input_type":"text","text":"查看知识库1"}],"mentions":[{"mention_id":"m1","type":"knowledge_base","resource_id":"ds_1","display_name":"知识库1","start":2,"end":7}]}`),
	})
	mentions, ok := item["mentions"].([]any)
	if !ok || len(mentions) != 1 {
		t.Fatalf("mentions missing from history response: %#v", item["mentions"])
	}
}

func TestChatHistoryResponseIncludesThinkingDuration(t *testing.T) {
	item := chatHistoryToResponseItem(orm.ChatHistory{
		Result:            "<think>分析并调用工具</think>最终答案",
		ThinkingDurationS: 7,
	})
	if got := item["thinking_time_s"]; got != int64(7) {
		t.Fatalf("thinking_time_s: got %#v want 7", got)
	}
	if got := item["reasoning_content"]; got != "分析并调用工具" {
		t.Fatalf("reasoning_content: got %#v", got)
	}
}

func TestChatHistoryResponseHidesLegacyCollectedContext(t *testing.T) {
	item := chatHistoryToResponseItem(orm.ChatHistory{
		RawContent: `<collected-task-context>large internal context</collected-task-context>
<current-task-request>
这是当前需要执行的任务要求，请使用上方已完成的历史执行结果作答：
生成本周调研报告
</current-task-request>`,
	})
	if got := item["query"]; got != "生成本周调研报告" {
		t.Fatalf("query = %q", got)
	}
}

func TestElapsedThinkingSecondsRoundsUp(t *testing.T) {
	tests := []struct {
		name    string
		elapsed time.Duration
		want    int64
	}{
		{name: "initial reasoning chunk", elapsed: 0, want: 1},
		{name: "sub-second reasoning", elapsed: 250 * time.Millisecond, want: 1},
		{name: "exact second", elapsed: time.Second, want: 1},
		{name: "partial next second", elapsed: time.Second + time.Millisecond, want: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := elapsedThinkingSeconds(tc.elapsed); got != tc.want {
				t.Fatalf("elapsedThinkingSeconds(%s) = %d, want %d", tc.elapsed, got, tc.want)
			}
		})
	}
}

func TestGetConversationDetailFiltersMissingDatasets(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ChatHistory{}, &orm.Dataset{}, &orm.ExternalAgentBinding{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	deletedAt := now.Add(-time.Hour)
	if err := db.Create([]orm.Dataset{
		{
			ID:          "ds_live",
			KbID:        "ds_live",
			DisplayName: "Live Dataset",
			BaseModel: orm.BaseModel{
				CreateUserID:   "u1",
				CreateUserName: "User 1",
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		},
		{
			ID:          "ds_deleted",
			KbID:        "ds_deleted",
			DisplayName: "Deleted Dataset",
			BaseModel: orm.BaseModel{
				CreateUserID:   "u1",
				CreateUserName: "User 1",
				CreatedAt:      now,
				UpdatedAt:      now,
				DeletedAt:      &deletedAt,
			},
		},
	}).Error; err != nil {
		t.Fatalf("create datasets: %v", err)
	}
	if err := db.Create(&orm.Conversation{
		ID:           "conv-1",
		DisplayName:  "test",
		ChannelID:    "default",
		SearchConfig: json.RawMessage(`{"dataset_list":[{"id":"ds_live"},{"id":"ds_deleted"},{"id":"ds_missing"}],"creators":["u1"],"top_k":3}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "u1",
			CreateUserName: "User 1",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/conversations/conv-1:detail", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"name": "conv-1:detail"})
	rec := httptest.NewRecorder()

	GetConversationDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Conversation struct {
			SearchConfig map[string]any `json:"search_config"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	rawList, ok := resp.Conversation.SearchConfig["dataset_list"].([]any)
	if !ok {
		t.Fatalf("expected dataset_list array, got %T", resp.Conversation.SearchConfig["dataset_list"])
	}
	if len(rawList) != 1 {
		t.Fatalf("expected one existing dataset, got %#v", rawList)
	}
	selector, _ := rawList[0].(map[string]any)
	if selector["id"] != "ds_live" {
		t.Fatalf("expected ds_live to remain, got %#v", rawList)
	}
	if resp.Conversation.SearchConfig["top_k"] != float64(3) {
		t.Fatalf("expected top_k preserved, got %#v", resp.Conversation.SearchConfig["top_k"])
	}
}

func TestGetConversationHistoryReturnsStoredMultimodalInput(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ChatHistory{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	ext := buildChatHistoryExt(map[string]any{
		"input": []any{
			map[string]any{"input_type": "text", "text": "记住这个是王牌超"},
			map[string]any{
				"input_type":   "image",
				"uri":          "/var/lib/lazymind/uploads/tmp/users/u1/files/upload_a.jpg",
				"input_base64": "data:image/jpeg;base64,/9j/abc",
			},
		},
	}, "记住这个是王牌超")
	if err := db.Create(&orm.Conversation{
		ID:           "conv-1",
		DisplayName:  "记住这个是王牌超",
		ChannelID:    "default",
		SearchConfig: json.RawMessage(`{}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   "u1",
			CreateUserName: "User 1",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&orm.ChatHistory{
		ID:             "h_1",
		Seq:            1,
		ConversationID: "conv-1",
		RawContent:     "记住这个是王牌超",
		Content:        "记住这个是王牌超",
		Result:         "好的",
		ToolCallTurns:  8,
		Ext:            ext,
		TimeMixin:      orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error; err != nil {
		t.Fatalf("create history: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/conversations/conv-1:history", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"name": "conv-1:history"})
	rec := httptest.NewRecorder()

	GetConversationHistory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ConversationID string `json:"conversation_id"`
		History        []struct {
			Input         []map[string]any `json:"input"`
			ToolCallTurns int              `json:"tool_call_turns"`
		} `json:"history"`
		TotalSize int `json:"total_size"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ConversationID != "conv-1" {
		t.Fatalf("expected conversation_id conv-1, got %q", resp.ConversationID)
	}
	if resp.TotalSize != 1 {
		t.Fatalf("expected total_size 1, got %d", resp.TotalSize)
	}
	if len(resp.History) != 1 || len(resp.History[0].Input) != 2 {
		t.Fatalf("expected response history input to include 2 items, got %#v", resp.History)
	}
	if got := resp.History[0].Input[1]["input_type"]; got != "image" {
		t.Fatalf("expected image input in history response, got %#v", got)
	}
	if got := resp.History[0].Input[1]["uri"]; got != "/var/lib/lazymind/uploads/tmp/users/u1/files/upload_a.jpg" {
		t.Fatalf("expected image uri in history response, got %#v", got)
	}
	if got := resp.History[0].ToolCallTurns; got != 8 {
		t.Fatalf("expected tool_call_turns 8, got %d", got)
	}
}

func TestLoadConversationHistoryPageUsesDatabasePaging(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ChatHistory{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	histories := make([]orm.ChatHistory, 0, 55)
	for seq := 1; seq <= 55; seq++ {
		histories = append(histories, orm.ChatHistory{
			ID: "h_" + strconv.Itoa(seq), Seq: seq, ConversationID: "conv-page",
			RawContent: "question", Result: "answer",
			TimeMixin: orm.TimeMixin{CreateTime: now.Add(time.Duration(seq) * time.Second), UpdateTime: now},
		})
	}
	if err := db.Create(&histories).Error; err != nil {
		t.Fatalf("create histories: %v", err)
	}

	page, total, err := loadConversationHistoryPage(t.Context(), "conv-page", 10, 20)
	if err != nil {
		t.Fatalf("load page: %v", err)
	}
	if total != 55 || len(page) != 10 {
		t.Fatalf("total=%d page=%d, want 55/10", total, len(page))
	}
	if page[0].Seq != 35 || page[9].Seq != 26 {
		t.Fatalf("unexpected page bounds: first=%d last=%d", page[0].Seq, page[9].Seq)
	}

	if !db.Migrator().HasIndex(&orm.ChatHistory{}, "idx_chat_histories_conversation_seq") {
		t.Fatal("chat history pagination index was not created")
	}
}

func TestLoadConversationHistoryPageMergesGeneratingHistoryWithoutDuplicates(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ChatHistory{})
	stateStore, err := state.NewSQLiteStore(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	store.Init(db.DB, nil, stateStore)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	for seq := 1; seq <= 4; seq++ {
		history := orm.ChatHistory{
			ID: "h_" + strconv.Itoa(seq), Seq: seq, ConversationID: "conv-live",
			RawContent: "question", Result: "answer",
			TimeMixin: orm.TimeMixin{CreateTime: now.Add(time.Duration(seq) * time.Second), UpdateTime: now},
		}
		if err := db.Create(&history).Error; err != nil {
			t.Fatalf("create history %d: %v", seq, err)
		}
	}
	if err := setChatInput(t.Context(), stateStore, "conv-live", "h_5", "generating", 5, nil); err != nil {
		t.Fatalf("set generating input: %v", err)
	}
	if err := setChatStatus(t.Context(), stateStore, "conv-live", "h_5", "generating", ""); err != nil {
		t.Fatalf("set generating status: %v", err)
	}
	if err := setChatInput(t.Context(), stateStore, "conv-live", "h_4", "persisted", 4, nil); err != nil {
		t.Fatalf("set duplicate input: %v", err)
	}
	if err := setChatStatus(t.Context(), stateStore, "conv-live", "h_4", "generating", ""); err != nil {
		t.Fatalf("set duplicate status: %v", err)
	}

	page, total, err := loadConversationHistoryPage(t.Context(), "conv-live", 3, 0)
	if err != nil {
		t.Fatalf("load first page: %v", err)
	}
	if total != 5 || len(page) != 3 {
		t.Fatalf("total=%d page=%d, want 5/3", total, len(page))
	}
	if page[0].ID != "h_5" || page[1].ID != "h_4" || page[2].ID != "h_3" {
		t.Fatalf("unexpected merged order: %#v", []string{page[0].ID, page[1].ID, page[2].ID})
	}

	page, total, err = loadConversationHistoryPage(t.Context(), "conv-live", 3, 3)
	if err != nil {
		t.Fatalf("load second page: %v", err)
	}
	if total != 5 || len(page) != 2 || page[0].ID != "h_2" || page[1].ID != "h_1" {
		t.Fatalf("unexpected second page: total=%d page=%#v", total, page)
	}
}

func TestBuildChatRequestBodyMergesInputURIsIntoFiles(t *testing.T) {
	body := buildChatRequestBody(context.TODO(), nil, "conv-1", "sid", "what animal", nil, map[string]any{
		"input": []any{
			map[string]any{"input_type": "text", "text": "hello"},
			map[string]any{"input_type": "image", "uri": "/var/lib/lazymind/uploads/tmp/u1/a.png"},
			map[string]any{"input_type": "file", "uri": "/var/lib/lazymind/uploads/tmp/u1/b.pdf"},
		},
	}, nil, "", 1)

	files, ok := body["files"].(map[string][]string)
	if !ok {
		t.Fatalf("expected files to be map[string][]string, got %#v", body["files"])
	}
	currentFiles := files["1"]
	if len(currentFiles) != 2 {
		t.Fatalf("expected 2 file paths from input, got %#v", currentFiles)
	}
	if currentFiles[0] != "/var/lib/lazymind/uploads/tmp/u1/a.png" || currentFiles[1] != "/var/lib/lazymind/uploads/tmp/u1/b.pdf" {
		t.Fatalf("unexpected files order/content: %#v", currentFiles)
	}
}

func TestBuildChatRequestBodyFilesMergeDedupesAndSkipsHTTP(t *testing.T) {
	body := buildChatRequestBody(context.TODO(), nil, "conv-1", "sid", "q", nil, map[string]any{
		"files": []any{"/data/x.jpg"},
		"input": []any{
			map[string]any{"input_type": "image", "uri": "https://cdn.example.com/p.png"},
			map[string]any{"input_type": "image", "uri": "/data/x.jpg"},
			map[string]any{"input_type": "image", "uri": "/data/y.jpeg"},
		},
	}, nil, "", 1)

	files, ok := body["files"].(map[string][]string)
	if !ok {
		t.Fatalf("expected files to be map[string][]string, got %#v", body["files"])
	}
	currentFiles := files["1"]
	if len(currentFiles) != 2 {
		t.Fatalf("expected 2 paths (dedupe + skip https), got %#v", currentFiles)
	}
	if currentFiles[0] != "/data/x.jpg" || currentFiles[1] != "/data/y.jpeg" {
		t.Fatalf("unexpected files: %#v", currentFiles)
	}
}

func TestBuildLazyChatRequestMapsAllFields(t *testing.T) {
	req := buildLazyChatRequest(map[string]any{
		"query":      "injected context\n\nhello",
		"user_query": "hello",
		"session_id": "conv-1",
		"history": []any{
			map[string]any{"role": "user", "content": "q1"},
			map[string]any{"role": "assistant", "content": "a1"},
		},
		"filters": map[string]any{
			"kb_id":   []any{"ds_1"},
			"creator": []any{"u1"},
			"tags":    []any{"t1"},
		},
		"files":            map[string]any{"1": []any{"f1", "f2"}},
		"current_turn_seq": 7,
		"reasoning":        false,
		"databases":        []any{map[string]any{"name": "db1"}},
		"dataset":          "default",
		"local_fs_sources": []any{
			map[string]any{"source_id": "src-1"},
		},
		"disabled_tools": []any{"bing"},
		"available_skills": []any{
			"coding/git-workflow",
		},
		"use_memory": true,
		"environment_context": map[string]any{
			"time": map[string]any{
				"now":      "2026-05-11T11:48:00.000Z",
				"timezone": "Asia/Shanghai",
			},
		},
		"user_id":         "user-1",
		"conversation_id": "conv-id-1",
		"mode":            "manual",
		"debug":           true,
		"priority":        9,
		"trace":           true,
		"llm_config": map[string]any{
			"llm": map[string]any{"source": "openai", "model": "gpt-4o"},
		},
		"ocr_config": map[string]any{
			"ocr_type": "mineru",
			"ocr_url":  "https://mineru.net/api/v4/",
		},
		"tool_config": map[string]any{
			"bing": "token-1",
		},
		"mcp_config": []any{
			map[string]any{
				"id":        "msp_1",
				"name":      "context7",
				"transport": "sse",
				"url":       "https://mcp.example.com/sse",
			},
		},
		"has_subagents":   true,
		"enable_workflow": true,
		"enable_subagent": false,
		"workflow_context": map[string]any{
			"session_id": "workflow-session-1",
		},
	})

	if req.Message.Query != "injected context\n\nhello" || req.Message.UserQuery != "hello" || req.Conversation.SessionID != "conv-1" {
		t.Fatalf("unexpected base fields: %#v", req)
	}
	if len(req.Message.History) != 2 || req.Message.History[0].Role != "user" || req.Message.History[1].Content != "a1" {
		t.Fatalf("unexpected history: %#v", req.Message.History)
	}
	if req.Retrieval.Filters == nil || len(req.Retrieval.Filters.DatasetIDs) != 1 || req.Retrieval.Filters.DatasetIDs[0] != "ds_1" {
		t.Fatalf("unexpected filters: %#v", req.Retrieval.Filters)
	}
	if len(req.Retrieval.Filters.Creators) != 1 || req.Retrieval.Filters.Creators[0] != "u1" {
		t.Fatalf("unexpected creators: %#v", req.Retrieval.Filters.Creators)
	}
	if len(req.Retrieval.Filters.Tags) != 1 || req.Retrieval.Filters.Tags[0] != "t1" {
		t.Fatalf("unexpected tags: %#v", req.Retrieval.Filters.Tags)
	}
	if len(req.Message.Files) != 1 || len(req.Message.Files["1"]) != 2 || req.Message.Files["1"][0] != "f1" || req.Message.Files["1"][1] != "f2" {
		t.Fatalf("unexpected files: %#v", req.Message.Files)
	}
	if req.Message.CurrentTurnSeq != 7 {
		t.Fatalf("unexpected current_turn_seq: %d", req.Message.CurrentTurnSeq)
	}
	if len(req.Retrieval.Databases) != 1 || req.Retrieval.Dataset != "default" || len(req.Retrieval.LocalFSSources) != 1 {
		t.Fatalf("unexpected retrieval: %#v", req.Retrieval)
	}
	if req.Runtime.Reasoning {
		t.Fatalf("expected reasoning to be false")
	}
	if !req.Runtime.Debug || req.Runtime.Priority == nil || *req.Runtime.Priority != 9 || !req.Runtime.Trace {
		t.Fatalf("unexpected runtime flags: %#v", req.Runtime)
	}
	if len(req.Agent.DisabledTools) != 1 || req.Agent.DisabledTools[0] != "bing" {
		t.Fatalf("unexpected disabled_tools: %#v", req.Agent.DisabledTools)
	}
	if len(req.Agent.AvailableSkills) != 1 || req.Agent.AvailableSkills[0] != "coding/git-workflow" {
		t.Fatalf("unexpected available_skills: %#v", req.Agent.AvailableSkills)
	}
	if !req.Agent.HasSubagents || req.Agent.EnableSubagent == nil || *req.Agent.EnableSubagent {
		t.Fatalf("unexpected agent flags: %#v", req.Agent)
	}
	if !req.Personalization.UseMemory {
		t.Fatalf("expected use_memory to be true")
	}
	timeContext, _ := req.Runtime.EnvironmentContext["time"].(map[string]any)
	if timeContext["now"] != "2026-05-11T11:48:00.000Z" || timeContext["timezone"] != "Asia/Shanghai" {
		t.Fatalf("unexpected environment_context: %#v", req.Runtime.EnvironmentContext)
	}
	if req.Conversation.UserID != "user-1" || req.Conversation.ConversationID != "conv-id-1" || req.Conversation.Mode != "manual" {
		t.Fatalf("unexpected conversation: %#v", req.Conversation)
	}
	if req.Runtime.LLMConfig == nil || req.Runtime.LLMConfig["llm"] == nil {
		t.Fatalf("expected llm_config to be forwarded, got %#v", req.Runtime.LLMConfig)
	}
	if req.Runtime.OCRConfig == nil || req.Runtime.OCRConfig["ocr_type"] != "mineru" {
		t.Fatalf("expected ocr_config to be forwarded, got %#v", req.Runtime.OCRConfig)
	}
	if req.Runtime.ToolConfig == nil || req.Runtime.ToolConfig["bing"] != "token-1" {
		t.Fatalf("expected tool_config to be forwarded, got %#v", req.Runtime.ToolConfig)
	}
	if len(req.Runtime.MCPConfig) != 1 {
		t.Fatalf("expected mcp_config to be forwarded, got %#v", req.Runtime.MCPConfig)
	}
	if req.Workflow.EnableWorkflow == nil || !*req.Workflow.EnableWorkflow || req.Workflow.WorkflowContext["session_id"] != "workflow-session-1" {
		t.Fatalf("unexpected workflow options: %#v", req.Workflow)
	}

	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	for _, key := range []string{"message", "conversation", "retrieval", "runtime", "personalization", "agent", "workflow"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("expected grouped key %q in payload: %s", key, payload)
		}
	}
	for _, key := range []string{"query", "history", "session_id", "filters", "llm_config", "workflow_context", "enable_thinking"} {
		if _, ok := raw[key]; ok {
			t.Fatalf("unexpected top-level key %q in payload: %s", key, payload)
		}
	}
}

func TestBuildLLMConfigFromSelectedModels(t *testing.T) {
	llmConfig := buildLLMConfig([]selectedRuntimeModel{
		{ModelType: "llm", ProviderName: "OpenAI", ModelName: "gpt-4o", BaseURL: "https://api.openai.com/v1/", APIKey: "sk-from-db"},
		{ModelType: "evo_llm", ProviderName: "OpenAI", ModelName: "gpt-4o-mini", BaseURL: "https://api.openai.com/v1/", APIKey: "sk-from-db"},
		{ModelType: "embed_main", ProviderName: "OpenAI", ModelName: "text-embedding-3-small", BaseURL: "https://api.openai.com/v1/", APIKey: "sk-from-db"},
		{ModelType: "reranker", ProviderName: "OpenAI", ModelName: "rerank-multilingual-v3.0", BaseURL: "https://api.openai.com/v1/", APIKey: "sk-from-db"},
	})

	chatCfg := llmConfig["llm"].(map[string]any)
	evoCfg := llmConfig["evo_llm"].(map[string]any)
	embedCfg := llmConfig["embed_main"].(map[string]any)
	rerankCfg := llmConfig["reranker"].(map[string]any)

	if chatCfg["source"] != "openai" || chatCfg["model"] != "gpt-4o" || chatCfg["api_key"] != "sk-from-db" {
		t.Fatalf("unexpected llm config: %#v", chatCfg)
	}
	if evoCfg["model"] != "gpt-4o-mini" {
		t.Fatalf("unexpected evo_llm config: %#v", evoCfg)
	}
	if embedCfg["model"] != "text-embedding-3-small" {
		t.Fatalf("unexpected embed_main config: %#v", embedCfg)
	}
	if rerankCfg["model"] != "rerank-multilingual-v3.0" {
		t.Fatalf("unexpected reranker config: %#v", rerankCfg)
	}
}

func TestBuildLazyChatRequestDefaultsReasoningTrue(t *testing.T) {
	req := buildLazyChatRequest(map[string]any{
		"query":      "hello",
		"session_id": "conv-1",
	})

	if !req.Runtime.Reasoning {
		t.Fatalf("expected reasoning default true")
	}
}

func TestShouldEmitStreamFrame(t *testing.T) {
	tests := []struct {
		name    string
		delta   string
		sources []any
		want    bool
	}{
		{name: "text chunk", delta: "answer", sources: nil, want: true},
		{name: "source-only chunk", delta: "", sources: []any{map[string]any{"index": 1}}, want: true},
		{name: "empty chunk", delta: "", sources: nil, want: false},
	}

	for _, tt := range tests {
		if got := shouldEmitStreamFrame(tt.delta, tt.sources); got != tt.want {
			t.Fatalf("%s: got %v want %v", tt.name, got, tt.want)
		}
	}
}

func TestFeedBackChatHistoryCancelsFeedback(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ChatHistory{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	if err := db.Create(&orm.ChatHistory{
		ID:             "h_1",
		Seq:            1,
		ConversationID: "conv-1",
		RawContent:     "question",
		Content:        "question",
		Result:         "answer",
		FeedBack:       2,
		Reason:         "not helpful",
		ExpectedAnswer: "better answer",
		TimeMixin:      orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error; err != nil {
		t.Fatalf("create history: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/core/conversations:feedBackChatHistory",
		strings.NewReader(`{"history_id":"h_1","type":"FEED_BACK_TYPE_UNSPECIFIED"}`),
	)
	rec := httptest.NewRecorder()

	FeedBackChatHistory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var history orm.ChatHistory
	if err := db.Where("id = ?", "h_1").First(&history).Error; err != nil {
		t.Fatalf("load history: %v", err)
	}
	if history.FeedBack != 0 {
		t.Fatalf("expected feedback to be cancelled, got %d", history.FeedBack)
	}
	if history.Reason != "" || history.ExpectedAnswer != "" {
		t.Fatalf("expected feedback detail to be cleared, got reason=%q expected_answer=%q", history.Reason, history.ExpectedAnswer)
	}
}

func TestFeedBackChatHistoryReplacesOnlyTargetFeedback(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/feedback-update.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.ChatHistory{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now()
	for _, history := range []orm.ChatHistory{
		{
			ID: "h_1", Seq: 1, ConversationID: "conv-1", RawContent: "question", Content: "question", Result: "answer",
			FeedBack: 2, Reason: "old reason", ExpectedAnswer: "old expected answer",
			TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
		},
		{
			ID: "h_2", Seq: 1, ConversationID: "conv-1", RawContent: "question", Content: "question", Result: "another answer",
			FeedBack: 1, TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
		},
	} {
		if err := db.Create(&history).Error; err != nil {
			t.Fatalf("create history: %v", err)
		}
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/core/conversations:feedBackChatHistory",
		strings.NewReader(`{"history_id":"h_1","type":"FEED_BACK_TYPE_UNLIKE","reason":"new reason","expected_answer":"new expected answer"}`),
	)
	rec := httptest.NewRecorder()
	FeedBackChatHistory(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var target, sibling orm.ChatHistory
	if err := db.Where("id = ?", "h_1").First(&target).Error; err != nil {
		t.Fatalf("load target: %v", err)
	}
	if target.FeedBack != 2 || target.Reason != "new reason" || target.ExpectedAnswer != "new expected answer" {
		t.Fatalf("target feedback was not replaced: %#v", target)
	}
	if err := db.Where("id = ?", "h_2").First(&sibling).Error; err != nil {
		t.Fatalf("load sibling: %v", err)
	}
	if sibling.FeedBack != 1 {
		t.Fatalf("sibling feedback was unexpectedly reset: %#v", sibling)
	}
}

func TestWorkflowModeFromReqBody(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		want string
	}{
		{
			name: "workflow_context auto wins",
			body: map[string]any{
				"workflow_context": map[string]any{"workflow_mode": "auto"},
				"agentic_config":   map[string]any{"workflow_mode": "dynamic"},
			},
			want: "auto",
		},
		{
			name: "agentic_config fallback",
			body: map[string]any{
				"agentic_config": map[string]any{"workflow_mode": "auto"},
			},
			want: "auto",
		},
		{
			name: "missing defaults to dynamic",
			body: map[string]any{},
			want: "dynamic",
		},
		{
			name: "invalid value defaults to dynamic",
			body: map[string]any{
				"workflow_context": map[string]any{"workflow_mode": "invalid"},
			},
			want: "dynamic",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := workflowModeFromReqBody(tc.body); got != tc.want {
				t.Fatalf("workflowModeFromReqBody() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveWorkflowModeWithFallback(t *testing.T) {
	raw := map[string]any{"workflow_mode": "auto"}
	reqBody := map[string]any{
		"agentic_config": map[string]any{"workflow_mode": "dynamic"},
	}
	if got := resolveWorkflowModeWithFallback(raw, reqBody); got != "auto" {
		t.Fatalf("expected raw body to win, got %q", got)
	}
	if got := resolveWorkflowModeWithFallback(map[string]any{}, reqBody); got != "dynamic" {
		t.Fatalf("expected agentic_config fallback, got %q", got)
	}
}

func TestUserExplicitlyRequestedWorkflowRetry(t *testing.T) {
	for _, query := range []string{"重试", "帮我重试这个失败步骤", "retry the failed step", "try again"} {
		if !userExplicitlyRequestedWorkflowRetry(query) {
			t.Errorf("expected explicit retry for %q", query)
		}
	}
	for _, query := range []string{"继续", "不要重试", "do not retry", "分析为什么重试失败"} {
		if userExplicitlyRequestedWorkflowRetry(query) {
			t.Errorf("unexpected retry authorization for %q", query)
		}
	}
}
