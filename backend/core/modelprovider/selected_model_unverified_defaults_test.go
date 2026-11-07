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

func TestGetSelectedModelsHidesUnverifiedCustomCatalogDefaults(t *testing.T) {
	db := setupListProviderTestDB(t)
	if err := db.AutoMigrate(&orm.UserSelectedModel{}); err != nil {
		t.Fatal(err)
	}
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	base := orm.BaseModel{CreateUserID: "owner", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	fixtures := []struct {
		providerID string
		modelKey   string
		modelType  string
		isCustom   bool
		verified   bool
		isDefault  bool
	}{
		{providerID: "custom-unverified-default", modelKey: "llm", modelType: "llm", isCustom: true, isDefault: true},
		{providerID: "custom-unverified-manual", modelKey: "rerank", modelType: "rerank", isCustom: true},
		{providerID: "custom-verified-default", modelKey: "stt", modelType: "stt", isCustom: true, verified: true, isDefault: true},
		{providerID: "official-unverified-default", modelKey: "tts", modelType: "tts", isDefault: true},
	}
	for _, fixture := range fixtures {
		groupID := fixture.providerID + "-group"
		modelID := fixture.providerID + "-model"
		for _, row := range []any{
			&orm.UserModelProvider{
				ID: fixture.providerID, Name: "OpenAI", ProtocolName: "OpenAI", IsCustom: fixture.isCustom,
				Category: "model", Capabilities: "has_models", BaseURL: "https://gateway.example/v1/", BaseModel: base,
			},
			&orm.UserModelProviderGroup{
				ID: groupID, UserModelProviderID: fixture.providerID, Name: groupID,
				BaseURL: "https://gateway.example/v1/", IsVerified: fixture.verified, BaseModel: base,
			},
			&orm.UserModelProviderGroupModel{
				ID: modelID, UserModelProviderID: fixture.providerID, UserModelProviderGroupID: groupID,
				ProviderName: "OpenAI", Name: modelID, ModelType: fixture.modelType,
				IsDefault: fixture.isDefault, BaseModel: base,
			},
			&orm.UserSelectedModel{
				UserID: "owner", ModelKey: fixture.modelKey, UserModelProviderGroupModelID: modelID,
			},
		} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/model_providers/selected", nil)
	request.Header.Set("X-User-Id", "owner")
	recorder := httptest.NewRecorder()
	GetSelectedModels(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("selected GET: %d %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Selections []struct {
				ModelID string `json:"model_id"`
			} `json:"selections"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(response.Data.Selections))
	for _, item := range response.Data.Selections {
		got = append(got, item.ModelID)
	}
	want := []string{
		"custom-unverified-manual-model",
		"custom-verified-default-model",
		"official-unverified-default-model",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selected model IDs = %v, want %v; response=%s", got, want, recorder.Body.String())
	}
}
