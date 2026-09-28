package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestListHTTPNameOnlySearchFiltersBeforePagination(t *testing.T) {
	db := testutil.NewTestDB(t)
	for _, id := range []string{"name-a", "name-b", "description", "category", "tags", "head", "index", "other-owner", "deleted"} {
		testutil.SeedSkillWithRevision(t, db, id, "rev-"+id)
		setHandlerSkillMetadata(t, db, id, "Other skill "+id, "writing", "daily notes", `["team"]`)
	}
	for _, id := range []string{"name-a", "name-b", "other-owner", "deleted"} {
		setHandlerSkillMetadata(t, db, id, "摘要 Needle "+id, "writing", "daily notes", `["team"]`)
	}
	setHandlerSkillMetadata(t, db, "description", "Description hit", "writing", "摘要 needle", `["team"]`)
	setHandlerSkillMetadata(t, db, "category", "Category hit", "摘要 needle", "daily notes", `["team"]`)
	setHandlerSkillMetadata(t, db, "tags", "Tag hit", "writing", "daily notes", `["摘要 needle"]`)
	setHandlerHeadContent(t, db, "rev-head", "摘要 needle")
	seedHandlerSearchIndex(t, db, "index", "rev-index", "摘要 needle")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "other-owner").Update("owner_user_id", "user_002").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "deleted").Update("deleted_at", testutil.TimeFixture()).Error; err != nil {
		t.Fatal(err)
	}
	withHandlerDB(t, db)
	for _, tc := range []struct {
		query string
		total float64
		id    string
	}{
		{query: "&name_only=true&page=1&page_size=1", total: 2, id: "name-b"},
		{query: "&name_only=true&page=2&page_size=1", total: 2, id: "name-a"},
		{query: "&name_only=true&page=3&page_size=1", total: 2},
		{query: "&page_size=20", total: 7},
		{query: "&name_only=false&page_size=20", total: 7},
	} {
		t.Run(tc.query, func(t *testing.T) {
			data := listSkillsHTTP(t, "/api/core/skills?keyword="+url.QueryEscape(" 摘要 NEEDLE ")+tc.query)
			items := data["items"].([]any)
			if data["total"] != tc.total {
				t.Fatalf("total = %v, want %v", data["total"], tc.total)
			}
			if tc.id != "" && (len(items) != 1 || items[0].(map[string]any)["skill_id"] != tc.id) {
				t.Fatalf("items = %v, want only %s", items, tc.id)
			}
			if tc.query == "&name_only=true&page=3&page_size=1" && len(items) != 0 {
				t.Fatalf("last page = %v, want empty", items)
			}
		})
	}
}

func TestListHTTPNameOnlySearchTreatsWildcardCharactersLiterally(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "literal", "rev-literal")
	testutil.SeedSkillWithRevision(t, db, "similar", "rev-similar")
	setHandlerSkillMetadata(t, db, "literal", "Summary_摘要%Nice!", "writing", "plain", `[]`)
	setHandlerSkillMetadata(t, db, "similar", "SummaryX摘要AnythingNice!", "writing", "plain", `[]`)
	withHandlerDB(t, db)
	data := listSkillsHTTP(t, "/api/core/skills?name_only=true&keyword="+url.QueryEscape(" summary_摘要%nice! "))
	items := data["items"].([]any)
	if data["total"] != float64(1) || len(items) != 1 || items[0].(map[string]any)["skill_id"] != "literal" {
		t.Fatalf("result = %v, want literal name match", data)
	}
}

func TestListHTTPRejectsInvalidNameOnly(t *testing.T) {
	db := testutil.NewTestDB(t)
	withHandlerDB(t, db)
	req := httptest.NewRequest(http.MethodGet, "/api/core/skills?name_only=invalid", nil)
	req.Header.Set("X-User-Id", "user_001")
	rec := httptest.NewRecorder()
	List(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
