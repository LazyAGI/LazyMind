package migrations_test

import (
	"path/filepath"
	"sort"
	"testing"
)

func TestEmbeddingIndexStateMigrationAndAggregate(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, aggregate := range []bool{false, true} {
				db := openDatabase(t, dialect)
				for _, version := range []string{"v0_1", "v0_2"} {
					applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
				}
				if aggregate {
					applySQL(t, db, migrationPath(t, "version_mode/v0_3/*.up.sql"), dialect)
				} else {
					paths, err := filepath.Glob("dev_mode/v0_3/*.up.sql")
					if err != nil {
						t.Fatal(err)
					}
					sort.Strings(paths)
					for _, path := range paths {
						applySQL(t, db, path, dialect)
					}
				}
				_, err := db.Exec("INSERT INTO embedding_index_states(scope,active_model_id,signature,phase,generation,updated_at) VALUES ('global','model','sha256:test','ready',1,CURRENT_TIMESTAMP)")
				if err != nil {
					t.Fatal(err)
				}
				var modelID, signature string
				if err := db.QueryRow("SELECT active_model_id,signature FROM embedding_index_states WHERE scope='global'").Scan(&modelID, &signature); err != nil || modelID != "model" || signature != "sha256:test" {
					t.Fatalf("embedding state round trip: model=%q signature=%q err=%v", modelID, signature, err)
				}
				down := migrationPath(t, "dev_mode/v0_3/*_embedding_index_state.down.sql")
				if aggregate {
					down = migrationPath(t, "version_mode/v0_3/*.down.sql")
				}
				applySQL(t, db, down, dialect)
				if _, err := db.Exec("SELECT scope FROM embedding_index_states"); err == nil {
					t.Fatal("down migration left embedding index state table behind")
				}
			}
		})
	}
}
