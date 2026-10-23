package modelprovider

import (
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestLocalSupplierIdentitySurvivesOpenAIProtocolWithoutUsingCustomName(t *testing.T) {
	db := setupListProviderTestDB(t)
	for _, name := range []string{"LMDeploy", "vLLM"} {
		template := orm.DefaultModelProvider{ID: "template-" + name, Name: name}
		if err := db.Create(&template).Error; err != nil {
			t.Fatal(err)
		}
		for _, provider := range []orm.UserModelProvider{
			{Name: name, ProtocolName: "OpenAI"},
			{Name: "my-local", IsCustom: true, ProtocolName: "OpenAI", DefaultModelProviderID: template.ID},
			{Name: "my-local", IsCustom: true, ProtocolName: name},
		} {
			if !isLocalOpenAISupplier(t.Context(), db, provider) {
				t.Errorf("local identity lost: %+v", provider)
			}
		}
		if isLocalOpenAISupplier(t.Context(), db, orm.UserModelProvider{Name: name, IsCustom: true, ProtocolName: "OpenAI"}) {
			t.Errorf("custom display name %q was used as supplier identity", name)
		}
	}
}

func TestVLLMCanonicalSpellingPreservesCatalogAndUserIdentities(t *testing.T) {
	db := setupListProviderTestDB(t)
	legacy := orm.DefaultModelProvider{ID: "legacy-vllm", Name: "VLLM", Category: "model", Capabilities: "has_models"}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	user := orm.UserModelProvider{ID: "user-vllm", DefaultModelProviderID: legacy.ID, Name: "VLLM", ProtocolName: "OpenAI", BaseURL: "https://proxy.example/api/v3/", Category: "model", Capabilities: "has_models", BaseModel: orm.BaseModel{CreateUserID: "local-owner"}}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	custom := orm.UserModelProvider{ID: "custom-vllm", DefaultModelProviderID: legacy.ID, Name: "VLLM", IsCustom: true, BaseModel: orm.BaseModel{CreateUserID: "local-owner"}}
	if err := db.Create(&custom).Error; err != nil {
		t.Fatal(err)
	}
	id, err := upsertDefaultProvider(db, time.Now(), "model", []string{"has_models"}, catalogSupplier{Name: "vLLM", Description: map[string]string{"zh-CN": "自部署模型", "en-US": "Self-hosted models"}, BaseURL: "http://localhost:8000/v1/"})
	if err != nil {
		t.Fatal(err)
	}
	if id != legacy.ID {
		t.Fatalf("created duplicate vLLM identity: %q", id)
	}
	var count int64
	if err := db.Model(&orm.DefaultModelProvider{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("count=%d, err=%v", count, err)
	}
	if err := syncUserProvidersFromDefaults(t.Context(), db, "local-owner", ""); err != nil {
		t.Fatal(err)
	}
	var saved orm.UserModelProvider
	if err := db.Where("id = ?", user.ID).Take(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Name != "vLLM" || saved.BaseURL != user.BaseURL || saved.ProtocolName != user.ProtocolName || saved.DefaultModelProviderID != legacy.ID {
		t.Fatalf("supplier configuration changed: %+v", saved)
	}
	var savedCustom orm.UserModelProvider
	if err := db.Where("id = ?", custom.ID).Take(&savedCustom).Error; err != nil {
		t.Fatal(err)
	}
	if savedCustom.Name != "VLLM" {
		t.Fatal("custom name was changed")
	}
}
