package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func seedGroupModelFixture(t *testing.T) {
	t.Helper()
	db := setupListProviderTestDB(t)
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	provider := orm.UserModelProvider{
		ID:                     "provider-openai",
		DefaultModelProviderID: "default-openai",
		Name:                   "OpenAI",
		Description:            "OpenAI provider",
		BaseURL:                "https://api.openai.com/v1/",
		Category:               "model",
		Capabilities:           "multi_group,custom_base_url,has_models",
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
	group := orm.UserModelProviderGroup{
		ID:                  "group-openai",
		UserModelProviderID: provider.ID,
		Name:                "OpenAI",
		BaseURL:             provider.BaseURL,
		APIKey:              "secret",
		IsVerified:          true,
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
}

func TestAddGroupModelDefaultsLLMMaxInputTokens(t *testing.T) {
	seedGroupModelFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/model_providers/provider-openai/groups/group-openai/models", strings.NewReader(`{"name":"custom-llm","model_type":"llm"}`))
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{
		"model_provider_id": "provider-openai",
		"group_id":          "group-openai",
	})
	rec := httptest.NewRecorder()
	AddGroupModel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Data addGroupModelResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.MaxInputTokens == nil || *payload.Data.MaxInputTokens != defaultLLMMaxInputTokens {
		t.Fatalf("max_input_tokens = %v, want %s", payload.Data.MaxInputTokens, defaultLLMMaxInputTokens)
	}

	var stored orm.UserModelProviderGroupModel
	if err := store.DB().Take(&stored, "id = ?", payload.Data.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.MaxInputTokens == nil || *stored.MaxInputTokens != defaultLLMMaxInputTokens {
		t.Fatalf("stored max_input_tokens = %v, want %s", stored.MaxInputTokens, defaultLLMMaxInputTokens)
	}
}

func TestAddGroupModelRejectsMaxInputTokensForEmbed(t *testing.T) {
	seedGroupModelFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/model_providers/provider-openai/groups/group-openai/models", strings.NewReader(`{"name":"custom-embed","model_type":"embed","max_input_tokens":"8K"}`))
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{
		"model_provider_id": "provider-openai",
		"group_id":          "group-openai",
	})
	rec := httptest.NewRecorder()
	AddGroupModel(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateGroupModelMaxInputTokens(t *testing.T) {
	seedGroupModelFixture(t)

	now := time.Now().UTC()
	tokens := "128K"
	row := orm.UserModelProviderGroupModel{
		ID:                       "model-llm",
		UserModelProviderID:      "provider-openai",
		UserModelProviderGroupID: "group-openai",
		ProviderName:             "OpenAI",
		Name:                     "gpt-test",
		ModelType:                "llm",
		MaxInputTokens:           &tokens,
		IsDefault:                false,
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
	if err := store.DB().Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/model_providers/provider-openai/groups/group-openai/models/model-llm", strings.NewReader(`{"max_input_tokens":"1m"}`))
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{
		"model_provider_id": "provider-openai",
		"group_id":          "group-openai",
		"model_id":          "model-llm",
	})
	rec := httptest.NewRecorder()
	UpdateGroupModel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var stored orm.UserModelProviderGroupModel
	if err := store.DB().Take(&stored, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.MaxInputTokens == nil || *stored.MaxInputTokens != "1M" {
		t.Fatalf("stored max_input_tokens = %v, want 1M", stored.MaxInputTokens)
	}
}

func TestUpdateGroupModelRejectsMissingMaxInputTokens(t *testing.T) {
	seedGroupModelFixture(t)

	now := time.Now().UTC()
	tokens := "128K"
	row := orm.UserModelProviderGroupModel{
		ID:                       "model-llm-required",
		UserModelProviderID:      "provider-openai",
		UserModelProviderGroupID: "group-openai",
		ProviderName:             "OpenAI",
		Name:                     "gpt-custom",
		ModelType:                "llm",
		MaxInputTokens:           &tokens,
		IsDefault:                false,
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
	if err := store.DB().Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/model_providers/provider-openai/groups/group-openai/models/model-llm-required", strings.NewReader(`{}`))
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{
		"model_provider_id": "provider-openai",
		"group_id":          "group-openai",
		"model_id":          "model-llm-required",
	})
	rec := httptest.NewRecorder()
	UpdateGroupModel(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var stored orm.UserModelProviderGroupModel
	if err := store.DB().Take(&stored, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.MaxInputTokens == nil || *stored.MaxInputTokens != "128K" {
		t.Fatalf("stored max_input_tokens = %v, want 128K", stored.MaxInputTokens)
	}
}

func TestUpdateGroupModelRejectsCatalogMaxInputTokens(t *testing.T) {
	seedGroupModelFixture(t)

	now := time.Now().UTC()
	tokens := "128K"
	row := orm.UserModelProviderGroupModel{
		ID:                       "model-llm-catalog",
		UserModelProviderID:      "provider-openai",
		UserModelProviderGroupID: "group-openai",
		ProviderName:             "OpenAI",
		Name:                     "gpt-catalog",
		ModelType:                "llm",
		MaxInputTokens:           &tokens,
		IsDefault:                true,
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
	if err := store.DB().Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/model_providers/provider-openai/groups/group-openai/models/model-llm-catalog", strings.NewReader(`{"max_input_tokens":"1m"}`))
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{
		"model_provider_id": "provider-openai",
		"group_id":          "group-openai",
		"model_id":          "model-llm-catalog",
	})
	rec := httptest.NewRecorder()
	UpdateGroupModel(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
