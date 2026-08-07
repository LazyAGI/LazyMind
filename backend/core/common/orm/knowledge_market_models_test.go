package orm

import (
	"path/filepath"
	"testing"
)

// TestKnowledgeMarketModelsAutoMigrate verifies that the knowledge market
// tables are created correctly on a fresh database.
func TestKnowledgeMarketModelsAutoMigrate(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "knowledge_market.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}

	if err := db.AutoMigrate(&KnowledgeMarketItem{}, &KnowledgeMarketInstall{}); err != nil {
		t.Fatalf("auto migrate knowledge market models: %v", err)
	}

	for _, model := range []any{
		&KnowledgeMarketItem{},
		&KnowledgeMarketInstall{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("expected table for %T to exist", model)
		}
	}

	itemColumns := []string{
		"id", "category", "name", "description", "icon", "domain", "tags",
		"version", "version_date", "version_note",
		"package_url", "package_revision", "online_access_url", "data_source",
		"sample_questions", "status", "sort_order", "created_at", "updated_at",
	}
	for _, col := range itemColumns {
		if !db.Migrator().HasColumn(&KnowledgeMarketItem{}, col) {
			t.Fatalf("expected knowledge_market_items.%s column", col)
		}
	}

	installColumns := []string{
		"market_item_id", "user_id", "installed_version", "dataset_id",
		"install_state", "installed_at", "config", "created_at", "updated_at",
	}
	for _, col := range installColumns {
		if !db.Migrator().HasColumn(&KnowledgeMarketInstall{}, col) {
			t.Fatalf("expected knowledge_market_installs.%s column", col)
		}
	}

	if !db.Migrator().HasIndex(&KnowledgeMarketItem{}, "idx_knowledge_market_items_category_status") {
		t.Fatal("expected idx_knowledge_market_items_category_status index")
	}
	if !db.Migrator().HasIndex(&KnowledgeMarketInstall{}, "idx_knowledge_market_installs_user") {
		t.Fatal("expected idx_knowledge_market_installs_user index")
	}
}
