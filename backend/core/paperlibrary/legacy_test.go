package paperlibrary

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

const legacyGraphFixture = `{"schemaVersion":3,"folders":[{"id":"root","name":"Research","zoteroSources":["users:7:ROOT"]},{"id":"child","name":"Nested","parentId":"root"}],"collections":[{"id":"collection","name":"Reading","custom":true}],"papers":[{"id":"paper","title":"Saved title","authors":"Jane Doe, John Smith","venue":"Journal","year":2024,"folderId":"child","status":"reading","important":true,"source":"zotero","tags":["saved"],"metadata":{"identifiers":{"doi":"10.1234/preserved","pmid":"77"},"confidence":{"title":"user"}},"attachmentIds":["attachment"],"noteIds":["note"],"collectionIds":["collection"],"annotationIds":["annotation"],"relatedPaperIds":[],"zoteroSources":["users:7:PAPER"],"customPaper":{"keep":true}}],"attachments":[{"id":"attachment","paperId":"paper","name":"Saved.pdf","kind":"pdf","contentRef":"document","source":"zotero","contentHash":"original-hash","mimeType":"application/pdf","size":99,"addedAt":"2026-10-20T10:00:00Z"}],"notes":[{"id":"note","paperId":"paper","title":"Saved note","content":"Keep every word","kind":"note","createdAt":"2026-10-20T10:00:00Z","updatedAt":"2026-10-20T10:00:00Z","customNote":true}],"annotations":[{"id":"annotation","paperId":"paper","comment":"Highlight","page":4}],"relations":[],"mergeSnapshots":[{"id":"snapshot","before":{"untouched":true}}],"migrationSnapshots":[{"id":"migration","data":{"original":true}}],"hiddenArxivWorkIds":["removed"],"unknownFuture":{"opaque":[1,2,3]}}`

func legacyModels() []any {
	return []any{&orm.Dataset{}, &orm.Document{}, &orm.AcademicWork{}, &orm.AcademicWorkDocument{}, &orm.AcademicDocumentState{}, &orm.AcademicUserPaperState{}, &orm.PaperLibrary{}, &orm.PaperLibraryLegacyState{}, &orm.PaperLibraryEntry{}, &orm.PaperLibraryFolder{}, &orm.PaperLibraryAttachment{}, &orm.PaperLibraryNote{}, &orm.PaperImportBatch{}, &orm.PaperImportItem{}}
}
func legacyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return orm.MigrateTestDB(t, legacyModels()...).DB
}
func seedLegacy(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	for _, row := range []any{
		&orm.Dataset{ID: "library", Scene: "general", ProcessingLevel: "stored", Ext: json.RawMessage(`{"library_kind":"paper","other":"keep"}`), BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.Document{ID: "document", DatasetID: "library", DisplayName: "Saved.pdf"},
		&orm.PaperLibrary{DatasetID: "library", DataJSON: json.RawMessage(legacyGraphFixture), Revision: 17, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}
func TestLegacyConversionPreservesGraphAndNeverReplays(t *testing.T) {
	db := legacyTestDB(t)
	seedLegacy(t, db)
	view, err := Load(t.Context(), db, "library", "owner", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Data.Papers) != 1 || view.Data.Papers[0].Metadata.Title != "Saved title" || view.Data.Papers[0].Status != "reading" || !view.Data.Papers[0].Important || len(view.Data.Papers[0].Metadata.Authors) != 2 {
		t.Fatalf("lost paper: %+v", view.Data.Papers)
	}
	if len(view.Data.Folders) != 2 || len(view.Data.Notes) != 1 || len(view.Data.Attachments) != 1 || view.Data.Attachments[0].ContentRef != "document" || view.Data.Attachments[0].ContentHash != "original-hash" {
		t.Fatalf("lost children: %+v", view.Data)
	}
	raw, err := json.Marshal(view.Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"collections":[`, `"annotations":[`, `"mergeSnapshots":[`, `"migrationSnapshots":[`, `"unknownFuture":`, `"pmid":"77"`, `"zoteroSources":["users:7:PAPER"]`, `"customNote":true`, `"mimeType":"application/pdf"`} {
		if !strings.Contains(string(raw), fragment) {
			t.Fatalf("missing %s: %s", fragment, raw)
		}
	}
	var archived orm.PaperLibrary
	if err = db.Take(&archived, "dataset_id=?", "library").Error; err != nil || string(archived.DataJSON) != legacyGraphFixture || archived.Revision != 17 {
		t.Fatal("archive changed", err)
	}
	var ds orm.Dataset
	if err = db.Take(&ds, "id=?", "library").Error; err != nil || ds.Scene != "academic_papers" || !strings.Contains(string(ds.Ext), `"other":"keep"`) {
		t.Fatal("scene not preserved", ds, err)
	}
	if err = db.Model(&orm.PaperLibraryEntry{}).Where("dataset_id=?", "library").Updates(map[string]any{"status": "read", "metadata_json": json.RawMessage(`{"title":"Corrected","authors":["Changed"]}`)}).Error; err != nil {
		t.Fatal(err)
	}
	reopened := db.Session(&gorm.Session{NewDB: true})
	next, err := Load(t.Context(), reopened, "library", "owner", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if next.Data.Papers[0].Status != "read" || next.Data.Papers[0].Metadata.Title != "Corrected" {
		t.Fatal("archive overwrote normalized changes", next)
	}
	now := time.Now().UTC()
	if err = db.Model(&orm.PaperLibraryEntry{}).Where("dataset_id=?", "library").Update("removed_at", now).Error; err != nil {
		t.Fatal(err)
	}
	removed, err := Load(t.Context(), reopened, "library", "owner", nil, true)
	if err != nil || len(removed.Data.Papers) != 0 {
		t.Fatal("removed membership resurrected", removed, err)
	}
	counts, err := Statistics(t.Context(), reopened, []string{"library"})
	if err != nil || counts["library"].PaperCount != 0 {
		t.Fatal(counts, err)
	}
}
func TestLegacyConversionRollsBackOnFailure(t *testing.T) {
	db := legacyTestDB(t)
	seedLegacy(t, db)
	db.Callback().Create().Before("gorm:create").Register("fail-legacy-note", func(tx *gorm.DB) {
		if tx.Statement.Table == "paper_library_notes" {
			tx.AddError(errors.New("interrupted conversion"))
		}
	})
	if err := EnsureLegacy(t.Context(), db, "library"); err == nil {
		t.Fatal("injected failure was hidden")
	}
	var count int64
	for _, model := range []any{&orm.PaperLibraryEntry{}, &orm.PaperLibraryFolder{}, &orm.PaperLibraryLegacyState{}} {
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("partial conversion %T: %d %v", model, count, err)
		}
	}
	db.Callback().Create().Remove("fail-legacy-note")
	if err := EnsureLegacy(t.Context(), db, "library"); err != nil {
		t.Fatal(err)
	}
}
func TestSnapshotImportIsAtomicAndPreservesEdits(t *testing.T) {
	db := legacyTestDB(t)
	seedLegacy(t, db)
	if err := EnsureLegacy(t.Context(), db, "library"); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.PaperLibraryEntry{}).Where("dataset_id=? AND id=?", "library", "paper").Update("status", "read").Error; err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"folders":[],"papers":[{"id":"from-zotero","metadata":{"title":"Provider title","authors":[],"identifiers":{"doi":"10.1234/preserved"}},"status":"unread","source":"zotero","zoteroSources":["users:8:OTHER"]}],"attachments":[],"notes":[{"id":"new-note","paperId":"from-zotero","title":"Imported note","content":"Added once","kind":"note"}],"annotations":[{"id":"new-annotation","paperId":"from-zotero","comment":"New highlight"}]}`)
	for i := 0; i < 2; i++ {
		if err := ImportSnapshot(t.Context(), db, "library", "owner", raw); err != nil {
			t.Fatal(err)
		}
	}
	view, err := Load(t.Context(), db, "library", "owner", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Data.Papers) != 1 || view.Data.Papers[0].ID != "paper" || view.Data.Papers[0].Status != "read" || view.Data.Papers[0].Metadata.Title != "Saved title" || len(view.Data.Notes) != 2 {
		t.Fatal("import overwrote edits or duplicated entities", view)
	}
	encoded, _ := json.Marshal(view.Data)
	if !strings.Contains(string(encoded), `"users:8:OTHER"`) || !strings.Contains(string(encoded), `"paperId":"paper"`) {
		t.Fatal("source identity or aliases lost", string(encoded))
	}
	bad := json.RawMessage(`{"papers":[{"id":"new-paper","metadata":{"title":"Must roll back","authors":[],"identifiers":{}}}],"notes":[{"id":"invalid-note","paperId":"missing"}]}`)
	if err = ImportSnapshot(t.Context(), db, "library", "owner", bad); err == nil {
		t.Fatal("bad references accepted")
	}
	var count int64
	if err = db.Model(&orm.PaperLibraryEntry{}).Where("id=?", "new-paper").Count(&count).Error; err != nil || count != 0 {
		t.Fatal("partial import survived", count, err)
	}
}
func TestLegacyConversionSurvivesSQLiteReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.sqlite")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(legacyModels()...); err != nil {
		t.Fatal(err)
	}
	seedLegacy(t, db)
	if err = EnsureLegacy(t.Context(), db, "library"); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	if err = sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := reopened.DB()
	defer conn.Close()
	view, err := Load(t.Context(), reopened, "library", "owner", nil, true)
	if err != nil || len(view.Data.Papers) != 1 || string(view.Data.Legacy) != legacyGraphFixture {
		t.Fatal("restart lost library", view, err)
	}
}

func TestLegacyDuplicateIdentitiesKeepBothConfirmedRecords(t *testing.T) {
	db := legacyTestDB(t)
	seedLegacy(t, db)
	var graph map[string]any
	if err := json.Unmarshal([]byte(legacyGraphFixture), &graph); err != nil {
		t.Fatal(err)
	}
	papers := graph["papers"].([]any)
	papers = append(papers, map[string]any{"id": "second", "title": "Different user correction", "authors": "Another author", "status": "unread", "source": "local", "metadata": map[string]any{"identifiers": map[string]any{"doi": "10.1234/preserved"}}})
	graph["papers"] = papers
	raw, _ := json.Marshal(graph)
	if err := db.Model(&orm.PaperLibrary{}).Where("dataset_id=?", "library").Update("data_json", raw).Error; err != nil {
		t.Fatal(err)
	}
	view, err := Load(t.Context(), db, "library", "owner", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Data.Papers) != 2 {
		t.Fatal("duplicate library entries were discarded", view)
	}
	for _, paper := range view.Data.Papers {
		if paper.Metadata.Identifiers.DOI != "10.1234/preserved" {
			t.Fatal("confirmed identifier discarded", paper)
		}
		if paper.ID == "second" && (paper.Metadata.Title != "Different user correction" || paper.AssociationConflict == "") {
			t.Fatal("duplicate identity was not surfaced", paper)
		}
	}
}
func TestSnapshotRejectsUnavailableClientDocumentReference(t *testing.T) {
	db := legacyTestDB(t)
	seedLegacy(t, db)
	raw := json.RawMessage(`{"papers":[{"id":"new","metadata":{"title":"New","authors":[],"identifiers":{}},"source":"zotero"}],"attachments":[{"id":"invented","paperId":"new","kind":"pdf","contentRef":"missing","name":"Missing.pdf"}]}`)
	if err := ImportSnapshot(t.Context(), db, "library", "owner", raw); err == nil {
		t.Fatal("invented document accepted")
	}
	var count int64
	if err := db.Model(&orm.PaperLibraryEntry{}).Where("id=?", "new").Count(&count).Error; err != nil || count != 0 {
		t.Fatal("rejected import partially committed", count, err)
	}
}

func TestSnapshotRestoreKeepsForeignArchiveAndDeduplicatesIt(t *testing.T) {
	db := legacyTestDB(t)
	seedLegacy(t, db)
	foreign := json.RawMessage(`{"schemaVersion":3,"papers":[],"unknownHistorical":{"must":"survive","externalId":9007199254740993123}}`)
	payload := rawJSON(map[string]any{"papers": []any{}, "legacy": foreign})
	for i := 0; i < 2; i++ {
		if err := ImportSnapshot(t.Context(), db, "library", "owner", payload); err != nil {
			t.Fatal(err)
		}
	}
	view, err := Load(t.Context(), db, "library", "owner", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var graph map[string]json.RawMessage
	encoded, _ := json.Marshal(view.Data)
	if err = json.Unmarshal(encoded, &graph); err != nil {
		t.Fatal(err)
	}
	var archives []map[string]json.RawMessage
	if err = json.Unmarshal(graph["restoredArchives"], &archives); err != nil || len(archives) != 1 {
		t.Fatal("archive missing or repeated", archives, err)
	}
	var expected, actual any
	if err = decodeAuxiliaryJSON(foreign, &expected); err != nil {
		t.Fatal(err)
	}
	if err = decodeAuxiliaryJSON(archives[0]["data"], &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expected, actual) {
		t.Fatal("restored archive changed", string(archives[0]["data"]))
	}
	if string(view.Data.Legacy) != legacyGraphFixture {
		t.Fatal("destination archive overwritten")
	}
	if err = ImportSnapshot(t.Context(), db, "library", "owner", encoded); err != nil {
		t.Fatal(err)
	}
	view, err = Load(t.Context(), db, "library", "owner", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(view.Data)
	_ = json.Unmarshal(encoded, &graph)
	_ = json.Unmarshal(graph["restoredArchives"], &archives)
	if len(archives) != 1 {
		t.Fatal("own archive was redundantly restored", len(archives))
	}
}

func TestLegacyRemovedPapersRetainHiddenMaterialsWithoutResurrection(t *testing.T) {
	db := legacyTestDB(t)
	seedLegacy(t, db)
	var graph map[string]json.RawMessage
	if err := json.Unmarshal([]byte(legacyGraphFixture), &graph); err != nil {
		t.Fatal(err)
	}
	graph["hiddenArxivPapers"] = graph["papers"]
	graph["papers"] = json.RawMessage(`[]`)
	graph["notes"] = json.RawMessage(`[{"id":"note","paperId":"paper","title":"Hidden","content":"Keep hidden note","kind":"note"},{"id":"orphan","paperId":"removed-manual-paper","title":"Orphan","content":"Retain removed material","kind":"note"}]`)
	raw := rawJSON(graph)
	if err := db.Model(&orm.PaperLibrary{}).Where("dataset_id=?", "library").Update("data_json", raw).Error; err != nil {
		t.Fatal(err)
	}
	view, err := Load(t.Context(), db, "library", "owner", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Data.Papers) != 0 || len(view.Data.Attachments) != 0 || len(view.Data.Notes) != 0 {
		t.Fatal("removed paper or children reappeared", view)
	}
	var entries, notes, attachments int64
	db.Model(&orm.PaperLibraryEntry{}).Where("dataset_id=? AND removed_at IS NOT NULL", "library").Count(&entries)
	db.Model(&orm.PaperLibraryNote{}).Where("dataset_id=?", "library").Count(&notes)
	db.Model(&orm.PaperLibraryAttachment{}).Where("dataset_id=? AND detached_at IS NOT NULL", "library").Count(&attachments)
	if entries != 2 || notes != 2 || attachments != 1 {
		t.Fatal("removed material was discarded", entries, notes, attachments)
	}
	if string(view.Data.Legacy) != string(raw) {
		t.Fatal("original hidden graph changed")
	}
}

func TestLegacyConversionKeepsExistingWorkWithoutPublicIdentifiers(t *testing.T) {
	db := legacyTestDB(t)
	seedLegacy(t, db)
	work := orm.AcademicWork{ID: "existing-work", CanonicalTitle: "Original scholarly identity", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)}
	if err := db.Create(&work).Error; err != nil {
		t.Fatal(err)
	}
	graph := json.RawMessage(`{"papers":[{"id":"paper","academicWorkId":"existing-work","title":"User correction","authors":"A、B","source":"local","status":"read","metadata":{"identifiers":{}}}],"notes":[],"attachments":[],"folders":[]}`)
	if err := db.Model(&orm.PaperLibrary{}).Where("dataset_id=?", "library").Update("data_json", graph).Error; err != nil {
		t.Fatal(err)
	}
	view, err := Load(t.Context(), db, "library", "owner", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Data.Papers) != 1 || view.Data.Papers[0].AcademicWorkID != work.ID || len(view.Data.Papers[0].Metadata.Authors) != 2 || view.Data.Papers[0].Metadata.Title != "User correction" {
		t.Fatal("existing identity or corrected metadata lost", view)
	}
}

func TestSnapshotIdentityRemapDoesNotRewriteHistoricalGraphs(t *testing.T) {
	db := legacyTestDB(t)
	seedLegacy(t, db)
	raw := json.RawMessage(`{"papers":[{"id":"source-id","metadata":{"title":"Provider","authors":[],"identifiers":{"doi":"10.1234/preserved"}}}],"annotations":[{"id":"new-annotation","paperId":"source-id"}],"migrationSnapshots":[{"id":"restore-original","data":{"papers":[{"id":"source-id"}],"notes":[{"id":"historical-note","paperId":"source-id"}]}}]}`)
	if err := ImportSnapshot(t.Context(), db, "library", "owner", raw); err != nil {
		t.Fatal(err)
	}
	view, err := Load(t.Context(), db, "library", "owner", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var graph map[string]json.RawMessage
	encoded, _ := json.Marshal(view.Data)
	_ = json.Unmarshal(encoded, &graph)
	var history []map[string]json.RawMessage
	_ = json.Unmarshal(graph["migrationSnapshots"], &history)
	found := false
	for _, snapshot := range history {
		if textField(snapshot, "id") == "restore-original" {
			found = true
			if !strings.Contains(string(snapshot["data"]), `"paperId":"source-id"`) || !strings.Contains(string(snapshot["data"]), `"id":"source-id"`) {
				t.Fatal("historical graph rewritten", string(snapshot["data"]))
			}
		}
	}
	if !found {
		t.Fatal("restore snapshot missing")
	}
	if !strings.Contains(string(graph["annotations"]), `"paperId":"paper"`) {
		t.Fatal("active references were not remapped", string(graph["annotations"]))
	}
}

func TestVersionedSnapshotRetainsOrphanedRemovedMaterials(t *testing.T) {
	for _, version := range []int{3, 4} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			db := legacyTestDB(t)
			if err := db.Create(&orm.Dataset{ID: "library", Scene: "academic_papers", Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
				t.Fatal(err)
			}
			raw := rawJSON(map[string]any{"schemaVersion": version, "folders": []any{}, "papers": []any{map[string]any{"id": "visible", "metadata": map[string]any{"title": "Visible", "authors": []string{}, "identifiers": map[string]any{}}, "source": "local"}}, "notes": []any{map[string]any{"id": "removed-note", "paperId": "removed-manual", "title": "Keep", "content": "Historical note"}}, "attachments": []any{map[string]any{"id": "removed-attachment", "paperId": "removed-manual", "name": "Old.pdf", "kind": "pdf", "contentRef": "missing-old-document", "source": "local"}}})
			for attempt := 0; attempt < 2; attempt++ {
				if err := ImportSnapshot(t.Context(), db, "library", "owner", raw); err != nil {
					t.Fatal(err)
				}
			}
			view, err := Load(t.Context(), db, "library", "owner", nil, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(view.Data.Papers) != 1 || view.Data.Papers[0].ID != "visible" || len(view.Data.Attachments) != 0 || len(view.Data.Notes) != 0 {
				t.Fatal("historical paper resurrected", view)
			}
			for _, tc := range []struct {
				model any
				want  int64
			}{{&orm.PaperLibraryEntry{}, 2}, {&orm.PaperLibraryNote{}, 1}, {&orm.PaperLibraryAttachment{}, 1}, {&orm.AcademicWorkDocument{}, 0}} {
				var count int64
				if err = db.Model(tc.model).Count(&count).Error; err != nil || count != tc.want {
					t.Fatalf("lost historical material or fabricated link %T: %d %v", tc.model, count, err)
				}
			}
			encoded, _ := json.Marshal(view.Data)
			if !strings.Contains(string(encoded), `"restoredArchives"`) || !strings.Contains(string(encoded), `"Historical note"`) {
				t.Fatal("backup no longer contains removed materials", string(encoded))
			}
		})
	}
}

func TestVersionedSnapshotKeepsUnavailableOriginalWithoutWorkLink(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprint(foreign), func(t *testing.T) {
			db := legacyTestDB(t)
			if err := db.Create(&orm.Dataset{ID: "library", Scene: "academic_papers", Ext: json.RawMessage(`{}`)}).Error; err != nil {
				t.Fatal(err)
			}
			if foreign {
				if err := db.Create(&orm.Document{ID: "historical-original", DatasetID: "another-library", Ext: json.RawMessage(`{}`)}).Error; err != nil {
					t.Fatal(err)
				}
			}
			raw := json.RawMessage(`{"schemaVersion":4,"folders":[],"papers":[{"id":"paper","metadata":{"title":"Retained bibliography","authors":[],"identifiers":{}}}],"notes":[],"attachments":[{"id":"attachment","paperId":"paper","contentRef":"historical-original","name":"Unavailable.pdf","kind":"pdf"}]}`)
			err := ImportSnapshot(t.Context(), db, "library", "owner", raw)
			if foreign {
				if err == nil {
					t.Fatal("foreign original accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			view, err := Load(t.Context(), db, "library", "owner", nil, true)
			if err != nil || len(view.Data.Attachments) != 1 || view.Data.Attachments[0].Available || view.Data.Attachments[0].ContentRef != "historical-original" {
				t.Fatal("historical original not retained as unavailable", view, err)
			}
			var count int64
			if err = db.Model(&orm.AcademicWorkDocument{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("fabricated scholarly document link", count, err)
			}
		})
	}
}
