package migrate

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
)

// This fixture is inserted before the pre-drop backup migration, exercising
// the actual upgrade path rather than only checking an empty final schema.
func TestPopulatedLegacyLibraryUpgrade(t *testing.T) {
	catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			sqlDB := paperLibraryMigrationDB(t, driver, "populated_legacy")
			var dialect gorm.Dialector = sqlite.Dialector{Conn: sqlDB}
			if driver == "postgres" {
				dialect = postgres.New(postgres.Config{Conn: sqlDB})
			}
			db, err := gorm.Open(dialect, &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			const payload = `{"schemaVersion":3,"folders":[{"id":"parent","name":"Parent"},{"id":"child","name":"Child","parentId":"parent"}],"papers":[{"id":"paper","title":"Legacy title","authors":"A, B","status":"read","source":"zotero","important":true,"folderId":"child","metadata":{"identifiers":{"doi":"10.1234/legacy"}},"zoteroSources":["users:1:ITEM"]}],"attachments":[{"id":"attachment","paperId":"paper","name":"Legacy.pdf","kind":"pdf","contentRef":"legacy-document","source":"zotero"}],"notes":[{"id":"note","paperId":"paper","title":"Note","content":"Preserved","kind":"note"}],"annotations":[{"id":"annotation","paperId":"paper","comment":"Preserved highlight"}],"collections":[{"id":"collection","name":"Collection"}],"relations":[],"mergeSnapshots":[],"migrationSnapshots":[],"future":{"keep":true}}`
			seeded := false
			for i, mode := range catalog.Modes {
				if i < len(catalog.Modes)-1 {
					execMigrationFileForDriver(t, sqlDB, mode.Aggregate.UpPath, driver)
					continue
				}
				for _, migration := range mode.Dev {
					if migration.FileVersion == 20261026123246 {
						now := time.Now().UTC()
						for _, row := range []any{
							&orm.Dataset{ID: "legacy-dataset", Scene: "general", Ext: json.RawMessage(`{"library_kind":"paper","keep":true}`), BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}},
							&orm.Document{ID: "legacy-document", DatasetID: "legacy-dataset", DisplayName: "Legacy.pdf", Ext: json.RawMessage(`{}`), Tags: json.RawMessage(`[]`), BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}},
							&orm.PaperLibrary{DatasetID: "legacy-dataset", DataJSON: json.RawMessage(payload), Revision: 23, CreatedAt: now, UpdatedAt: now},
						} {
							if err = db.Create(row).Error; err != nil {
								t.Fatal(err)
							}
						}
						seeded = true
					}
					execMigrationFileForDriver(t, sqlDB, migration.UpPath, driver)
					if migration.FileVersion == 20261026123246 {
						var backedUp string
						if err := sqlDB.QueryRow(`SELECT data_json FROM paper_library_migration_backups WHERE dataset_id='legacy-dataset'`).Scan(&backedUp); err != nil || backedUp != payload {
							t.Fatal("pre-drop migration did not back up the graph", err)
						}
					}
					if migration.FileVersion == 20261026123247 {
						if _, err := sqlDB.Exec(`SELECT dataset_id FROM paper_libraries`); err == nil {
							t.Fatal("immutable entity migration no longer drops its old table")
						}
					}
				}
			}
			if !seeded {
				t.Fatal("fixture was not inserted before entity migration")
			}
			var archive orm.PaperLibrary
			if err = db.Take(&archive, "dataset_id=?", "legacy-dataset").Error; err != nil || string(archive.DataJSON) != payload || archive.Revision != 23 {
				t.Fatal("SQL upgrade discarded graph", err)
			}
			var ds orm.Dataset
			if err = db.Take(&ds, "id=?", "legacy-dataset").Error; err != nil || ds.Scene != "academic_papers" {
				t.Fatal("paper scene was not converted", ds.Scene, err)
			}
			view, err := paperlibrary.Load(t.Context(), db, "legacy-dataset", "owner", nil, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(view.Data.Papers) != 1 || view.Data.Papers[0].Status != "read" || !view.Data.Papers[0].Important || len(view.Data.Folders) != 2 || len(view.Data.Attachments) != 1 || len(view.Data.Notes) != 1 {
				t.Fatalf("normalized conversion lost content: %+v", view)
			}
			raw, err := json.Marshal(view.Data)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{`"future":{"keep":true}`, `"annotations":[`, `"collections":[`, `"zoteroSources":["users:1:ITEM"]`} {
				if !strings.Contains(string(raw), key) {
					t.Fatalf("lost auxiliary field %s: %s", key, raw)
				}
			}
			if err = db.Model(&orm.PaperLibraryEntry{}).Where("dataset_id=?", "legacy-dataset").Update("status", "reading").Error; err != nil {
				t.Fatal(err)
			}
			view, err = paperlibrary.Load(t.Context(), db.Session(&gorm.Session{NewDB: true}), "legacy-dataset", "owner", nil, true)
			if err != nil || view.Data.Papers[0].Status != "reading" {
				t.Fatal("conversion replayed archive", err)
			}
		})
	}
}

// Exercise the real migration runner for all histories this patch can meet:
// a populated pre-entity library, main's already-dropped table, and a local
// database that ran the earlier branch's preservation SQL before this fix.
func TestPaperLibraryArchiveRunnerHistories(t *testing.T) {
	const payload = `{"schemaVersion":3,"papers":[{"id":"legacy","title":"Keep me"}]}`
	const backupVersion = uint64(20261026123246)
	const entityVersion = uint64(20261026123247)
	const preserveVersion = uint64(20261027092144)
	const finalizeVersion = uint64(20261028022508)
	full, err := (&Runner{dir: "../migrations"}).loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	mode := full.Modes[len(full.Modes)-1]
	fileByVersion := map[uint64]migrationFile{}
	for _, migration := range mode.Dev {
		fileByVersion[migration.FileVersion] = migration
	}
	for _, driver := range []string{"sqlite", "postgres"} {
		for _, history := range []string{"before_drop", "main_already_dropped", "branch_already_preserved"} {
			t.Run(driver+"/"+history, func(t *testing.T) {
				sqlDB := paperLibraryMigrationDB(t, driver, "archive_runner_"+history)
				for _, earlier := range full.Modes[:len(full.Modes)-1] {
					execMigrationFileForDriver(t, sqlDB, earlier.Aggregate.UpPath, driver)
				}
				for _, migration := range mode.Dev {
					if migration.FileVersion >= backupVersion {
						break
					}
					execMigrationFileForDriver(t, sqlDB, migration.UpPath, driver)
				}
				if history != "main_already_dropped" {
					seedPaperLibraryArchive(t, sqlDB, driver, payload)
				}
				if history != "before_drop" {
					execMigrationFileForDriver(t, sqlDB, fileByVersion[entityVersion].UpPath, driver)
				}
				if history == "branch_already_preserved" {
					// The old branch ran the entity migration without DROP TABLE.
					execMigrationFileForDriver(t, sqlDB, paperLibraryMigration+".up.sql", driver)
					seedPaperLibraryArchive(t, sqlDB, driver, payload)
					// Its already-applied preservation step made this marker table.
					if _, err := sqlDB.Exec(`CREATE TABLE paper_library_legacy_states (
						dataset_id VARCHAR(255) PRIMARY KEY, extras_json JSON NOT NULL,
						migrated_at TIMESTAMP, updated_at TIMESTAMP NOT NULL)`); err != nil {
						t.Fatal(err)
					}
				}
				dir := filepath.Join(t.TempDir(), "dev_mode", "v0_3")
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				for _, version := range []uint64{backupVersion, entityVersion, preserveVersion, finalizeVersion} {
					for _, path := range []string{fileByVersion[version].UpPath, fileByVersion[version].DownPath} {
						body, err := os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(dir, filepath.Base(path)), body, 0644); err != nil {
							t.Fatal(err)
						}
					}
				}
				runner := &Runner{driver: driver, dir: filepath.Dir(filepath.Dir(dir)), db: sqlDB}
				if history != "before_drop" {
					if err := runner.prepare(); err != nil {
						t.Fatal(err)
					}
					for _, version := range []uint64{entityVersion, preserveVersion} {
						if version == preserveVersion && history != "branch_already_preserved" {
							continue
						}
						migration := fileByVersion[version]
						if err := runner.insertHistory(sqlDB, migration.Version, migration.Name); err != nil {
							t.Fatal(err)
						}
					}
					maxVersion := fileByVersion[entityVersion].Version
					if history == "branch_already_preserved" {
						maxVersion = fileByVersion[preserveVersion].Version
					}
					if err := runner.writeState(sqlDB, &maxVersion, false); err != nil {
						t.Fatal(err)
					}
				}
				if err := runner.Up(0); err != nil {
					t.Fatal("runner upgrade:", err)
				}
				if history != "main_already_dropped" {
					assertPaperLibraryArchive(t, sqlDB, payload)
				}
				if _, err := sqlDB.Exec(`SELECT dataset_id FROM paper_library_migration_backups`); err == nil {
					t.Fatal("temporary backup survived final migration")
				}
				if history != "before_drop" {
					return
				}
				for _, version := range []uint64{finalizeVersion, preserveVersion, entityVersion, backupVersion} {
					if err := runner.Down(1); err != nil {
						t.Fatalf("runner down %d: %v", version, err)
					}
					if version == entityVersion {
						// A single-step rollback must expose the original graph before
						// the earlier backup migration is rolled back.
						assertPaperLibraryArchive(t, sqlDB, payload)
						var count int
						if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM paper_library_migration_backups`).Scan(&count); err != nil || count != 1 {
							t.Fatal("single-step rollback lost its backup", err)
						}
					}
				}
				assertPaperLibraryArchive(t, sqlDB, payload)
				if _, err := sqlDB.Exec(`SELECT dataset_id FROM paper_library_migration_backups`); err == nil {
					t.Fatal("complete rollback left a temporary backup")
				}
				if err := runner.Up(0); err != nil {
					t.Fatal("runner re-upgrade:", err)
				}
				assertPaperLibraryArchive(t, sqlDB, payload)
			})
		}
	}
}

func seedPaperLibraryArchive(t *testing.T, db *sql.DB, driver, payload string) {
	t.Helper()
	placeholder := "?"
	if driver == "postgres" {
		placeholder = "$1"
	}
	if _, err := db.Exec(`INSERT INTO paper_libraries (dataset_id,data_json,revision,created_at,updated_at)
		VALUES ('archive-runner',`+placeholder+`,23,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, payload); err != nil {
		t.Fatal(err)
	}
}

func assertPaperLibraryArchive(t *testing.T, db *sql.DB, payload string) {
	t.Helper()
	var stored string
	var revision int64
	if err := db.QueryRow(`SELECT data_json,revision FROM paper_libraries WHERE dataset_id='archive-runner'`).Scan(&stored, &revision); err != nil || stored != payload || revision != 23 {
		t.Fatalf("archive graph lost: payload=%q revision=%d error=%v", stored, revision, err)
	}
}
