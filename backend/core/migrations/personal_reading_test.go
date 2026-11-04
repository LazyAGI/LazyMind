package migrations_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestPersonalReadingMigration(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, aggregate := range []bool{false, true} {
				db := openDatabase(t, dialect)
				for _, version := range []string{"v0_1", "v0_2"} {
					applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
				}
				up := migrationPath(t, "dev_mode/v0_3/*_personal_reading_status.up.sql")
				if aggregate {
					applySQL(t, db, migrationPath(t, "version_mode/v0_3/*.up.sql"), dialect)
				} else {
					paths, err := filepath.Glob("dev_mode/v0_3/*.up.sql")
					if err != nil {
						t.Fatal(err)
					}
					sort.Strings(paths)
					for _, path := range paths {
						if path == up {
							continue
						}
						applySQL(t, db, path, dialect)
					}
				}
				if _, err := db.Exec("INSERT INTO academic_user_paper_states(user_id,dataset_id,paper_id,is_important,updated_at) VALUES ('u','d','p',TRUE,CURRENT_TIMESTAMP)"); err != nil {
					t.Fatal(err)
				}
				for _, statement := range []string{
					"INSERT INTO academic_works(id,canonical_title,normalized_title,authors_json,external_ids_json,metadata_provenance_json,created_at,updated_at) VALUES ('p','Preserved title','preserved title','[]','{}','{}',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)",
					"INSERT INTO paper_library_entries(dataset_id,id,academic_work_id,tags_json,metadata_json,created_at,updated_at) VALUES ('d','entry','p','[]','{}',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)",
					"INSERT INTO paper_library_notes(dataset_id,id,paper_id,title,content,created_at,updated_at) VALUES ('d','note','entry','Note','Preserved note',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)",
					"INSERT INTO paper_library_attachments(dataset_id,id,paper_id,name,created_at,updated_at) VALUES ('d','attachment','entry','Original.pdf',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)",
				} {
					if _, err := db.Exec(statement); err != nil {
						t.Fatal(err)
					}
				}
				if !aggregate {
					if _, err := db.Exec("UPDATE paper_library_entries SET status='read'"); err != nil {
						t.Fatal(err)
					}
				}
				if !aggregate {
					applySQL(t, db, up, dialect)
				}
				var status string
				var important bool
				if err := db.QueryRow("SELECT reading_status,is_important FROM academic_user_paper_states WHERE user_id='u'").Scan(&status, &important); err != nil || status != "unread" || !important {
					t.Fatalf("upgrade lost state: %s %v %v", status, important, err)
				}
				if rows, err := db.Query("SELECT status FROM paper_library_entries"); err == nil {
					rows.Close()
					t.Fatal("shared status still present")
				}
				var preserved string
				if err := db.QueryRow("SELECT w.canonical_title || ':' || n.content || ':' || a.name FROM paper_library_entries e JOIN academic_works w ON w.id=e.academic_work_id JOIN paper_library_notes n ON n.paper_id=e.id AND n.dataset_id=e.dataset_id JOIN paper_library_attachments a ON a.paper_id=e.id AND a.dataset_id=e.dataset_id").Scan(&preserved); err != nil || preserved != "Preserved title:Preserved note:Original.pdf" {
					t.Fatalf("library data lost: %s %v", preserved, err)
				}
				applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
				if err := db.QueryRow("SELECT is_important FROM academic_user_paper_states WHERE user_id='u'").Scan(&important); err != nil || !important {
					t.Fatalf("rollback lost importance: %v %v", important, err)
				}
				if err := db.QueryRow("SELECT status FROM paper_library_entries WHERE id='entry'").Scan(&status); err != nil || status != "unread" {
					t.Fatalf("rollback status: %s %v", status, err)
				}
				if aggregate {
					applySQL(t, db, migrationPath(t, "version_mode/v0_3/*.down.sql"), dialect)
				}

			}
		})
	}
}
