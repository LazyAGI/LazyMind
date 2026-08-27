package modelconfig

import (
	"context"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/modelprovider"
)

func TestBuildLLMConfigAddsOpenCodeDescriptor(t *testing.T) {
	config := BuildLLMConfig([]SelectedRuntimeModel{{
		ModelType: "evo_llm", TechnicalModelType: "vlm",
		ProviderName: "OpenAI", ModelName: "gpt-4o-mini",
		BaseURL: "https://api.openai.com/v1/", APIKey: "secret",
	}})
	role, ok := config["evo_llm"].(map[string]any)
	if !ok {
		t.Fatalf("missing evo_llm config: %#v", config)
	}
	descriptor, ok := role["opencode"].(modelprovider.OpenCodeModelDescriptor)
	if !ok || descriptor.Model != "openai/gpt-4o-mini" {
		t.Fatalf("unexpected OpenCode descriptor: %#v", role["opencode"])
	}
}

func TestBuildLLMConfigDropsIneligibleEvoModel(t *testing.T) {
	config := BuildLLMConfig([]SelectedRuntimeModel{{
		ModelType: "evo_llm", TechnicalModelType: "vlm",
		ProviderName: "Unknown", ModelName: "gpt-4o-mini",
		BaseURL: "https://example.com/v1", APIKey: "secret",
	}})
	if config != nil {
		t.Fatalf("ineligible evo model must not reach runtime: %#v", config)
	}
}

func TestLoadLLMConfigSkipsStaleOwnEvoAndUsesEligibleSharedSelection(t *testing.T) {
	db := orm.MigrateTestDB(t,
		&orm.UserSelectedModel{},
		&orm.UserModelProviderGroupModel{},
		&orm.UserModelProviderGroup{},
	)
	now := time.Now().UTC()
	seed := func(userID, suffix, provider, model, technicalType string, shared bool) {
		group := orm.UserModelProviderGroup{
			ID: "group-" + suffix, UserModelProviderID: "provider-" + suffix,
			Name: suffix, BaseURL: "https://api.openai.com/v1/", APIKey: "sk-" + suffix,
			IsVerified: true,
			BaseModel:  orm.BaseModel{CreateUserID: userID, CreatedAt: now, UpdatedAt: now},
		}
		modelRow := orm.UserModelProviderGroupModel{
			ID: "model-" + suffix, UserModelProviderID: group.UserModelProviderID,
			UserModelProviderGroupID: group.ID, ProviderName: provider,
			Name: model, ModelType: technicalType,
			BaseModel: orm.BaseModel{CreateUserID: userID, CreatedAt: now, UpdatedAt: now},
		}
		selection := orm.UserSelectedModel{
			UserID: userID, ModelKey: "evo_llm",
			UserModelProviderGroupModelID: modelRow.ID, Share: shared,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := db.DB.Create(&group).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.DB.Create(&modelRow).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.DB.Create(&selection).Error; err != nil {
			t.Fatal(err)
		}
	}

	seed("user-1", "stale", "OpenAI", "not-supported", "llm", false)
	seed("admin-1", "shared", "OpenAI", "gpt-4o-mini", "vlm", true)

	config, err := LoadLLMConfig(context.Background(), db.DB, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	role, ok := config["evo_llm"].(map[string]any)
	if !ok || role["model"] != "gpt-4o-mini" {
		t.Fatalf("expected eligible shared evo model, got %#v", config)
	}
}
