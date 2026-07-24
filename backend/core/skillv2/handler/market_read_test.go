package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	skillmarket "lazymind/core/skillv2/market"
	skillservice "lazymind/core/skillv2/service"
	"lazymind/core/skillv2/testutil"
	"lazymind/core/store"
)

func TestMarketListKeepsInstalledItemWithInstalledState(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "market_skill", "market_rev1")
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{
		ID:            "market_item1",
		SourceSkillID: "market_skill",
		Status:        "published",
		CreatedAt:     testutil.TimeFixture(),
		UpdatedAt:     testutil.TimeFixture(),
	})
	service := skillmarket.NewService(skillmarket.ServiceDeps{
		DB:        db.DB,
		BlobStore: skillmarket.NewBlobStore(db.DB, skillmarket.NewLocalObjectStore(t.TempDir())),
	})
	installed, err := service.Install(context.Background(), skillmarket.InstallRequest{
		MarketItemID: "market_item1",
		UserID:       "user_002",
		UserName:     "user2",
	})
	if err != nil {
		t.Fatalf("install market skill: %v", err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(http.MethodGet, "/api/core/skill-market?page=1&page_size=100", nil)
	req.Header.Set("X-User-Id", "user_002")
	rec := httptest.NewRecorder()

	MarketList(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				MarketItemID     string `json:"market_item_id"`
				Installed        bool   `json:"installed"`
				InstalledSkillID string `json:"installed_skill_id"`
			} `json:"items"`
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 || resp.Data.Total != 1 || len(resp.Data.Items) != 1 {
		t.Fatalf("unexpected market list response: %#v", resp)
	}
	item := resp.Data.Items[0]
	if item.MarketItemID != "market_item1" || !item.Installed || item.InstalledSkillID != installed.SkillID {
		t.Fatalf("unexpected installed market item: %#v, installed=%#v", item, installed)
	}
}

func TestMarketListInstalledStateIsScopedByUserAndDelete(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "market_skill", "market_rev1")
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{
		ID:            "market_item1",
		SourceSkillID: "market_skill",
		Status:        "published",
		CreatedAt:     testutil.TimeFixture(),
		UpdatedAt:     testutil.TimeFixture(),
	})
	marketSvc := skillmarket.NewService(skillmarket.ServiceDeps{
		DB:        db.DB,
		BlobStore: skillmarket.NewBlobStore(db.DB, skillmarket.NewLocalObjectStore(t.TempDir())),
	})
	userA, err := marketSvc.Install(context.Background(), skillmarket.InstallRequest{
		MarketItemID: "market_item1",
		UserID:       "user_a",
		UserName:     "User A",
	})
	if err != nil {
		t.Fatalf("install market skill for user A: %v", err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	itemA := marketListItemForUser(t, "user_a")
	if !itemA.Installed || itemA.InstalledSkillID != userA.SkillID {
		t.Fatalf("user A installed state = %#v, want installed skill %q", itemA, userA.SkillID)
	}
	itemB := marketListItemForUser(t, "user_b")
	if itemB.Installed || itemB.InstalledSkillID != "" {
		t.Fatalf("user B installed state leaked from user A: %#v", itemB)
	}

	userB, err := marketSvc.Install(context.Background(), skillmarket.InstallRequest{
		MarketItemID: "market_item1",
		UserID:       "user_b",
		UserName:     "User B",
	})
	if err != nil {
		t.Fatalf("install market skill for user B: %v", err)
	}
	itemB = marketListItemForUser(t, "user_b")
	if !itemB.Installed || itemB.InstalledSkillID != userB.SkillID {
		t.Fatalf("user B installed state = %#v, want installed skill %q", itemB, userB.SkillID)
	}

	skillSvc := skillservice.NewSkillService(skillservice.SkillServiceDeps{DB: db.DB})
	if err := skillSvc.DeleteSkill(context.Background(), skillservice.DeleteSkillRequest{SkillID: userA.SkillID, UserID: "user_a"}); err != nil {
		t.Fatalf("delete user A installed skill: %v", err)
	}
	if got := testutil.CountRows(t, db, "skill_market_installs", "skill_id = ? AND user_id = ?", userA.SkillID, "user_a"); got != 0 {
		t.Fatalf("user A market install rows after delete = %d, want 0", got)
	}
	if got := testutil.CountRows(t, db, "skill_market_installs", "skill_id = ? AND user_id = ?", userB.SkillID, "user_b"); got != 1 {
		t.Fatalf("user B market install rows after user A delete = %d, want 1", got)
	}
	itemA = marketListItemForUser(t, "user_a")
	if itemA.Installed || itemA.InstalledSkillID != "" {
		t.Fatalf("user A installed state after delete = %#v, want not installed", itemA)
	}
	itemB = marketListItemForUser(t, "user_b")
	if !itemB.Installed || itemB.InstalledSkillID != userB.SkillID {
		t.Fatalf("user B installed state after user A delete = %#v, want installed skill %q", itemB, userB.SkillID)
	}
}

func TestMarketListDoesNotTreatPublisherSourceAsInstalled(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "market_skill", "market_rev1")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "market_skill").Updates(map[string]any{
		"owner_user_id":    "admin_001",
		"owner_user_name":  "admin",
		"create_user_id":   "admin_001",
		"create_user_name": "admin",
	}).Error; err != nil {
		t.Fatalf("reassign market source owner: %v", err)
	}
	adminID := "admin_001"
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{
		ID:            "market_item1",
		SourceSkillID: "market_skill",
		Status:        "published",
		CreatedBy:     &adminID,
		CreatedAt:     testutil.TimeFixture(),
		UpdatedAt:     testutil.TimeFixture(),
	})
	testutil.MustCreate(t, db, &testutil.SkillMarketInstallRow{
		MarketItemID: "market_item1",
		UserID:       "admin_001",
		SkillID:      "market_skill",
		CreatedAt:    testutil.TimeFixture(),
		UpdatedAt:    testutil.TimeFixture(),
	})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	item := marketListItemForUser(t, "admin_001")
	if item.Installed || item.InstalledSkillID != "" {
		t.Fatalf("publisher source skill was treated as installed: %#v", item)
	}
}

func TestMarketListSkipsDeletedMarketSource(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "market_skill", "market_rev1")
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{
		ID:            "market_item1",
		SourceSkillID: "market_skill",
		Status:        "published",
		CreatedAt:     testutil.TimeFixture(),
		UpdatedAt:     testutil.TimeFixture(),
	})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	skillSvc := skillservice.NewSkillService(skillservice.SkillServiceDeps{DB: db.DB})
	if err := skillSvc.DeleteSkill(context.Background(), skillservice.DeleteSkillRequest{SkillID: "market_skill", UserID: "user_001"}); err != nil {
		t.Fatalf("delete market source skill: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skill-market?page=1&page_size=100", nil)
	req.Header.Set("X-User-Id", "user_001")
	rec := httptest.NewRecorder()

	MarketList(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Items []marketListTestItem `json:"items"`
			Total int                  `json:"total"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 || resp.Data.Total != 0 || len(resp.Data.Items) != 0 {
		t.Fatalf("unexpected market list response after source delete: %#v", resp)
	}
}

func TestMarketTagsListsPublishedItemTags(t *testing.T) {
	db := testutil.NewTestDB(t)
	fixtures := []struct {
		skillID  string
		revision string
		tags     string
		status   string
	}{
		{skillID: "market_debug_1", revision: "market_debug_rev1", tags: `["debugging"]`, status: "published"},
		{skillID: "market_debug_2", revision: "market_debug_rev2", tags: `["debugging","research"]`, status: "published"},
		{skillID: "market_draft", revision: "market_draft_rev1", tags: `["writing"]`, status: "draft"},
		{skillID: "market_deleted", revision: "market_deleted_rev1", tags: `["obsolete"]`, status: "published"},
	}
	for index, fixture := range fixtures {
		testutil.SeedSkillWithRevision(t, db, fixture.skillID, fixture.revision)
		testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{
			ID:            fixture.skillID + "_item",
			SourceSkillID: fixture.skillID,
			Status:        fixture.status,
			Tags:          []byte(fixture.tags),
			SortOrder:     index,
			CreatedAt:     testutil.TimeFixture(),
			UpdatedAt:     testutil.TimeFixture(),
		})
	}
	deletedAt := testutil.TimeFixture()
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "market_deleted").Update("deleted_at", deletedAt).Error; err != nil {
		t.Fatalf("delete market source: %v", err)
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	req := httptest.NewRequest(http.MethodGet, "/api/core/skill-market/tags", nil)
	rec := httptest.NewRecorder()

	MarketTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Tags []string `json:"tags"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 || len(resp.Data.Tags) != 2 || resp.Data.Tags[0] != "debugging" || resp.Data.Tags[1] != "research" {
		t.Fatalf("tags = %#v, want [debugging research]", resp.Data.Tags)
	}

	filterReq := httptest.NewRequest(http.MethodGet, "/api/core/skill-market?tags=research", nil)
	filterReq.Header.Set("X-User-Id", "user_002")
	filterRec := httptest.NewRecorder()
	MarketList(filterRec, filterReq)
	if filterRec.Code != http.StatusOK {
		t.Fatalf("filtered status=%d body=%s, want 200", filterRec.Code, filterRec.Body.String())
	}
	var filtered struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				MarketItemID string   `json:"market_item_id"`
				Tags         []string `json:"tags"`
			} `json:"items"`
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.NewDecoder(filterRec.Body).Decode(&filtered); err != nil {
		t.Fatalf("decode filtered response: %v", err)
	}
	if filtered.Code != 0 || filtered.Data.Total != 1 || len(filtered.Data.Items) != 1 || filtered.Data.Items[0].MarketItemID != "market_debug_2_item" {
		t.Fatalf("unexpected filtered response: %#v", filtered)
	}
	if got := filtered.Data.Items[0].Tags; len(got) != 2 || got[0] != "debugging" || got[1] != "research" {
		t.Fatalf("filtered item tags = %#v", got)
	}
}

type marketListTestItem struct {
	MarketItemID     string `json:"market_item_id"`
	Installed        bool   `json:"installed"`
	InstalledSkillID string `json:"installed_skill_id"`
}

func marketListItemForUser(t *testing.T, userID string) marketListTestItem {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/core/skill-market?page=1&page_size=100", nil)
	req.Header.Set("X-User-Id", userID)
	rec := httptest.NewRecorder()

	MarketList(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Items []marketListTestItem `json:"items"`
			Total int                  `json:"total"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 || resp.Data.Total != 1 || len(resp.Data.Items) != 1 {
		t.Fatalf("unexpected market list response: %#v", resp)
	}
	return resp.Data.Items[0]
}
