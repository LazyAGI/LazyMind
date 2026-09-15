package modelprovider_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/cloudclient"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/modelprovider"
	"lazymind/core/store"
)

type cloudCatalogTokens struct{}

func (cloudCatalogTokens) AccessToken(context.Context, time.Duration) (string, error) {
	return "cloud-access-canary", nil
}

type cloudCatalogClient struct{ available bool }

func (cloudCatalogClient) Origin() string { return "https://cloud.example" }

func (client cloudCatalogClient) GetProviderBootstrap(context.Context, string) (cloudclient.ModelProviderBootstrap, error) {
	if !client.available {
		return cloudclient.ModelProviderBootstrap{
			ProviderKey: "lazymind-cloud", DisplayName: "LazyMind Cloud",
			HasTokenPlan: true, ModelKey: "lazymind-text-default", ReasonCode: "model_unavailable",
			Models: []cloudclient.PublicCloudModel{},
		}, nil
	}
	return cloudclient.ModelProviderBootstrap{
		ProviderKey: "lazymind-cloud", DisplayName: "LazyMind Cloud",
		Available: true, HasTokenPlan: true, CloudChatAvailable: true,
		ModelKey: "lazymind-text-default",
		Models: []cloudclient.PublicCloudModel{{
			ModelKey: "lazymind-text-default", DisplayName: "LazyMind Text",
			Capabilities: []string{"chat", "stream", "tool_calls"}, Status: "available",
		}},
	}, nil
}

func installCloudCatalogProvider(t *testing.T, available bool) {
	t.Helper()
	provider := &modelconfig.CloudRuntimeProvider{
		Session: cloudCatalogTokens{}, Client: cloudCatalogClient{available: available},
	}
	modelconfig.SetRuntimeProvider(provider)
	modelprovider.SetCloudReadinessProvider(provider)
	modelprovider.SetCloudCatalogProvider(provider)
	t.Cleanup(func() {
		modelconfig.SetRuntimeProvider(nil)
		modelprovider.SetCloudReadinessProvider(nil)
		modelprovider.SetCloudCatalogProvider(nil)
	})
}

func cloudSelectionTestDB(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
}

func TestListUserModelsProjectsCloudWithoutCreatingPersonalProviderRows(t *testing.T) {
	cloudSelectionTestDB(t)
	installCloudCatalogProvider(t, true)

	req := httptest.NewRequest(http.MethodGet, "/api/core/model_providers/models?model_type=llm", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	modelprovider.ListUserModelsByModelType(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list Cloud models status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Models []map[string]any `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Models) != 1 {
		t.Fatalf("Cloud-only catalog models=%#v", envelope.Data.Models)
	}
	model := envelope.Data.Models[0]
	if model["source"] != "cloud" || model["provider_id"] != "lazymind-cloud" || model["id"] != "lazymind-text-default" {
		t.Fatalf("Cloud system model projection=%#v", model)
	}
	if groupID, _ := model["user_model_provider_group_id"].(string); groupID != "" {
		t.Fatalf("Cloud model was projected as a personal Provider Group: %#v", model)
	}
	if strings.Contains(rec.Body.String(), "cloud-access-canary") {
		t.Fatal("Cloud catalog exposed the in-memory Access Token")
	}
}

func TestSelectingCloudRetainsDormantLocalSelectionButMakesCloudAuthoritative(t *testing.T) {
	cloudSelectionTestDB(t)
	installCloudCatalogProvider(t, true)
	db := store.DB()
	now := time.Now().UTC()
	provider := orm.UserModelProvider{
		ID: "provider-local", DefaultModelProviderID: "default-local", Name: "Local OpenAI",
		Category: "model", Capabilities: "has_models",
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "User", CreatedAt: now, UpdatedAt: now},
	}
	group := orm.UserModelProviderGroup{
		ID: "group-local", UserModelProviderID: provider.ID, Name: "Personal",
		BaseURL: "https://personal.example/v1", APIKey: "personal-secret", IsVerified: true,
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "User", CreatedAt: now, UpdatedAt: now},
	}
	model := orm.UserModelProviderGroupModel{
		ID: "model-local", UserModelProviderID: provider.ID, UserModelProviderGroupID: group.ID,
		ProviderName: provider.Name, Name: "personal-chat", ModelType: "llm",
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "User", CreatedAt: now, UpdatedAt: now},
	}
	selection := orm.UserSelectedModel{
		UserID: "user-1", UserName: "User", ModelKey: "llm",
		UserModelProviderGroupModelID: model.ID, Share: true, CreatedAt: now, UpdatedAt: now,
	}
	for _, row := range []any{&provider, &group, &model, &selection} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	body := []byte(`{"selections":[{"model_key":"llm","source":"cloud","model_id":"lazymind-text-default"}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/core/model_providers/selected_models", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "user-1")
	req.Header.Set("X-User-Name", "User")
	rec := httptest.NewRecorder()
	modelprovider.SetSelectedModels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("select Cloud status=%d body=%s", rec.Code, rec.Body.String())
	}

	var localCount int64
	if err := db.Model(&orm.UserSelectedModel{}).
		Where("user_id = ? AND model_type = ?", "user-1", "llm").Count(&localCount).Error; err != nil {
		t.Fatal(err)
	}
	if localCount != 1 {
		t.Fatalf("dormant local selection count=%d want=1", localCount)
	}
	var shared bool
	if err := db.Model(&orm.UserSelectedModel{}).
		Select("share").Where("user_id = ? AND model_type = ?", "user-1", "llm").Scan(&shared).Error; err != nil {
		t.Fatal(err)
	}
	if shared {
		t.Fatal("dormant local selection remained shared after selecting account-scoped Cloud")
	}
	var cloudCount int64
	if err := db.Table("user_selected_cloud_models").
		Where("user_id = ? AND model_type = ? AND public_model_key = ?", "user-1", "llm", "lazymind-text-default").
		Count(&cloudCount).Error; err != nil {
		t.Fatal(err)
	}
	if cloudCount != 1 {
		t.Fatalf("Cloud selection count=%d want=1", cloudCount)
	}

	readyReq := httptest.NewRequest(http.MethodGet, "/api/core/model_providers/models/ready?model_type=llm", nil)
	readyReq.Header.Set("X-User-Id", "user-1")
	readyRec := httptest.NewRecorder()
	modelprovider.GetModelReady(readyRec, readyReq)
	if readyRec.Code != http.StatusOK {
		t.Fatalf("Cloud readiness status=%d body=%s", readyRec.Code, readyRec.Body.String())
	}
	var readyEnvelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(readyRec.Body.Bytes(), &readyEnvelope); err != nil {
		t.Fatal(err)
	}
	if readyEnvelope.Data["source"] != "cloud" || readyEnvelope.Data["ready"] != true {
		t.Fatalf("explicit Cloud readiness=%#v", readyEnvelope.Data)
	}

	otherReq := httptest.NewRequest(http.MethodGet, "/api/core/model_providers/selected_models", nil)
	otherReq.Header.Set("X-User-Id", "user-2")
	otherRec := httptest.NewRecorder()
	modelprovider.GetSelectedModels(otherRec, otherReq)
	if otherRec.Code != http.StatusOK {
		t.Fatalf("other user selections status=%d body=%s", otherRec.Code, otherRec.Body.String())
	}
	var otherEnvelope struct {
		Data struct {
			Selections []map[string]any `json:"selections"`
		} `json:"data"`
	}
	if err := json.Unmarshal(otherRec.Body.Bytes(), &otherEnvelope); err != nil {
		t.Fatal(err)
	}
	if len(otherEnvelope.Data.Selections) != 0 {
		t.Fatalf("Cloud selection crossed local-user boundary: %#v", otherEnvelope.Data.Selections)
	}

	localBody := []byte(`{"selections":[{"model_key":"llm","source":"own","model_id":"model-local"}]}`)
	localReq := httptest.NewRequest(http.MethodPut, "/api/core/model_providers/selected_models", bytes.NewReader(localBody))
	localReq.Header.Set("Content-Type", "application/json")
	localReq.Header.Set("X-User-Id", "user-1")
	localReq.Header.Set("X-User-Name", "User")
	localRec := httptest.NewRecorder()
	modelprovider.SetSelectedModels(localRec, localReq)
	if localRec.Code != http.StatusOK {
		t.Fatalf("switch back to local status=%d body=%s", localRec.Code, localRec.Body.String())
	}
	cloudCount = 0
	if err := db.Table("user_selected_cloud_models").
		Where("user_id = ? AND model_type = ?", "user-1", "llm").Count(&cloudCount).Error; err != nil {
		t.Fatal(err)
	}
	if cloudCount != 0 {
		t.Fatalf("switching back to local left %d authoritative Cloud selections", cloudCount)
	}
}

func TestCloudSelectionRejectsAModelFromTheWrongCapabilitySlot(t *testing.T) {
	cloudSelectionTestDB(t)
	installCloudCatalogProvider(t, true)
	body := []byte(`{"selections":[{"model_key":"vlm","source":"cloud","model_id":"lazymind-text-default"}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/core/model_providers/selected_models", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	modelprovider.SetSelectedModels(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("capability-mismatched Cloud selection status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUnavailableCloudDefaultTemporarilyFallsBackToDormantLocalModel(t *testing.T) {
	cloudSelectionTestDB(t)
	installCloudCatalogProvider(t, false)
	db := store.DB()
	now := time.Now().UTC()
	provider := orm.UserModelProvider{
		ID: "provider-dormant", DefaultModelProviderID: "default-dormant", Name: "Dormant Local",
		Category: "model", Capabilities: "has_models",
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "User", CreatedAt: now, UpdatedAt: now},
	}
	group := orm.UserModelProviderGroup{
		ID: "group-dormant", UserModelProviderID: provider.ID, Name: "Dormant",
		BaseURL: "https://personal.example/v1", APIKey: "personal-secret", IsVerified: true,
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "User", CreatedAt: now, UpdatedAt: now},
	}
	model := orm.UserModelProviderGroupModel{
		ID: "model-dormant", UserModelProviderID: provider.ID, UserModelProviderGroupID: group.ID,
		ProviderName: provider.Name, Name: "personal-chat", ModelType: "llm",
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "User", CreatedAt: now, UpdatedAt: now},
	}
	selection := orm.UserSelectedModel{
		UserID: "user-1", UserName: "User", ModelKey: "llm",
		UserModelProviderGroupModelID: model.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, row := range []any{&provider, &group, &model, &selection} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(
		"INSERT INTO user_selected_cloud_models "+
			"(user_id,user_name,model_type,public_model_key,display_name_snapshot,created_at,updated_at) "+
			"VALUES (?,?,?,?,?,?,?)",
		"user-1", "User", "llm", "lazymind-text-default", "LazyMind Text", now, now,
	).Error; err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/model_providers/models/ready?model_type=llm", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	modelprovider.GetModelReady(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Cloud readiness status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data["source"] != "own" || envelope.Data["ready"] != true || envelope.Data["fallback_from"] != "cloud" {
		t.Fatalf("Cloud default did not expose the temporary local fallback: %#v", envelope.Data)
	}

	config, err := modelconfig.LoadLLMConfig(context.Background(), db, "user-1")
	if err != nil {
		t.Fatalf("load local fallback after Cloud became unavailable: %v", err)
	}
	llm, ok := config["llm"].(map[string]any)
	if !ok || llm["model"] != "personal-chat" || llm["base_url"] != "https://personal.example/v1" {
		t.Fatalf("runtime did not use the dormant local model: %#v", config)
	}
}
