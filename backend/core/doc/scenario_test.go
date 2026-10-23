package doc

import (
	"testing"
)

func TestScenarioPresetsAndCustomOwnership(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.Exec(`CREATE TABLE learning_capability_profiles (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO learning_capability_profiles (id, owner_id) VALUES ('my-profile', 'u'), ('foreign-profile', 'other')`).Error; err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"general", "academic_papers", "legal", "technical", "chinese_modern", "chinese_classical", "english_learning", "historical", "my-profile"} {
		got, err := normalizeScenario(t.Context(), db.DB, "u", key)
		if err != nil || got != key {
			t.Fatalf("scenario %s: got %q, %v", key, got, err)
		}
	}
	for _, key := range []string{"foreign-profile", "missing", "custom"} {
		if _, err := normalizeScenario(t.Context(), db.DB, "u", key); err == nil {
			t.Fatalf("unowned scenario %q was accepted", key)
		}
	}
	if got, err := normalizeScenario(t.Context(), db.DB, "u", ""); err != nil || got != "general" {
		t.Fatalf("legacy scene: %q %v", got, err)
	}
}
