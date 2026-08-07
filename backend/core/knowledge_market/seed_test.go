package knowledge_market

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

const testCatalog = `
knowledge_market_items:
  - id: law-cn
    category: industry
    name: 中国法律法规知识库
    description: 法律知识库
    icon: "⚖"
    domain: 法律
    tags: [法律法规, 司法解释]
    version: v2.3.0
    version_date: "2026-07-18"
    version_note: 新增司法解释
    package_url: ""
    online_access_url: https://www.modelscope.cn/datasets/simpleai/HC3-Chinese/summary
    data_source: 官方公开文件
    sample_questions:
      - 劳动合同在什么情况下可以解除？
`

const testCatalogSecond = `
knowledge_market_items:
  - id: finance
    category: industry
    name: 金融监管与业务知识库
    description: 金融知识库
    icon: "¥"
    domain: 金融
    tags: [监管规则]
    version: v1.8.0
    version_date: "2026-07-10"
    data_source: 金融监管机构公开文件
    sample_questions: []
`

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "knowledge_market_test.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	if err := db.AutoMigrate(&orm.KnowledgeMarketItem{}); err != nil {
		t.Fatalf("auto migrate knowledge market models: %v", err)
	}
	return db.DB
}

func writeCatalog(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

func getItem(t *testing.T, db *gorm.DB, id string) *orm.KnowledgeMarketItem {
	t.Helper()
	var row orm.KnowledgeMarketItem
	if err := db.Where("id = ?", id).Take(&row).Error; err != nil {
		t.Fatalf("load item %q: %v", id, err)
	}
	return &row
}

func countItems(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&orm.KnowledgeMarketItem{}).Count(&n).Error; err != nil {
		t.Fatalf("count items: %v", err)
	}
	return n
}

func TestLoadCatalogParsesAndUpserts(t *testing.T) {
	db := newTestDB(t)
	path := writeCatalog(t, testCatalog)

	if err := SeedCatalog(context.Background(), db, path); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	if n := countItems(t, db); n != 1 {
		t.Fatalf("expected 1 item, got %d", n)
	}

	row := getItem(t, db, "law-cn")
	if row.Category != "industry" || row.Name != "中国法律法规知识库" {
		t.Fatalf("unexpected seeded content: %+v", row)
	}
	if row.Status != "published" || row.SortOrder != 0 {
		t.Fatalf("expected published with sort_order 0, got status=%q sort_order=%d", row.Status, row.SortOrder)
	}
	if row.OnlineAccessURL != "https://www.modelscope.cn/datasets/simpleai/HC3-Chinese/summary" {
		t.Fatalf("unexpected online_access_url %q", row.OnlineAccessURL)
	}
}

func TestSeedCatalogRejectsInvalidItem(t *testing.T) {
	db := newTestDB(t)
	path := writeCatalog(t, `
knowledge_market_items:
  - id: bad
    category: industry
    name: ""
`)
	if err := SeedCatalog(context.Background(), db, path); err == nil {
		t.Fatal("expected error for item missing name")
	}
	if n := countItems(t, db); n != 0 {
		t.Fatalf("expected no rows after failed seed, got %d", n)
	}
}

func TestSeedCatalogIdempotentKeepsUpdatedAt(t *testing.T) {
	db := newTestDB(t)
	path := writeCatalog(t, testCatalog)

	if err := SeedCatalog(context.Background(), db, path); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	first := getItem(t, db, "law-cn")
	if err := SeedCatalog(context.Background(), db, path); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	second := getItem(t, db, "law-cn")

	if n := countItems(t, db); n != 1 {
		t.Fatalf("expected 1 item after idempotent seed, got %d", n)
	}
	if !first.UpdatedAt.Equal(second.UpdatedAt) {
		t.Fatalf("updated_at changed on unchanged catalog: %v -> %v", first.UpdatedAt, second.UpdatedAt)
	}
}

func TestSeedCatalogUpdatesChangedContent(t *testing.T) {
	db := newTestDB(t)
	path := writeCatalog(t, testCatalog)

	if err := SeedCatalog(context.Background(), db, path); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	first := getItem(t, db, "law-cn")

	changed := strings.Replace(testCatalog, "description: 法律知识库", "description: 更新后的法律知识库描述", 1)
	changed = strings.Replace(changed, "online_access_url: https://www.modelscope.cn/datasets/simpleai/HC3-Chinese/summary", "online_access_url: https://www.modelscope.cn/datasets/simpleai/HC3-Chinese/summary?tab=files", 1)
	if changed == testCatalog {
		t.Fatal("fixture replacement failed")
	}
	path = writeCatalog(t, changed)
	if err := SeedCatalog(context.Background(), db, path); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	second := getItem(t, db, "law-cn")
	if first.UpdatedAt.Equal(second.UpdatedAt) {
		t.Fatalf("expected updated_at to change when content changes")
	}
	if second.OnlineAccessURL != "https://www.modelscope.cn/datasets/simpleai/HC3-Chinese/summary?tab=files" {
		t.Fatalf("expected online_access_url updated, got %q", second.OnlineAccessURL)
	}
}

func TestSeedCatalogOfflinesRemovedItems(t *testing.T) {
	db := newTestDB(t)

	if err := SeedCatalog(context.Background(), db, writeCatalog(t, testCatalog)); err != nil {
		t.Fatalf("seed with law-cn: %v", err)
	}
	if err := SeedCatalog(context.Background(), db, writeCatalog(t, testCatalogSecond)); err != nil {
		t.Fatalf("seed with finance: %v", err)
	}

	removed := getItem(t, db, "law-cn")
	if removed.Status != "offline" {
		t.Fatalf("expected law-cn to become offline, got %q", removed.Status)
	}
	kept := getItem(t, db, "finance")
	if kept.Status != "published" {
		t.Fatalf("expected finance to stay published, got %q", kept.Status)
	}
	if n := countItems(t, db); n != 2 {
		t.Fatalf("expected 2 rows (offline keeps rows), got %d", n)
	}
}
