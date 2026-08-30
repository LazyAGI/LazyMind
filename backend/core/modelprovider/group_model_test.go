package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

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
			name:      "embedding includes legacy aliases",
			modelType: "embed",
			want:      []string{"embed", "embedding", "embed_main"},
		},
		{
			name:      "legacy embedding includes current aliases",
			modelType: "embedding",
			want:      []string{"embed", "embedding", "embed_main"},
		},
		{
			name:      "runtime embedding includes persisted aliases",
			modelType: "embed_main",
			want:      []string{"embed", "embedding", "embed_main"},
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
			name:      "vision-language model includes legacy uppercase type",
			modelType: "vlm",
			want:      []string{"vlm", "VLM"},
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

func TestAddGroupModelNormalizesLegacyAliases(t *testing.T) {
	tests := []struct {
		modelType string
		want      string
	}{
		{modelType: "embedding", want: "embed"},
		{modelType: "embed_main", want: "embed"},
		{modelType: "VLM", want: "vlm"},
	}
	for _, tt := range tests {
		t.Run(tt.modelType, func(t *testing.T) {
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
			for _, row := range []any{&provider, &group} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}

			body := `{"name":"custom-model","model_type":"` + tt.modelType + `"}`
			req := httptest.NewRequest(http.MethodPost, "/api/core/model_providers/provider-openai/groups/group-openai/models", strings.NewReader(body))
			req.Header.Set("X-User-Id", "user-1")
			req = mux.SetURLVars(req, map[string]string{
				"model_provider_id": provider.ID,
				"group_id":          group.ID,
			})
			rec := httptest.NewRecorder()

			AddGroupModel(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
			}
			var payload struct {
				Data addGroupModelResponse `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Data.ModelType != tt.want {
				t.Fatalf("response model_type = %q, want %q", payload.Data.ModelType, tt.want)
			}

			var stored orm.UserModelProviderGroupModel
			if err := db.Take(&stored, "id = ?", payload.Data.ID).Error; err != nil {
				t.Fatal(err)
			}
			if stored.ModelType != tt.want {
				t.Fatalf("stored model_type = %q, want %q", stored.ModelType, tt.want)
			}
		})
	}
}

func TestAddGroupModelRestoresSoftDeletedCustomModel(t *testing.T) {
	db := setupListProviderTestDB(t)
	if err := db.AutoMigrate(&orm.UserSelectedModel{}); err != nil {
		t.Fatal(err)
	}
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
	for _, row := range []any{&provider, &group} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	addModel := func(modelType string) (*httptest.ResponseRecorder, addGroupModelResponse) {
		t.Helper()
		body := `{"name":"reusable-model","model_type":"` + modelType + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/core/model_providers/provider-openai/groups/group-openai/models", strings.NewReader(body))
		req.Header.Set("X-User-Id", "user-1")
		req = mux.SetURLVars(req, map[string]string{
			"model_provider_id": provider.ID,
			"group_id":          group.ID,
		})
		rec := httptest.NewRecorder()
		AddGroupModel(rec, req)

		var payload struct {
			Data addGroupModelResponse `json:"data"`
		}
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
		}
		return rec, payload.Data
	}

	createdRec, created := addModel("llm")
	if createdRec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200: %s", createdRec.Code, createdRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/core/model_providers/provider-openai/groups/group-openai/models/"+created.ID, nil)
	deleteReq.Header.Set("X-User-Id", "user-1")
	deleteReq = mux.SetURLVars(deleteReq, map[string]string{
		"model_provider_id": provider.ID,
		"group_id":          group.ID,
		"model_id":          created.ID,
	})
	deleteRec := httptest.NewRecorder()
	DeleteGroupModel(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200: %s", deleteRec.Code, deleteRec.Body.String())
	}

	var deleted orm.UserModelProviderGroupModel
	if err := db.Take(&deleted, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if deleted.DeletedAt == nil {
		t.Fatal("expected model to be soft deleted")
	}

	restoredRec, restored := addModel("VLM")
	if restoredRec.Code != http.StatusOK {
		t.Fatalf("restore status = %d, want 200: %s", restoredRec.Code, restoredRec.Body.String())
	}
	if restored.ID != created.ID {
		t.Fatalf("restored id = %q, want original id %q", restored.ID, created.ID)
	}
	if restored.ModelType != "vlm" {
		t.Fatalf("restored model_type = %q, want %q", restored.ModelType, "vlm")
	}

	var stored orm.UserModelProviderGroupModel
	if err := db.Take(&stored, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.DeletedAt != nil {
		t.Fatalf("restored deleted_at = %v, want nil", stored.DeletedAt)
	}
	if stored.ModelType != "vlm" {
		t.Fatalf("stored model_type = %q, want %q", stored.ModelType, "vlm")
	}
	var count int64
	if err := db.Model(&orm.UserModelProviderGroupModel{}).
		Where("user_model_provider_group_id = ? AND name = ?", group.ID, "reusable-model").
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("physical row count = %d, want 1", count)
	}

	duplicateRec, _ := addModel("llm")
	if duplicateRec.Code != http.StatusConflict {
		t.Fatalf("active duplicate status = %d, want 409: %s", duplicateRec.Code, duplicateRec.Body.String())
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
