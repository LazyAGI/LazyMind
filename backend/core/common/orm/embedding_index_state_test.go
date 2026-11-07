package orm

import "testing"

func TestEmbeddingIndexStateAutoMigrate(t *testing.T) {
	db := MigrateTestDB(t, &EmbeddingIndexState{})
	if !db.Migrator().HasTable(&EmbeddingIndexState{}) {
		t.Fatal("embedding index state table missing")
	}
	for _, column := range []string{
		"scope", "active_model_id", "active_source", "active_model_name", "active_provider_name", "signature", "phase",
		"generation", "target_model_id", "target_source", "target_signature", "updated_at",
	} {
		if !db.Migrator().HasColumn(&EmbeddingIndexState{}, column) {
			t.Fatalf("embedding index state column %q missing", column)
		}
	}
}
