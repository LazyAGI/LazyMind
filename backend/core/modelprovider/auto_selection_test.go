package modelprovider

import (
	"reflect"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestAutoSelectUnconfiguredProviderModelsSkipsExistingAndReportsMissing(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:auto_select_unconfigured_provider?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now().UTC()
	if err := db.Create(&orm.UserSelectedModel{
		UserID: "user-1", UserName: "User One", ModelKey: "llm",
		UserModelProviderGroupModelID: "existing-llm", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create existing selection: %v", err)
	}
	models := []orm.UserModelProviderGroupModel{
		{ID: "llm-first", Name: "llm-catalog-first", ModelType: "llm"},
		{ID: "llm-second", Name: "llm-catalog-second", ModelType: "llm"},
		{ID: "vlm-first", Name: "vlm-catalog-first", ModelType: "vlm"},
		{ID: "image-first", Name: "image-catalog-first", ModelType: "text2image"},
	}
	result, err := autoSelectUnconfiguredProviderModels(
		db, "user-1", "User One", "Example", "https://example.com/v1/", models, now,
	)
	if err != nil {
		t.Fatalf("auto select: %v", err)
	}

	wantConfigured := []autoSelectedModel{
		{ModelKey: "vlm", Name: "vlm-catalog-first"},
		{ModelKey: "image_generator", Name: "image-catalog-first"},
	}
	if !reflect.DeepEqual(result.Configured, wantConfigured) {
		t.Fatalf("configured = %#v, want %#v", result.Configured, wantConfigured)
	}
	if !reflect.DeepEqual(result.Missing, []string{"embed_main"}) {
		t.Fatalf("missing = %#v, want embed_main", result.Missing)
	}

	var selections []orm.UserSelectedModel
	if err := db.Order("model_type ASC").Find(&selections).Error; err != nil {
		t.Fatalf("load selections: %v", err)
	}
	if len(selections) != 3 {
		t.Fatalf("selection count = %d, want 3", len(selections))
	}
	selectedIDs := map[string]string{}
	for _, selection := range selections {
		selectedIDs[selection.ModelKey] = selection.UserModelProviderGroupModelID
	}
	if selectedIDs["llm"] != "existing-llm" ||
		selectedIDs["vlm"] != "vlm-first" ||
		selectedIDs["text2image"] != "image-first" {
		t.Fatalf("selected model ids = %#v", selectedIDs)
	}
}

func TestAutoSelectUnconfiguredProviderModelsPrefersVerifiedFreeModels(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:auto_select_free_provider_models?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&orm.UserSelectedModel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	models := []orm.UserModelProviderGroupModel{
		{ID: "paid-llm", Name: "deepseek-ai/DeepSeek-V4-Flash", ModelType: "llm"},
		{ID: "free-llm", Name: "deepseek-ai/DeepSeek-V3", ModelType: "llm", FreeAutoSelectPriority: 1},
		{ID: "paid-vlm", Name: "Pro/moonshotai/Kimi-K2.6", ModelType: "vlm"},
		{ID: "free-vlm", Name: "Qwen/Qwen3.5-4B", ModelType: "vlm", FreeAutoSelectPriority: 1},
		{ID: "paid-embed", Name: "Qwen/Qwen3-Embedding-8B", ModelType: "embed"},
		{ID: "free-embed", Name: "BAAI/bge-m3", ModelType: "embed", FreeAutoSelectPriority: 1},
		{ID: "paid-image", Name: "Qwen/Qwen-Image", ModelType: "text2image"},
		{ID: "free-image", Name: "Kwai-Kolors/Kolors", ModelType: "image_editing", FreeAutoSelectPriority: 1},
	}
	result, err := autoSelectUnconfiguredProviderModels(
		db, "user-1", "User One", "SiliconFlow", "https://api.siliconflow.cn/v1/", models, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("auto select: %v", err)
	}
	wantConfigured := []autoSelectedModel{
		{ModelKey: "llm", Name: "deepseek-ai/DeepSeek-V3"},
		{ModelKey: "vlm", Name: "Qwen/Qwen3.5-4B"},
		{ModelKey: "embed_main", Name: "BAAI/bge-m3"},
		{ModelKey: "image_generator", Name: "Kwai-Kolors/Kolors"},
	}
	if !reflect.DeepEqual(result.Configured, wantConfigured) || len(result.Missing) != 0 {
		t.Fatalf("result = %#v, want configured %#v and no missing roles", result, wantConfigured)
	}

	var selections []orm.UserSelectedModel
	if err := db.Find(&selections).Error; err != nil {
		t.Fatalf("load selections: %v", err)
	}
	selectedIDs := map[string]string{}
	for _, selection := range selections {
		selectedIDs[selection.ModelKey] = selection.UserModelProviderGroupModelID
	}
	wantIDs := map[string]string{
		"llm": "free-llm", "vlm": "free-vlm", "embed_main": "free-embed", "image_editing": "free-image",
	}
	if !reflect.DeepEqual(selectedIDs, wantIDs) {
		t.Fatalf("selected model ids = %#v, want %#v", selectedIDs, wantIDs)
	}
}

func TestPreferredAutoModelScopesSenseNovaPreferencesToTokenPlan(t *testing.T) {
	models := []orm.UserModelProviderGroupModel{
		{ID: "classic-first", Name: "SenseChat-5", ModelType: "llm"},
		{
			ID: "token-plan", Name: "sensenova-6.7-flash-lite", ModelType: "llm",
			FreeAutoSelectPriority: 1,
			FreeAutoSelectBaseURLs: `["https://token.sensenova.cn/v1/chat/completions"]`,
		},
	}
	classic, ok := preferredAutoModel(
		"https://api.sensenova.cn/compatible-mode/v1/", []string{"llm"}, models,
	)
	if !ok || classic.ID != "classic-first" {
		t.Fatalf("classic selection = %#v, want catalog-order fallback", classic)
	}
	tokenPlan, ok := preferredAutoModel(
		sensenovaNewPlatformBaseURL, []string{"llm"}, models,
	)
	if !ok || tokenPlan.ID != "token-plan" {
		t.Fatalf("Token Plan selection = %#v, want free Token Plan model", tokenPlan)
	}
}

func TestAutoSelectUnconfiguredProviderModelsDoesNothingWhenAllRolesExist(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:auto_select_all_configured?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&orm.UserSelectedModel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now().UTC()
	for _, slot := range autoModelSlots {
		if err := db.Create(&orm.UserSelectedModel{
			UserID: "user-1", UserName: "User One", ModelKey: slot.ModelKey,
			UserModelProviderGroupModelID: "existing-" + slot.ModelKey,
			CreatedAt:                     now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatalf("create %s selection: %v", slot.ModelKey, err)
		}
	}

	result, err := autoSelectUnconfiguredProviderModels(
		db, "user-1", "User One", "Another Provider", "https://example.com/v1/", nil, now,
	)
	if err != nil {
		t.Fatalf("auto select: %v", err)
	}
	if len(result.Configured) != 0 || len(result.Missing) != 0 {
		t.Fatalf("unexpected result when all roles exist: %#v", result)
	}
}
