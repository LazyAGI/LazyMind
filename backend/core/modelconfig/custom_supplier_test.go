package modelconfig

import (
	"context"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestCustomSuppliersKeepIndependentCredentialsAndTechnicalAdapter(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{})
	base := orm.BaseModel{CreateUserID: "owner", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	contextWindow, inputLimit, outputLimit := "1M", "128K", "32K"
	for _, fixture := range []struct{ id, display, role string }{{"a", "My proxy", "llm"}, {"b", "Other proxy", "vlm"}} {
		for _, row := range []any{
			&orm.UserModelProvider{ID: fixture.id, DefaultModelProviderID: "catalog", Name: fixture.display, IsCustom: true, ProtocolName: "OpenAI", Category: "model", BaseURL: "https://" + fixture.id + ".example/v1/", BaseModel: base},
			&orm.UserModelProviderGroup{ID: fixture.id, UserModelProviderID: fixture.id, Name: fixture.display, BaseURL: "https://" + fixture.id + ".example/v1/", APIKey: fixture.id + "-key\n" + fixture.id + "-disabled-key", IsVerified: true, BaseModel: base},
			&orm.UserModelProviderGroupModel{ID: fixture.id, UserModelProviderID: fixture.id, UserModelProviderGroupID: fixture.id, ProviderName: "OpenAI", Name: "gpt-4o-mini", ModelType: "llm", MaxInputTokens: &contextWindow, InputTokenLimit: &inputLimit, MaxOutputTokens: &outputLimit, BaseModel: base},
			&orm.UserSelectedModel{UserID: "owner", ModelKey: fixture.role, UserModelProviderGroupModelID: fixture.id},
		} {
			if err := db.DB.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx := context.Background()
	check := func() {
		t.Helper()
		config, err := LoadLLMConfig(ctx, db.DB, "owner")
		if err != nil {
			t.Fatal(err)
		}
		for role, id := range map[string]string{"llm": "a", "vlm": "b"} {
			model, ok := config[role].(map[string]any)
			if !ok || model["source"] != "openai" || model["api_key"] != id+"-key" || model["base_url"] != "https://"+id+".example/v1/" {
				t.Fatal("custom supplier identity changed runtime protocol or crossed credentials")
			}
			if model["max_input_tokens"] != contextWindow || model["input_token_limit"] != inputLimit || model["max_output_tokens"] != outputLimit {
				t.Fatal("selected model lost its independent token limits")
			}
		}
	}
	check()
	if err := db.DB.Model(&orm.UserModelProvider{}).Where("id = ?", "a").Update("name", "Renamed supplier").Error; err != nil {
		t.Fatal(err)
	}
	check()
	if err := db.DB.Model(&orm.UserModelProvider{}).Where("id = ?", "a").Updates(map[string]any{"active_key_initialized": true, "active_key_id": "", "active_key_group_id": ""}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "a").Update("is_verified", false).Error; err != nil {
		t.Fatal(err)
	}
	config, err := LoadLLMConfig(ctx, db.DB, "owner")
	if err != nil || config["llm"] != nil {
		t.Fatal("disabled supplier remained usable")
	}
	if model, ok := config["vlm"].(map[string]any); !ok || model["api_key"] != "b-key" {
		t.Fatal("disabling one custom supplier affected another")
	}
}
