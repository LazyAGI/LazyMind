package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func setupCheckProviderTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbName := "check_provider_" + strings.ReplaceAll(t.Name(), "/", "_")
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.DefaultModelProvider{},
		&orm.DefaultModel{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store.Init(db, db, nil)
	return db
}

func seedCheckProviderCatalog(t *testing.T, db *gorm.DB, models []orm.DefaultModel) {
	t.Helper()

	now := time.Now()
	provider := orm.DefaultModelProvider{
		ID:          "provider-qwen",
		Name:        "Qwen",
		Description: "Qwen provider",
		BaseURL:     "https://dashscope.aliyuncs.com/compatible-mode/v1",
		Category:    "model",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	for i := range models {
		models[i].DefaultModelProviderID = provider.ID
		models[i].ProviderName = provider.Name
		models[i].CreatedAt = now
		models[i].UpdatedAt = now
	}
	if err := db.Create(&models).Error; err != nil {
		t.Fatalf("create models: %v", err)
	}
}

func TestDoModelProviderCheckTriesDefaultLLMsInDBOrderAndStopsOnSuccess(t *testing.T) {
	db := setupCheckProviderTestDB(t)
	seedCheckProviderCatalog(t, db, []orm.DefaultModel{
		{ID: "00-embed", Name: "text-embedding-v4", ModelType: "embed"},
		{ID: "01-llm", Name: "qwen-disabled", ModelType: "llm"},
		{ID: "02-llm", Name: "qwen-ready", ModelType: "llm"},
		{ID: "03-llm", Name: "qwen-untouched", ModelType: "llm"},
	})

	var tried []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/model/check" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body algoModelCheckBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		tried = append(tried, body.Model)
		w.Header().Set("Content-Type", "application/json")
		if body.Model == "qwen-ready" {
			_, _ = w.Write([]byte(`{"success":true,"message":"ok"}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":false,"message":"Model disabled"}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)

	result, err := doCheck(t.Context(), "model", "Qwen", "https://example.test/v1", "test-key")
	if err != nil {
		t.Fatalf("doCheck error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
	if result.Model != "qwen-ready" {
		t.Fatalf("expected successful model to be reported, got %q", result.Model)
	}
	want := []string{"qwen-disabled", "qwen-ready"}
	if !reflect.DeepEqual(tried, want) {
		t.Fatalf("unexpected probe order: got %v want %v", tried, want)
	}
}

func TestDoModelProviderCheckStopsOnCredentialFailure(t *testing.T) {
	db := setupCheckProviderTestDB(t)
	seedCheckProviderCatalog(t, db, []orm.DefaultModel{
		{ID: "01-llm", Name: "qwen-one", ModelType: "llm"},
		{ID: "02-llm", Name: "qwen-two", ModelType: "llm"},
	})

	var tried []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body algoModelCheckBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		tried = append(tried, body.Model)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid api key"}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)

	result, err := doCheck(t.Context(), "model", "Qwen", "https://example.test/v1", "test-key")
	if err != nil {
		t.Fatalf("doCheck error: %v", err)
	}
	if result == nil || result.Success {
		t.Fatalf("expected failure, got %+v", result)
	}
	if len(tried) != 1 || tried[0] != "qwen-one" {
		t.Fatalf("expected credential failure to stop after first probe, got %v", tried)
	}
	if !strings.Contains(result.Message, "1/2") || !strings.Contains(result.Message, "qwen-one") {
		t.Fatalf("unexpected failure message: %q", result.Message)
	}
}

func TestDoModelProviderCheckAggregatesModelFailures(t *testing.T) {
	db := setupCheckProviderTestDB(t)
	seedCheckProviderCatalog(t, db, []orm.DefaultModel{
		{ID: "01-llm", Name: "qwen-one", ModelType: "llm"},
		{ID: "02-llm", Name: "qwen-two", ModelType: "llm"},
		{ID: "03-llm", Name: "qwen-three", ModelType: "llm"},
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body algoModelCheckBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"message":"model not found: ` + body.Model + `"}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)

	result, err := doCheck(t.Context(), "model", "Qwen", "https://example.test/v1", "test-key")
	if err != nil {
		t.Fatalf("doCheck error: %v", err)
	}
	if result == nil || result.Success {
		t.Fatalf("expected failure, got %+v", result)
	}
	for _, want := range []string{"tried 3 llm models", "qwen-one", "qwen-two", "qwen-three"} {
		if !strings.Contains(result.Message, want) {
			t.Fatalf("expected message to contain %q, got %q", want, result.Message)
		}
	}
}

func TestDefaultLLMProbeModelsLimitsCandidates(t *testing.T) {
	db := setupCheckProviderTestDB(t)
	models := make([]orm.DefaultModel, 0, maxModelProviderProbeModels+2)
	for i := 0; i < maxModelProviderProbeModels+2; i++ {
		models = append(models, orm.DefaultModel{
			ID:        "llm-" + string(rune('a'+i)),
			Name:      "qwen-" + string(rune('a'+i)),
			ModelType: "llm",
		})
	}
	seedCheckProviderCatalog(t, db, models)

	got, err := defaultLLMProbeModels(t.Context(), "Qwen", maxModelProviderProbeModels)
	if err != nil {
		t.Fatalf("defaultLLMProbeModels error: %v", err)
	}
	if len(got) != maxModelProviderProbeModels {
		t.Fatalf("expected %d candidates, got %d: %v", maxModelProviderProbeModels, len(got), got)
	}
}
