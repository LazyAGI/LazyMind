package modelconfig

import (
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestLoadLLMConfigUsesCatalogRuntimeIdentityForLocalOpenAIProtocol(t *testing.T) {
	db := orm.MigrateTestDB(t,
		&orm.DefaultModelProvider{},
		&orm.UserModelProvider{},
		&orm.UserModelProviderGroup{},
		&orm.UserModelProviderGroupModel{},
		&orm.UserSelectedModel{},
	).DB
	now := time.Now().UTC()
	base := orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}
	template := orm.DefaultModelProvider{ID: "vllm-template", Name: "vLLM", Category: "model", CreatedAt: now, UpdatedAt: now}
	parent := orm.UserModelProvider{
		ID:                     "local",
		DefaultModelProviderID: template.ID,
		Name:                   "My local gateway",
		IsCustom:               true,
		ProtocolName:           "OpenAI",
		Category:               "model",
		Capabilities:           "has_models",
		BaseURL:                "http://local.example/v1/",
		BaseModel:              base,
	}
	group := orm.UserModelProviderGroup{
		ID:                  "local-group",
		UserModelProviderID: parent.ID,
		Name:                parent.Name,
		BaseURL:             parent.BaseURL,
		APIKey:              "local-key",
		IsVerified:          true,
		BaseModel:           base,
	}
	chat := orm.UserModelProviderGroupModel{
		ID:                       "chat",
		UserModelProviderID:      parent.ID,
		UserModelProviderGroupID: group.ID,
		ProviderName:             "OpenAI",
		Name:                     "local-chat",
		ModelType:                "llm",
		BaseModel:                base,
	}
	image := orm.UserModelProviderGroupModel{
		ID:                       "image",
		UserModelProviderID:      parent.ID,
		UserModelProviderGroupID: group.ID,
		ProviderName:             "OpenAI",
		Name:                     "local-image",
		ModelType:                "text2image",
		BaseModel:                base,
	}
	for _, row := range []any{&template, &parent, &group, &chat, &image} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, selection := range []orm.UserSelectedModel{
		{UserID: "owner", ModelKey: "llm", UserModelProviderGroupModelID: chat.ID},
		{UserID: "owner", ModelKey: "text2image", UserModelProviderGroupModelID: image.ID},
	} {
		if err := db.Create(&selection).Error; err != nil {
			t.Fatal(err)
		}
	}

	config, err := LoadLLMConfig(t.Context(), db, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if config["llm"] == nil {
		t.Fatalf("local chat adapter was filtered: %#v", config)
	}
	if config["text2image"] != nil {
		t.Fatalf("local vLLM media adapter was incorrectly emitted: %#v", config["text2image"])
	}
}
