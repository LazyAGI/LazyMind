package modelprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestUnsupportedOpenRouterTypesCannotBecomeRuntimeSelections(t *testing.T) {
	for _, typ := range []string{"rerank", "image_editing", "multimodal_embedding"} {
		parent := orm.UserModelProvider{Name: "Custom gateway", ProtocolName: "OpenRouter"}
		if validateSupplierModelType(t.Context(), nil, parent, typ) == nil {
			t.Errorf("save accepted %s", typ)
		}
		model := orm.UserModelProviderGroupModel{ProviderName: "OpenRouter", ModelType: typ}
		for _, slot := range []string{"reranker", "text2image", "embed_image"} {
			if modelSupportsSelection(model, slot, "https://openrouter.ai/api/v1/") {
				t.Errorf("selection accepted %s as %s", typ, slot)
			}
		}
	}
}

func TestLocalSupplierTemplateKeepsMediaUnsupportedAfterOpenAIProtocolSelection(t *testing.T) {
	for _, name := range []string{"vLLM", "LMDeploy"} {
		t.Run(name, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.DefaultModelProvider{}, &orm.UserModelProvider{}).DB
			template := orm.DefaultModelProvider{ID: "template", Name: name}
			if err := db.Create(&template).Error; err != nil {
				t.Fatal(err)
			}
			parent := orm.UserModelProvider{Name: "my-local", IsCustom: true, ProtocolName: "OpenAI", DefaultModelProviderID: template.ID}
			for _, typ := range []string{"text2image", "text2video"} {
				if validateSupplierModelType(t.Context(), db, parent, typ) == nil {
					t.Errorf("%s template accepted OpenAI media type %s", name, typ)
				}
			}
			if validateSupplierModelType(t.Context(), db, parent, "embed") != nil {
				t.Errorf("%s template rejected compatible embedding", name)
			}
		})
	}
}

func TestUnsupportedOpenRouterSelectionsAreNotReadyAndRemainVisible(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{})
	now := time.Now().UTC()
	group := orm.UserModelProviderGroup{ID: "g", Name: "Gateway", IsVerified: true, BaseURL: "https://openrouter.ai/api/v1/", BaseModel: orm.BaseModel{CreateUserID: "u", CreatedAt: now, UpdatedAt: now}}
	model := orm.UserModelProviderGroupModel{ID: "m", UserModelProviderGroupID: "g", ProviderName: "OpenRouter", Name: "rerank", ModelType: "rerank", BaseModel: group.BaseModel}
	selection := orm.UserSelectedModel{UserID: "u", ModelKey: "reranker", UserModelProviderGroupModelID: "m", Share: true, CreatedAt: now, UpdatedAt: now}
	for _, row := range []any{&group, &model, &selection} {
		if err := db.DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, shared := range []bool{false, true} {
		ready, err := hasValidModelSelection(context.Background(), db.DB, "u", "reranker", shared)
		if err != nil || ready {
			t.Fatalf("unsupported ready=%v err=%v shared=%v", ready, err, shared)
		}
	}
	items, err := loadSelectedModels(context.Background(), db.DB, "u")
	if err != nil || len(items) != 1 || items[0].Availability != "unavailable" || items[0].UnavailableReason != "runtime_unsupported" {
		t.Fatalf("selected=%#v err=%v", items, err)
	}
	var retained orm.UserModelProviderGroupModel
	if err := db.DB.Take(&retained, "id = ?", "m").Error; err != nil || retained.ModelType != "rerank" {
		t.Fatalf("manual choice was altered: %#v %v", retained, err)
	}
	if err := db.DB.Model(&retained).Update("model_type", "multimodal_embedding").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Model(&orm.UserSelectedModel{}).Where("user_id = ?", "u").Update("model_type", "embed_image").Error; err != nil {
		t.Fatal(err)
	}
	if ready, err := HasAnyMultimodalEmbeddingSelection(context.Background(), db.DB); err != nil || ready {
		t.Fatalf("unsupported multimodal selection appears ready: %v %v", ready, err)
	}
	if shared, err := getSharedModelDetail(context.Background(), db.DB, "embed_image"); err != nil || shared != nil {
		t.Fatalf("unsupported shared detail advertised: %#v %v", shared, err)
	}
}

func TestOpenRouterRuntimeCandidatesExcludeMissingAdaptersButInventoryRetainsThem(t *testing.T) {
	db := setupListProviderTestDB(t)
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	base := orm.BaseModel{CreateUserID: "u", CreatedAt: now, UpdatedAt: now}
	parent := orm.UserModelProvider{ID: "p", Name: "OpenRouter", Capabilities: "has_models", Category: "model", BaseModel: base}
	group := orm.UserModelProviderGroup{ID: "g", UserModelProviderID: "p", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1/", APIKey: "saved-key", IsVerified: true, BaseModel: base}
	models := []orm.UserModelProviderGroupModel{
		{ID: "unsupported", UserModelProviderID: "p", UserModelProviderGroupID: "g", ProviderName: "OpenRouter", Name: "vector-image", ModelType: "multimodal_embedding", BaseModel: base},
		{ID: "supported", UserModelProviderID: "p", UserModelProviderGroupID: "g", ProviderName: "OpenRouter", Name: "chat", ModelType: "llm", BaseModel: base},
	}
	for _, row := range []any{&parent, &group, &models} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := func(inventory bool) []groupModelListItem {
		req := httptest.NewRequest(http.MethodGet, "/models", nil)
		req.Header.Set("X-User-Id", "u")
		rec := httptest.NewRecorder()
		if inventory {
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": "p", "group_id": "g"})
			ListGroupModels(rec, req)
		} else {
			ListUserModelsByModelType(rec, req)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Data groupModelListResponse `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Data.Models
	}
	candidates := request(false)
	if len(candidates) != 1 || candidates[0].ID != "supported" {
		t.Fatalf("runtime candidates=%#v", candidates)
	}
	inventory := request(true)
	if len(inventory) != 2 {
		t.Fatalf("inventory=%#v", inventory)
	}
	for _, model := range inventory {
		if model.ID == "unsupported" && model.Availability != "unavailable" {
			t.Fatalf("missing unavailable inventory flag: %#v", model)
		}
	}
}
