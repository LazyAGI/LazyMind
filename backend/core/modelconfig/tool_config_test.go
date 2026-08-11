package modelconfig

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestLoadSearchToolConfigReturnsSelectedTavilyCredential(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&orm.UserModelProvider{},
		&orm.UserModelProviderGroup{},
		&orm.UserSelectedProvider{},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	provider := orm.UserModelProvider{
		ID: "provider-tavily", DefaultModelProviderID: "default-tavily",
		Name: "Tavily", Category: "search",
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "user-1",
			CreatedAt: now, UpdatedAt: now},
	}
	group := orm.UserModelProviderGroup{
		ID: "group-tavily", UserModelProviderID: provider.ID, Name: "Tavily",
		APIKey: "secret-token", IsVerified: true,
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "user-1",
			CreatedAt: now, UpdatedAt: now},
	}
	selected := orm.UserSelectedProvider{
		UserID: "user-1", UserName: "user-1", Category: "search",
		UserModelProviderGroupID: group.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, value := range []any{&provider, &group, &selected} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}

	config, err := LoadSearchToolConfig(t.Context(), db, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if config["tavily"] != "secret-token" {
		t.Fatalf("unexpected search tool config: %#v", config)
	}
}
