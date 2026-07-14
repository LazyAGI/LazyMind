package modelprovider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func setupSelectedProviderTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbName := "selected_provider_" + strings.ReplaceAll(t.Name(), "/", "_")
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.DefaultModelProvider{},
		&orm.UserModelProvider{},
		&orm.UserModelProviderGroup{},
		&orm.UserSelectedProvider{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store.Init(db, nil, nil)
	return db
}

func seedProviderGroup(t *testing.T, db *gorm.DB, userID, providerID, groupID, name, category string) {
	t.Helper()

	now := time.Now()
	provider := orm.UserModelProvider{
		ID:       providerID,
		Name:     name,
		Category: category,
		BaseModel: orm.BaseModel{
			CreateUserID: userID,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
	group := orm.UserModelProviderGroup{
		ID:                  groupID,
		UserModelProviderID: providerID,
		Name:                name,
		BaseURL:             "https://example.test/" + category,
		APIKey:              "secret",
		IsVerified:          true,
		BaseModel: orm.BaseModel{
			CreateUserID: userID,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
}

func performSetSelectedProvider(body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/core/model_providers/selected_providers", strings.NewReader(body))
	req.Header.Set("X-User-Id", "user-1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()
	SetSelectedProvider(rec, req)
	return rec
}

func TestSetSelectedProviderUsesSelectionsShape(t *testing.T) {
	db := setupSelectedProviderTestDB(t)
	seedProviderGroup(t, db, "user-1", "provider-ocr", "group-ocr", "MinerU", "ocr")
	seedProviderGroup(t, db, "user-1", "provider-search", "group-search", "Bing", "search")

	rec := performSetSelectedProvider(`{"selections":[{"category":"ocr","group_id":"group-ocr"},{"category":"search","group_id":"group-search"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var rows []orm.UserSelectedProvider
	if err := db.Order("category ASC").Find(&rows).Error; err != nil {
		t.Fatalf("query selections: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 selections, got %d", len(rows))
	}
	if rows[0].Category != "ocr" || rows[0].UserModelProviderGroupID != "group-ocr" {
		t.Fatalf("unexpected ocr row: %#v", rows[0])
	}
	if rows[1].Category != "search" || rows[1].UserModelProviderGroupID != "group-search" {
		t.Fatalf("unexpected search row: %#v", rows[1])
	}
}

func TestSetSelectedProviderClearsCategoryWithEmptyGroupID(t *testing.T) {
	db := setupSelectedProviderTestDB(t)
	seedProviderGroup(t, db, "user-1", "provider-ocr", "group-ocr", "MinerU", "ocr")

	rec := performSetSelectedProvider(`{"selections":[{"category":"ocr","group_id":"group-ocr"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected initial status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = performSetSelectedProvider(`{"selections":[{"category":"ocr","group_id":""}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected clear status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var count int64
	if err := db.Model(&orm.UserSelectedProvider{}).Where("user_id = ? AND category = ?", "user-1", "ocr").Count(&count).Error; err != nil {
		t.Fatalf("count selections: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected selection to be cleared, got %d rows", count)
	}
}

func TestSetSelectedProviderRejectsLegacyGroupIDOnlyShape(t *testing.T) {
	db := setupSelectedProviderTestDB(t)
	seedProviderGroup(t, db, "user-1", "provider-ocr", "group-ocr", "MinerU", "ocr")

	rec := performSetSelectedProvider(`{"group_id":"group-ocr"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetSelectedProviderRejectsDefaultBaseURLWithoutAPIKey(t *testing.T) {
	db := setupSelectedProviderTestDB(t)
	now := time.Now()
	if err := db.Create(&orm.DefaultModelProvider{
		ID:          "default-search",
		Name:        "Search",
		Description: "Search",
		BaseURL:     "https://api.search.test",
		Category:    "search",
		CreatedAt:   now,
		UpdatedAt:   now,
	}).Error; err != nil {
		t.Fatalf("create default provider: %v", err)
	}
	if err := db.Create(&orm.UserModelProvider{
		ID:                     "provider-search",
		DefaultModelProviderID: "default-search",
		Name:                   "Search",
		Category:               "search",
		BaseModel:              orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if err := db.Create(&orm.UserModelProviderGroup{
		ID:                  "group-search",
		UserModelProviderID: "provider-search",
		Name:                "Search",
		BaseURL:             "https://api.search.test",
		APIKey:              "",
		IsVerified:          true,
		BaseModel:           orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}

	rec := performSetSelectedProvider(`{"selections":[{"category":"search","group_id":"group-search"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLoadSelectedProvidersSkipsUnverifiedGroups(t *testing.T) {
	db := setupSelectedProviderTestDB(t)
	seedProviderGroup(t, db, "user-1", "provider-search", "group-search", "Bing", "search")
	if err := db.Model(&orm.UserModelProviderGroup{}).
		Where("id = ?", "group-search").
		Update("is_verified", false).Error; err != nil {
		t.Fatalf("mark group unverified: %v", err)
	}
	now := time.Now()
	if err := db.Create(&orm.UserSelectedProvider{
		UserID:                   "user-1",
		UserName:                 "User 1",
		Category:                 "search",
		UserModelProviderGroupID: "group-search",
		CreatedAt:                now,
		UpdatedAt:                now,
	}).Error; err != nil {
		t.Fatalf("create selected provider: %v", err)
	}

	out, err := loadSelectedProviders(t.Context(), db, "user-1")
	if err != nil {
		t.Fatalf("loadSelectedProviders: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected unverified selected provider to be hidden, got %#v", out)
	}
}

func TestLoadSelectedProvidersSkipsCrossUserGroups(t *testing.T) {
	db := setupSelectedProviderTestDB(t)
	seedProviderGroup(t, db, "admin", "provider-search", "group-search", "Tavily", "search")

	now := time.Now()
	if err := db.Create(&orm.UserSelectedProvider{
		UserID:                   "user-1",
		UserName:                 "User 1",
		Category:                 "search",
		UserModelProviderGroupID: "group-search",
		CreatedAt:                now,
		UpdatedAt:                now,
	}).Error; err != nil {
		t.Fatalf("create selected provider: %v", err)
	}

	out, err := loadSelectedProviders(t.Context(), db, "user-1")
	if err != nil {
		t.Fatalf("loadSelectedProviders: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected cross-user selected provider to be hidden, got %#v", out)
	}
}
