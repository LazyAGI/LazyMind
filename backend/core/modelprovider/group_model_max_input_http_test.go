package modelprovider

import (
	"context"
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
	if payload.Data.MaxInputTokens == nil || *payload.Data.MaxInputTokens != DefaultLLMMaxInputTokens {
		t.Fatalf("max_input_tokens = %v, want %s", payload.Data.MaxInputTokens, DefaultLLMMaxInputTokens)
	}

	var stored orm.UserModelProviderGroupModel
	if err := store.DB().Take(&stored, "id = ?", payload.Data.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.MaxInputTokens == nil || *stored.MaxInputTokens != DefaultLLMMaxInputTokens {
		t.Fatalf("stored max_input_tokens = %v, want %s", stored.MaxInputTokens, DefaultLLMMaxInputTokens)
	}
}

func TestAddGroupModelPrefersRequestMaxInputTokensOverContextWindows(t *testing.T) {
	seedGroupModelFixture(t)
	if err := LoadContextWindows("../config/model_context_windows.yaml"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/model_providers/provider-openai/groups/group-openai/models", strings.NewReader(`{"name":"qwen-plus","model_type":"llm","max_input_tokens":"8K"}`))
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
	if payload.Data.MaxInputTokens == nil || *payload.Data.MaxInputTokens != "8K" {
		t.Fatalf("max_input_tokens = %v, want 8K from request", payload.Data.MaxInputTokens)
	}
}

func TestAddGroupModelKeepsExplicitMaxInputTokens(t *testing.T) {
	seedGroupModelFixture(t)
	if err := LoadContextWindows("../config/model_context_windows.yaml"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/model_providers/provider-openai/groups/group-openai/models", strings.NewReader(`{"name":"qwen-plus","model_type":"llm","max_input_tokens":"128K"}`))
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
	if payload.Data.MaxInputTokens == nil || *payload.Data.MaxInputTokens != "128K" {
		t.Fatalf("max_input_tokens = %v, want explicit 128K", payload.Data.MaxInputTokens)
	}
}

func TestAddGroupModelLooksUpContextWindowsWhenBodyOmitsTokens(t *testing.T) {
	seedGroupModelFixture(t)
	if err := LoadContextWindows("../config/model_context_windows.yaml"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/model_providers/provider-openai/groups/group-openai/models", strings.NewReader(`{"name":"qwen-plus","model_type":"llm"}`))
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
	if payload.Data.MaxInputTokens == nil || *payload.Data.MaxInputTokens != "1M" {
		t.Fatalf("max_input_tokens = %v, want 1M from context windows", payload.Data.MaxInputTokens)
	}
}

func TestAddGroupModelIgnoresMaxInputTokensForEmbed(t *testing.T) {
	seedGroupModelFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/model_providers/provider-openai/groups/group-openai/models", strings.NewReader(`{"name":"custom-embed","model_type":"embed","max_input_tokens":"8K"}`))
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
	if payload.Data.MaxInputTokens != nil {
		t.Fatalf("embed max_input_tokens = %v, want nil", payload.Data.MaxInputTokens)
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

func TestAddGroupModelVisionRoundTrip(t *testing.T) {
	seedGroupModelFixture(t)
	for _, item := range []struct {
		name, kind   string
		vision, want bool
	}{
		{"visual", "llm", true, true}, {"text", "llm", false, false}, {"embed", "embed", true, false},
	} {
		body, _ := json.Marshal(map[string]any{"name": item.name, "model_type": item.kind, "vision": item.vision})
		req := httptest.NewRequest(http.MethodPost, "/models", strings.NewReader(string(body)))
		req.Header.Set("X-User-Id", "user-1")
		req = mux.SetURLVars(req, map[string]string{"model_provider_id": "provider-openai", "group_id": "group-openai"})
		rec := httptest.NewRecorder()
		AddGroupModel(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("add: %s", rec.Body.String())
		}
		var payload struct {
			Data addGroupModelResponse `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		var stored orm.UserModelProviderGroupModel
		if err := store.DB().Take(&stored, "id = ?", payload.Data.ID).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Vision != item.want || payload.Data.Vision != item.want {
			t.Fatalf("vision lost or incorrectly applied: %+v", item)
		}
	}
}

func TestVisionLLMReadinessRespectsOwnSelection(t *testing.T) {
	seedGroupModelFixture(t)
	db := store.DB()
	if err := db.AutoMigrate(&orm.UserSelectedModel{}); err != nil {
		t.Fatal(err)
	}
	model := orm.UserModelProviderGroupModel{ID: "own-visual", UserModelProviderID: "provider-openai", UserModelProviderGroupID: "group-openai", Name: "visual", ModelType: "llm", Vision: true, BaseModel: orm.BaseModel{CreateUserID: "user-1"}}
	if err := db.Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	selection := orm.UserSelectedModel{UserID: "user-1", ModelKey: "llm", UserModelProviderGroupModelID: model.ID}
	if err := db.Create(&selection).Error; err != nil {
		t.Fatal(err)
	}
	ready, err := hasVisionLLMSelection(context.Background(), db, "user-1")
	if err != nil || !ready {
		t.Fatalf("visual LLM not ready: %v", err)
	}
	if err := db.Model(&model).Update("vision", false).Error; err != nil {
		t.Fatal(err)
	}
	ready, err = hasVisionLLMSelection(context.Background(), db, "user-1")
	if err != nil || ready {
		t.Fatalf("text-only LLM considered visual: %v", err)
	}
}
