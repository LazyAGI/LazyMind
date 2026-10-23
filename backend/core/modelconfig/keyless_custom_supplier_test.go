package modelconfig

import (
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestVerifiedKeylessCustomSupplierEmitsSkipAuthForAllModelRoles(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.DefaultModelProvider{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}, &orm.EvolutionModelValidation{})
	base := orm.BaseModel{CreateUserID: "owner", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	parent := orm.UserModelProvider{ID: "local", DefaultModelProviderID: "missing-catalog", Name: "qwen自用", IsCustom: true, ProtocolName: "OpenAI", Category: "model", Capabilities: "has_models", BaseURL: "http://local.example:8000/", BaseModel: base}
	group := orm.UserModelProviderGroup{ID: "local-group", UserModelProviderID: parent.ID, Name: parent.Name, BaseURL: parent.BaseURL, IsVerified: true, BaseModel: base}
	if err := db.DB.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	roles := map[string]string{"llm": "llm", "evo_llm": "llm", "embed_main": "embed", "vlm": "vlm", "stt": "stt", "tts": "tts", "text2image": "text2image", "image_editing": "image_editing", "text2video": "text2video"}
	for role, technical := range roles {
		model := orm.UserModelProviderGroupModel{ID: role, UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "OpenAI", Name: "local-" + role, ModelType: technical, IsDefault: role == "embed_main", BaseModel: base}
		if err := db.DB.Create(&model).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.DB.Create(&orm.UserSelectedModel{UserID: "owner", ModelKey: role, UserModelProviderGroupModelID: model.ID, Share: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []string{"owner", "shared-reader"} {
		config, err := LoadLLMConfig(t.Context(), db.DB, user)
		if err != nil {
			t.Fatal(err)
		}
		for role := range roles {
			got, ok := config[role].(map[string]any)
			if !ok || got["skip_auth"] != true || got["api_key"] != "" || got["base_url"] != "http://local.example:8000/v1/" {
				t.Errorf("%s role %s did not preserve keyless runtime: %#v", user, role, config[role])
			}
		}
	}
	embed, err := LoadAdminEmbedConfig(t.Context(), db.DB)
	if err != nil || embed["skip_auth"] != true || embed["api_key"] != "" {
		t.Fatalf("keyless admin embedding=%#v err=%v", embed, err)
	}
	evolution, err := LoadEvolutionModels(t.Context(), db.DB, "owner")
	if err != nil || evolution.AvailableDefaultRef == "" {
		t.Fatalf("keyless evolution unavailable: %+v err=%v", evolution, err)
	}
	config, _, err := ResolveEvolutionModel(t.Context(), db.DB, "owner", evolution.AvailableDefaultRef)
	if err != nil || config["skip_auth"] != true || config["api_key"] != "" {
		t.Fatalf("keyless evolution config=%#v err=%v", config, err)
	}
}
