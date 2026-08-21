package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/core/common"
	skillservice "lazymind/core/skillv2/service"
	"lazymind/core/skillv2/testutil"
	"lazymind/core/store"
)

func TestListPageSizeResponseKeepsRequestedValue(t *testing.T) {
	db := testutil.NewTestDB(t)
	for i := 0; i < 150; i++ {
		testutil.SeedSkillWithRevision(t, db, fmt.Sprintf("skill-%03d", i), fmt.Sprintf("rev-%03d", i))
	}
	withHandlerDB(t, db)

	tests := []struct {
		name         string
		query        string
		wantPageSize float64
		wantItems    int
	}{
		{name: "default", query: "", wantPageSize: 20, wantItems: 20},
		{name: "within limit", query: "?page_size=50", wantPageSize: 50, wantItems: 50},
		{name: "over limit", query: "?page_size=500", wantPageSize: 500, wantItems: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := listSkillsHTTP(t, "/api/core/skills"+tt.query)
			if data["page_size"] != tt.wantPageSize {
				t.Fatalf("page_size = %#v, want %v", data["page_size"], tt.wantPageSize)
			}
			items, ok := data["items"].([]any)
			if !ok {
				t.Fatalf("items = %#v, want array", data["items"])
			}
			if len(items) != tt.wantItems {
				t.Fatalf("items len = %d, want %d", len(items), tt.wantItems)
			}
		})
	}
}

func TestListHTTPUsesFreshServiceKeywordSearch(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill-current", "rev-current")
	testutil.SeedSkillWithRevision(t, db, "skill-stale", "rev-stale")
	setHandlerSkillMetadata(t, db, "skill-current", "Planner", "writing", "daily notes", `["team"]`)
	setHandlerSkillMetadata(t, db, "skill-stale", "Researcher", "writing", "daily notes", `["team"]`)
	seedHandlerSearchIndex(t, db, "skill-current", "rev-current", "needle from current index")
	seedHandlerSearchIndex(t, db, "skill-stale", "old-rev", "needle from stale index")
	setHandlerHeadContent(t, db, "rev-stale", "current head without requested term")
	withHandlerDB(t, db)

	data := listSkillsHTTP(t, "/api/core/skills?keyword=needle&page_size=20")
	if data["total"] != float64(1) {
		t.Fatalf("total = %#v, want 1", data["total"])
	}
	items, ok := data["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v, want one item", data["items"])
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("item = %#v, want object", items[0])
	}
	if item["skill_id"] != "skill-current" {
		t.Fatalf("skill_id = %#v, want skill-current", item["skill_id"])
	}

	serviceResp, err := skillservice.NewSkillService(skillservice.SkillServiceDeps{DB: db.DB}).ListSkills(context.Background(), skillservice.ListSkillsRequest{
		UserID:  "user_001",
		Keyword: "needle",
		Limit:   20,
	})
	if err != nil {
		t.Fatalf("service ListSkills returned error: %v", err)
	}
	if serviceResp.Total != 1 || len(serviceResp.Items) != 1 || serviceResp.Items[0].ID != item["skill_id"] {
		t.Fatalf("service response = %#v, http item = %#v; want same keyword semantics", serviceResp, item)
	}
}

func withHandlerDB(t *testing.T, db *testutil.TestDB) {
	t.Helper()
	oldDB := store.DB()
	t.Cleanup(func() { store.Init(oldDB, nil, nil) })
	store.Init(db.DB, nil, nil)
}

func listSkillsHTTP(t *testing.T, target string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("X-User-Id", "user_001")
	rec := httptest.NewRecorder()
	List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response common.APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v, want object", response.Data)
	}
	return data
}

func setHandlerSkillMetadata(t *testing.T, db *testutil.TestDB, skillID, name, category, description, tags string) {
	t.Helper()
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", skillID).Updates(map[string]any{
		"skill_name":  name,
		"category":    category,
		"description": description,
		"tags":        []byte(tags),
	}).Error; err != nil {
		t.Fatalf("update skill metadata: %v", err)
	}
}

func setHandlerHeadContent(t *testing.T, db *testutil.TestDB, revisionID, content string) {
	t.Helper()
	if err := db.Model(&testutil.SkillBlobRow{}).
		Where("hash = ?", "h_skill_"+revisionID).
		Updates(map[string]any{"content": []byte(content), "size": len([]byte(content))}).Error; err != nil {
		t.Fatalf("update head content: %v", err)
	}
}

func seedHandlerSearchIndex(t *testing.T, db *testutil.TestDB, skillID, headRevisionID, content string) {
	t.Helper()
	testutil.MustCreate(t, db, &testutil.SkillSearchIndexRow{
		SkillID:        skillID,
		OwnerUserID:    "user_001",
		HeadRevisionID: headRevisionID,
		Content:        content,
		UpdatedAt:      testutil.TimeFixture(),
	})
}
