package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/skillv2/testutil"
	"lazymind/core/store"
)

func seedMarketPresentation(t *testing.T) *testutil.TestDB {
	t.Helper()
	db := testutil.NewTestDB(t)
	for i, id := range []string{"alpha", "beta"} {
		testutil.SeedSkillWithRevision(t, db, id, id+"-rev")
		if err := db.Model(&testutil.SkillRow{}).Where("id = ?", id).Updates(map[string]any{
			"skill_name": "research-" + id, "description": "Original research instructions", "owner_user_id": "skill-market:market-" + id,
		}).Error; err != nil {
			t.Fatal(err)
		}
		testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{
			ID: "market-" + id, SourceSkillID: id, Status: "published", SortOrder: i,
			Tags:        []byte(`["research"]`),
			DisplayI18n: []byte(`{"zh-CN":{"name":"深度调研","description":"检索资料并核对证据","tag_labels":{"research":"研究"}}}`),
			CreatedAt:   testutil.TimeFixture(), UpdatedAt: testutil.TimeFixture(),
		})
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	return db
}

func decodeMarketPresentationResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

func TestMarketPresentationBilingualSearchBeforePagination(t *testing.T) {
	seedMarketPresentation(t)
	for _, keyword := range []string{"深度调研", "核对证据", "research-"} {
		req := httptest.NewRequest(http.MethodGet, "/api/core/skill-market?keyword="+url.QueryEscape(keyword)+"&page=2&page_size=1", nil)
		req.Header.Set("Accept-Language", "zh-CN")
		rec := httptest.NewRecorder()
		MarketList(rec, req)
		data := decodeMarketPresentationResponse(t, rec)
		if data["total"] != float64(2) {
			t.Fatalf("keyword %q: total=%v, want 2", keyword, data["total"])
		}
		items := data["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("keyword %q: items=%v", keyword, items)
		}
		item := items[0].(map[string]any)
		if item["market_item_id"] != "market-beta" || item["display_name"] != "深度调研" {
			t.Fatalf("item=%v", item)
		}
		if item["source"].(map[string]any)["name"] != "research-beta" {
			t.Fatal("presentation replaced canonical name")
		}
		if rec.Header().Get("Content-Language") != "zh-CN" || !strings.Contains(rec.Header().Get("Vary"), "Accept-Language") {
			t.Fatalf("language headers=%v", rec.Header())
		}
	}
}

func TestMarketPresentationDetailAndTagsRespectLanguage(t *testing.T) {
	seedMarketPresentation(t)
	for _, locale := range []string{"zh-CN", "en-US"} {
		req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/core/skill-market/market-alpha", nil), map[string]string{"market_item_id": "market-alpha"})
		req.Header.Set("Accept-Language", locale)
		rec := httptest.NewRecorder()
		MarketGet(rec, req)
		data := decodeMarketPresentationResponse(t, rec)
		want := "深度调研"
		if locale == "en-US" {
			want = "research-alpha"
		}
		if data["display_name"] != want || data["display_locale"] != locale {
			t.Fatalf("locale=%s data=%v", locale, data)
		}
		tagReq := httptest.NewRequest(http.MethodGet, "/api/core/skill-market/tags", nil)
		tagReq.Header.Set("Accept-Language", locale)
		tagRec := httptest.NewRecorder()
		MarketTags(tagRec, tagReq)
		tags := decodeMarketPresentationResponse(t, tagRec)
		if got := tags["tags"].([]any); len(got) != 1 || got[0] != "research" {
			t.Fatalf("canonical tags=%v", got)
		}
		if locale == "zh-CN" && tags["tag_labels"].(map[string]any)["research"] != "研究" {
			t.Fatalf("labels=%v", tags)
		}
	}
}

func TestMarketPresentationOnlyEditPreservesSourceRevision(t *testing.T) {
	db := seedMarketPresentation(t)
	var before testutil.SkillRow
	if err := db.Where("id = ?", "alpha").Take(&before).Error; err != nil {
		t.Fatal(err)
	}
	req := mux.SetURLVars(httptest.NewRequest(http.MethodPatch, "/api/core/admin/skill-market/market-alpha", strings.NewReader(`{"display_name":"资料研究","display_description":"整理来源与研究结论","tag_labels":{"research":"调研"}}`)), map[string]string{"market_item_id": "market-alpha"})
	req.Header.Set("X-User-Id", "admin")
	rec := httptest.NewRecorder()
	MarketEdit(rec, req)
	decodeMarketPresentationResponse(t, rec)
	var after testutil.SkillRow
	if err := db.Where("id = ?", "alpha").Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if *after.HeadRevisionID != *before.HeadRevisionID || after.SkillName != before.SkillName || after.Description != before.Description {
		t.Fatal("display edit changed source runtime metadata")
	}
	getReq := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/core/skill-market/market-alpha", nil), map[string]string{"market_item_id": "market-alpha"})
	getRec := httptest.NewRecorder()
	MarketGet(getRec, getReq)
	data := decodeMarketPresentationResponse(t, getRec)
	if data["display_name"] != "资料研究" || data["display_description"] != "整理来源与研究结论" {
		t.Fatalf("presentation not persisted: %v", data)
	}
}
