package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/core/skillv2/testutil"
	"lazymind/core/store"
)

func TestPersonalSkillFiltersExcludeMarketplaceSource(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "personal_skill", "personal_rev")
	testutil.SeedSkillWithRevision(t, db, "market_source", "market_rev")
	for skillID, values := range map[string]map[string]any{
		"personal_skill": {
			"owner_user_id": "admin_001",
			"category":      "personal-category",
			"tags":          []byte(`["personal-tag"]`),
		},
		"market_source": {
			"owner_user_id": "admin_001",
			"category":      "market-category",
			"tags":          []byte(`["market-tag"]`),
		},
	} {
		if err := db.Model(&testutil.SkillRow{}).Where("id = ?", skillID).Updates(values).Error; err != nil {
			t.Fatalf("update %s: %v", skillID, err)
		}
	}
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{
		ID:            "market_item1",
		SourceSkillID: "market_source",
		Status:        "published",
		CreatedAt:     testutil.TimeFixture(),
		UpdatedAt:     testutil.TimeFixture(),
	})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	assertPersonalFilterValues(t, ListTags, "tags", []string{"personal-tag"})
	assertPersonalFilterValues(t, ListCategories, "categories", []string{"personal-category"})
}

func assertPersonalFilterValues(t *testing.T, handler http.HandlerFunc, field string, want []string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/core/skills/"+field, nil)
	req.Header.Set("X-User-Id", "admin_001")
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("%s status=%d body=%s, want 200", field, rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int                 `json:"code"`
		Data map[string][]string `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode %s response: %v", field, err)
	}
	got := resp.Data[field]
	if resp.Code != 0 || len(got) != len(want) || len(got) != 1 || got[0] != want[0] {
		t.Fatalf("%s = %#v, want %#v", field, got, want)
	}
}
