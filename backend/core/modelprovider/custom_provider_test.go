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

func customProviderRequest(method, id, body string) *http.Request {
	r := httptest.NewRequest(method, "/model_providers", strings.NewReader(body))
	r.Header.Set("X-User-Id", "user-1")
	return mux.SetURLVars(r, map[string]string{"model_provider_id": id})
}

func TestCustomSupplierCRUDKeepsTechnicalProtocolAndCanonicalStorage(t *testing.T) {
	db := setupListProviderTestDB(t)
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now()
	source := orm.UserModelProvider{ID: "source", Name: "OpenAI", DefaultModelProviderID: "catalog", Category: "model", Capabilities: "has_models", BaseURL: "https://api.openai.com/v1/", BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	CreateCustomProvider(rec, customProviderRequest(http.MethodPost, "", `{"name":"My proxy","source_provider_id":"source","base_url":"https://proxy.example/v1"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("create %d: %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			ID      string `json:"id"`
			GroupID string `json:"group_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var custom orm.UserModelProvider
	if err := db.First(&custom, "id = ?", envelope.Data.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !custom.IsCustom || custom.Name != "My proxy" || RuntimeProviderName(custom) != "OpenAI" || custom.DefaultModelProviderID != "catalog" {
		t.Fatal("custom identity lost its source protocol")
	}
	var group orm.UserModelProviderGroup
	if err := db.First(&group, "id = ?", envelope.Data.GroupID).Error; err != nil || group.Name != "My proxy" {
		t.Fatal("canonical credential storage missing", err)
	}
	rec = httptest.NewRecorder()
	CreateGroup(rec, customProviderRequest(http.MethodPost, custom.ID, `{"name":"Second","verify":false}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("second hidden group allowed: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	UpdateCustomProvider(rec, customProviderRequest(http.MethodPatch, custom.ID, `{"name":"Renamed proxy"}`))
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	db.First(&custom, "id = ?", custom.ID)
	db.First(&group, "id = ?", group.ID)
	if custom.Name != "Renamed proxy" || group.Name != "Renamed proxy" || RuntimeProviderName(custom) != "OpenAI" {
		t.Fatal("rename changed adapter or backup display identity")
	}
	rec = httptest.NewRecorder()
	DeleteCustomProvider(rec, customProviderRequest(http.MethodDelete, source.ID, ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("catalog deletion accepted %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	DeleteCustomProvider(rec, customProviderRequest(http.MethodDelete, custom.ID, ""))
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	db.First(&custom, "id = ?", custom.ID)
	db.First(&group, "id = ?", group.ID)
	if custom.DeletedAt == nil || group.DeletedAt == nil {
		t.Fatal("delete did not preserve soft-deleted records")
	}
}

func TestCustomSupplierSeedSyncDoesNotSuppressCatalogSupplier(t *testing.T) {
	db := setupListProviderTestDB(t)
	now := time.Now()
	if err := db.Create(&orm.DefaultModelProvider{ID: "catalog", Name: "OpenAI", Category: "model", Capabilities: "has_models", Description: "Official", BaseURL: "https://api.openai.com/v1/", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserModelProvider{ID: "custom", DefaultModelProviderID: "catalog", Name: "Proxy", ProtocolName: "OpenAI", IsCustom: true, Category: "model", BaseURL: "https://proxy.example/v1/", BaseModel: orm.BaseModel{CreateUserID: "user-1"}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := syncUserProvidersFromDefaults(context.Background(), db, "user-1", ""); err != nil {
		t.Fatal(err)
	}
	var rows []orm.UserModelProvider
	db.Where("create_user_id = ?", "user-1").Find(&rows)
	if len(rows) != 2 {
		t.Fatal("custom supplier prevented catalog initialization")
	}
	var custom orm.UserModelProvider
	db.First(&custom, "id = ?", "custom")
	if custom.Name != "Proxy" || custom.BaseURL != "https://proxy.example/v1/" {
		t.Fatal("catalog sync overwrote custom settings")
	}
}

func TestCustomSupplierChecksUseProtocolAfterRename(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "key-one")
	if err := db.AutoMigrate(&orm.UserModelProviderGroupModel{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.Model(&parent).Updates(map[string]any{"name": "My gateway", "protocol_name": "OpenAI", "is_custom": true}).Error; err != nil {
		t.Fatal(err)
	}
	var received algoModelCheckBody
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	req := customProviderRequest(http.MethodPost, parent.ID, `{}`)
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	rec := httptest.NewRecorder()
	CheckGroup(rec, req)
	if rec.Code != http.StatusOK || received.Source != "openai" {
		t.Fatalf("custom identity used as adapter: status=%d source=%s", rec.Code, received.Source)
	}
	req = customProviderRequest(http.MethodPost, parent.ID, `{"name":"custom-chat","model_type":"llm"}`)
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	rec = httptest.NewRecorder()
	AddGroupModel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var model orm.UserModelProviderGroupModel
	if err := db.Where("user_model_provider_group_id = ? AND name = ?", group.ID, "custom-chat").Take(&model).Error; err != nil {
		t.Fatal(err)
	}
	if model.ProviderName != "OpenAI" {
		t.Fatal("custom model stored display name as runtime protocol")
	}
}

func TestModelSupplierCapabilitiesDoNotAdvertiseGroups(t *testing.T) {
	rows := buildListItems(context.Background(), nil, []orm.UserModelProvider{
		{ID: "model", Category: "model", Capabilities: "multi_group,custom_base_url,has_models"},
		{ID: "ocr", Category: "ocr", Capabilities: "multi_group,custom_base_url"},
	}, "zh-CN")
	for _, capability := range rows[0].Capabilities {
		if capability == "multi_group" {
			t.Fatal("model API still advertises connections")
		}
	}
	if len(rows[1].Capabilities) != 2 {
		t.Fatal("non-model capabilities changed")
	}
}
