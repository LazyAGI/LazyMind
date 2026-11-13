package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func TestRemoveKeyClearsBuiltinSupplierModels(t *testing.T) {
	for _, tc := range []struct {
		name        string
		custom      bool
		keys        string
		target      string
		wantStatus  int
		wantRemoved bool
	}{
		{"Qwen", false, "key-one", "key-one", http.StatusOK, true},
		{"OpenAI", false, "key-one\nkey-two", "key-two", http.StatusOK, true},
		{"vLLM", false, "key-one\nkey-two", "key-two", http.StatusOK, false},
		{"LMDeploy", false, "key-one\nkey-two", "key-two", http.StatusOK, false},
		{"Custom", true, "key-one\nkey-two", "key-two", http.StatusOK, false},
		{"Missing", false, "key-one", "missing", http.StatusNotFound, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, parent, group := setupEncryptedGroupKeyTest(t, tc.keys)
			if err := db.AutoMigrate(&orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}, &orm.EmbeddingIndexState{}); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&parent).Updates(map[string]any{"name": tc.name, "is_custom": tc.custom, "protocol_name": "OpenAI"}).Error; err != nil {
				t.Fatal(err)
			}
			// Models in every connection of this supplier must be removed, but never
			// models owned by another supplier or account.
			models := []orm.UserModelProviderGroupModel{
				{ID: "chat", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ModelType: "llm", BaseModel: group.BaseModel},
				{ID: "embed", UserModelProviderID: parent.ID, UserModelProviderGroupID: "other-group", ModelType: "embed", BaseModel: group.BaseModel},
				{ID: "other-provider", UserModelProviderID: "other-provider", UserModelProviderGroupID: "other", ModelType: "llm", BaseModel: group.BaseModel},
				{ID: "other-user", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ModelType: "llm", BaseModel: orm.BaseModel{CreateUserID: "other-user"}},
			}
			for _, model := range models {
				model.Name = model.ID
				if err := db.Create(&model).Error; err != nil {
					t.Fatal(err)
				}
			}
			selection := orm.UserSelectedModel{UserModelProviderGroupModelID: "chat", ModelKey: "llm", UserID: parent.CreateUserID}
			if err := db.Create(&selection).Error; err != nil {
				t.Fatal(err)
			}
			state := orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveSource: "own", ActiveModelID: "embed", Signature: "retained-vectors", Phase: "ready", Generation: 3}
			if err := db.Create(&state).Error; err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(removeKeyRequest{KeyID: groupKeyID(group.ID, tc.target)})
			req := httptest.NewRequest(http.MethodDelete, "/keys", strings.NewReader(string(body)))
			req.Header.Set("X-User-Id", parent.CreateUserID)
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
			rec := httptest.NewRecorder()
			RemoveKey(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
			}
			for _, model := range models {
				var stored orm.UserModelProviderGroupModel
				if err := db.Take(&stored, "id = ?", model.ID).Error; err != nil {
					t.Fatal(err)
				}
				wantDeleted := tc.wantRemoved && (model.ID == "chat" || model.ID == "embed")
				if (stored.DeletedAt != nil) != wantDeleted {
					t.Errorf("model %s deleted=%v, want %v", model.ID, stored.DeletedAt != nil, wantDeleted)
				}
			}
			var count int64
			if err := db.Model(&orm.UserSelectedModel{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if (count == 0) != tc.wantRemoved {
				t.Errorf("selected model count=%d", count)
			}
			if err := db.Take(&state, "scope = ?", globalEmbeddingScope).Error; err != nil {
				t.Fatal(err)
			}
			if tc.wantRemoved {
				if state.Phase != "detached" || state.ActiveModelID != "" || state.Generation != 4 || state.Signature != "retained-vectors" {
					t.Errorf("embedding state=%+v", state)
				}
			} else if state.Phase != "ready" || state.ActiveModelID != "embed" || state.Generation != 3 {
				t.Errorf("embedding state changed=%+v", state)
			}
		})
	}
}

func TestRemoveKeyRollsBackWhenModelCleanupFails(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "key-one\nkey-two")
	if err := db.AutoMigrate(&orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}, &orm.EmbeddingIndexState{}); err != nil {
		t.Fatal(err)
	}
	model := orm.UserModelProviderGroupModel{ID: "embed", Name: "embed", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ModelType: "embed", BaseModel: group.BaseModel}
	if err := db.Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	selection := orm.UserSelectedModel{UserID: parent.CreateUserID, ModelKey: "embed_main", UserModelProviderGroupModelID: model.ID}
	if err := db.Create(&selection).Error; err != nil {
		t.Fatal(err)
	}
	state := orm.EmbeddingIndexState{Scope: globalEmbeddingScope, ActiveSource: "own", ActiveModelID: model.ID, Signature: "retained-vectors", Phase: "ready", Generation: 3}
	if err := db.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	if err := prepareProviderKeySelection(t.Context(), db, &parent); err != nil {
		t.Fatal(err)
	}
	// Fail after detaching the embedding state and removing its selection, so
	// the transaction must restore those changes as well as the encrypted key.
	if err := db.Exec(`CREATE TRIGGER fail_model_cleanup BEFORE UPDATE OF deleted_at ON user_model_provider_group_models BEGIN SELECT RAISE(ABORT, 'cleanup failed'); END`).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/keys", strings.NewReader(`{"api_key":"key-one"}`))
	req.Header.Set("X-User-Id", parent.CreateUserID)
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	rec := httptest.NewRecorder()
	RemoveKey(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
	}
	assertStoredEncryptedAPIKeys(t, db, group.ID, "key-one\nkey-two", true)
	if err := db.Take(&model, "id = ?", model.ID).Error; err != nil {
		t.Fatal(err)
	}
	if model.DeletedAt != nil {
		t.Fatal("model removal was not rolled back")
	}
	var count int64
	if err := db.Model(&orm.UserSelectedModel{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("selection count=%d err=%v", count, err)
	}
	if err := db.Take(&state, "scope = ?", globalEmbeddingScope).Error; err != nil {
		t.Fatal(err)
	}
	if state.Phase != "ready" || state.ActiveModelID != model.ID || state.Generation != 3 {
		t.Fatalf("embedding state was not rolled back: %+v", state)
	}
	var stored orm.UserModelProvider
	if err := db.Take(&stored, "id = ?", parent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ActiveKeyID != parent.ActiveKeyID || stored.ActiveKeyRevision != parent.ActiveKeyRevision {
		t.Fatal("active key changed on failed deletion")
	}
}

func TestRemoveLastKeyPreservesExemptSupplierModels(t *testing.T) {
	for _, name := range []string{"vLLM", "LMDeploy", "Custom"} {
		t.Run(name, func(t *testing.T) {
			db, parent, group := setupEncryptedGroupKeyTest(t, "key-one")
			if err := db.AutoMigrate(&orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&parent).Updates(map[string]any{"name": name, "is_custom": name == "Custom", "protocol_name": "OpenAI"}).Error; err != nil {
				t.Fatal(err)
			}
			model := orm.UserModelProviderGroupModel{ID: "chat", Name: "chat", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ModelType: "llm", BaseModel: group.BaseModel}
			if err := db.Create(&model).Error; err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodDelete, "/keys", strings.NewReader(`{"api_key":"key-one"}`))
			req.Header.Set("X-User-Id", parent.CreateUserID)
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
			rec := httptest.NewRecorder()
			RemoveKey(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
			}
			assertStoredEncryptedAPIKeys(t, db, group.ID, "", false)
			if err := db.Take(&model, "id = ?", model.ID).Error; err != nil {
				t.Fatal(err)
			}
			if model.DeletedAt != nil {
				t.Fatal("exempt supplier model was removed")
			}
		})
	}
}
