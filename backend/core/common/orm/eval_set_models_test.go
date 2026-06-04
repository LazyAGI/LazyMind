package orm

import (
	"path/filepath"
	"testing"
)

func TestEvalSetModelsAutoMigrate(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "evalset.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}

	if err := db.AutoMigrate(&EvalSet{}, &EvalSetShard{}, &EvalSetItem{}); err != nil {
		t.Fatalf("auto migrate eval set models: %v", err)
	}

	for _, table := range []string{"eval_sets", "eval_set_shards", "eval_set_items"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("expected table %s to exist", table)
		}
	}
}
