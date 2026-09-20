package modelconfig

import (
	"testing"

	"lazymind/core/common/orm"
)

func TestLoadLLMConfigIgnoresRetiredMetadataSelections(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{})
	for _, uid := range []string{"user", "shared-user"} {
		group := orm.UserModelProviderGroup{
			ID: uid, IsVerified: true, APIKeyCiphertext: "obsolete-invalid-ciphertext",
			BaseModel: orm.BaseModel{CreateUserID: uid},
		}
		model := orm.UserModelProviderGroupModel{
			ID: uid, UserModelProviderGroupID: uid, Name: "retired", ModelType: "llm",
			BaseModel: orm.BaseModel{CreateUserID: uid},
		}
		selection := orm.UserSelectedModel{
			UserID: uid, ModelKey: "conversation_metadata", UserModelProviderGroupModelID: uid,
			Share: uid == "shared-user",
		}
		for _, row := range []any{&group, &model, &selection} {
			if err := db.DB.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	config, err := LoadLLMConfig(t.Context(), db.DB, "user")
	if err != nil || len(config) != 0 {
		t.Fatalf("retired own/shared selections must be ignored before decrypting: config=%v err=%v", config, err)
	}
}
