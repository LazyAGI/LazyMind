package chat

import (
	"reflect"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func setupToolConfigTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := t.Name() + "_" + time.Now().Format("150405.000000000")
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.UserModelProvider{},
		&orm.UserModelProviderGroup{},
		&orm.UserSelectedProvider{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedSelectedSearchTool(
	t *testing.T,
	db *gorm.DB,
	userID string,
	providerName string,
	groupID string,
	apiKey string,
	shared bool,
) {
	seedSelectedToolProvider(t, db, userID, providerName, groupID, apiKey, searchProviderCategory, searchProviderCategory, shared)
}

func seedSelectedToolProvider(
	t *testing.T,
	db *gorm.DB,
	userID string,
	providerName string,
	groupID string,
	apiKey string,
	providerCategory string,
	selectionCategory string,
	shared bool,
) {
	t.Helper()
	now := time.Now()
	provider := orm.UserModelProvider{
		ID:                     "provider-" + groupID,
		DefaultModelProviderID: "default-" + groupID,
		Name:                   providerName,
		Category:               providerCategory,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	group := orm.UserModelProviderGroup{
		ID:                  groupID,
		UserModelProviderID: provider.ID,
		Name:                providerName,
		BaseURL:             "https://example.test",
		APIKey:              apiKey,
		IsVerified:          true,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	selected := orm.UserSelectedProvider{
		UserID:                   userID,
		UserName:                 userID,
		Category:                 selectionCategory,
		UserModelProviderGroupID: group.ID,
		Share:                    shared,
		CreatedAt:                now,
		UpdatedAt:                now,
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if err := db.Create(&selected).Error; err != nil {
		t.Fatalf("seed selection: %v", err)
	}
}

func seedConfiguredToolProvider(
	t *testing.T,
	db *gorm.DB,
	userID string,
	providerName string,
	groupID string,
	apiKey string,
	providerCategory string,
) {
	t.Helper()
	now := time.Now()
	provider := orm.UserModelProvider{
		ID:                     "provider-" + groupID,
		DefaultModelProviderID: "default-" + groupID,
		Name:                   providerName,
		Category:               providerCategory,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	group := orm.UserModelProviderGroup{
		ID:                  groupID,
		UserModelProviderID: provider.ID,
		Name:                providerName,
		BaseURL:             "https://example.test",
		APIKey:              apiKey,
		IsVerified:          true,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("seed group: %v", err)
	}
}

func TestSearchToolConfigEntryUsesSelectedGoogleCustomSearch(t *testing.T) {
	db := setupToolConfigTestDB(t)
	seedSelectedSearchTool(t, db, "user-1", "Google Custom Search", "group-google", "key|engine", false)

	entry, err := searchToolConfigEntry(t.Context(), db, "user-1")
	if err != nil {
		t.Fatalf("searchToolConfigEntry error: %v", err)
	}
	if entry["google"] != "key|engine" {
		t.Fatalf("unexpected tool config: %#v", entry)
	}
}

func TestSearchToolConfigEntryMapsBochaAndSciverse(t *testing.T) {
	tests := []struct {
		name         string
		providerName string
		wantKey      string
	}{
		{name: "bocha", providerName: "Bocha", wantKey: "bocha"},
		{name: "sciverse", providerName: "Sciverse", wantKey: "sciverse"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupToolConfigTestDB(t)
			seedSelectedSearchTool(t, db, "user-1", tt.providerName, "group-"+tt.name, "test-key", false)

			entry, err := searchToolConfigEntry(t.Context(), db, "user-1")
			if err != nil {
				t.Fatalf("searchToolConfigEntry error: %v", err)
			}
			if entry[tt.wantKey] != "test-key" {
				t.Fatalf("unexpected tool config: %#v", entry)
			}
		})
	}
}

func TestSearchToolConfigEntryFallsBackToSharedSelection(t *testing.T) {
	db := setupToolConfigTestDB(t)
	seedSelectedSearchTool(t, db, "admin", "Sciverse", "group-shared", "shared-key", true)

	entry, err := searchToolConfigEntry(t.Context(), db, "user-1")
	if err != nil {
		t.Fatalf("searchToolConfigEntry error: %v", err)
	}
	if entry["sciverse"] != "shared-key" {
		t.Fatalf("unexpected shared tool config: %#v", entry)
	}
}

func TestSearchToolConfigEntryUsesSelectedSciverseDatasource(t *testing.T) {
	db := setupToolConfigTestDB(t)
	seedSelectedToolProvider(t, db, "user-1", "Sciverse", "group-sciverse-datasource", "datasource-key", datasourceProviderCategory, datasourceProviderCategory, false)

	entry, err := searchToolConfigEntry(t.Context(), db, "user-1")
	if err != nil {
		t.Fatalf("searchToolConfigEntry error: %v", err)
	}
	if entry["sciverse"] != "datasource-key" {
		t.Fatalf("unexpected tool config: %#v", entry)
	}
}

func TestSearchToolConfigEntryFallsBackToConfiguredSciverseDatasource(t *testing.T) {
	db := setupToolConfigTestDB(t)
	seedConfiguredToolProvider(t, db, "user-1", "Sciverse", "group-sciverse-configured", "configured-key", datasourceProviderCategory)

	entry, err := searchToolConfigEntry(t.Context(), db, "user-1")
	if err != nil {
		t.Fatalf("searchToolConfigEntry error: %v", err)
	}
	if entry["sciverse"] != "configured-key" {
		t.Fatalf("unexpected tool config: %#v", entry)
	}
}

func TestMergeToolConfigKeepsFeishuAndSearchTool(t *testing.T) {
	got := mergeToolConfig(
		nil,
		map[string]any{"feishu": "feishu-token"},
		map[string]any{"sciverse": "search-token"},
	)
	if got["feishu"] != "feishu-token" || got["sciverse"] != "search-token" {
		t.Fatalf("unexpected merged tool config: %#v", got)
	}
}

func TestCloudToolProvidersIncludeGoogleDrive(t *testing.T) {
	want := map[string]bool{
		"feishu":      true,
		"googledrive": true,
		"notion":      true,
	}
	for _, provider := range _cloudToolProviders {
		delete(want, provider)
	}
	if len(want) != 0 {
		t.Fatalf("missing cloud tool providers: %#v", want)
	}
}

func TestSearchToolConfigEntrySplitsMultiKeyCredential(t *testing.T) {
	db := setupToolConfigTestDB(t)
	seedSelectedSearchTool(t, db, "user-1", "Sciverse", "group-sciverse", "key-1\n key-2 \n", false)

	entry, err := searchToolConfigEntry(t.Context(), db, "user-1")
	if err != nil {
		t.Fatalf("searchToolConfigEntry error: %v", err)
	}
	want := []string{"key-1", "key-2"}
	if !reflect.DeepEqual(entry["sciverse"], want) {
		t.Fatalf("unexpected multi-key tool config: %#v", entry)
	}
}
