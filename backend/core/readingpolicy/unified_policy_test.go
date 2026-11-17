package readingpolicy

import (
	"context"
	"encoding/json"
	"lazymind/core/common/orm"
	"testing"
)

func TestUnifiedDefaultsAndCapabilities(t *testing.T) {
	p := Defaults()
	b, _ := json.Marshal(p)
	var got map[string]any
	json.Unmarshal(b, &got)
	limits := got["limits"].(map[string]any)
	if limits["daily_pdf_documents"] != float64(100) || limits["daily_study_documents"] != float64(50) {
		t.Fatalf("independent defaults: %s", b)
	}
	if len(p.Modes) != 3 || p.Modes["ai_study"] != OnDemand || len(Registry()) != 3 {
		t.Fatalf("two preparation categories plus guide: %s", b)
	}
	if _, ok := limits["study_items_per_document"]; ok {
		t.Fatal("item cap still advertised")
	}
}

func TestLegacyPolicyConservativeMigration(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserReadingPolicy{})
	raw := `{"preset":"custom","modes":{"pdf_parse":"automatic","chat_prepare":"automatic","formula":"automatic","figure":"disabled","table":"on_demand"},"limits":{"daily_documents":7,"study_items_per_document":3,"automatic_concurrency":1}}`
	if err := db.Create(&orm.UserReadingPolicy{UserID: "u", PolicyJSON: raw}).Error; err != nil {
		t.Fatal(err)
	}
	p, err := Load(context.Background(), db.DB, "u")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(p)
	var got map[string]any
	json.Unmarshal(b, &got)
	limits := got["limits"].(map[string]any)
	if len(p.Modes) != 3 || p.Modes["ai_study"] != Disabled || limits["daily_pdf_documents"] != float64(7) || limits["daily_study_documents"] != float64(7) {
		t.Fatalf("migration: %s", b)
	}
}
