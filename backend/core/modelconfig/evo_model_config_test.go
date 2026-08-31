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

func TestBuildLLMConfigAddsCustomChatModelForEvo(t *testing.T) {
	config := BuildLLMConfig([]SelectedRuntimeModel{{
		ModelType: "evo_llm", TechnicalModelType: "llm", IsDefault: false,
		ProviderName: "OpenAI", ModelName: "deepseek-v4-flash",
		BaseURL: "https://gateway.example.com/v1/", APIKey: "secret",
	}})
	role, ok := config["evo_llm"].(map[string]any)
	if !ok {
		t.Fatalf("missing custom evo_llm config: %#v", config)
	}
	descriptor, ok := role["opencode"].(modelprovider.OpenCodeModelDescriptor)
	if !ok || descriptor.Model != "openai/deepseek-v4-flash" {
		t.Fatalf("unexpected custom OpenCode descriptor: %#v", role["opencode"])
	}
}

func TestBuildLLMConfigNormalizesOnlyOpenAIBaseURLForLazyLLM(t *testing.T) {
	config := BuildLLMConfig([]SelectedRuntimeModel{
		{
			ModelType: "llm", ProviderName: "OpenAI", ModelName: "private-model",
			BaseURL: "http://127.0.0.1:8000/chat/completions", APIKey: "secret",
		},
		{
			ModelType: "vlm", ProviderName: "Qwen", ModelName: "qwen-vl",
			BaseURL: "https://models.example.com/custom/path", APIKey: "secret",
		},
	})
	llm := config["llm"].(map[string]any)
	if llm["source"] != "openai" {
		t.Fatalf("OpenAI source = %q", llm["source"])
	}
	if llm["base_url"] != "http://127.0.0.1:8000/v1/" {
		t.Fatalf("OpenAI base_url = %q", llm["base_url"])
	}
	vlm := config["vlm"].(map[string]any)
	if vlm["source"] != "qwen" {
		t.Fatalf("proxied Qwen source = %q", vlm["source"])
	}
	if vlm["base_url"] != "https://models.example.com/custom/path" {
		t.Fatalf("non-OpenAI base_url changed: %q", vlm["base_url"])
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
	seed := func(userID, suffix, provider, model, technicalType string, isDefault, shared bool) {
		group := orm.UserModelProviderGroup{
			ID: "group-" + suffix, UserModelProviderID: "provider-" + suffix,
			Name: suffix, BaseURL: "https://api.openai.com/v1/", APIKey: "sk-" + suffix,
			IsVerified: true,
			BaseModel:  orm.BaseModel{CreateUserID: userID, CreatedAt: now, UpdatedAt: now},
		}
		modelRow := orm.UserModelProviderGroupModel{
			ID: "model-" + suffix, UserModelProviderID: group.UserModelProviderID,
			UserModelProviderGroupID: group.ID, ProviderName: provider,
			Name: model, ModelType: technicalType, IsDefault: isDefault,
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

	seed("user-1", "stale", "OpenAI", "not-supported", "llm", true, false)
	seed("admin-1", "shared", "OpenAI", "gpt-4o-mini", "vlm", true, true)

	config, err := LoadLLMConfig(context.Background(), db.DB, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	role, ok := config["evo_llm"].(map[string]any)
	if !ok || role["model"] != "gpt-4o-mini" {
		t.Fatalf("expected eligible shared evo model, got %#v", config)
	}
}
