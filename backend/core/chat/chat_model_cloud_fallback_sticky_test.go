package chat

import (
	"encoding/json"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/modelprovider"
)

func TestAutoChatModelKeepsSuccessfulCloudFallback(t *testing.T) {
	for _, scenario := range []string{"next_round", "new_local_default", "cloud_unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			db := newPromptTestDB(t).DB
			installChatCloudCatalog(t)
			seedAvailableChatModel(t, db, "user-1", "provider-a", "group-a", "model-a", "OpenAI", "A", "gpt-a", "llm", true, "fixture-a")
			seedSelectedChatModel(t, db, "user-1", "model-a", false)
			const cloudID = "lazymind-text-default"
			if err := db.Create(&orm.UserSelectedCloudModel{
				UserID: "user-1", ModelType: "llm", PublicModelKey: cloudID, DisplayNameSnapshot: "Cloud B",
			}).Error; err != nil {
				t.Fatal(err)
			}
			conversation, _, err := ensureConversation(t.Context(), db, "cloud-sticky", "Cloud sticky", nil, nil, "user-1", "User", false, "", nil, &initialChatModelSelection{Mode: "auto"})
			if err != nil {
				t.Fatal(err)
			}
			body := map[string]any{"conversation_id": conversation.ID, "user_id": "user-1", "run_id": "cloud-success"}
			if err := applyConversationChatModelConfig(t.Context(), db, "user-1", body); err != nil {
				t.Fatal(err)
			}
			state := body[chatModelFallbackBodyKey].(*chatFallbackState)
			if state.currentID != "own:model-a" || state.candidate("cloud:"+cloudID) == nil {
				t.Fatal("initial Auto selection must prefer the local model and retain the enabled Cloud alternative")
			}
			var ext json.RawMessage
			event := fallbackWireEvent("cloud-success", "switch-cloud", RuntimeEventModelSwitched, ModelSwitchedData{
				SwitchIndex: 1, From: state.identity("own:model-a"), To: state.identity("cloud:" + cloudID), ReasonCode: "not_found",
			})
			if accepted, err := acceptChatFallbackEvent(body, event, "cloud-success", &ext); err != nil || !accepted {
				t.Fatalf("accept Cloud success: accepted=%t err=%v", accepted, err)
			}
			if err := persistChatFallbackProgress(t.Context(), db, conversation.ID, "cloud-history", "cloud-success", "hello", "", "Cloud B completed", 1, ext, nil, body); err != nil {
				t.Fatal(err)
			}
			terminal := &RunTerminal{Status: "completed", Reason: "normal"}
			if err := db.Model(&orm.ChatHistory{}).Where("id = ?", "cloud-history").Updates(map[string]any{
				"run_status": "completed", "run_terminal": terminalJSON(terminal),
			}).Error; err != nil {
				t.Fatal(err)
			}
			persistSuccessfulChatModel(t.Context(), db, "user-1", conversation.ID, "cloud-success", body, terminal)
			var stored orm.Conversation
			if err := db.Where("id = ?", conversation.ID).Take(&stored).Error; err != nil {
				t.Fatal(err)
			}
			var snapshot chatModelSnapshot
			if err := json.Unmarshal(stored.ChatModelSnapshot, &snapshot); err != nil || snapshot.Source != "cloud" || snapshot.SuccessfulRunID != "cloud-success" {
				t.Fatal("successful Cloud fallback was not persisted before the next request")
			}
			if scenario == "new_local_default" {
				seedAvailableChatModel(t, db, "user-1", "provider-c", "group-c", "model-c", "OpenAI", "C", "gpt-c", "llm", true, "fixture-c")
				if err := db.Model(&orm.UserSelectedModel{}).Where("user_id = ?", "user-1").Update("user_model_provider_group_model_id", "model-c").Error; err != nil {
					t.Fatal(err)
				}
			} else if scenario == "cloud_unavailable" {
				modelprovider.SetCloudCatalogProvider(staticChatCloudCatalog{catalog: modelprovider.CloudModelCatalog{Known: true}})
			}
			next := map[string]any{"conversation_id": conversation.ID, "user_id": "user-1", "run_id": "next-round"}
			if err := applyConversationChatModelConfig(t.Context(), db, "user-1", next); err != nil {
				t.Fatal(err)
			}
			nextState := next[chatModelFallbackBodyKey].(*chatFallbackState)
			if scenario == "cloud_unavailable" {
				if nextState.currentID != "own:model-a" || nextState.candidate("cloud:"+cloudID) != nil {
					t.Fatal("a Cloud model no longer in the authorized catalog must not remain an execution candidate")
				}
				return
			}
			route := chatModelRouteFromBody(next)
			llm := llmConfigFromBody(next)["llm"].(map[string]any)
			if nextState.currentID != "cloud:"+cloudID || route.Source != "cloud" || route.ModelID != cloudID || route.Mode != "auto" || route.Reason != "session_sticky" || llm["model"] != cloudID {
				t.Fatalf("next request did not retain successful Cloud B: current=%q route=%#v model=%v", nextState.currentID, route, llm["model"])
			}
		})
	}
}

func TestAutoChatModelDoesNotEnableAnUnselectedCloudFallback(t *testing.T) {
	db := newPromptTestDB(t).DB
	installChatCloudCatalog(t)
	seedAvailableChatModel(t, db, "user-1", "provider-a", "group-a", "model-a", "OpenAI", "A", "gpt-a", "llm", true, "fixture-a")
	seedSelectedChatModel(t, db, "user-1", "model-a", false)
	conversation, _, err := ensureConversation(t.Context(), db, "cloud-not-enabled", "No Cloud selection", nil, nil, "user-1", "User", false, "", nil, &initialChatModelSelection{Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"conversation_id": conversation.ID, "user_id": "user-1"}
	if err := applyConversationChatModelConfig(t.Context(), db, "user-1", body); err != nil {
		t.Fatal(err)
	}
	state := body[chatModelFallbackBodyKey].(*chatFallbackState)
	if state.currentID != "own:model-a" || state.candidate("cloud:lazymind-text-default") != nil {
		t.Fatal("Cloud catalog visibility alone must not enable a fallback model")
	}
	invoked := true
	if err := db.Create(&orm.ChatHistory{
		ID: "failed-own-history", ConversationID: conversation.ID, RunID: "failed-own-run", Seq: 1,
		RunStatus: "failed", Ext: mergeChatModelRouteIntoExt(nil, body),
		RunTerminal: terminalJSON(&RunTerminal{Status: "failed", Reason: "model_failure", Code: "service_unavailable", ModelInvoked: &invoked}),
	}).Error; err != nil {
		t.Fatal(err)
	}
	next := map[string]any{"conversation_id": conversation.ID, "user_id": "user-1"}
	if err := applyConversationChatModelConfig(t.Context(), db, "user-1", next); err != nil {
		t.Fatal(err)
	}
	nextState := next[chatModelFallbackBodyKey].(*chatFallbackState)
	if nextState.currentID != "own:model-a" || nextState.candidate("cloud:lazymind-text-default") != nil {
		t.Fatal("a previous local-model failure must not enable an unselected Cloud model")
	}
}
