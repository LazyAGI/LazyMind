package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestCompatibleDBModelTypes(t *testing.T) {
	tests := []struct {
		name      string
		modelType string
		want      []string
	}{
		{
			name:      "cross-modal embedding includes legacy aliases",
			modelType: "cross_modal_embed",
			want:      []string{"cross_modal_embed", "multimodal_embedding", "embed_image"},
		},
		{
			name:      "evo includes text and vision chat models",
			modelType: "evo_llm",
			want:      []string{"llm", "vlm"},
		},
		{
			name:      "legacy multimodal embedding includes current aliases",
			modelType: "multimodal_embedding",
			want:      []string{"cross_modal_embed", "multimodal_embedding", "embed_image"},
		},
		{
			name:      "runtime image embedding includes persisted aliases",
			modelType: "embed_image",
			want:      []string{"cross_modal_embed", "multimodal_embedding", "embed_image"},
		},
		{
			name:      "other model types remain exact",
			modelType: "llm",
			want:      []string{"llm"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compatibleDBModelTypes(tt.modelType); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("compatibleDBModelTypes(%q) = %v, want %v", tt.modelType, got, tt.want)
			}
		})
	}
}

func TestListUserModelsWithoutTypeReturnsAnyVerifiedModel(t *testing.T) {
	db := setupListProviderTestDB(t)
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	provider := orm.UserModelProvider{
		ID:                     "provider-openai",
		DefaultModelProviderID: "default-openai",
		Name:                   "OpenAI",
		Description:            "OpenAI provider",
		BaseURL:                "https://api.openai.com/v1",
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
	model := orm.UserModelProviderGroupModel{
		ID:                       "model-multimodal",
		UserModelProviderID:      provider.ID,
		UserModelProviderGroupID: group.ID,
		ProviderName:             provider.Name,
		Name:                     "multimodal-model",
		ModelType:                "multimodal_embedding",
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
	if err := db.Create(&model).Error; err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/model_providers/models", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ListUserModelsByModelType(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Data groupModelListResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.Models) != 1 || payload.Data.Models[0].ID != model.ID {
		t.Fatalf("expected the verified non-LLM model, got %#v", payload.Data.Models)
	}
}

func TestListUserModelsForEvoIncludesCustomChatModel(t *testing.T) {
	db := setupListProviderTestDB(t)
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	now := time.Now().UTC()
	provider := orm.UserModelProvider{
		ID: "provider-openai", DefaultModelProviderID: "default-openai",
		Name: "OpenAI", Description: "OpenAI provider",
		BaseURL: "https://gateway.example.com/v1", Category: "model",
		Capabilities: "multi_group,custom_base_url,has_models",
		BaseModel:    orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}
	group := orm.UserModelProviderGroup{
		ID: "group-openai", UserModelProviderID: provider.ID,
		Name: "Custom gateway", BaseURL: provider.BaseURL, APIKey: "secret", IsVerified: true,
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}
	models := []orm.UserModelProviderGroupModel{
		{
			ID: "custom-chat", UserModelProviderID: provider.ID,
			UserModelProviderGroupID: group.ID, ProviderName: provider.Name,
			Name: "deepseek-v4-flash", ModelType: "llm", IsDefault: false,
			BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
		},
		{
			ID: "unsupported-builtin", UserModelProviderID: provider.ID,
			UserModelProviderGroupID: group.ID, ProviderName: provider.Name,
			Name: "unsupported-built-in", ModelType: "llm", IsDefault: true,
			BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
		},
	}
	for _, row := range []any{&provider, &group, &models} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/model_providers/models?model_type=evo_llm", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ListUserModelsByModelType(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Data groupModelListResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.Models) != 1 || payload.Data.Models[0].ID != "custom-chat" {
		t.Fatalf("expected only the custom chat model for Evo, got %#v", payload.Data.Models)
	}
}
