package migrations_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestReadingUsageMigrationPreservesHistoricalClaims(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := openDatabase(t, dialect)
			for _, version := range []string{"v0_1", "v0_2"} {
				applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
			}
			up := migrationPath(t, "dev_mode/v0_3/*_create_reading_usage.up.sql")
			paths, _ := filepath.Glob("dev_mode/v0_3/*.up.sql")
			sort.Strings(paths)
			for _, p := range paths {
				if p != up {
					applySQL(t, db, p, dialect)
				}
			}
			for _, row := range []struct{ id, kind, resource, payload, result string }{
				{"study", "document_reading_preparation", "reading_automatic", `{"source_sha256":"same"}`, `{"model_requests":100}`},
				{"retry", "document_reading_preparation", "reading_automatic", `{"source_sha256":"same"}`, `{"model_requests":2}`},
				{"cache", "document_reading_preparation", "reading_automatic", `{"source_sha256":"cached"}`, `{"model_requests":0}`},
				{"manual", "document_reading_preparation", "reading_explicit", `{"source_sha256":"manual"}`, `{"model_requests":1}`},
				{"parse", "document_pdf_ir", "document_pdf_ir", `{"source_sha256":"same","automatic":true}`, `{}`},
			} {
				q := `INSERT INTO async_jobs(id,job_type,resource_type,create_user_id,status,payload_json,result_json,started_at,next_run_at,created_at,updated_at) VALUES ('` + row.id + `','` + row.kind + `','` + row.resource + `','u','succeeded','` + row.payload + `','` + row.result + `',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			applySQL(t, db, up, dialect)
			var n int
			if err := db.QueryRow("SELECT count(*) FROM reading_usage").Scan(&n); err != nil || n != 2 {
				t.Fatalf("deduped historical claims: %d %v", n, err)
			}
			applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
			if err := db.QueryRow("SELECT count(*) FROM async_jobs").Scan(&n); err != nil || n != 5 {
				t.Fatalf("rollback lost jobs: %d %v", n, err)
			}
		})
	}
}
