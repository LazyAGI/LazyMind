package orm

import (
	"testing"
)

func TestEvalSetModelsAutoMigrate(t *testing.T) {
	db := MigrateTestDB(t, &EvalSet{}, &EvalSetShard{}, &EvalSetItem{})

	for _, table := range []string{"eval_sets", "eval_set_shards", "eval_set_items"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("expected table %s to exist", table)
		}
	}
}
