package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

func TestForkUsesCurrentConversationModelSelection(t *testing.T) {
	for _, test := range []struct {
		name               string
		mode               string
		unavailable        bool
		historyUnavailable bool
		wantModel          string
	}{
		{name: "selected model without a new reply", mode: "fixed", wantModel: "model-new"},
		{name: "selected model replaces unavailable history model", mode: "fixed", historyUnavailable: true, wantModel: "model-new"},
		{name: "unavailable selection falls back to history", mode: "fixed", unavailable: true, wantModel: "model-fork"},
		{name: "auto selection without a new reply", mode: "auto", wantModel: "model-fork"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, source, histories, _ := forkFixture(t, 1)
			var historyExt map[string]any
			if err := json.Unmarshal(histories[0].Ext, &historyExt); err != nil {
				t.Fatal(err)
			}
			historyExt["conversation_config_snapshot"].(map[string]any)["max_input_tokens"] = "4096"
			histories[0].Ext = marshalChatHistoryExt(historyExt)
			if err := db.Model(&histories[0]).Update("ext", histories[0].Ext).Error; err != nil {
				t.Fatal(err)
			}
			store.Init(db, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			seedAvailableChatModel(t, db, "u1", "provider-new", "group-new", "model-new", "New", "New", "New", "llm", true, "fake-new-key")
			body := `{"mode":"fixed","model_id":"model-new","source":"own","expected_version":0}`
			if test.mode == "auto" {
				body = `{"mode":"auto","expected_version":0}`
			}
			recorder := httptest.NewRecorder()
			PatchConversationModel(recorder, sidechatRequest(http.MethodPatch, "/api/core/conversations/"+source.ID+"/model", "u1", body, map[string]string{"conversation_id": source.ID}))
			if recorder.Code != http.StatusOK {
				t.Fatalf("save current selection: %d %s", recorder.Code, recorder.Body.String())
			}
			if test.unavailable {
				if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-new").Update("is_verified", false).Error; err != nil {
					t.Fatal(err)
				}
			}
			if test.historyUnavailable {
				if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-fork").Update("is_verified", false).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Where("id = ?", source.ID).Take(&source).Error; err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			caller := doc.DatasetCatalogCaller{UserID: "u1"}
			preview, err := buildForkPreview(ctx, db, caller, source, histories)
			if err != nil {
				t.Fatal(err)
			}
			if got := preview.ConfigSnapshotSummary.Model; got == nil || got.ModelID != test.wantModel || got.Mode != test.mode {
				t.Fatalf("preview selection = %#v, want %s / %s", got, test.mode, test.wantModel)
			}
			wantLimit := "4096"
			if test.wantModel == "model-new" {
				wantLimit = ""
			}
			if preview.ConfigSnapshotSummary.MaxInputTokens != wantLimit || len(preview.ConfigIssues) != 0 {
				t.Fatalf("unexpected model configuration: %#v, issues=%#v", preview.ConfigSnapshotSummary, preview.ConfigIssues)
			}
			result, err := createConversationFork(ctx, db, caller, source.ID, "current-model", forkCreateRequest{SourceHistoryID: histories[0].ID, ExpectedPrefixRevision: preview.PrefixRevision})
			if err != nil {
				t.Fatal(err)
			}
			var branch orm.Conversation
			if err := db.Where("id = ?", result.Conversation["conversation_id"]).Take(&branch).Error; err != nil {
				t.Fatal(err)
			}
			if branch.ChatModelMode == nil || *branch.ChatModelMode != test.mode || branch.ChatModelID == nil || *branch.ChatModelID != test.wantModel {
				t.Fatalf("branch did not persist selected model: mode=%v model=%v", branch.ChatModelMode, branch.ChatModelID)
			}
			request := map[string]any{"conversation_id": branch.ID}
			if err := applyConversationChatModelConfig(ctx, db, "u1", request); err != nil {
				t.Fatal(err)
			}
			if got := chatModelRouteFromBody(request); got == nil || got.ModelID != test.wantModel || got.Mode != test.mode {
				t.Fatalf("first request model = %#v", got)
			}
			var original orm.ChatHistory
			if err := db.Where("id = ?", histories[0].ID).Take(&original).Error; err != nil || string(original.Ext) != string(histories[0].Ext) {
				t.Fatalf("fork changed source history: %v", err)
			}
			var unchanged orm.Conversation
			if err := db.Where("id = ?", source.ID).Take(&unchanged).Error; err != nil || unchanged.ChatModelVersion != source.ChatModelVersion || string(unchanged.ChatModelSnapshot) != string(source.ChatModelSnapshot) {
				t.Fatalf("fork changed source model selection: %v", err)
			}
		})
	}
}

func TestForkThinkingDepthSettingsPersistForNextRequest(t *testing.T) {
	db, source, histories, request := forkFixture(t, 1)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	ctx := context.Background()
	result, err := createConversationFork(ctx, db, doc.DatasetCatalogCaller{UserID: "u1"}, source.ID, "depth-settings", request)
	if err != nil {
		t.Fatal(err)
	}
	id := result.Conversation["conversation_id"].(string)
	for _, test := range []struct {
		user string
		body string
		want int
	}{
		{user: "other-user", body: `{"thinking_depth":"low"}`, want: http.StatusNotFound},
		{user: "u1", body: `{"thinking_depth":"invalid"}`, want: http.StatusBadRequest},
		{user: "u1", body: `{"thinking_depth":"low"}`, want: http.StatusOK},
	} {
		recorder := httptest.NewRecorder()
		PatchConversationSettings(recorder, sidechatRequest(http.MethodPatch, "/api/core/conversations/"+id+"/settings", test.user, test.body, map[string]string{"conversation_id": id}))
		if recorder.Code != test.want {
			t.Fatalf("save depth status = %d, want %d: %s", recorder.Code, test.want, recorder.Body.String())
		}
		var branch orm.Conversation
		if err := db.Where("id = ?", id).Take(&branch).Error; err != nil {
			t.Fatal(err)
		}
		wantDepth := "high"
		if test.want == http.StatusOK {
			wantDepth = "low"
		}
		body := map[string]any{}
		applyForkRequestDefaults(body, branch, false)
		if branch.ThinkingDepth != wantDepth || resolveThinkingDepth(ctx, db, id, "u1", body) != wantDepth {
			t.Fatalf("saved depth not used by next request: stored=%s request=%v", branch.ThinkingDepth, body["thinking_depth"])
		}
	}
	var original orm.ChatHistory
	if err := db.Where("id = ?", histories[0].ID).Take(&original).Error; err != nil || forkConfigFromHistory(original).ThinkingDepth != "high" {
		t.Fatalf("saving branch depth changed source: %v", err)
	}
}
