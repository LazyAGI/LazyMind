package modelprovider

import (
	"context"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestEvoAlwaysRequiresDynamicSelection(t *testing.T) {
	dynamic, err := requiresDynamicSelection(context.Background(), EvoModelKey)
	if err != nil || !dynamic {
		t.Fatalf("evo_llm dynamic requirement = (%v, %v), want (true, nil)", dynamic, err)
	}
}

func TestLoadSelectedModelsRequiresVerifiedEvoGroup(t *testing.T) {
	db := orm.MigrateTestDB(t,
		&orm.UserSelectedModel{},
		&orm.UserModelProviderGroupModel{},
		&orm.UserModelProviderGroup{},
	)
	now := time.Now().UTC()
	group := orm.UserModelProviderGroup{
		ID: "group-1", UserModelProviderID: "provider-1", Name: "Qwen",
		BaseURL: "https://dashscope.aliyuncs.com/", APIKey: "secret",
		IsVerified: false,
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1", CreateUserName: "user-1",
			CreatedAt: now, UpdatedAt: now,
		},
	}
	model := orm.UserModelProviderGroupModel{
		ID: "model-1", UserModelProviderID: group.UserModelProviderID,
		UserModelProviderGroupID: group.ID, ProviderName: "Qwen",
		Name: "qwen3-max", ModelType: "llm",
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1", CreateUserName: "user-1",
			CreatedAt: now, UpdatedAt: now,
		},
	}
	selection := orm.UserSelectedModel{
		UserID: "user-1", UserName: "user-1", ModelKey: EvoModelKey,
		UserModelProviderGroupModelID: model.ID,
		CreatedAt:                     now, UpdatedAt: now,
	}
	for _, row := range []any{&group, &model, &selection} {
		if err := db.DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	items, err := loadSelectedModels(context.Background(), db.DB, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("unverified Evo selection must be hidden, got %#v", items)
	}
	if err := db.DB.Model(&orm.UserModelProviderGroup{}).
		Where("id = ?", group.ID).Update("is_verified", true).Error; err != nil {
		t.Fatal(err)
	}
	items, err = loadSelectedModels(context.Background(), db.DB, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ModelKey != EvoModelKey {
		t.Fatalf("verified Evo selection must be visible, got %#v", items)
	}
}
