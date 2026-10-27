package migrate

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
)

// This fixture is inserted before the normalized-entity migration, exercising
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
					if migration.FileVersion == 20261026123247 {
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
