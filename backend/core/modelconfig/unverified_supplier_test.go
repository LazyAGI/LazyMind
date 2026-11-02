package modelconfig

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/modelprovider"
	"lazymind/core/store"
)

func unverifiedSupplierFixture(t *testing.T) (*gorm.DB, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	db := orm.MigrateTestDB(t, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}, &orm.UserSelectedCloudModel{}, &orm.EvolutionModelValidation{})
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	base := orm.BaseModel{CreateUserID: "owner", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	keyID := fmt.Sprintf("%x", sha256.Sum256([]byte("connection\x00saved-key")))
	for _, row := range []any{
		&orm.UserModelProvider{ID: "supplier", Name: "OpenAI", Category: "model", Capabilities: "has_models", BaseURL: server.URL + "/v1", ActiveKeyInitialized: true, ActiveKeyGroupID: "connection", ActiveKeyID: keyID, BaseModel: base},
		&orm.UserModelProviderGroup{ID: "connection", UserModelProviderID: "supplier", Name: "Manual models", APIKey: "saved-key", BaseURL: server.URL + "/v1", IsVerified: false, BaseModel: base},
		&orm.UserModelProviderGroupModel{ID: "chat", UserModelProviderID: "supplier", UserModelProviderGroupID: "connection", ProviderName: "OpenAI", Name: "private-chat", ModelType: "llm", BaseModel: base},
		&orm.UserModelProviderGroupModel{ID: "embed", UserModelProviderID: "supplier", UserModelProviderGroupID: "connection", ProviderName: "OpenAI", Name: "private-embed", ModelType: "embed", IsDefault: true, BaseModel: base},
	} {
		if err := db.DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db.DB, &calls
}

func TestUnverifiedSupplierExplicitModelSelectionAndRuntime(t *testing.T) {
	db, calls := unverifiedSupplierFixture(t)
	request := httptest.NewRequest(http.MethodGet, "/models?model_type=llm", nil)
	request.Header.Set("X-User-Id", "owner")
	listed := httptest.NewRecorder()
	modelprovider.ListUserModelsByModelType(listed, request)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"id":"chat"`) {
		t.Fatalf("saved unverified model unavailable for explicit selection: %d %s", listed.Code, listed.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/selected-models", strings.NewReader(`{"selections":[{"model_key":"llm","model_id":"chat"},{"model_key":"evo_llm","model_id":"chat"}]}`))
	request.Header.Set("X-User-Id", "owner")
	selected := httptest.NewRecorder()
	modelprovider.SetSelectedModels(selected, request)
	if selected.Code != http.StatusOK || !strings.Contains(selected.Body.String(), `"availability":"available"`) {
		t.Fatalf("explicit saved model selection failed: %d %s", selected.Code, selected.Body.String())
	}
	config, err := LoadLLMConfig(context.Background(), db, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"llm", "evo_llm"} {
		model, ok := config[role].(map[string]any)
		if !ok || model["api_key"] != "saved-key" {
			t.Fatalf("explicit %s unavailable: %#v", role, config)
		}
	}
	evolution, err := LoadEvolutionModels(context.Background(), db, "owner")
	if err != nil || !evolution.CanSelect || evolution.AvailableDefaultRef == "" {
		t.Fatalf("unverified evolution unavailable: %+v %v", evolution, err)
	}
	if calls.Load() != 0 {
		t.Fatal("explicit selection or runtime attempted discovery validation")
	}
}

func TestUnverifiedSupplierIsNotAutomaticallyChosen(t *testing.T) {
	db, _ := unverifiedSupplierFixture(t)
	if err := db.Create(&orm.UserSelectedModel{UserID: "owner", ModelKey: "llm", UserModelProviderGroupModelID: "chat", Share: true}).Error; err != nil {
		t.Fatal(err)
	}
	config, err := LoadLLMConfig(context.Background(), db, "other")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := config["llm"]; exists {
		t.Fatal("unverified supplier automatically used as shared fallback")
	}
	embed, err := LoadAdminEmbedConfig(context.Background(), db)
	if err != nil || embed != nil {
		t.Fatalf("unverified default embedding automatically used: %#v %v", embed, err)
	}
}

func TestExplicitSelectionRejectsDisabledOrMissingHostedKey(t *testing.T) {
	for _, state := range []string{"disabled", "missing-hosted-key"} {
		t.Run(state, func(t *testing.T) {
			db, _ := unverifiedSupplierFixture(t)
			if err := db.Model(&orm.UserModelProvider{}).Where("id = ?", "supplier").Updates(map[string]any{"active_key_group_id": "", "active_key_id": ""}).Error; err != nil {
				t.Fatal(err)
			}
			if state == "missing-hosted-key" {
				if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "connection").Update("api_key", "").Error; err != nil {
					t.Fatal(err)
				}
			}
			request := httptest.NewRequest(http.MethodPost, "/selected-models", strings.NewReader(`{"selections":[{"model_key":"llm","model_id":"chat"}]}`))
			request.Header.Set("X-User-Id", "owner")
			selected := httptest.NewRecorder()
			modelprovider.SetSelectedModels(selected, request)
			if selected.Code != http.StatusBadRequest {
				t.Fatalf("unavailable credentials saved as usable selection: %d %s", selected.Code, selected.Body.String())
			}
			if err := db.Create(&orm.UserSelectedModel{UserID: "owner", ModelKey: "llm", UserModelProviderGroupModelID: "chat"}).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := LoadLLMConfig(context.Background(), db, "owner"); err == nil {
				t.Fatal("unavailable credentials admitted by runtime")
			}
		})
	}
}

func TestUnavailableOtherRoleDoesNotBlockExplicitChatRuntime(t *testing.T) {
	db, _ := unverifiedSupplierFixture(t)
	base := orm.BaseModel{CreateUserID: "owner"}
	for _, row := range []any{
		&orm.UserSelectedModel{UserID: "owner", ModelKey: "llm", UserModelProviderGroupModelID: "chat"},
		&orm.UserModelProvider{ID: "disabled", Name: "OpenAI", Capabilities: "has_models", BaseURL: "https://api.openai.com/v1", ActiveKeyInitialized: true, BaseModel: base},
		&orm.UserModelProviderGroup{ID: "disabled-group", UserModelProviderID: "disabled", APIKey: "disabled-key", BaseModel: base},
		&orm.UserModelProviderGroupModel{ID: "disabled-tts", UserModelProviderID: "disabled", UserModelProviderGroupID: "disabled-group", ProviderName: "OpenAI", Name: "tts-1", ModelType: "tts", BaseModel: base},
		&orm.UserSelectedModel{UserID: "owner", ModelKey: "tts", UserModelProviderGroupModelID: "disabled-tts"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	config, err := LoadLLMConfig(context.Background(), db, "owner")
	if err != nil {
		t.Fatalf("unrelated disabled TTS blocked chat: %v", err)
	}
	if config["llm"] == nil || config["tts"] != nil {
		t.Fatalf("wrong runtime roles: %#v", config)
	}
}
