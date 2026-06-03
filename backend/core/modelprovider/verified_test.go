package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func setupVerifiedProviderTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbName := "verified_provider_" + strings.ReplaceAll(t.Name(), "/", "_")
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
	store.Init(db, nil, nil)
	return db
}

func seedVerifiedProviderGroup(t *testing.T, db *gorm.DB, userID, providerID, groupID, name, category string) {
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
		BaseURL:             "https://example.test/" + category + "/" + strings.ToLower(name),
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

func TestLoadVerifiedGroupsForUserReturnsOnlyOwnGroups(t *testing.T) {
	db := setupVerifiedProviderTestDB(t)
	seedVerifiedProviderGroup(t, db, "user-1", "provider-mineru", "group-mineru", "MinerU", "ocr")
	seedVerifiedProviderGroup(t, db, "user-1", "provider-paddle", "group-paddle", "PaddleOCR", "ocr")
	seedVerifiedProviderGroup(t, db, "user-2", "provider-shared", "group-shared", "SharedOCR", "ocr")
	seedVerifiedProviderGroup(t, db, "user-2", "provider-search", "group-search", "SharedSearch", "search")

	now := time.Now()
	if err := db.Create(&orm.UserSelectedProvider{
		UserID:                   "user-2",
		UserName:                 "Shared User",
		Category:                 "ocr",
		UserModelProviderGroupID: "group-shared",
		Share:                    true,
		CreatedAt:                now,
		UpdatedAt:                now,
	}).Error; err != nil {
		t.Fatalf("create shared selection: %v", err)
	}

	groups, err := loadVerifiedGroupsForUser(t.Context(), db, "user-1", "ocr")
	if err != nil {
		t.Fatalf("loadVerifiedGroupsForUser: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 own groups, got %d: %#v", len(groups), groups)
	}
	if groups[0].GroupID != "group-mineru" {
		t.Fatalf("unexpected first group: %#v", groups[0])
	}
	if groups[1].GroupID != "group-paddle" {
		t.Fatalf("unexpected second group: %#v", groups[1])
	}
}

func TestGetVerifiedProviderReturnsOwnReady(t *testing.T) {
	db := setupVerifiedProviderTestDB(t)
	seedVerifiedProviderGroup(t, db, "user-1", "provider-mineru", "group-mineru", "MinerU", "ocr")
	now := time.Now()
	if err := db.Create(&orm.UserSelectedProvider{
		UserID:                   "user-1",
		UserName:                 "User 1",
		Category:                 "ocr",
		UserModelProviderGroupID: "group-mineru",
		CreatedAt:                now,
		UpdatedAt:                now,
	}).Error; err != nil {
		t.Fatalf("create own selection: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/model_providers/verified?category=ocr", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	GetVerifiedProvider(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		common.APIResponse
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode response data: %v", err)
	}
	if ready, _ := data["ready"].(bool); !ready {
		t.Fatalf("expected ready=true, got %v", data["ready"])
	}
	if source, _ := data["source"].(string); source != "own" {
		t.Fatalf("expected source=own, got %q: %s", source, rec.Body.String())
	}
	if _, exists := data["groups"]; exists {
		t.Fatalf("did not expect groups in ready response: %s", rec.Body.String())
	}
}

func TestGetVerifiedProviderFallsBackToSharedReady(t *testing.T) {
	db := setupVerifiedProviderTestDB(t)
	seedVerifiedProviderGroup(t, db, "admin", "provider-admin-search", "group-admin-search", "Tavily", "search")
	now := time.Now()
	if err := db.Create(&orm.UserSelectedProvider{
		UserID:                   "admin",
		UserName:                 "Admin",
		Category:                 "search",
		UserModelProviderGroupID: "group-admin-search",
		Share:                    true,
		CreatedAt:                now,
		UpdatedAt:                now,
	}).Error; err != nil {
		t.Fatalf("create shared selection: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/model_providers/verified?category=search", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	GetVerifiedProvider(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		common.APIResponse
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode response data: %v", err)
	}
	if ready, _ := data["ready"].(bool); !ready {
		t.Fatalf("expected ready=true, got %v", data["ready"])
	}
	if source, _ := data["source"].(string); source != "shared" {
		t.Fatalf("expected source=shared, got %q: %s", source, rec.Body.String())
	}
	if sharedByName, _ := data["shared_by_name"].(string); sharedByName != "Admin" {
		t.Fatalf("expected shared_by_name=Admin, got %q: %s", sharedByName, rec.Body.String())
	}
	if _, exists := data["groups"]; exists {
		t.Fatalf("did not expect groups in ready response: %s", rec.Body.String())
	}
}

func TestListUserProviderGroupsByCategoryExcludesSharedGroups(t *testing.T) {
	db := setupVerifiedProviderTestDB(t)
	seedVerifiedProviderGroup(t, db, "user-1", "provider-user-search", "group-user-search", "Bing", "search")
	seedVerifiedProviderGroup(t, db, "admin", "provider-admin-search", "group-admin-search", "Tavily", "search")

	now := time.Now()
	if err := db.Create(&orm.UserSelectedProvider{
		UserID:                   "admin",
		UserName:                 "Admin",
		Category:                 "search",
		UserModelProviderGroupID: "group-admin-search",
		Share:                    true,
		CreatedAt:                now,
		UpdatedAt:                now,
	}).Error; err != nil {
		t.Fatalf("create shared provider selection: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/model_providers/provider_groups?category=search", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ListUserProviderGroupsByCategory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		common.APIResponse
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode response data: %v", err)
	}
	groups, _ := data["groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("expected one own group, got %#v: %s", data["groups"], rec.Body.String())
	}
	group, _ := groups[0].(map[string]any)
	if groupID, _ := group["group_id"].(string); groupID != "group-user-search" {
		t.Fatalf("expected only current user's group, got %#v: %s", group, rec.Body.String())
	}
}
