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
