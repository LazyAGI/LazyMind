package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/modelprovider"
	"lazymind/core/store"
)

func TestSidechatRejectsHistoricalFixedModelAfterConfigurationEdit(t *testing.T) {
	db := newSidechatTestDB(t)
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	model := seedAvailableChatModel(t, db, "user-1", "provider-a", "group-a", "model-a", "OpenAI", "OpenAI", "Model A", "llm", true, "synthetic-key-a")
	seedAvailableChatModel(t, db, "user-1", "provider-b", "group-b", "model-b", "OpenAI", "OpenAI", "Model B", "llm", true, "synthetic-key-b")
	seedSelectedChatModel(t, db, "user-1", "model-b", false)
	parent := sidechatTestConversation(t, db, "edited-model-parent", "user-1", "Parent")
	if err := db.Model(&parent).Updates(map[string]any{
		"chat_model_mode": chatModelModeFixed, "chat_model_id": "model-b", "chat_model_version": 1,
		"chat_model_snapshot": json.RawMessage(`{"model_id":"model-b","model_name":"Model B"}`),
	}).Error; err != nil {
		t.Fatal(err)
	}
	history := sidechatTestHistory(t, db, "successful-model-a", parent.ID, 1, "question", "answer", "completed")
	if err := db.Model(&history).Updates(map[string]any{
		"run_id":       "successful-run",
		"run_terminal": json.RawMessage(`{"status":"completed","reason":"normal","partial_output":false}`),
		"ext":          json.RawMessage(`{"model_route":{"mode":"auto","model_id":"model-a","provider_id":"provider-a","provider_name":"OpenAI","model_name":"Model A","source":"own"}}`),
	}).Error; err != nil {
		t.Fatal(err)
	}

	request := sidechatRequest(http.MethodPatch, "/model", "user-1", `{"model_type":"embed"}`, map[string]string{
		"model_provider_id": model.UserModelProviderID, "group_id": model.UserModelProviderGroupID, "model_id": model.ID,
	})
	recorder := httptest.NewRecorder()
	modelprovider.UpdateGroupModel(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("edit historical model: %d %s", recorder.Code, recorder.Body.String())
	}

	child, _, err := createSidechatConversation(context.Background(), db, "user-1", "user-1", parent.ID, createSidechatRequest{})
	if !errors.Is(err, errChatModelUnavailable) {
		t.Errorf("sidechat must reject the edited historical model as unavailable, got %v", err)
	}
	if child != nil {
		t.Errorf("unavailable historical model returned a child conversation: %s", child.ID)
	}
	var children int64
	if err := db.Model(&orm.Conversation{}).Where("parent_conversation_id = ?", parent.ID).Count(&children).Error; err != nil {
		t.Fatal(err)
	}
	if children != 0 {
		t.Fatalf("unavailable historical model persisted %d child conversations", children)
	}
}

func TestForkBindingsLockModelSupplierBeforePersisting(t *testing.T) {
	for _, test := range []struct {
		name        string
		mode        string
		replacement bool
	}{
		{name: "historical fixed model", mode: chatModelModeFixed},
		{name: "auto binding with model ID", mode: chatModelModeAuto},
		{name: "replacement fixed model", mode: chatModelModeFixed, replacement: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, source, _, request := forkFixture(t, 1)
			store.Init(db, db, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			modelID, providerID := "model-fork", "provider-fork"
			if test.mode == chatModelModeAuto {
				if err := db.Model(&source).Update("chat_model_mode", chatModelModeAuto).Error; err != nil {
					t.Fatal(err)
				}
			}
			if test.replacement {
				if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-fork").Update("is_verified", false).Error; err != nil {
					t.Fatal(err)
				}
				modelID, providerID = "replacement-model", "replacement-provider"
				seedAvailableChatModel(t, db, "u1", providerID, "replacement-group", modelID, "OpenAI", "OpenAI", "Replacement", "llm", true, "synthetic-key")
				request.ReplacementModel = &initialChatModelSelection{Mode: chatModelModeFixed, ModelID: modelID, Source: "own"}
			}

			// SQLite does not emit FOR UPDATE SQL. Inspect the real supplier query's
			// locking clause and its order relative to the new reference write.
			providerLocked, bindingCreated, lockedBeforeCreate := false, false, false
			const lockCallback = "test:fork-model-supplier-lock"
			if err := db.Callback().Query().After("gorm:query").Register(lockCallback, func(tx *gorm.DB) {
				if tx.Error != nil || tx.Statement.Table != "user_model_providers" {
					return
				}
				locking, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
				provider, matched := tx.Statement.Dest.(*orm.UserModelProvider)
				if ok && locking.Strength == "UPDATE" && matched && provider.ID == providerID {
					providerLocked = true
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Callback().Query().Remove(lockCallback) })
			const createCallback = "test:fork-model-reference-write"
			if err := db.Callback().Create().Before("gorm:create").Register(createCallback, func(tx *gorm.DB) {
				if tx.Statement.Table != "conversations" {
					return
				}
				branch, ok := tx.Statement.Dest.(*orm.Conversation)
				if ok && branch.ChatModelID != nil && *branch.ChatModelID == modelID {
					bindingCreated = true
					lockedBeforeCreate = providerLocked
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Callback().Create().Remove(createCallback) })

			result, err := createConversationFork(context.Background(), db, doc.DatasetCatalogCaller{UserID: "u1"}, source.ID, "supplier-locked-fork", request)
			if err != nil {
				t.Fatalf("create fork: %v", err)
			}
			var branch orm.Conversation
			if err := db.Where("id = ?", result.Conversation["conversation_id"]).Take(&branch).Error; err != nil {
				t.Fatal(err)
			}
			if branch.ChatModelMode == nil || *branch.ChatModelMode != test.mode || branch.ChatModelID == nil || *branch.ChatModelID != modelID {
				t.Fatalf("fork did not persist the requested binding: mode=%v model=%v", branch.ChatModelMode, branch.ChatModelID)
			}
			if !bindingCreated || !lockedBeforeCreate {
				t.Fatalf("fork persisted model %s without first locking supplier %s", modelID, providerID)
			}
		})
	}
}
