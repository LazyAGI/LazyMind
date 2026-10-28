package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func unclassifiedModelFixture(t *testing.T) (*gorm.DB, orm.UserModelProvider, orm.UserModelProviderGroup) {
	t.Helper()
	db, parent, group, _ := setupProbeTest(t, &orm.DefaultModel{}, &orm.UserSelectedModel{}, &orm.UserSelectedCloudModel{}, &orm.Conversation{})
	parent.Capabilities = "has_models,custom_base_url"
	if err := db.Save(&parent).Error; err != nil {
		t.Fatal(err)
	}
	return db, parent, group
}

func unclassifiedModelCall(t *testing.T, handler http.HandlerFunc, parent orm.UserModelProvider, group orm.UserModelProviderGroup, modelID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/models", strings.NewReader(body))
	req.Header.Set("X-User-Id", parent.CreateUserID)
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID, "model_id": modelID})
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func addedUnclassifiedModel(t *testing.T, parent orm.UserModelProvider, group orm.UserModelProviderGroup, context string) addGroupModelResponse {
	t.Helper()
	body := map[string]any{"name": "unlisted-model", "model_type": "", "vision": true}
	if context != "" {
		body["max_input_tokens"] = context
	}
	raw, _ := json.Marshal(body)
	rec := unclassifiedModelCall(t, AddGroupModel, parent, group, "", string(raw))
	if rec.Code != http.StatusOK {
		t.Fatalf("unclassified model save=%d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data addGroupModelResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.ID == "" || response.Data.ModelType != "" || response.Data.Vision {
		t.Fatalf("unclassified identity/capability was guessed: %s", rec.Body.String())
	}
	return response.Data
}

func TestUnclassifiedModelSavesOnlyExplicitContext(t *testing.T) {
	for _, context := range []string{"", "262144", "131073"} {
		t.Run("context="+context, func(t *testing.T) {
			db, parent, group := unclassifiedModelFixture(t)
			added := addedUnclassifiedModel(t, parent, group, context)
			var saved orm.UserModelProviderGroupModel
			if err := db.Take(&saved, "id = ?", added.ID).Error; err != nil {
				t.Fatal(err)
			}
			if saved.ModelType != "" || tokenLimitString(saved.MaxInputTokens) != context || saved.Vision {
				t.Fatalf("explicit context was lost or inferred: %+v", saved)
			}
			rec := unclassifiedModelCall(t, ListGroupModels, parent, group, "", "")
			var response struct {
				Data groupModelListResponse `json:"data"`
			}
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil {
				t.Fatal(rec.Body.String())
			}
			for _, item := range response.Data.Models {
				if item.ID == saved.ID {
					if item.ModelType != "" || item.Availability != "unavailable" || tokenLimitString(item.MaxInputTokens) != context {
						t.Fatalf("unknown inventory advertised runtime availability: %+v", item)
					}
					return
				}
			}
			t.Fatal("saved unclassified model missing from inventory")
		})
	}
}

func TestUnclassifiedModelEditsPreserveContextUntilExplicitlyChanged(t *testing.T) {
	for _, target := range []string{"", "llm", "vlm"} {
		t.Run("type="+target, func(t *testing.T) {
			db, parent, group := unclassifiedModelFixture(t)
			context := "262144"
			model := orm.UserModelProviderGroupModel{ID: "unknown", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "OpenAI", Name: "unlisted-model", ModelType: "", MaxInputTokens: &context, BaseModel: parent.BaseModel}
			if err := db.Create(&model).Error; err != nil {
				t.Fatal(err)
			}
			body := `{"name":"renamed-model","model_type":"` + target + `"}`
			rec := unclassifiedModelCall(t, UpdateGroupModel, parent, group, model.ID, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("edit=%d %s", rec.Code, rec.Body.String())
			}
			if err := db.Take(&model, "id = ?", model.ID).Error; err != nil {
				t.Fatal(err)
			}
			if model.Name != "renamed-model" || model.ModelType != target || tokenLimitString(model.MaxInputTokens) != context {
				t.Fatalf("classification changed explicit context: %+v", model)
			}
			if target == "" {
				rec = unclassifiedModelCall(t, UpdateGroupModel, parent, group, model.ID, `{"max_input_tokens":"131073"}`)
				if rec.Code != http.StatusOK {
					t.Fatalf("context edit=%d %s", rec.Code, rec.Body.String())
				}
				var changed orm.UserModelProviderGroupModel
				if err := db.Take(&changed, "id = ?", model.ID).Error; err != nil || tokenLimitString(changed.MaxInputTokens) != "131073" {
					t.Fatalf("explicit unknown context was not retained: %+v %v", changed, err)
				}
				rec = unclassifiedModelCall(t, UpdateGroupModel, parent, group, model.ID, `{"model_type":"","max_input_tokens":""}`)
				if rec.Code != http.StatusOK {
					t.Fatalf("clear=%d %s", rec.Code, rec.Body.String())
				}
				var cleared orm.UserModelProviderGroupModel
				if err := db.Take(&cleared, "id = ?", model.ID).Error; err != nil || cleared.MaxInputTokens != nil {
					t.Fatalf("unknown context did not clear without a default: %+v %v", cleared, err)
				}
			}
		})
	}
}

func TestUnclassifiedModelRejectsInvalidMetadataAndKeepsReferenceProtection(t *testing.T) {
	for _, body := range []string{
		`{"name":"invalid","model_type":"made-up-type"}`,
		`{"name":"invalid","model_type":"","max_input_tokens":"0"}`,
		`{"name":"invalid","model_type":"","max_input_tokens":"1.5K"}`,
		`{"name":"invalid","model_type":"","input_token_limit":"1024"}`,
		`{"name":"invalid","model_type":"","max_output_tokens":"1024"}`,
	} {
		t.Run(body, func(t *testing.T) {
			_, parent, group := unclassifiedModelFixture(t)
			rec := unclassifiedModelCall(t, AddGroupModel, parent, group, "", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("invalid metadata saved=%d %s", rec.Code, rec.Body.String())
			}
		})
	}
	db, parent, group := unclassifiedModelFixture(t)
	model := orm.UserModelProviderGroupModel{ID: "referenced", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "OpenAI", Name: "bound-model", ModelType: "llm", BaseModel: parent.BaseModel}
	if err := db.Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserSelectedModel{UserID: parent.CreateUserID, ModelKey: "llm", UserModelProviderGroupModelID: model.ID}).Error; err != nil {
		t.Fatal(err)
	}
	rec := unclassifiedModelCall(t, UpdateGroupModel, parent, group, model.ID, `{"model_type":""}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("default binding lost its type guard=%d %s", rec.Code, rec.Body.String())
	}
}

func TestUnclassifiedModelPatchValidatesContextAndDoesNotInferMissingWindow(t *testing.T) {
	for _, context := range []string{"0", "1.5K", "not-a-token-count", "-262144"} {
		t.Run(context, func(t *testing.T) {
			db, parent, group := unclassifiedModelFixture(t)
			model := orm.UserModelProviderGroupModel{ID: "unknown", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "OpenAI", Name: "unlisted-model", ModelType: "", BaseModel: parent.BaseModel}
			if err := db.Create(&model).Error; err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]string{"max_input_tokens": context})
			rec := unclassifiedModelCall(t, UpdateGroupModel, parent, group, model.ID, string(raw))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("invalid unknown context saved=%d %s", rec.Code, rec.Body.String())
			}
		})
	}
	db, parent, group := unclassifiedModelFixture(t)
	model := orm.UserModelProviderGroupModel{ID: "unknown", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "OpenAI", Name: "unlisted-model", ModelType: "", BaseModel: parent.BaseModel}
	if err := db.Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	if err := LoadContextWindows("../config/model_context_windows.yaml"); err != nil {
		t.Fatal(err)
	}
	rec := unclassifiedModelCall(t, UpdateGroupModel, parent, group, model.ID, `{"name":"qwen-plus","model_type":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unknown rename=%d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data groupModelListItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.Data.MaxInputTokens != nil || response.Data.Availability != "unavailable" {
		t.Fatalf("rename inferred a catalog window or enabled an unknown runtime: %s %v", rec.Body.String(), err)
	}
}

func TestUnclassifiedModelCannotRunOrBecomeDefault(t *testing.T) {
	db, parent, group := unclassifiedModelFixture(t)
	model := orm.UserModelProviderGroupModel{ID: "unknown", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "OpenAI", Name: "unlisted-model", ModelType: "", BaseModel: parent.BaseModel}
	if err := db.Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"OpenAI", "vLLM", "Claude", "OpenRouter"} {
		if RuntimeSupportsModelType(provider, "") || RuntimeSupportsModelType(provider, " \t ") {
			t.Errorf("%s advertised an unclassified runtime", provider)
		}
	}
	var calls atomic.Int32
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	rec := unclassifiedModelCall(t, ProbeGroupModel, parent, group, model.ID, "")
	result := decodeProbeTest(t, rec)
	if rec.Code != http.StatusOK || result.Code != "unknown_type" || calls.Load() != 0 {
		t.Fatalf("unclassified model reached inference: %s calls=%d", rec.Body.String(), calls.Load())
	}
	rec = unclassifiedModelCall(t, SetSelectedModels, parent, group, "", `{"selections":[{"model_key":"llm","model_id":"unknown","source":"own"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unclassified model became default=%d %s", rec.Code, rec.Body.String())
	}
	var count int64
	if err := db.Model(&orm.UserSelectedModel{}).Where("user_id = ?", parent.CreateUserID).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("invalid default persisted", err)
	}
}
