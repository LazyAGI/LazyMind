package modelconfig

import (
	"reflect"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func toolConfigTestDB(t *testing.T) *gorm.DB {
	t.Helper()
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
	return db
}

func seedToolProvider(t *testing.T, db *gorm.DB, userID, name, category, key string) {
	t.Helper()
	now := time.Now()
	provider := orm.UserModelProvider{
		ID: "provider-" + name, DefaultModelProviderID: "default-" + name,
		Name: name, Category: category,
		BaseModel: orm.BaseModel{CreateUserID: userID, CreateUserName: userID,
			CreatedAt: now, UpdatedAt: now},
	}
	group := orm.UserModelProviderGroup{
		ID: "group-" + name, UserModelProviderID: provider.ID, Name: name,
		APIKey: key, IsVerified: true,
		BaseModel: orm.BaseModel{CreateUserID: userID, CreateUserName: userID,
			CreatedAt: now, UpdatedAt: now},
	}
	selected := orm.UserSelectedProvider{
		UserID: userID, UserName: userID, Category: category,
		UserModelProviderGroupID: group.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, value := range []any{&provider, &group, &selected} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestIsCloudToolProvider(t *testing.T) {
	tests := []struct {
		provider string
		want     bool
	}{
		{provider: "feishu", want: true},
		{provider: "notion", want: true},
		{provider: "github", want: true},
		{provider: "wechat", want: true},
		{provider: "googledrive", want: true},
		{provider: "obsidian", want: false},
		{provider: " OBSIDIAN ", want: false},
		{provider: "unknown", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			if got := IsCloudToolProvider(tt.provider); got != tt.want {
				t.Fatalf("IsCloudToolProvider(%q) = %v, want %v", tt.provider, got, tt.want)
			}
		})
	}
}

func TestLoadSearchToolConfigReturnsSelectedTavilyCredential(t *testing.T) {
	db := toolConfigTestDB(t)
	seedToolProvider(t, db, "user-1", "Tavily", "search", "secret-token")

	config, err := LoadSearchToolConfig(t.Context(), db, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if config["tavily"] != "secret-token" {
		t.Fatalf("unexpected search tool config: %#v", config)
	}
}

func TestLoadToolConfigForCapabilitiesUsesWorkflowAllowlist(t *testing.T) {
	db := toolConfigTestDB(t)
	seedToolProvider(t, db, "user-1", "Tavily", "search", "web-token")
	seedToolProvider(t, db, "user-1", "Sciverse", "datasource", "academic-token")

	academic, err := LoadToolConfigForCapabilities(
		t.Context(), db, "user-1", []string{"academic_search", "kb"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(academic, map[string]any{"sciverse": "academic-token"}) {
		t.Fatalf("unexpected academic config: %#v", academic)
	}

	localOnly, err := LoadToolConfigForCapabilities(
		t.Context(), db, "user-1", []string{"academic_writer_write_sections"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(localOnly) != 0 {
		t.Fatalf("local-only step received unrelated credentials: %#v", localOnly)
	}
}
