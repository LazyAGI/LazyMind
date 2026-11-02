package modelprovider

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestServiceProfileRuntimeAdapters(t *testing.T) {
	for _, tc := range []struct{ name, adapter string }{
		{"DoubaoCodingPlan", "OpenAI"}, {"DoubaoAgentPlan", "OpenAI"},
		{"SenseNovaTokenPlan", "OpenAI"}, {"QwenCodingPlan", "OpenAI"},
		{"QwenTokenPlan", "OpenAI"}, {"QwenTokenPlanTeam", "OpenAI"},
		{"GLMCodingPlan", "OpenAI"}, {"KimiCode", "Claude"}, {"MiniMaxMPlan", "OpenAI"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := orm.UserModelProvider{Name: tc.name}
			if got := RuntimeProviderName(row); got != tc.adapter {
				t.Fatalf("adapter = %q, want %q", got, tc.adapter)
			}
			row.ProtocolName = "Claude"
			if got := RuntimeProviderName(row); got != "Claude" {
				t.Fatalf("explicit protocol lost: %q", got)
			}
			row.IsCustom, row.ProtocolName = true, "OpenAI"
			if got := RuntimeProviderName(row); got != "OpenAI" {
				t.Fatalf("custom protocol lost: %q", got)
			}
		})
	}
}

func TestServiceProfileListIdentitySurvivesProtocolChange(t *testing.T) {
	for _, custom := range []bool{false, true} {
		rows := buildListItems(t.Context(), nil, []orm.UserModelProvider{{Name: "QwenTokenPlanTeam", IsCustom: custom, ProtocolName: "Claude"}}, "zh-CN")
		encoded, err := json.Marshal(rows[0])
		if err != nil {
			t.Fatal(err)
		}
		var item map[string]any
		if err := json.Unmarshal(encoded, &item); err != nil {
			t.Fatal(err)
		}
		if custom {
			if item["brand_key"] != nil || item["service_key"] != nil {
				t.Fatalf("custom grouped as brand: %s", encoded)
			}
		} else if item["brand_key"] != "Qwen" || item["service_key"] != "token_team" {
			t.Fatalf("service identity absent/changed with protocol: %s", encoded)
		}
	}
}

func TestServiceProfileCatalogHasIndependentPlanTemplates(t *testing.T) {
	raw, err := os.ReadFile("../config/model_catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := loadModelCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]catalogSupplier{}
	for _, supplier := range catalog["model_providers"].Suppliers {
		byName[supplier.Name] = supplier
	}
	for _, name := range []string{"DoubaoCodingPlan", "DoubaoAgentPlan", "SenseNovaTokenPlan", "QwenCodingPlan", "QwenTokenPlan", "QwenTokenPlanTeam", "GLMCodingPlan", "KimiCode", "MiniMaxMPlan"} {
		supplier, ok := byName[name]
		if !ok {
			t.Errorf("missing independent service %s", name)
			continue
		}
		if supplier.BaseURL == "" || supplier.Description["zh-CN"] == "" || supplier.Description["en-US"] == "" {
			t.Errorf("incomplete service %s", name)
		}
		if len(supplier.Models) != 0 {
			t.Errorf("%s must not borrow a standard catalog", name)
		}
	}
}

func TestServiceProfileSyncRebindsLegacyWithoutReplacingConfiguration(t *testing.T) {
	for _, tc := range []struct{ brand, plan, endpoint string }{
		{"Doubao", "DoubaoCodingPlan", "https://ark.cn-beijing.volces.com/api/coding/v3/"},
		{"Doubao", "DoubaoAgentPlan", "https://ark.cn-beijing.volces.com/api/plan/"},
		{"SenseNova", "SenseNovaTokenPlan", "https://token.sensenova.cn/v1/"},
		{"Qwen", "QwenCodingPlan", "https://coding.dashscope.aliyuncs.com/v1/"},
		{"GLM", "GLMCodingPlan", "https://open.bigmodel.cn/api/coding/paas/v4/"},
		{"Kimi", "KimiCode", "https://api.kimi.com/coding/"},
	} {
		t.Run(tc.plan, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.DefaultModelProvider{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}).DB
			now := time.Now().UTC()
			defs := []orm.DefaultModelProvider{{ID: "standard", Name: tc.brand, Category: "model", BaseURL: "https://standard.example/v1/", CreatedAt: now, UpdatedAt: now}, {ID: "plan", Name: tc.plan, Category: "model", BaseURL: tc.endpoint, CreatedAt: now, UpdatedAt: now}}
			if err := db.Create(&defs).Error; err != nil {
				t.Fatal(err)
			}
			base := orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}
			row := orm.UserModelProvider{ID: "saved-provider", DefaultModelProviderID: "standard", Name: tc.brand, ProtocolName: "Claude", RequestConfig: `{"default_protocol":"anthropic","endpoints":{"anthropic":"https://private.example/custom"}}`, BaseURL: tc.endpoint, ActiveKeyID: "saved-key", ActiveKeyGroupID: "saved-group", ActiveKeyInitialized: true, ActiveKeyRevision: 9, Category: "model", BaseModel: base}
			group := orm.UserModelProviderGroup{ID: "saved-group", UserModelProviderID: row.ID, Name: "my configured service", BaseURL: tc.endpoint, APIKeyCiphertext: "existing-encrypted-key", CredentialRevision: 8, IsVerified: true, BaseModel: base}
			context := "262144"
			model := orm.UserModelProviderGroupModel{ID: "saved-model", UserModelProviderID: row.ID, UserModelProviderGroupID: group.ID, ProviderName: "Claude", Name: "hand-set", ModelType: "llm", MaxInputTokens: &context, BaseModel: base}
			for _, fixture := range []any{&row, &group, &model} {
				if err := db.Create(fixture).Error; err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := syncUserProvidersFromDefaults(t.Context(), db, "owner", ""); err != nil {
					t.Fatal(err)
				}
			}
			var got orm.UserModelProvider
			if err := db.Take(&got, "id = ?", row.ID).Error; err != nil {
				t.Fatal(err)
			}
			if got.DefaultModelProviderID != "plan" || got.Name != tc.plan {
				t.Fatalf("legacy identity not rebound in place: %#v", got)
			}
			if got.BaseURL != row.BaseURL || got.RequestConfig != row.RequestConfig || got.ProtocolName != row.ProtocolName || got.ActiveKeyID != row.ActiveKeyID || got.ActiveKeyGroupID != row.ActiveKeyGroupID || got.ActiveKeyRevision != row.ActiveKeyRevision {
				t.Fatal("service migration modified saved connection configuration")
			}
			var gotGroup orm.UserModelProviderGroup
			if err := db.Take(&gotGroup, "id = ?", group.ID).Error; err != nil {
				t.Fatal(err)
			}
			if gotGroup.APIKeyCiphertext != group.APIKeyCiphertext || gotGroup.CredentialRevision != group.CredentialRevision || !gotGroup.IsVerified || gotGroup.BaseURL != group.BaseURL {
				t.Fatal("service migration modified credential group")
			}
			var gotModel orm.UserModelProviderGroupModel
			if err := db.Take(&gotModel, "id = ?", model.ID).Error; err != nil {
				t.Fatal(err)
			}
			if gotModel.UserModelProviderID != row.ID || gotModel.MaxInputTokens == nil || *gotModel.MaxInputTokens != context {
				t.Fatal("service migration replaced model or context")
			}
			var count int64
			db.Model(&orm.UserModelProvider{}).Where("create_user_id = ? AND deleted_at IS NULL", "owner").Count(&count)
			if count != 2 {
				t.Fatalf("sync not idempotent / missing new standard leaf: %d", count)
			}
		})
	}
}

func TestServiceProfileSyncConflictAndCustomDoNotMerge(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.DefaultModelProvider{}, &orm.UserModelProvider{}).DB
	now := time.Now().UTC()
	defs := []orm.DefaultModelProvider{{ID: "standard", Name: "Doubao", Category: "model", CreatedAt: now, UpdatedAt: now}, {ID: "plan", Name: "DoubaoCodingPlan", Category: "model", CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&defs).Error; err != nil {
		t.Fatal(err)
	}
	rows := []orm.UserModelProvider{
		{ID: "legacy", DefaultModelProviderID: "standard", Name: "Doubao", BaseURL: "https://ark.cn-beijing.volces.com/api/coding/v3/", ActiveKeyID: "old-key", BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		{ID: "existing-plan", DefaultModelProviderID: "plan", Name: "DoubaoCodingPlan", ActiveKeyID: "plan-key", BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		{ID: "custom", DefaultModelProviderID: "standard", Name: "Doubao", IsCustom: true, BaseURL: "https://ark.cn-beijing.volces.com/api/coding/v3/", ActiveKeyID: "custom-key", BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		{ID: "other-owner", DefaultModelProviderID: "standard", Name: "Doubao", BaseURL: "https://ark.cn-beijing.volces.com/api/coding/v3/", ActiveKeyID: "foreign-key", BaseModel: orm.BaseModel{CreateUserID: "other"}},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := syncUserProvidersFromDefaults(t.Context(), db, "owner", ""); err != nil {
		t.Fatal(err)
	}
	for _, original := range rows {
		var got orm.UserModelProvider
		if err := db.Take(&got, "id = ?", original.ID).Error; err != nil {
			t.Fatal(err)
		}
		if got.DefaultModelProviderID != original.DefaultModelProviderID || got.Name != original.Name || got.ActiveKeyID != original.ActiveKeyID {
			t.Fatalf("conflict/custom/foreign credentials changed: %s", original.ID)
		}
	}
}

func TestServiceProfileProtocolRootsAreOfficialAndIsolated(t *testing.T) {
	for _, tc := range []struct{ base, openai, anthropic string }{
		{"https://ark.cn-beijing.volces.com/api/plan/v3/", "https://ark.cn-beijing.volces.com/api/plan/v3/", "https://ark.cn-beijing.volces.com/api/plan"},
		{"https://token.sensenova.cn/v1/", "https://token.sensenova.cn/v1/", "https://token.sensenova.cn"},
		{"https://coding.dashscope.aliyuncs.com/apps/anthropic", "https://coding.dashscope.aliyuncs.com/v1/", "https://coding.dashscope.aliyuncs.com/apps/anthropic"},
		{"https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1/", "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1/", "https://token-plan.cn-beijing.maas.aliyuncs.com/apps/anthropic"},
		{"https://api.kimi.com/coding/", "https://api.kimi.com/coding/v1/", "https://api.kimi.com/coding/"},
	} {
		got, ok := knownSupplierProtocolDefaults(tc.base)
		if !ok || got["openai"] != tc.openai || got["anthropic"] != tc.anthropic {
			t.Errorf("official mapping %s: %#v (%v)", tc.base, got, ok)
		}
	}
	for _, base := range []string{"https://gateway.example/api/plan/v3/", "https://ark.cn-beijing.volces.com/tenant/api/plan/v3/", "https://coding.dashscope.aliyuncs.com/tenant/v1/", "https://api.kimi.com/private/coding/", "http://api.kimi.com/coding/", "https://token.sensenova.cn/v1/?tenant=1"} {
		if got, ok := knownSupplierProtocolDefaults(base); ok {
			t.Errorf("nonofficial contract was rewritten: %s %#v", base, got)
		}
	}
}

func TestServiceProfileSharedAnthropicRootUsesExplicitService(t *testing.T) {
	for _, tc := range []struct{ name, openai string }{
		{"GLM", "https://open.bigmodel.cn/api/paas/v4/"},
		{"GLMCodingPlan", "https://open.bigmodel.cn/api/coding/paas/v4/"},
	} {
		got := providerRequestDefaults(nil, orm.UserModelProvider{Name: tc.name, ProtocolName: "Claude", BaseURL: "https://open.bigmodel.cn/api/anthropic"})
		if got["openai"] != tc.openai {
			t.Errorf("%s selected wrong service root: %#v", tc.name, got)
		}
	}
}

func TestServiceProfileLegacyMigrationPreservesEffectiveProtocol(t *testing.T) {
	for _, tc := range []struct{ brand, plan, endpoint, adapter string }{
		{"Doubao", "DoubaoCodingPlan", "https://ark.cn-beijing.volces.com/api/coding/", "Claude"},
		{"Doubao", "DoubaoAgentPlan", "https://ark.cn-beijing.volces.com/api/plan", "Claude"},
		{"Qwen", "QwenCodingPlan", "https://coding.dashscope.aliyuncs.com/apps/anthropic", "Claude"},
		{"Qwen", "QwenCodingPlan", "https://coding.dashscope.aliyuncs.com/v1/", "OpenAI"},
		{"SenseNova", "SenseNovaTokenPlan", "https://token.sensenova.cn/v1/", "OpenAI"},
		{"SenseNova", "SenseNovaTokenPlan", "https://token.sensenova.cn/v1/chat/completions", "OpenAI"},
		{"SenseNova", "SenseNovaTokenPlan", "https://token.sensenova.cn/", "OpenAI"},
		{"Kimi", "KimiCode", "https://api.kimi.com/coding/v1/", "OpenAI"},
		{"Kimi", "KimiCode", "https://api.kimi.com/coding/", "Claude"},
	} {
		t.Run(tc.plan+"/"+tc.adapter, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.DefaultModelProvider{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroupModel{}).DB
			now := time.Now().UTC()
			defs := []orm.DefaultModelProvider{{ID: "standard", Name: tc.brand, Category: "model", CreatedAt: now, UpdatedAt: now}, {ID: "plan", Name: tc.plan, Category: "model", CreatedAt: now, UpdatedAt: now}}
			if err := db.Create(&defs).Error; err != nil {
				t.Fatal(err)
			}
			base := orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}
			row := orm.UserModelProvider{ID: "original-provider", DefaultModelProviderID: "standard", Name: tc.brand, ProtocolName: tc.brand, BaseURL: tc.endpoint, Category: "model", BaseModel: base}
			model := orm.UserModelProviderGroupModel{ID: "original-model", UserModelProviderID: row.ID, UserModelProviderGroupID: "group", Name: "configured-model", ModelType: "llm", ProviderName: tc.brand, BaseModel: base}
			for _, fixture := range []any{&row, &model} {
				if err := db.Create(fixture).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := syncUserProvidersFromDefaults(t.Context(), db, "owner", ""); err != nil {
				t.Fatal(err)
			}
			if err := db.Take(&row, "id = ?", row.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Take(&model, "id = ?", model.ID).Error; err != nil {
				t.Fatal(err)
			}
			if row.Name != tc.plan || row.BaseURL != tc.endpoint || RuntimeProviderName(row) != tc.adapter || model.ProviderName != tc.adapter {
				t.Fatalf("migration lost effective protocol: parent=%s model=%s want=%s", RuntimeProviderName(row), model.ProviderName, tc.adapter)
			}
			if tc.plan == "SenseNovaTokenPlan" {
				if got := LazyLLMBaseURL(RuntimeProviderName(row), row.BaseURL); got != "https://token.sensenova.cn/v1/" {
					t.Fatalf("legacy Token Plan runtime root = %q", got)
				}
			}
		})
	}
}

func TestServiceProfileCredentialNamespacesAndAmbiguousLegacyRoots(t *testing.T) {
	for name, profile := range providerServiceProfiles {
		row := orm.UserModelProvider{Name: name, ProtocolName: "Claude"}
		brand, service := ProviderServiceIdentity(row)
		if brand != profile.brand || service != profile.service || CatalogCredentialKey(row) != name {
			t.Fatalf("explicit identity changed for %s", name)
		}
		row.IsCustom = true
		brand, service = ProviderServiceIdentity(row)
		if brand != "" || service != "" || CatalogCredentialKey(row) != "Claude" {
			t.Fatalf("custom inherited builtin identity %s", name)
		}
	}
	for _, tc := range []struct{ key, base string }{
		{"Qwen", "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1/"},
		{"Minimax", "https://api.minimax.cn/v1/"},
		{"GLM", "https://open.bigmodel.cn/api/anthropic"},
		{"Doubao", "https://proxy.example/api/coding/v3/"},
		{"Doubao", "https://ark.cn-beijing.volces.com/private/api/coding/v3/"},
		{"Doubao", "https://ark.cn-beijing.volces.com/api%2Fcoding/v3/"},
		{"Doubao", "https://ark.cn-beijing.volces.com/api/coding/v3/?tenant=1"},
		{"QwenTokenPlanTeam", "https://coding.dashscope.aliyuncs.com/v1/"},
	} {
		if got := ResolveServiceCatalogKey(tc.key, tc.base); got != tc.key {
			t.Errorf("ambiguous/explicit/proxy identity guessed %q -> %q", tc.key, got)
		}
	}
}

func TestServiceProfileTokenPlanDoesNotAdvertiseUnsupportedAnthropicAuth(t *testing.T) {
	response := requestConfigResponse(nil, orm.UserModelProvider{Name: "SenseNovaTokenPlan", BaseURL: "https://token.sensenova.cn/v1/"})
	for _, protocol := range response.Protocols {
		if protocol.Protocol == "anthropic" && protocol.Supported {
			t.Fatal("runtime only sends x-api-key, but Token Plan requires Bearer")
		}
		if protocol.Protocol == "openai" && !protocol.Supported {
			t.Fatal("OpenAI protocol should remain available")
		}
	}
}

func TestServiceProfileDoesNotOfferUnrelatedProviderRootPresets(t *testing.T) {
	for name, profile := range providerServiceProfiles {
		if profile.service == "standard" {
			continue
		}
		if presets := buildBaseURLPresets(orm.UserModelProvider{Name: name, BaseURL: "https://configured.example/v1/"}); len(presets) != 0 {
			t.Errorf("%s exposes unrelated suppliers' address presets: %#v", name, presets)
		}
	}
}

func TestServiceProfileMigrationRejectsStaleConfigurationSnapshot(t *testing.T) {
	for _, changed := range []string{"base_url", "protocol_name", "request_config", "name", "default_model_provider_id"} {
		t.Run(changed, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.DefaultModelProvider{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroupModel{}).DB
			now := time.Now().UTC()
			defs := []orm.DefaultModelProvider{{ID: "standard", Name: "Qwen", Category: "model", CreatedAt: now, UpdatedAt: now}, {ID: "plan", Name: "QwenCodingPlan", Category: "model", CreatedAt: now, UpdatedAt: now}}
			if err := db.Create(&defs).Error; err != nil {
				t.Fatal(err)
			}
			base := orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}
			snapshot := orm.UserModelProvider{ID: "legacy", DefaultModelProviderID: "standard", Name: "Qwen", ProtocolName: "Qwen", BaseURL: "https://coding.dashscope.aliyuncs.com/v1/", ActiveKeyID: "saved-key", Category: "model", BaseModel: base}
			if err := db.Create(&snapshot).Error; err != nil {
				t.Fatal(err)
			}
			model := orm.UserModelProviderGroupModel{ID: "model", UserModelProviderID: snapshot.ID, UserModelProviderGroupID: "group", Name: "saved-model", ModelType: "llm", ProviderName: "Qwen", BaseModel: base}
			if err := db.Create(&model).Error; err != nil {
				t.Fatal(err)
			}
			values := map[string]string{"base_url": "https://coding.dashscope.aliyuncs.com/apps/anthropic", "protocol_name": "Claude", "request_config": `{"default_protocol":"anthropic"}`, "name": "QwenTokenPlanTeam", "default_model_provider_id": "newer-template"}
			if err := db.Model(&orm.UserModelProvider{}).Where("id = ?", snapshot.ID).Update(changed, values[changed]).Error; err != nil {
				t.Fatal(err)
			}
			var committed orm.UserModelProvider
			if err := db.Take(&committed, "id = ?", snapshot.ID).Error; err != nil {
				t.Fatal(err)
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				return rebindLegacyServiceProviders(tx, []orm.UserModelProvider{snapshot}, defs, now)
			})
			if err == nil {
				t.Fatalf("stale %s snapshot overwrote a newer user configuration", changed)
			}
			var got orm.UserModelProvider
			if err := db.Take(&got, "id = ?", snapshot.ID).Error; err != nil {
				t.Fatal(err)
			}
			if got.Name != committed.Name || got.DefaultModelProviderID != committed.DefaultModelProviderID || got.ProtocolName != committed.ProtocolName || got.RequestConfig != committed.RequestConfig || got.BaseURL != committed.BaseURL || got.ActiveKeyID != committed.ActiveKeyID {
				t.Fatal("newer committed configuration changed")
			}
			if err := db.Take(&model, "id = ?", model.ID).Error; err != nil {
				t.Fatal(err)
			}
			if model.ProviderName != "Qwen" {
				t.Fatal("stale migration changed model adapter")
			}
		})
	}
}
