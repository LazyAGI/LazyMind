package modelprovider

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
)

func TestServiceBackupRetainsCatalogIdentityAfterProtocolSwitch(t *testing.T) {
	db := setupListProviderTestDB(t)
	parent := orm.UserModelProvider{ID: "plan", Name: "QwenCodingPlan", ProtocolName: "Claude", DefaultModelProviderID: "installation-plan-id", Category: "model", BaseURL: "https://coding.dashscope.aliyuncs.com/apps/anthropic", BaseModel: orm.BaseModel{CreateUserID: "local-user"}}
	group := orm.UserModelProviderGroup{ID: "plan-group", UserModelProviderID: parent.ID, Name: "Qwen", BaseURL: parent.BaseURL, APIKey: "fixture-plan-key", CredentialRevision: 1, BaseModel: parent.BaseModel}
	for _, value := range []any{&parent, &group} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	payload, err := (CredentialBackupSource{DB: db}).LoadCredential(context.Background(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(payload.APIKey)
	if payload.ProviderCatalogKey != "QwenCodingPlan" || payload.DisplayName != "QwenCodingPlan" {
		t.Fatalf("backup lost service identity: catalog=%s display=%s", payload.ProviderCatalogKey, payload.DisplayName)
	}
}

func TestRestoreNativeCatalogNeverSelectsPlanBySharedProtocol(t *testing.T) {
	_, db, _ := newCredentialRestoreSinkFixture(t)
	providers := []orm.UserModelProvider{
		{ID: "aaa-plan", Name: "QwenCodingPlan", ProtocolName: "OpenAI", DefaultModelProviderID: "plan-id", Category: "model", BaseModel: orm.BaseModel{CreateUserID: "local-user"}},
		{ID: "zzz-standard", Name: "OpenAI", DefaultModelProviderID: "openai-id", Category: "model", BaseModel: orm.BaseModel{CreateUserID: "local-user"}},
	}
	if err := db.Create(&providers).Error; err != nil {
		t.Fatal(err)
	}
	got, err := restoreCatalogSupplier(db, "local-user", "OpenAI")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "zzz-standard" {
		t.Fatalf("native catalog restored into service %s", got.Name)
	}
}

func TestRestoreLegacyBrandBackupResolvesOfficialPlanWithoutCopyingStandard(t *testing.T) {
	_, db, _ := newCredentialRestoreSinkFixture(t)
	providers := []orm.UserModelProvider{
		{ID: "standard", Name: "Qwen", DefaultModelProviderID: "standard-id", Category: "model", BaseURL: "https://dashscope.aliyuncs.com/", BaseModel: orm.BaseModel{CreateUserID: "local-user"}},
		{ID: "coding", Name: "QwenCodingPlan", DefaultModelProviderID: "coding-id", Category: "model", BaseURL: "https://coding.dashscope.aliyuncs.com/v1/", BaseModel: orm.BaseModel{CreateUserID: "local-user"}},
	}
	if err := db.Create(&providers).Error; err != nil {
		t.Fatal(err)
	}
	payload := credentialvault.ProviderCredential{ProviderCatalogKey: "Qwen", DisplayName: "Qwen", BaseURL: "https://coding.dashscope.aliyuncs.com/v1", APIKey: []byte("fixture-key")}
	got, err := restoredSupplier(db, "local-user", payload, credentialvault.ConflictFail, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "coding" || got.IsCustom {
		t.Fatalf("legacy plan lost independent catalog identity: %s custom=%v", got.Name, got.IsCustom)
	}
}

func TestRestorePlanKeyCannotFallBackToDifferentService(t *testing.T) {
	_, db, _ := newCredentialRestoreSinkFixture(t)
	parent := orm.UserModelProvider{ID: "coding", Name: "QwenCodingPlan", ProtocolName: "OpenAI", DefaultModelProviderID: "coding-id", Category: "model", BaseModel: orm.BaseModel{CreateUserID: "local-user"}}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := restoreCatalogSupplier(db, "local-user", "OpenAI"); err == nil {
		t.Fatal("generic protocol selected an unrelated plan")
	}
}

func TestSelectedModelRetainsServiceIdentityWithLegacyGroupName(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserSelectedModel{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{})
	base := orm.BaseModel{CreateUserID: "local-user"}
	parent := orm.UserModelProvider{ID: "coding", Name: "QwenCodingPlan", ProtocolName: "OpenAI", Category: "model", BaseURL: "https://coding.dashscope.aliyuncs.com/v1/", BaseModel: base}
	group := orm.UserModelProviderGroup{ID: "coding-group", UserModelProviderID: parent.ID, Name: "Qwen", BaseURL: parent.BaseURL, APIKey: "fixture-key", BaseModel: base}
	model := orm.UserModelProviderGroupModel{ID: "coding-model", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "OpenAI", Name: "qwen3-coder-plus", ModelType: "llm", BaseModel: base}
	selection := orm.UserSelectedModel{UserID: base.CreateUserID, ModelKey: "llm", UserModelProviderGroupModelID: model.ID}
	for _, row := range []any{&parent, &group, &model, &selection} {
		if err := db.DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	rows, err := loadSelectedModels(context.Background(), db.DB, base.CreateUserID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	var result []map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0]["brand_key"] != "Qwen" || result[0]["service_key"] != "coding" || result[0]["is_custom"] != false {
		t.Fatalf("selected model lost supplier service identity: %s", encoded)
	}
}

func TestLegacyServiceRestoreKeepsEndpointProtocolWhenCreatingIndependentCopy(t *testing.T) {
	for _, fixture := range []struct{ key, name, endpoint, adapter string }{
		{"Kimi", "KimiCode", "https://api.kimi.com/coding/v1/", "OpenAI"},
		{"Qwen", "QwenCodingPlan", "https://coding.dashscope.aliyuncs.com/apps/anthropic", "Claude"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			_, db, _ := newCredentialRestoreSinkFixture(t)
			parent := orm.UserModelProvider{ID: "plan", Name: fixture.name, DefaultModelProviderID: "plan-id", Category: "model", BaseURL: "https://different.example/v1/", BaseModel: orm.BaseModel{CreateUserID: "local-user"}}
			if err := db.Create(&parent).Error; err != nil {
				t.Fatal(err)
			}
			payload := credentialvault.ProviderCredential{ProviderCatalogKey: fixture.key, DisplayName: fixture.key, BaseURL: fixture.endpoint, APIKey: []byte("fixture-key")}
			got, err := restoredSupplier(db, "local-user", payload, credentialvault.ConflictFail, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if RuntimeProviderName(got) != fixture.adapter {
				t.Fatalf("restored service endpoint uses %s, want %s", RuntimeProviderName(got), fixture.adapter)
			}
		})
	}
}
