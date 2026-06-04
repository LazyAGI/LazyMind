package modelprovider

import (
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func setupListProviderTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbName := "list_provider_" + strings.ReplaceAll(t.Name(), "/", "_")
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.DefaultModel{},
		&orm.UserModelProvider{},
		&orm.UserModelProviderGroup{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestBuildListItemsReturnsConfigurationFlagFromVerifiedGroups(t *testing.T) {
	db := setupListProviderTestDB(t)
	now := time.Now()
	rows := []orm.UserModelProvider{
		{
			ID:                     "provider-configured",
			DefaultModelProviderID: "default-configured",
			Name:                   "Bing",
			Description:            "Bing Search",
			BaseURL:                "https://api.bing.test/",
			Category:               "search",
			BaseModel: orm.BaseModel{
				CreateUserID: "user-1",
				CreatedAt:    now,
				UpdatedAt:    now,
			},
		},
		{
			ID:                     "provider-unverified",
			DefaultModelProviderID: "default-unverified",
			Name:                   "Tavily",
			Description:            "Tavily Search",
			BaseURL:                "https://api.tavily.test/",
			Category:               "search",
			BaseModel: orm.BaseModel{
				CreateUserID: "user-1",
				CreatedAt:    now,
				UpdatedAt:    now,
			},
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create providers: %v", err)
	}
	if err := db.Create(&orm.UserModelProviderGroup{
		ID:                  "group-configured",
		UserModelProviderID: "provider-configured",
		Name:                "Bing",
		BaseURL:             "https://api.bing.test/",
		APIKey:              "secret",
		IsVerified:          true,
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}).Error; err != nil {
		t.Fatalf("create verified group: %v", err)
	}
	if err := db.Create(&orm.UserModelProviderGroup{
		ID:                  "group-unverified",
		UserModelProviderID: "provider-unverified",
		Name:                "Tavily",
		BaseURL:             "https://api.tavily.test/",
		APIKey:              "secret",
		IsVerified:          false,
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}).Error; err != nil {
		t.Fatalf("create unverified group: %v", err)
	}

	items := buildListItems(t.Context(), db, rows)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if !items[0].IsConfigured {
		t.Fatalf("expected configured provider to be marked configured: %#v", items[0])
	}
	if items[1].IsConfigured {
		t.Fatalf("expected provider without verified groups to be missing: %#v", items[1])
	}
}

func TestBuildListItemsRequiresVerifiedGroupWithNonEmptyAPIKey(t *testing.T) {
	db := setupListProviderTestDB(t)
	now := time.Now()
	rows := []orm.UserModelProvider{
		{
			ID:                     "provider-empty-key",
			DefaultModelProviderID: "default-empty-key",
			Name:                   "Sciverse",
			Description:            "Sciverse Search",
			BaseURL:                "https://api.sciverse.space",
			Category:               "search",
			BaseModel: orm.BaseModel{
				CreateUserID: "user-1",
				CreatedAt:    now,
				UpdatedAt:    now,
			},
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create providers: %v", err)
	}
	if err := db.Create(&orm.UserModelProviderGroup{
		ID:                  "group-empty-key",
		UserModelProviderID: "provider-empty-key",
		Name:                "Sciverse",
		BaseURL:             "https://api.sciverse.space",
		APIKey:              "",
		IsVerified:          true,
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}).Error; err != nil {
		t.Fatalf("create verified group: %v", err)
	}

	items := buildListItems(t.Context(), db, rows)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].IsConfigured {
		t.Fatalf("expected verified group with empty key to be missing: %#v", items[0])
	}
}
