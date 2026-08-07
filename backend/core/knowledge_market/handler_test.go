package knowledge_market

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// handlerTestCatalog mirrors the real catalog shape with five items that have
// distinct domains and one tag (司法解释) that must never match keyword search.
const handlerTestCatalog = `
knowledge_market_items:
  - id: law-cn
    category: industry
    name: 中国法律法规知识库
    description: 收录现行有效的法律、行政法规。提供法规查找与条款溯源。
    icon: "⚖"
    domain: 法律
    tags: [法律法规, 司法解释]
    version: v2.3.0
    version_date: "2026-07-18"
    version_note: 新增司法解释
    package_url: ""
    online_access_url: https://www.modelscope.cn/datasets/simpleai/HC3-Chinese/summary
    data_source: 国家法律法规数据库及官方公开文件
    sample_questions:
      - 劳动合同在什么情况下可以解除？
  - id: finance
    category: industry
    name: 金融监管与业务知识库
    description: 收录金融监管规则与常见业务问题。
    icon: "¥"
    domain: 金融
    tags: [监管规则, 金融业务]
    version: v1.8.0
    version_date: "2026-07-10"
    version_note: ""
    package_url: ""
    data_source: 金融监管机构公开文件
    sample_questions: []
  - id: gov
    category: industry
    name: 政务办事知识库
    description: 收录高频政务办事指南与公共服务信息。
    icon: "🏛"
    domain: 政务
    tags: [办事指南, 公共服务]
    version: v1.2.0
    version_date: "2026-06-01"
    version_note: ""
    package_url: ""
    data_source: 政府公开文件
    sample_questions: []
  - id: law-qa
    category: evaluation
    name: 法律问答评测集
    description: 面向法律领域问答能力的评测数据集。
    icon: "📖"
    domain: 法律问答
    tags: [法律, 中文]
    version: v1.0.0
    version_date: "2026-07-01"
    version_note: ""
    package_url: ""
    data_source: 人工标注
    sample_questions: []
  - id: medical-qa
    category: evaluation
    name: 医学问答评测集
    description: 面向医学领域问答能力的评测数据集。
    icon: "🩺"
    domain: 医学问答
    tags: [医学, 论文问答]
    version: v1.0.0
    version_date: "2026-07-01"
    version_note: ""
    package_url: ""
    data_source: 人工标注
    sample_questions: []
`

// newHandlerTestRouter seeds a test catalog, initializes the shared store and
// returns a router with the three read-only knowledge market routes.
func newHandlerTestRouter(t *testing.T) *mux.Router {
	t.Helper()
	db := newTestDB(t)
	path := writeCatalog(t, handlerTestCatalog)
	if err := SeedCatalog(context.Background(), db, path); err != nil {
		t.Fatalf("seed handler test catalog: %v", err)
	}
	store.Init(db, db, nil)
	return marketRouter()
}

// marketRouter registers the read-only routes exactly like routes.go does.
func marketRouter() *mux.Router {
	r := mux.NewRouter()
	r.UseEncodedPath()
	r.HandleFunc("/knowledge-market", MarketList).Methods(http.MethodGet)
	r.HandleFunc("/knowledge-market/domains", MarketDomains).Methods(http.MethodGet)
	r.HandleFunc("/knowledge-market/items/{market_item_id}", MarketGet).Methods(http.MethodGet)
	r.HandleFunc("/knowledge-market/tasks", MarketListInstallTasks).Methods(http.MethodGet)
	r.HandleFunc("/knowledge-market/tasks/{job_id}", MarketGetInstallTask).Methods(http.MethodGet)
	r.HandleFunc("/knowledge-market/installs", MarketListInstalls).Methods(http.MethodGet)
	return r
}

func performGet(t *testing.T, router *mux.Router, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// mustData asserts a 200 OK business response and decodes its data payload.
func mustData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("business code=%d body=%s", resp.Code, rec.Body.String())
	}
	return resp.Data
}

func TestMarketListDefault(t *testing.T) {
	router := newHandlerTestRouter(t)
	data := mustData(t, performGet(t, router, "/knowledge-market"))

	if got := data["total"].(float64); got != 5 {
		t.Fatalf("total=%v, want 5", got)
	}
	if got := data["page"].(float64); got != 1 {
		t.Fatalf("page=%v, want 1", got)
	}
	if got := data["page_size"].(float64); got != 20 {
		t.Fatalf("page_size=%v, want 20", got)
	}
	items, ok := data["items"].([]any)
	if !ok || len(items) != 5 {
		t.Fatalf("items length=%d, want 5", len(items))
	}
	first := items[0].(map[string]any)
	if first["id"] != "law-cn" {
		t.Fatalf("first item id=%v, want law-cn", first["id"])
	}
	if _, exists := first["files"]; exists {
		t.Fatal("list item must not include files detail")
	}
	if tags := first["tags"].([]any); len(tags) != 2 {
		t.Fatalf("tags=%v, want 2 entries", tags)
	}
	if first["online_access_url"] != "https://www.modelscope.cn/datasets/simpleai/HC3-Chinese/summary" {
		t.Fatalf("online_access_url=%v, want catalog value", first["online_access_url"])
	}
}

func TestMarketListCategoryFilter(t *testing.T) {
	router := newHandlerTestRouter(t)

	data := mustData(t, performGet(t, router, "/knowledge-market?category=industry"))
	if got := data["total"].(float64); got != 3 {
		t.Fatalf("industry total=%v, want 3", got)
	}
	data = mustData(t, performGet(t, router, "/knowledge-market?category=evaluation"))
	if got := data["total"].(float64); got != 2 {
		t.Fatalf("evaluation total=%v, want 2", got)
	}
	if rec := performGet(t, router, "/knowledge-market?category=invalid"); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid category status=%d, want 400", rec.Code)
	}
}

func TestMarketListDomainFilter(t *testing.T) {
	router := newHandlerTestRouter(t)

	rec := performGet(t, router, "/knowledge-market?domain="+url.QueryEscape("法律"))
	data := mustData(t, rec)
	if got := data["total"].(float64); got != 1 {
		t.Fatalf("法律 domain total=%v, want 1", got)
	}
	items := data["items"].([]any)
	if items[0].(map[string]any)["id"] != "law-cn" {
		t.Fatalf("unexpected item %v", items[0])
	}

	data = mustData(t, performGet(t, router, "/knowledge-market?domain="+url.QueryEscape("法律问答")))
	if got := data["total"].(float64); got != 1 {
		t.Fatalf("法律问答 domain total=%v, want 1", got)
	}
	if items := data["items"].([]any); items[0].(map[string]any)["id"] != "law-qa" {
		t.Fatalf("unexpected item %v", items[0])
	}
}

func TestMarketListKeywordFilter(t *testing.T) {
	router := newHandlerTestRouter(t)

	// Keyword matches item names.
	data := mustData(t, performGet(t, router, "/knowledge-market?keyword="+url.QueryEscape("评测集")))
	if got := data["total"].(float64); got != 2 {
		t.Fatalf("keyword 评测集 total=%v, want 2", got)
	}

	// Keyword matches domain.
	data = mustData(t, performGet(t, router, "/knowledge-market?keyword="+url.QueryEscape("法律问答")))
	if got := data["total"].(float64); got != 1 {
		t.Fatalf("keyword 法律问答 total=%v, want 1", got)
	}

	// Tags must never match keyword search.
	data = mustData(t, performGet(t, router, "/knowledge-market?keyword="+url.QueryEscape("司法解释")))
	if got := data["total"].(float64); got != 0 {
		t.Fatalf("keyword 司法解释 total=%v, want 0 (tags must not match)", got)
	}
}

func TestMarketListPagination(t *testing.T) {
	router := newHandlerTestRouter(t)

	data := mustData(t, performGet(t, router, "/knowledge-market?page_size=2&page=1"))
	if got := len(data["items"].([]any)); got != 2 {
		t.Fatalf("page1 item count=%d, want 2", got)
	}
	if got := data["total"].(float64); got != 5 {
		t.Fatalf("total=%v, want 5", got)
	}

	data = mustData(t, performGet(t, router, "/knowledge-market?page_size=2&page=3"))
	if got := len(data["items"].([]any)); got != 1 {
		t.Fatalf("page3 item count=%d, want 1", got)
	}

	data = mustData(t, performGet(t, router, "/knowledge-market?page_size=2&page=10"))
	if got := len(data["items"].([]any)); got != 0 {
		t.Fatalf("page10 item count=%d, want 0", got)
	}

	data = mustData(t, performGet(t, router, "/knowledge-market?page_size=200"))
	if got := data["page_size"].(float64); got != 100 {
		t.Fatalf("page_size cap=%v, want 100", got)
	}

	data = mustData(t, performGet(t, router, "/knowledge-market?page=0&page_size=abc"))
	if got := data["page"].(float64); got != 1 || data["page_size"].(float64) != 20 {
		t.Fatalf("invalid pagination fallback page=%v page_size=%v", data["page"], data["page_size"])
	}
}

func TestMarketDomains(t *testing.T) {
	router := newHandlerTestRouter(t)
	data := mustData(t, performGet(t, router, "/knowledge-market/domains"))

	grouped, ok := data["domains"].(map[string]any)
	if !ok {
		t.Fatalf("domains=%v, want grouped by category", data["domains"])
	}

	industry := stringSlice(grouped["industry"])
	wantIndustry := map[string]bool{"法律": true, "金融": true, "政务": true}
	if len(industry) != len(wantIndustry) {
		t.Fatalf("industry domains=%v, want %d entries", industry, len(wantIndustry))
	}
	for _, domain := range industry {
		if !wantIndustry[domain] {
			t.Fatalf("unexpected domain %v", domain)
		}
	}

	evaluation := stringSlice(grouped["evaluation"])
	wantEvaluation := map[string]bool{"法律问答": true, "医学问答": true}
	if len(evaluation) != len(wantEvaluation) {
		t.Fatalf("evaluation domains=%v, want %d entries", evaluation, len(wantEvaluation))
	}
	for _, domain := range evaluation {
		if !wantEvaluation[domain] {
			t.Fatalf("unexpected domain %v", domain)
		}
	}
}

func TestMarketGet(t *testing.T) {
	router := newHandlerTestRouter(t)
	data := mustData(t, performGet(t, router, "/knowledge-market/items/law-cn"))

	if data["id"] != "law-cn" || data["domain"] != "法律" {
		t.Fatalf("unexpected detail: %v", data)
	}
	if questions := data["sample_questions"].([]any); len(questions) != 1 {
		t.Fatalf("sample_questions=%v, want 1 entry", questions)
	}
	if data["package_url"] != "" {
		t.Fatalf("package_url=%v, want empty", data["package_url"])
	}
	if data["online_access_url"] != "https://www.modelscope.cn/datasets/simpleai/HC3-Chinese/summary" {
		t.Fatalf("online_access_url=%v, want catalog value", data["online_access_url"])
	}
	if _, exists := data["package_revision"]; !exists {
		t.Fatal("detail must include package_revision")
	}
	if _, exists := data["files"]; exists {
		t.Fatal("detail must not include files")
	}
	if _, exists := data["package_size"]; exists {
		t.Fatal("detail must not include package_size")
	}
	if _, exists := data["version"]; exists {
		t.Fatal("detail must not expose version")
	}
	if _, exists := data["version_note"]; exists {
		t.Fatal("detail must not expose version_note")
	}
}

func TestMarketGetNotFound(t *testing.T) {
	router := newHandlerTestRouter(t)
	if rec := performGet(t, router, "/knowledge-market/items/does-not-exist"); rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}

func TestMarketListExcludesOfflineItems(t *testing.T) {
	db := newTestDB(t)
	path := writeCatalog(t, handlerTestCatalog)
	if err := SeedCatalog(context.Background(), db, path); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	if err := db.Model(&orm.KnowledgeMarketItem{}).Where("id = ?", "finance").Update("status", "offline").Error; err != nil {
		t.Fatalf("set finance offline: %v", err)
	}
	store.Init(db, db, nil)
	router := marketRouter()

	data := mustData(t, performGet(t, router, "/knowledge-market"))
	if got := data["total"].(float64); got != 4 {
		t.Fatalf("total=%v, want 4", got)
	}
	for _, item := range data["items"].([]any) {
		if item.(map[string]any)["id"] == "finance" {
			t.Fatal("offline item must not appear in list")
		}
	}
	if rec := performGet(t, router, "/knowledge-market/items/finance"); rec.Code != http.StatusNotFound {
		t.Fatalf("offline detail status=%d, want 404", rec.Code)
	}
	data = mustData(t, performGet(t, router, "/knowledge-market/domains"))
	grouped := data["domains"].(map[string]any)
	for _, domain := range stringSlice(grouped["industry"]) {
		if domain == "金融" {
			t.Fatal("offline item domain must not appear in domains")
		}
	}
}

// stringSlice converts a decoded JSON array into a Go string slice.
func stringSlice(value any) []string {
	raw, _ := value.([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(string))
	}
	return out
}

// domainsItemCatalog contains a single item whose id collides with the static
// /knowledge-market/domains route.
const domainsItemCatalog = `
knowledge_market_items:
  - id: domains
    category: industry
    name: 领域同名测试知识库
    description: 验证静态路由不遮蔽条目详情。
    icon: "🧪"
    domain: 测试
    tags: [测试]
    version: v1.0.0
    version_date: "2026-07-01"
    version_note: ""
    package_url: ""
    data_source: 测试数据
    sample_questions: []
`

// TestMarketItemIDNamedDomains verifies an item whose id equals "domains" is
// reachable through the items path and does not shadow the domains route.
func TestMarketItemIDNamedDomains(t *testing.T) {
	db := newTestDB(t)
	if err := SeedCatalog(context.Background(), db, writeCatalog(t, domainsItemCatalog)); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	store.Init(db, db, nil)
	router := marketRouter()

	data := mustData(t, performGet(t, router, "/knowledge-market/domains"))
	if _, ok := data["domains"].(map[string]any); !ok {
		t.Fatalf("domains=%v, want grouped by category", data["domains"])
	}

	data = mustData(t, performGet(t, router, "/knowledge-market/items/domains"))
	if data["id"] != "domains" {
		t.Fatalf("item id=%v, want domains", data["id"])
	}
}
