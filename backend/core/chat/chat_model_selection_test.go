package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
	"lazymind/core/workflow"
)

type chatModelRoundTripFunc func(*http.Request) (*http.Response, error)

func (f chatModelRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func mockEmptyChatScan(t *testing.T) {
	t.Helper()
	originalScanClient := localFSScanHTTPClient
	localFSScanHTTPClient = &http.Client{Transport: chatModelRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"items":[],"total":0}`)),
			Request:    request,
		}, nil
	})}
	t.Cleanup(func() { localFSScanHTTPClient = originalScanClient })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.invalid")
}

func seedAvailableChatModel(
	t *testing.T,
	db *gorm.DB,
	owner, providerID, groupID, modelID, providerName, groupName, modelName, modelType string,
	verified bool,
	apiKey string,
) orm.UserModelProviderGroupModel {
	t.Helper()
	now := time.Now().UTC()
	provider := orm.UserModelProvider{
		ID: providerID, DefaultModelProviderID: "catalog-" + providerID,
		Name: providerName, Description: providerName, Category: "model", Capabilities: "has_models",
		BaseModel: orm.BaseModel{
			CreateUserID: owner, CreateUserName: owner, CreatedAt: now, UpdatedAt: now,
		},
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("create provider %s: %v", providerID, err)
	}
	group := orm.UserModelProviderGroup{
		ID: groupID, UserModelProviderID: providerID, Name: groupName,
		BaseURL: "https://" + strings.ToLower(providerID) + ".example/v1", APIKey: apiKey, IsVerified: verified,
		BaseModel: orm.BaseModel{
			CreateUserID: owner, CreateUserName: owner, CreatedAt: now, UpdatedAt: now,
		},
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group %s: %v", groupID, err)
	}
	model := orm.UserModelProviderGroupModel{
		ID: modelID, UserModelProviderID: providerID, UserModelProviderGroupID: groupID,
		ProviderName: providerName, Name: modelName, ModelType: modelType,
		BaseModel: orm.BaseModel{
			CreateUserID: owner, CreateUserName: owner, CreatedAt: now, UpdatedAt: now,
		},
	}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("create model %s: %v", modelID, err)
	}
	return model
}

func seedSelectedChatModel(t *testing.T, db *gorm.DB, owner, modelID string, shared bool) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Create(&orm.UserSelectedModel{
		UserID: owner, UserName: owner, ModelKey: "llm",
		UserModelProviderGroupModelID: modelID, Share: shared, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("select model %s: %v", modelID, err)
	}
}

func TestAvailableChatModelsUseUserSelectionAsDefault(t *testing.T) {
	database := newPromptTestDB(t)
	db := database.DB

	catalogDefault := seedAvailableChatModel(t, db, "user-1", "provider-own", "group-own", "model-catalog-default", "DeepSeek", "个人连接", "DeepSeek-V3", "llm", true, "own-secret")
	if err := db.Model(&catalogDefault).Update("is_default", true).Error; err != nil {
		t.Fatalf("mark catalog default: %v", err)
	}
	seedAvailableChatModel(t, db, "user-1", "provider-selected", "group-selected", "model-user-default", "OpenAI", "工作连接", "gpt-5", "llm", true, "selected-secret")
	seedAvailableChatModel(t, db, "user-1", "provider-vlm", "group-vlm", "model-vlm", "Vision", "视觉连接", "vision-1", "vlm", true, "vlm-secret")
	seedAvailableChatModel(t, db, "user-1", "provider-unverified", "group-unverified", "model-unverified", "Broken", "未验证", "broken-chat", "llm", false, "broken-secret")
	seedAvailableChatModel(t, db, "admin", "provider-shared", "group-shared", "model-shared", "Moonshot", "团队共享", "kimi-k2", "llm", true, "shared-secret")
	seedAvailableChatModel(t, db, "other", "provider-private", "group-private", "model-private", "Private", "私有", "private-chat", "llm", true, "private-secret")
	seedSelectedChatModel(t, db, "user-1", "model-user-default", false)
	seedSelectedChatModel(t, db, "admin", "model-shared", true)

	models, err := loadAvailableChatModels(context.Background(), db, "user-1")
	if err != nil {
		t.Fatalf("load models: %v", err)
	}
	got := make(map[string]string, len(models))
	for _, model := range models {
		got[model.ID] = model.Source
	}
	want := map[string]string{
		"model-catalog-default": "own",
		"model-user-default":    "own",
		"model-shared":          "shared",
	}
	if len(got) != len(want) {
		t.Fatalf("available models=%#v, want %#v", got, want)
	}
	for id, source := range want {
		if got[id] != source {
			t.Fatalf("model %s source=%q, want %q; all=%#v", id, got[id], source, got)
		}
	}
	defaultModel, err := resolveDefaultChatModel(context.Background(), db, "user-1", models)
	if err != nil {
		t.Fatalf("resolve default: %v", err)
	}
	if defaultModel == nil || defaultModel.ID != "model-user-default" {
		t.Fatalf("default=%#v, want user_selected_models.llm", defaultModel)
	}
}

func TestConversationFixedModelOverridesOnlyLLMAndNeverFallsBack(t *testing.T) {
	database := newPromptTestDB(t)
	db := database.DB
	seedAvailableChatModel(t, db, "user-1", "provider-a", "group-a", "model-a", "OpenAI", "A", "gpt-default", "llm", true, "secret-a")
	seedAvailableChatModel(t, db, "user-1", "provider-b", "group-b", "model-b", "DeepSeek", "B", "deepseek-chat", "llm", true, "secret-b")
	seedSelectedChatModel(t, db, "user-1", "model-a", false)

	binding, err := resolveInitialChatModelBinding(context.Background(), db, "user-1", &initialChatModelSelection{Mode: chatModelModeFixed, ModelID: "model-b"})
	if err != nil {
		t.Fatalf("resolve fixed binding: %v", err)
	}
	conversation := orm.Conversation{ID: "conversation-fixed", BaseModel: orm.BaseModel{
		CreateUserID: "user-1", CreateUserName: "User", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}
	applyResolvedChatModelBinding(&conversation, binding)
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	body := map[string]any{
		"conversation_id": conversation.ID,
		"llm_config": map[string]any{
			"llm":        map[string]any{"model": "gpt-default"},
			"embed_main": map[string]any{"model": "embedding-model", "api_key": "embedding-secret"},
		},
	}
	if err := applyConversationChatModelConfig(context.Background(), db, "user-1", body); err != nil {
		t.Fatalf("apply fixed config: %v", err)
	}
	config := body["llm_config"].(map[string]any)
	llm := config["llm"].(map[string]any)
	if llm["model"] != "deepseek-chat" || llm["api_key"] != "secret-b" {
		t.Fatalf("fixed llm config=%#v", llm)
	}
	if config["embed_main"].(map[string]any)["model"] != "embedding-model" {
		t.Fatalf("non-chat role changed: %#v", config)
	}

	if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", "model-b").Update("deleted_at", time.Now().UTC()).Error; err != nil {
		t.Fatalf("delete bound model: %v", err)
	}
	body["llm_config"] = map[string]any{"llm": map[string]any{"model": "gpt-default"}}
	if err := applyConversationChatModelConfig(context.Background(), db, "user-1", body); !errors.Is(err, errChatModelUnavailable) {
		t.Fatalf("deleted fixed model error=%v, want unavailable without fallback", err)
	}

	legacy := orm.Conversation{ID: "conversation-legacy", BaseModel: orm.BaseModel{
		CreateUserID: "user-1", CreateUserName: "User", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("create legacy conversation: %v", err)
	}
	legacyBody := map[string]any{
		"conversation_id": legacy.ID,
		"llm_config":      map[string]any{"llm": map[string]any{"model": "legacy-default"}},
	}
	if err := applyConversationChatModelConfig(context.Background(), db, "user-1", legacyBody); err != nil {
		t.Fatalf("apply legacy config: %v", err)
	}
	if model := legacyBody["llm_config"].(map[string]any)["llm"].(map[string]any)["model"]; model != "legacy-default" {
		t.Fatalf("legacy conversation default changed to %v", model)
	}
}

func TestAutoChatModelStaysWithSuccessfulModelAndRoutingIsReadOnly(t *testing.T) {
	database := newPromptTestDB(t)
	db := database.DB
	seedAvailableChatModel(t, db, "user-1", "provider-default", "group-default", "model-default", "Default", "Default", "default-chat", "llm", true, "secret-default")
	seedAvailableChatModel(t, db, "user-1", "provider-other", "group-other", "model-other", "Other", "Other", "other-chat", "llm", true, "secret-other")
	seedSelectedChatModel(t, db, "user-1", "model-default", false)
	fixed, _, err := ensureConversation(context.Background(), db, "fixed-default", "Fixed", nil, nil, "user-1", "User", false, "", nil, nil)
	if err != nil || fixed.ChatModelID == nil || *fixed.ChatModelID != "model-default" || len(fixed.ChatModelSnapshot) == 0 {
		t.Fatalf("default fixed binding=%#v err=%v", fixed, err)
	}
	conversation, _, err := ensureConversation(context.Background(), db, "auto-sticky", "Auto", nil, nil, "user-1", "User", false, "", nil, &initialChatModelSelection{Mode: chatModelModeAuto})
	if err != nil {
		t.Fatalf("create auto conversation: %v", err)
	}
	apply := func(body map[string]any) *chatModelRoute {
		t.Helper()
		body["conversation_id"] = conversation.ID
		if err := applyChatRuntimeConfigs(context.Background(), db, "user-1", body); err != nil {
			t.Fatalf("apply runtime model: %v", err)
		}
		return chatModelRouteFromBody(body)
	}
	body := map[string]any{"query": "hello", "thinking_depth": "low", "context_usage_preview": true}
	first := apply(body)
	if first == nil || first.ModelID != "model-default" || first.Reason != "initial_selection" {
		t.Fatalf("initial auto route=%#v", first)
	}
	var stored orm.Conversation
	if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil || len(stored.ChatModelSnapshot) != 0 {
		t.Fatalf("preview wrote attempted model: snapshot=%s err=%v", stored.ChatModelSnapshot, err)
	}
	for _, terminal := range []*RunTerminal{
		{Status: "failed", Reason: "model_failure", Code: "service_unavailable"},
		{Status: "cancelled", Reason: "user_cancelled"},
	} {
		persistSuccessfulChatModel(context.Background(), db, "user-1", conversation.ID, "failed-or-cancelled", body, terminal)
	}
	if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil || len(stored.ChatModelSnapshot) != 0 {
		t.Fatalf("unsuccessful call changed snapshot=%s err=%v", stored.ChatModelSnapshot, err)
	}
	persistSuccessfulChatModel(context.Background(), db, "user-1", conversation.ID, "successful-run", body, &RunTerminal{Status: "completed", Reason: "normal"})
	if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	successSnapshot := append(json.RawMessage(nil), stored.ChatModelSnapshot...)
	var snapshot chatModelSnapshot
	if err := json.Unmarshal(successSnapshot, &snapshot); err != nil || snapshot.SuccessfulRunID != "successful-run" || snapshot.ModelID != "model-default" {
		t.Fatalf("successful snapshot=%s err=%v", successSnapshot, err)
	}
	if strings.Contains(string(successSnapshot), "secret-") {
		t.Fatalf("successful snapshot leaked credentials: %s", successSnapshot)
	}
	if err := db.Model(&orm.UserSelectedModel{}).Where("user_id = ? AND model_type = ?", "user-1", "llm").Update("user_model_provider_group_model_id", "model-other").Error; err != nil {
		t.Fatal(err)
	}
	for _, request := range []map[string]any{
		{"query": "simple", "thinking_depth": "low"},
		{"query": "complex", "thinking_depth": "max", "has_subagents": true, "files": map[string][]string{"1": {"/uploads/report.pdf"}}},
		{"query": "continue", "history": []map[string]any{{"content": strings.Repeat("context", 20000)}}, "context_prompt_export": true},
	} {
		if route := apply(request); route.ModelID != "model-default" || route.Reason != "session_sticky" {
			t.Fatalf("request signals or changed default switched model: %#v", route)
		}
	}
	if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil || !bytes.Equal(stored.ChatModelSnapshot, successSnapshot) || stored.ChatModelVersion != 1 {
		t.Fatalf("runtime/preview changed successful binding: %#v err=%v", stored, err)
	}
	if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", "model-default").Update("deleted_at", time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	if route := apply(map[string]any{"query": "new turn"}); route.ModelID != "model-other" || route.Reason != "model_unavailable" {
		t.Fatalf("unavailable successful model did not switch: %#v", route)
	}
	retryBody := map[string]any{"conversation_id": conversation.ID, chatModelRetryRouteBodyKey: first}
	if err := applyConversationChatModelConfig(context.Background(), db, "user-1", retryBody); !errors.Is(err, errChatModelUnavailable) {
		t.Fatalf("retry silently replaced deleted original model: %v", err)
	}
	if err := db.Model(&orm.Conversation{}).Where("id = ?", conversation.ID).Updates(map[string]any{
		"chat_model_mode": chatModelModeFixed, "chat_model_id": "model-other", "chat_model_version": 2,
	}).Error; err != nil {
		t.Fatal(err)
	}
	persistSuccessfulChatModel(context.Background(), db, "user-1", conversation.ID, "late-old-run", body, &RunTerminal{Status: "completed", Reason: "normal"})
	if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil || !bytes.Equal(stored.ChatModelSnapshot, successSnapshot) {
		t.Fatalf("old run overwrote new manual selection: %#v err=%v", stored, err)
	}
	if err := applyConversationChatModelConfig(context.Background(), db, "user-1", retryBody); err != nil || chatModelRouteFromBody(retryBody).ModelID != "model-other" {
		t.Fatalf("manual selection overridden on retry: %#v err=%v", chatModelRouteFromBody(retryBody), err)
	}
	if err := db.Model(&orm.Conversation{}).Where("id = ?", conversation.ID).Updates(map[string]any{
		"chat_model_mode": chatModelModeAuto, "chat_model_id": nil, "chat_model_snapshot": nil, "chat_model_version": 3,
	}).Error; err != nil {
		t.Fatal(err)
	}
	persistSuccessfulChatModel(context.Background(), db, "user-1", conversation.ID, "late-old-auto-run", body, &RunTerminal{Status: "completed", Reason: "normal"})
	stored = orm.Conversation{}
	if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil || len(stored.ChatModelSnapshot) != 0 {
		t.Fatalf("old run overwrote a newer Auto selection version: %#v err=%v", stored, err)
	}
}

func TestAutoChatModelUsesProviderFailureOnlyForNextTurn(t *testing.T) {
	for _, test := range []struct {
		name        string
		terminal    RunTerminal
		wantModelID string
	}{
		{name: "provider unavailable", terminal: RunTerminal{Status: "failed", Reason: "model_failure", Code: "service_unavailable"}, wantModelID: "model-other"},
		{name: "credentials unavailable", terminal: RunTerminal{Status: "failed", Reason: "model_failure", Code: "authentication_failed"}, wantModelID: "model-other"},
		{name: "context limit", terminal: RunTerminal{Status: "failed", Reason: "model_failure", Code: "token_limit"}, wantModelID: "model-default"},
		{name: "tool failure", terminal: RunTerminal{Status: "failed", Reason: "runtime_failure", Code: "tool_error"}, wantModelID: "model-default"},
		{name: "cancelled", terminal: RunTerminal{Status: "cancelled", Reason: "user_cancelled"}, wantModelID: "model-default"},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := newPromptTestDB(t)
			db := database.DB
			seedAvailableChatModel(t, db, "user-1", "provider-default", "group-default", "model-default", "Default", "Default", "default-chat", "llm", true, "secret-default")
			seedAvailableChatModel(t, db, "user-1", "provider-other", "group-other", "model-other", "Other", "Other", "other-chat", "llm", true, "secret-other")
			seedSelectedChatModel(t, db, "user-1", "model-default", false)
			conversation, _, err := ensureConversation(context.Background(), db, "auto-outcome", "Auto", nil, nil, "user-1", "User", false, "", nil, &initialChatModelSelection{Mode: chatModelModeAuto})
			if err != nil {
				t.Fatal(err)
			}
			body := map[string]any{"conversation_id": conversation.ID}
			if err := applyConversationChatModelConfig(context.Background(), db, "user-1", body); err != nil {
				t.Fatal(err)
			}
			firstRoute := chatModelRouteFromBody(body)
			if err := db.Create(&orm.ChatHistory{
				ID: "failed-history", ConversationID: conversation.ID, Seq: 1, RunID: "failed-run", RunStatus: test.terminal.Status,
				RunTerminal: terminalJSON(&test.terminal), Ext: mergeChatModelRouteIntoExt(nil, body),
				TimeMixin: orm.TimeMixin{CreateTime: time.Now().UTC(), UpdateTime: time.Now().UTC()},
			}).Error; err != nil {
				t.Fatal(err)
			}
			// Changing the settings default cannot switch an existing Auto session.
			if err := db.Model(&orm.UserSelectedModel{}).Where("user_id = ? AND model_type = ?", "user-1", "llm").Update("user_model_provider_group_model_id", "model-other").Error; err != nil {
				t.Fatal(err)
			}
			if err := applyConversationChatModelConfig(context.Background(), db, "user-1", body); err != nil || chatModelRouteFromBody(body).ModelID != test.wantModelID {
				t.Fatalf("next turn route=%#v err=%v, want %s", chatModelRouteFromBody(body), err, test.wantModelID)
			}
			retry := map[string]any{"conversation_id": conversation.ID, chatModelRetryRouteBodyKey: firstRoute}
			if err := applyConversationChatModelConfig(context.Background(), db, "user-1", retry); err != nil || chatModelRouteFromBody(retry).ModelID != "model-default" {
				t.Fatalf("retry replaced original model: %#v err=%v", chatModelRouteFromBody(retry), err)
			}
			// An explicit new Auto selection permits a fresh choice and ignores old failures.
			if err := db.Model(&orm.Conversation{}).Where("id = ?", conversation.ID).Updates(map[string]any{"chat_model_snapshot": nil, "chat_model_version": 2}).Error; err != nil {
				t.Fatal(err)
			}
			if err := applyConversationChatModelConfig(context.Background(), db, "user-1", retry); err != nil || chatModelRouteFromBody(retry).ModelID != "model-other" {
				t.Fatalf("new Auto selection reused old route: %#v err=%v", chatModelRouteFromBody(retry), err)
			}
		})
	}
}

func TestLastSuccessfulChatModelIgnoresLegacyAttemptedSnapshot(t *testing.T) {
	database := newPromptTestDB(t)
	db := database.DB
	mode := chatModelModeAuto
	conversation := orm.Conversation{ID: "legacy-attempt", ChatModelMode: &mode, ChatModelVersion: 1, ChatModelSnapshot: json.RawMessage(`{"model_id":"attempted","model_name":"Attempted"}`)}
	if snapshot, err := lastSuccessfulChatModelSnapshot(context.Background(), db, &conversation); err != nil || snapshot != nil {
		t.Fatalf("attempted snapshot treated as successful: %#v err=%v", snapshot, err)
	}
	terminal := &RunTerminal{Status: "completed", Reason: "normal"}
	ext := json.RawMessage(`{"model_route":{"mode":"auto","model_id":"succeeded","provider_id":"provider","provider_name":"Provider","model_name":"Successful","source":"own"}}`)
	if err := db.Create(&orm.MultiAnswersChatHistory{
		ID: "dual-success", ConversationID: conversation.ID, Seq: 1, RunID: "success-run", RunStatus: "completed", RunTerminal: terminalJSON(terminal), Ext: ext,
		TimeMixin: orm.TimeMixin{CreateTime: time.Now().UTC(), UpdateTime: time.Now().UTC()},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if snapshot, err := lastSuccessfulChatModelSnapshot(context.Background(), db, &conversation); err != nil || snapshot == nil || snapshot.ModelID != "succeeded" || snapshot.SuccessfulRunID != "success-run" {
		t.Fatalf("successful dual history missing: %#v err=%v", snapshot, err)
	}
	if string(conversation.ChatModelSnapshot) != `{"model_id":"attempted","model_name":"Attempted"}` {
		t.Fatalf("read-only lookup mutated attempted snapshot: %s", conversation.ChatModelSnapshot)
	}
}

func TestChatModelSuccessPersistsAcrossReplyModes(t *testing.T) {
	for _, replyMode := range []string{"nonstream", "stream", "dual"} {
		for _, status := range []string{"completed", "failed", "cancelled"} {
			t.Run(replyMode+"/"+status, func(t *testing.T) {
				database := newPromptTestDB(t)
				db := database.DB
				seedAvailableChatModel(t, db, "user-1", "provider-default", "group-default", "model-default", "Default", "Default", "default-chat", "llm", true, "secret-default")
				seedSelectedChatModel(t, db, "user-1", "model-default", false)
				conversation, _, err := ensureConversation(context.Background(), db, "reply-success", "Auto", nil, nil, "user-1", "User", false, "", nil, &initialChatModelSelection{Mode: chatModelModeAuto})
				if err != nil {
					t.Fatal(err)
				}
				terminal := RunTerminal{Status: status, Reason: "normal", PartialOutput: true}
				if status == "failed" {
					terminal.Reason, terminal.Code = "model_failure", "service_unavailable"
				} else if status == "cancelled" {
					terminal.Reason = "user_cancelled"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request LazyChatRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					encoder := json.NewEncoder(w)
					_ = encoder.Encode(map[string]any{"code": 200, "msg": "success", "data": map[string]any{"text": "answer"}})
					_ = encoder.Encode(map[string]any{"code": 200, "msg": "success", "data": map[string]any{"runtime_event": runFinishedEvent(request.Conversation.RunID, terminal)}})
				}))
				defer server.Close()
				body := map[string]any{"conversation_id": conversation.ID, "user_id": "user-1", "query": "hello", "run_id": "primary-run", "secondary_run_id": "secondary-run"}
				if err := applyConversationChatModelConfig(context.Background(), db, "user-1", body); err != nil {
					t.Fatal(err)
				}
				ext := mergeChatModelRouteIntoExt(nil, body)
				recorder := httptest.NewRecorder()
				target := chatPersistTarget{Seq: 1, HistoryID: "primary-history"}
				ctx := context.Background()
				switch replyMode {
				case "nonstream":
					handleNonStreamChat(recorder, ctx, db, nil, server.URL, body, conversation.ID, "hello", target, ext)
				case "stream":
					streamSingleAnswer(ctx, ctx, recorder, recorder, db, nil, server.URL, body, conversation.ID, "hello", target.HistoryID, target, ext)
				case "dual":
					streamDualAnswer(ctx, ctx, recorder, recorder, db, nil, server.URL, body, conversation.ID, "hello", target.HistoryID, "secondary-history", target, ext)
				}
				var stored orm.Conversation
				if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil {
					t.Fatal(err)
				}
				if status == "completed" {
					var snapshot chatModelSnapshot
					if json.Unmarshal(stored.ChatModelSnapshot, &snapshot) != nil || snapshot.ModelID != "model-default" || snapshot.SuccessfulRunID == "" {
						t.Fatalf("completed reply did not persist success: %s; response=%s", stored.ChatModelSnapshot, recorder.Body.String())
					}
				} else if len(stored.ChatModelSnapshot) != 0 {
					t.Fatalf("%s reply persisted successful model: %s", status, stored.ChatModelSnapshot)
				}
			})
		}
	}
}

func TestChatConversationPersistsSafeModelPreflightFailure(t *testing.T) {
	database := newPromptTestDB(t)
	db := database.DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	seedAvailableChatModel(
		t, db, "user-1", "provider-a", "group-a", "model-a",
		"OpenAI", "A", "gpt-default", "llm", true, "must-not-leak-preflight",
	)
	seedSelectedChatModel(t, db, "user-1", "model-a", false)
	binding, err := resolveInitialChatModelBinding(context.Background(), db, "user-1", nil)
	if err != nil {
		t.Fatalf("resolve default binding: %v", err)
	}
	conversation := orm.Conversation{ID: "conversation-preflight", BaseModel: orm.BaseModel{
		CreateUserID: "user-1", CreateUserName: "User", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}
	applyResolvedChatModelBinding(&conversation, binding)
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Model(&orm.UserModelProviderGroupModel{}).
		Where("id = ?", "model-a").
		Update("deleted_at", time.Now().UTC()).Error; err != nil {
		t.Fatalf("delete bound model: %v", err)
	}

	mockEmptyChatScan(t)

	requestBody := func(regenerate bool) string {
		action := ""
		if regenerate {
			action = `,"action":"CHAT_ACTION_REGENERATION"`
		}
		return `{"conversation_id":"conversation-preflight","stream":true,"input":[` +
			`{"input_type":"text","text":"explain the report"},` +
			`{"input_type":"file","uri":"/uploads/report.pdf","name":"report.pdf"}]` + action + `}`
	}
	send := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/core/conversations:chat", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-User-Id", "user-1")
		request.Header.Set("X-User-Name", "User")
		recorder := httptest.NewRecorder()
		ChatConversations(recorder, request)
		return recorder
	}

	first := send(requestBody(false))
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("first preflight status=%d body=%s", first.Code, first.Body.String())
	}
	var failureResponse struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &failureResponse); err != nil {
		t.Fatalf("decode structured preflight failure: %v", err)
	}
	if failureResponse.Code != 2001597 || failureResponse.Message != errChatModelUnavailable.Error() {
		t.Fatalf("unexpected structured preflight failure: %#v", failureResponse)
	}
	if strings.Contains(first.Body.String(), "must-not-leak-preflight") {
		t.Fatalf("preflight response leaked credentials: %s", first.Body.String())
	}

	var histories []orm.ChatHistory
	if err := db.Where("conversation_id = ?", conversation.ID).Order("seq ASC").Find(&histories).Error; err != nil {
		t.Fatalf("load preflight failure: %v", err)
	}
	if len(histories) != 1 {
		t.Fatalf("preflight failures=%d, want 1", len(histories))
	}
	history := histories[0]
	if history.RawContent != "explain the report" || history.Content != "explain the report" || history.Result != "" {
		t.Fatalf("unexpected preserved user turn: %#v", history)
	}
	terminal, err := parseRunTerminal(history.RunTerminal)
	if err != nil {
		t.Fatalf("parse preflight terminal: %v", err)
	}
	if terminal.Status != "failed" || terminal.Reason != "model_failure" || terminal.Code != "not_found" || terminal.PartialOutput {
		t.Fatalf("unexpected preflight terminal: %#v", terminal)
	}
	var ext struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(history.Ext, &ext); err != nil {
		t.Fatalf("decode preflight ext: %v", err)
	}
	if len(ext.Input) != 2 || ext.Input[1]["uri"] != "/uploads/report.pdf" {
		t.Fatalf("preflight input was not preserved: %#v", ext.Input)
	}
	if strings.Contains(string(history.Ext), "must-not-leak-preflight") || strings.Contains(string(history.RunTerminal), "must-not-leak-preflight") {
		t.Fatalf("persisted preflight failure leaked credentials: ext=%s terminal=%s", history.Ext, history.RunTerminal)
	}

	second := send(requestBody(true))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("regenerated preflight status=%d body=%s", second.Code, second.Body.String())
	}
	histories = nil
	if err := db.Where("conversation_id = ?", conversation.ID).Order("seq ASC").Find(&histories).Error; err != nil {
		t.Fatalf("reload regenerated preflight failure: %v", err)
	}
	if len(histories) != 1 {
		t.Fatalf("regenerated preflight created duplicate rows: %#v", histories)
	}
	var regeneratedExt struct {
		Attempts []failedRunAttempt `json:"failed_run_attempts"`
	}
	if err := json.Unmarshal(histories[0].Ext, &regeneratedExt); err != nil {
		t.Fatalf("decode regenerated preflight ext: %v", err)
	}
	if len(regeneratedExt.Attempts) != 1 || regeneratedExt.Attempts[0].RunID != history.RunID || regeneratedExt.Attempts[0].RunTerminal.Code != "not_found" {
		t.Fatalf("previous preflight failure was not archived: %#v", regeneratedExt.Attempts)
	}
}

func TestChatConversationPersistsUnavailableInitialSelection(t *testing.T) {
	tests := []struct {
		name               string
		conversationID     string
		selection          string
		wantMode           string
		wantModelID        string
		seedRecoveryBefore bool
	}{
		{
			name:               "missing fixed model",
			conversationID:     "new-invalid-fixed",
			selection:          `{"mode":"fixed","model_id":"missing-model"}`,
			wantMode:           chatModelModeFixed,
			wantModelID:        "missing-model",
			seedRecoveryBefore: true,
		},
		{
			name:           "auto without usable models",
			conversationID: "new-invalid-auto",
			selection:      `{"mode":"auto"}`,
			wantMode:       chatModelModeAuto,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := newPromptTestDB(t)
			db := database.DB
			store.Init(db, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })

			if test.seedRecoveryBefore {
				seedAvailableChatModel(
					t, db, "user-1", "provider-recovery", "group-recovery", "model-recovery",
					"Recovery", "Recovery", "recovery-chat", "llm", true, "must-not-leak-initial",
				)
			}

			mockEmptyChatScan(t)

			body := `{"conversation_id":"` + test.conversationID + `","stream":true,"input":[` +
				`{"input_type":"text","text":"explain the first report"},` +
				`{"input_type":"file","uri":"/uploads/first-report.pdf","name":"first-report.pdf"}],` +
				`"initial_model_selection":` + test.selection + `}`
			request := httptest.NewRequest(http.MethodPost, "/api/core/conversations:chat", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-User-Id", "user-1")
			request.Header.Set("X-User-Name", "User")
			recorder := httptest.NewRecorder()
			ChatConversations(recorder, request)

			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("initial selection status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var failureResponse struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &failureResponse); err != nil {
				t.Fatalf("decode initial failure: %v", err)
			}
			if failureResponse.Code != 2001597 || failureResponse.Message != errChatModelUnavailable.Error() {
				t.Fatalf("unexpected initial failure: %#v", failureResponse)
			}

			var conversation orm.Conversation
			if err := db.Where("id = ?", test.conversationID).Take(&conversation).Error; err != nil {
				t.Fatalf("load persisted conversation: %v", err)
			}
			if conversation.ChatModelMode == nil || *conversation.ChatModelMode != test.wantMode || conversation.ChatModelVersion != 1 {
				t.Fatalf("persisted unavailable binding=%#v", conversation)
			}
			if test.wantModelID == "" {
				if conversation.ChatModelID != nil {
					t.Fatalf("auto binding unexpectedly stored model id: %#v", conversation.ChatModelID)
				}
			} else if conversation.ChatModelID == nil || *conversation.ChatModelID != test.wantModelID {
				t.Fatalf("fixed binding model id=%#v, want %q", conversation.ChatModelID, test.wantModelID)
			}
			if len(conversation.ChatModelSnapshot) != 0 {
				t.Fatalf("unavailable binding stored a credential-bearing snapshot: %s", conversation.ChatModelSnapshot)
			}

			var histories []orm.ChatHistory
			if err := db.Where("conversation_id = ?", test.conversationID).Order("seq ASC").Find(&histories).Error; err != nil {
				t.Fatalf("load initial failed history: %v", err)
			}
			if len(histories) != 1 {
				t.Fatalf("initial failed histories=%d, want 1", len(histories))
			}
			history := histories[0]
			if history.RawContent != "explain the first report" || history.Content != "explain the first report" || history.Result != "" {
				t.Fatalf("unexpected initial failed history: %#v", history)
			}
			terminal, err := parseRunTerminal(history.RunTerminal)
			if err != nil {
				t.Fatalf("parse initial terminal: %v", err)
			}
			if terminal.Status != "failed" || terminal.Reason != "model_failure" || terminal.Code != "not_found" || terminal.PartialOutput {
				t.Fatalf("unexpected initial terminal: %#v", terminal)
			}
			var ext struct {
				Input []map[string]any `json:"input"`
			}
			if err := json.Unmarshal(history.Ext, &ext); err != nil {
				t.Fatalf("decode initial history ext: %v", err)
			}
			if len(ext.Input) != 2 || ext.Input[1]["uri"] != "/uploads/first-report.pdf" {
				t.Fatalf("initial input was not preserved: %#v", ext.Input)
			}
			persisted := recorder.Body.String() + string(conversation.ChatModelSnapshot) + string(history.Ext) + string(history.RunTerminal)
			if strings.Contains(persisted, "must-not-leak-initial") {
				t.Fatalf("initial failure leaked model credentials: %s", persisted)
			}
			if !test.seedRecoveryBefore {
				seedAvailableChatModel(
					t, db, "user-1", "provider-recovery", "group-recovery", "model-recovery",
					"Recovery", "Recovery", "recovery-chat", "llm", true, "must-not-leak-initial",
				)
			}

			patchRequest := httptest.NewRequest(
				http.MethodPatch,
				"/api/core/conversations/"+test.conversationID+"/model",
				bytes.NewBufferString(`{"mode":"fixed","model_id":"model-recovery","expected_version":1}`),
			)
			patchRequest.Header.Set("Content-Type", "application/json")
			patchRequest.Header.Set("X-User-Id", "user-1")
			patchRequest = mux.SetURLVars(patchRequest, map[string]string{"conversation_id": test.conversationID})
			patchRecorder := httptest.NewRecorder()
			PatchConversationModel(patchRecorder, patchRequest)
			if patchRecorder.Code != http.StatusOK {
				t.Fatalf("recovery patch status=%d body=%s", patchRecorder.Code, patchRecorder.Body.String())
			}
			if strings.Contains(patchRecorder.Body.String(), "must-not-leak-initial") {
				t.Fatalf("recovery patch leaked credentials: %s", patchRecorder.Body.String())
			}
			if err := db.Where("id = ?", test.conversationID).Take(&conversation).Error; err != nil {
				t.Fatalf("reload recovered conversation: %v", err)
			}
			if conversation.ChatModelMode == nil || *conversation.ChatModelMode != chatModelModeFixed ||
				conversation.ChatModelID == nil || *conversation.ChatModelID != "model-recovery" ||
				conversation.ChatModelVersion != 2 || len(conversation.ChatModelSnapshot) == 0 {
				t.Fatalf("recovered binding=%#v", conversation)
			}
			runtimeBody := map[string]any{"conversation_id": test.conversationID}
			if err := applyConversationChatModelConfig(context.Background(), db, "user-1", runtimeBody); err != nil {
				t.Fatalf("apply recovered runtime model: %v", err)
			}
			llmConfig := runtimeBody["llm_config"].(map[string]any)["llm"].(map[string]any)
			if llmConfig["model"] != "recovery-chat" {
				t.Fatalf("recovered runtime model=%#v", llmConfig)
			}
		})
	}
}

func TestConversationModelHandlersPersistVersionAndBlockActiveRuns(t *testing.T) {
	database := newPromptTestDB(t)
	db := database.DB
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	store.Init(db, nil, stateStore)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	seedAvailableChatModel(t, db, "user-1", "provider-a", "group-a", "model-a", "OpenAI", "A", "gpt-default", "llm", true, "must-not-leak-a")
	seedAvailableChatModel(t, db, "user-1", "provider-b", "group-b", "model-b", "DeepSeek", "B", "deepseek-chat", "llm", true, "must-not-leak-b")
	seedSelectedChatModel(t, db, "user-1", "model-a", false)
	binding, err := resolveInitialChatModelBinding(context.Background(), db, "user-1", nil)
	if err != nil {
		t.Fatalf("resolve default binding: %v", err)
	}
	conversation := orm.Conversation{ID: "conversation-switch", BaseModel: orm.BaseModel{
		CreateUserID: "user-1", CreateUserName: "User", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}
	applyResolvedChatModelBinding(&conversation, binding)
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	if err := setChatStatus(context.Background(), stateStore, conversation.ID, "history-running", "generating", ""); err != nil {
		t.Fatalf("set generating status: %v", err)
	}
	listRequest := httptest.NewRequest(http.MethodGet, "/api/core/chat/models?conversation_id="+conversation.ID, nil)
	listRequest.Header.Set("X-User-Id", "user-1")
	listRecorder := httptest.NewRecorder()
	ListChatModels(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list models status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	if strings.Contains(listRecorder.Body.String(), "must-not-leak") || strings.Contains(listRecorder.Body.String(), "base_url") {
		t.Fatalf("list response leaked model credentials: %s", listRecorder.Body.String())
	}
	var listPayload struct {
		Data chatModelsResponse `json:"data"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listPayload.Data.SwitchAllowed || listPayload.Data.SwitchBlockedReason != "generating" {
		t.Fatalf("generating switch state=%#v", listPayload.Data)
	}
	notOwnedListRequest := httptest.NewRequest(http.MethodGet, "/api/core/chat/models?conversation_id="+conversation.ID, nil)
	notOwnedListRequest.Header.Set("X-User-Id", "user-2")
	notOwnedListRecorder := httptest.NewRecorder()
	ListChatModels(notOwnedListRecorder, notOwnedListRequest)
	if notOwnedListRecorder.Code != http.StatusNotFound {
		t.Fatalf("not-owned list status=%d body=%s", notOwnedListRecorder.Code, notOwnedListRecorder.Body.String())
	}

	patchAs := func(userID, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPatch, "/api/core/conversations/"+conversation.ID+"/model", bytes.NewBufferString(body))
		request.Header.Set("X-User-Id", userID)
		request = mux.SetURLVars(request, map[string]string{"conversation_id": conversation.ID})
		recorder := httptest.NewRecorder()
		PatchConversationModel(recorder, request)
		return recorder
	}
	patch := func(body string) *httptest.ResponseRecorder {
		return patchAs("user-1", body)
	}
	notOwnedPatch := patchAs("user-2", `{"mode":"fixed","model_id":"model-b","expected_version":1}`)
	if notOwnedPatch.Code != http.StatusNotFound {
		t.Fatalf("not-owned patch status=%d body=%s", notOwnedPatch.Code, notOwnedPatch.Body.String())
	}
	busy := patch(`{"mode":"fixed","model_id":"model-b","expected_version":1}`)
	if busy.Code != http.StatusConflict {
		t.Fatalf("generating patch status=%d body=%s", busy.Code, busy.Body.String())
	}
	if err := clearChatData(context.Background(), stateStore, conversation.ID, "history-running"); err != nil {
		t.Fatalf("clear generating status: %v", err)
	}

	updated := patch(`{"mode":"fixed","model_id":"model-b","expected_version":1}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", updated.Code, updated.Body.String())
	}
	var patchPayload struct {
		Data struct {
			Selection chatModelSelectionResponse `json:"selection"`
		} `json:"data"`
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &patchPayload); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if patchPayload.Data.Selection.ModelID != "model-b" || patchPayload.Data.Selection.Version != 2 {
		t.Fatalf("updated selection=%#v", patchPayload.Data.Selection)
	}
	var stored orm.Conversation
	if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil {
		t.Fatalf("reload conversation: %v", err)
	}
	if stored.ChatModelID == nil || *stored.ChatModelID != "model-b" || stored.ChatModelVersion != 2 || len(stored.ChatModelSnapshot) == 0 {
		t.Fatalf("stored binding=%#v", stored)
	}

	stale := patch(`{"mode":"auto","expected_version":1}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale patch status=%d body=%s", stale.Code, stale.Body.String())
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "workflow-active", ConversationID: conversation.ID, WorkflowID: "workflow-1",
		Status: workflow.SessionStatusActive, CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create active workflow: %v", err)
	}
	workflowBusy := patch(`{"mode":"auto","expected_version":2}`)
	if workflowBusy.Code != http.StatusConflict {
		t.Fatalf("workflow patch status=%d body=%s", workflowBusy.Code, workflowBusy.Body.String())
	}
	if err := db.Model(&orm.WorkflowSession{}).
		Where("id = ?", "workflow-active").
		Update("status", workflow.SessionStatusCompleted).Error; err != nil {
		t.Fatalf("complete workflow: %v", err)
	}
	if err := db.Create(&orm.TaskCenterTask{
		ID: "background-active", UserID: "user-1", ConversationID: conversation.ID,
		TaskType: "background_chat", Status: "running", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create active background task: %v", err)
	}
	backgroundBusy := patch(`{"mode":"auto","expected_version":2}`)
	if backgroundBusy.Code != http.StatusConflict {
		t.Fatalf("background patch status=%d body=%s", backgroundBusy.Code, backgroundBusy.Body.String())
	}
}
