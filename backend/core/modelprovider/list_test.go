package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func setupListProviderTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbName := "list_provider_" + strings.ReplaceAll(t.Name(), "/", "_")
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.DefaultModelProvider{},
		&orm.DefaultModel{},
		&orm.UserModelProvider{},
		&orm.UserModelProviderGroup{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestListUserProvidersKeywordIsCaseInsensitive(t *testing.T) {
	db := setupListProviderTestDB(t)
	store.Init(db, db, nil)

	now := time.Now()
	rows := []orm.DefaultModelProvider{
		{
			ID:          "default-qwen",
			Name:        "Qwen",
			Description: "Qwen provider",
			BaseURL:     "https://dashscope.aliyuncs.com/",
			Category:    defaultProviderCategory,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          "default-openai",
			Name:        "OpenAI",
			Description: "OpenAI provider",
			BaseURL:     "https://api.openai.com/v1/",
			Category:    defaultProviderCategory,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create default providers: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/model_providers?keyword=qwen", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()

	ListUserProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload common.APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := payload.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected response data: %#v", payload.Data)
	}
	providers, ok := data["providers"].([]any)
	if !ok {
		t.Fatalf("unexpected providers: %#v", data["providers"])
	}
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d: %#v", len(providers), providers)
	}
	provider, ok := providers[0].(map[string]any)
	if !ok || provider["name"] != "Qwen" {
		t.Fatalf("expected Qwen provider, got %#v", providers[0])
	}
}

func TestListUserProvidersReturnsCatalogDescriptionForRequestedLanguage(t *testing.T) {
	db := setupListProviderTestDB(t)
	if err := SeedModelCatalog(t.Context(), db, "../config/model_catalog.yaml"); err != nil {
		t.Fatalf("seed model catalog: %v", err)
	}
	if err := SeedDatasourceCatalog(t.Context(), db, "../config/datasource_catalog.yaml"); err != nil {
		t.Fatalf("seed datasource catalog: %v", err)
	}
	var providerCount int64
	if err := db.Model(&orm.DefaultModelProvider{}).Count(&providerCount).Error; err != nil {
		t.Fatalf("count default providers: %v", err)
	}
	if providerCount != 18 {
		t.Fatalf("expected 18 catalog providers, got %d", providerCount)
	}

	var defaultProvider orm.DefaultModelProvider
	if err := db.Where("name = ?", "Qwen").Take(&defaultProvider).Error; err != nil {
		t.Fatalf("load default provider: %v", err)
	}
	var descriptions map[string]string
	if err := json.Unmarshal(defaultProvider.DescriptionI18n, &descriptions); err != nil {
		t.Fatalf("decode catalog descriptions: %v", err)
	}
	if strings.TrimSpace(descriptions[common.LocaleZhCN]) == "" || strings.TrimSpace(descriptions[common.LocaleEnUS]) == "" {
		t.Fatalf("expected complete catalog descriptions, got %#v", descriptions)
	}

	now := time.Now()
	userProvider := orm.UserModelProvider{
		ID:                     "user-qwen",
		DefaultModelProviderID: defaultProvider.ID,
		Name:                   defaultProvider.Name,
		Description:            "legacy user description",
		BaseURL:                defaultProvider.BaseURL,
		Category:               defaultProvider.Category,
		Capabilities:           defaultProvider.Capabilities,
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
	if err := db.Create(&userProvider).Error; err != nil {
		t.Fatalf("create user provider: %v", err)
	}
	if err := db.Create(&orm.UserModelProviderGroup{
		ID:                  "qwen-group",
		UserModelProviderID: userProvider.ID,
		Name:                "Qwen",
		BaseURL:             defaultProvider.BaseURL,
		APIKey:              "secret",
		IsVerified:          true,
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}).Error; err != nil {
		t.Fatalf("create provider group: %v", err)
	}
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	assertDescription := func(t *testing.T, path, header, want string, handler http.HandlerFunc) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-User-Id", "user-1")
		if header != "" {
			req.Header.Set("Accept-Language", header)
		}
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Language"); got != common.NormalizeLocale(header) {
			t.Fatalf("unexpected Content-Language %q", got)
		}
		var payload struct {
			Data listResponse `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(payload.Data.Providers) != 1 || payload.Data.Providers[0].Description != want {
			t.Fatalf("expected description %q, got %#v", want, payload.Data.Providers)
		}
	}

	assertDescription(
		t,
		"/api/core/model_providers?category=model&keyword=Qwen",
		"en-US",
		descriptions[common.LocaleEnUS],
		ListUserProviders,
	)
	assertDescription(
		t,
		"/api/core/model_providers?category=model&keyword=Qwen",
		"fr-FR",
		descriptions[common.LocaleZhCN],
		ListUserProviders,
	)
	assertDescription(
		t,
		"/api/core/model_providers:with_groups",
		"en",
		descriptions[common.LocaleEnUS],
		ListUserProvidersWithGroups,
	)
	var unchangedUserProvider orm.UserModelProvider
	if err := db.Where("id = ?", userProvider.ID).Take(&unchangedUserProvider).Error; err != nil {
		t.Fatalf("reload user provider: %v", err)
	}
	if unchangedUserProvider.Description != "legacy user description" {
		t.Fatalf("user provider description should not be backfilled, got %q", unchangedUserProvider.Description)
	}
}

func TestBuildListItemsReturnsConfigurationFlagFromVerifiedGroups(t *testing.T) {
	db := setupListProviderTestDB(t)
	now := time.Now()
	rows := []orm.UserModelProvider{
		{
			ID:                     "provider-configured",
			DefaultModelProviderID: "default-configured",
			Name:                   "Bing",
			Description:            "Bing Search",
			BaseURL:                "https://api.bing.test/",
			Category:               "search",
			BaseModel: orm.BaseModel{
				CreateUserID: "user-1",
				CreatedAt:    now,
				UpdatedAt:    now,
			},
		},
		{
			ID:                     "provider-unverified",
			DefaultModelProviderID: "default-unverified",
			Name:                   "Tavily",
			Description:            "Tavily Search",
			BaseURL:                "https://api.tavily.test/",
			Category:               "search",
			BaseModel: orm.BaseModel{
				CreateUserID: "user-1",
				CreatedAt:    now,
				UpdatedAt:    now,
			},
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create providers: %v", err)
	}
	if err := db.Create(&orm.UserModelProviderGroup{
		ID:                  "group-configured",
		UserModelProviderID: "provider-configured",
		Name:                "Bing",
		BaseURL:             "https://api.bing.test/",
		APIKey:              "secret",
		IsVerified:          true,
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}).Error; err != nil {
		t.Fatalf("create verified group: %v", err)
	}
	if err := db.Create(&orm.UserModelProviderGroup{
		ID:                  "group-unverified",
		UserModelProviderID: "provider-unverified",
		Name:                "Tavily",
		BaseURL:             "https://api.tavily.test/",
		APIKey:              "secret",
		IsVerified:          false,
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}).Error; err != nil {
		t.Fatalf("create unverified group: %v", err)
	}

	items := buildListItems(t.Context(), db, rows, common.LocaleZhCN)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if !items[0].IsConfigured {
		t.Fatalf("expected configured provider to be marked configured: %#v", items[0])
	}
	if items[1].IsConfigured {
		t.Fatalf("expected provider without verified groups to be missing: %#v", items[1])
	}
}

func TestBuildListItemsAllowsVerifiedCustomBaseURLWithoutAPIKey(t *testing.T) {
	db := setupListProviderTestDB(t)
	now := time.Now()
	rows := []orm.UserModelProvider{
		{
			ID:                     "provider-default-empty-key",
			DefaultModelProviderID: "default-empty-key",
			Name:                   "Sciverse",
			Description:            "Sciverse Search",
			BaseURL:                "https://api.sciverse.space",
			Category:               "search",
			BaseModel: orm.BaseModel{
				CreateUserID: "user-1",
				CreatedAt:    now,
				UpdatedAt:    now,
			},
		},
		{
			ID:                     "provider-custom-empty-key",
			DefaultModelProviderID: "default-empty-key",
			Name:                   "Sciverse Local",
			Description:            "Sciverse Search",
			BaseURL:                "https://api.sciverse.space",
			Category:               "search",
			BaseModel: orm.BaseModel{
				CreateUserID: "user-1",
				CreatedAt:    now,
				UpdatedAt:    now,
			},
		},
	}
	if err := db.Create(&orm.DefaultModelProvider{
		ID:          "default-empty-key",
		Name:        "Sciverse",
		Description: "Sciverse Search",
		BaseURL:     "https://api.sciverse.space",
		Category:    "search",
		CreatedAt:   now,
		UpdatedAt:   now,
	}).Error; err != nil {
		t.Fatalf("create default provider: %v", err)
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create providers: %v", err)
	}
	if err := db.Create(&orm.UserModelProviderGroup{
		ID:                  "group-default-empty-key",
		UserModelProviderID: "provider-default-empty-key",
		Name:                "Sciverse",
		BaseURL:             "https://api.sciverse.space",
		APIKey:              "",
		IsVerified:          true,
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}).Error; err != nil {
		t.Fatalf("create verified group: %v", err)
	}
	if err := db.Create(&orm.UserModelProviderGroup{
		ID:                  "group-custom-empty-key",
		UserModelProviderID: "provider-custom-empty-key",
		Name:                "Sciverse Local",
		BaseURL:             "http://localhost:9000/search",
		APIKey:              "",
		IsVerified:          true,
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}).Error; err != nil {
		t.Fatalf("create custom verified group: %v", err)
	}

	items := buildListItems(t.Context(), db, rows, common.LocaleZhCN)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].IsConfigured {
		t.Fatalf("expected default base URL with empty key to be missing: %#v", items[0])
	}
	if !items[1].IsConfigured {
		t.Fatalf("expected custom base URL with empty key to be configured: %#v", items[1])
	}
}

func TestBuildListItemsAddsMinerULocalPresetWhenConfigured(t *testing.T) {
	t.Setenv("LAZYMIND_DEPLOY_MINERU", "1")
	_ = os.Unsetenv("LAZYMIND_OCR_SERVER_TYPE")

	items := buildListItems(t.Context(), nil, []orm.UserModelProvider{
		{
			ID:                     "provider-mineru",
			DefaultModelProviderID: "default-mineru",
			Name:                   "MinerU",
			Description:            "MinerU OCR",
			BaseURL:                "https://mineru.net/api/v4/",
			Category:               "ocr",
		},
	}, common.LocaleZhCN)

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if len(items[0].BaseURLPresets) != 2 {
		t.Fatalf("expected 2 presets, got %#v", items[0].BaseURLPresets)
	}
	if items[0].BaseURLPresets[0].Key != "official" || items[0].BaseURLPresets[1].Key != "local" {
		t.Fatalf("unexpected preset order: %#v", items[0].BaseURLPresets)
	}
}

func TestBuildListItemsOmitsMinerULocalPresetWithoutConfiguredURL(t *testing.T) {
	_ = os.Unsetenv("LAZYMIND_DEPLOY_MINERU")
	_ = os.Unsetenv("LAZYMIND_OCR_SERVER_TYPE")

	items := buildListItems(t.Context(), nil, []orm.UserModelProvider{
		{
			ID:                     "provider-mineru",
			DefaultModelProviderID: "default-mineru",
			Name:                   "MinerU",
			Description:            "MinerU OCR",
			BaseURL:                "https://mineru.net/api/v4/",
			Category:               "ocr",
		},
	}, common.LocaleZhCN)

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if len(items[0].BaseURLPresets) != 1 {
		t.Fatalf("expected only official preset, got %#v", items[0].BaseURLPresets)
	}
	if items[0].BaseURLPresets[0].Key != "official" {
		t.Fatalf("expected official preset, got %#v", items[0].BaseURLPresets)
	}
}
