package academic

import (
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
)

func TestZoteroSnapshotHTTPPermissionsAndReplayPreserveEdits(t *testing.T) {
	db := paperLibraryTestDB(t, orm.OpenTestDB(t).DB)
	if err := db.AutoMigrate(&orm.PaperLibrary{}, &orm.PaperLibraryLegacyState{}); err != nil {
		t.Fatal(err)
	}
	paper := libraryDraft("zotero-paper", "Source title", "")
	paper.Source = "zotero"
	data := paperlibrary.EmptyData()
	data.Papers = []paperlibrary.Paper{*paper}
	body := string(marshal(map[string]any{"data": data, "source_id": "local:personal", "idempotency_key": "import-1"}))
	invoke := func(user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/academic/zotero/import?target_dataset_id=papers", strings.NewReader(body))
		r.Header.Set("X-User-Id", user)
		w := httptest.NewRecorder()
		ImportZoteroSnapshot(w, r)
		return w
	}
	if w := invoke("stranger"); w.Code != 403 {
		t.Fatalf("unauthorized import: %d %s", w.Code, w.Body.String())
	}
	var count int64
	db.Model(&orm.PaperLibraryEntry{}).Count(&count)
	if count != 0 {
		t.Fatal("denied import wrote data")
	}
	if w := invoke("owner"); w.Code != 200 {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	var entry orm.PaperLibraryEntry
	if err := db.Where("dataset_id=? AND id=?", "papers", paper.ID).Take(&entry).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.AcademicUserPaperState{UserID: "owner", DatasetID: "papers", PaperID: entry.AcademicWorkID, ReadingStatus: "read"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&entry).Update("revision", 2).Error; err != nil {
		t.Fatal(err)
	}

	if w := invoke("owner"); w.Code != 200 {
		t.Fatalf("replay: %d %s", w.Code, w.Body.String())
	}
	view := readLibrary(t, "papers")
	if len(view.Data.Papers) != 1 || view.Data.Papers[0].Status != "read" || view.Data.Papers[0].Revision != 2 {
		t.Fatalf("replay lost edit or duplicated data: %+v", view.Data.Papers)
	}
}

func TestZoteroReceiptPersistsSelectedSourceKeysAndDeduplicatesReplay(t *testing.T) {
	db, _ := zoteroStorageFixture(t)
	if err := paperlibrary.MergeEntityExtras(t.Context(), db, "papers", "papers", "paper", map[string]any{"zoteroSources": []string{"users:42:KEY"}}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := recordZoteroImport(t.Context(), db, "papers", "owner", "users:42", "receipt-key", []string{"KEY"}); err != nil {
			t.Fatal(err)
		}
	}
	var batches []orm.PaperImportBatch
	db.Where("entry_type=?", "zotero").Find(&batches)
	if len(batches) != 1 || batches[0].TotalItems != 1 {
		t.Fatal(batches)
	}
	var items []orm.PaperImportItem
	db.Where("batch_id=?", batches[0].ID).Find(&items)
	if len(items) != 1 || items[0].LibraryPaperID != "paper" {
		t.Fatal(items)
	}
	if err := recordZoteroImport(t.Context(), db, "papers", "owner", "users:OTHER", "another-key", []string{"KEY"}); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&orm.PaperImportBatch{}).Count(&count)
	if count != 1 {
		t.Fatal("unrelated source matched", count)
	}
}

func TestBackupReceiptExcludesUnrelatedExistingPapers(t *testing.T) {
	db, _ := zoteroStorageFixture(t)
	incoming := paperlibrary.EmptyData()
	incoming.Papers = []paperlibrary.Paper{*libraryDraft("restored", "Restored title", "")}
	raw := marshal(incoming)
	if err := paperlibrary.ImportSnapshot(t.Context(), db, "papers", "owner", raw); err != nil {
		t.Fatal(err)
	}
	if err := recordZoteroImport(t.Context(), db, "papers", "owner", "library-backup", "restore-one", nil, raw); err != nil {
		t.Fatal(err)
	}
	var batch orm.PaperImportBatch
	if err := db.Where("entry_type=?", "library_backup").Take(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if batch.TotalItems != 1 {
		t.Fatal("count includes existing papers", batch.TotalItems)
	}
	var item orm.PaperImportItem
	db.Where("batch_id=?", batch.ID).Take(&item)
	if item.LibraryPaperID != "restored" {
		t.Fatal(item.LibraryPaperID)
	}
}

func TestBackupReceiptBatchesLargeImports(t *testing.T) {
	db, _ := zoteroStorageFixture(t)
	incoming := paperlibrary.EmptyData()
	for i := 0; i < 401; i++ {
		incoming.Papers = append(incoming.Papers, *libraryDraft(fmt.Sprintf("batch-%d", i), fmt.Sprintf("Title %d", i), ""))
	}
	raw := marshal(incoming)
	if err := paperlibrary.ImportSnapshot(t.Context(), db, "papers", "owner", raw); err != nil {
		t.Fatal(err)
	}
	queries := 0
	if err := db.Callback().Query().After("gorm:query").Register("test:receipt_queries", func(*gorm.DB) { queries++ }); err != nil {
		t.Fatal(err)
	}
	if err := recordZoteroImport(t.Context(), db, "papers", "owner", "library-backup", "batch-receipt", nil, raw); err != nil {
		t.Fatal(err)
	}
	if queries > 30 {
		t.Fatalf("receipt used %d queries for 401 papers", queries)
	}
	var items []orm.PaperImportItem
	if err := db.Order("title_snapshot").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	if len(items) != 401 {
		t.Fatalf("got %d receipt items", len(items))
	}
	for _, item := range items {
		if item.TitleSnapshot == "" || item.LibraryPaperID == "" {
			t.Fatal("incomplete receipt", item)
		}
	}
}

func TestSnapshotImportReceiptsCoverMigrationAndNormalizedBackupIdentities(t *testing.T) {
	for _, source := range []string{"browser-local-v3", "library-backup", "local:personal"} {
		t.Run(source, func(t *testing.T) {
			db, _ := zoteroStorageFixture(t)
			existing := paperlibrary.EmptyData()
			existing.Papers = []paperlibrary.Paper{*libraryDraft("existing", "Existing paper", "10.1234/normalized"), *libraryDraft("arxiv-existing", "arXiv paper", "")}
			existing.Papers[1].Metadata.Identifiers.Arxiv = "2404.06654"
			if err := paperlibrary.ImportSnapshot(t.Context(), db, "papers", "owner", marshal(existing)); err != nil {
				t.Fatal(err)
			}
			incoming := paperlibrary.EmptyData()
			incoming.Papers = []paperlibrary.Paper{*libraryDraft("alias", "Imported title", "https://doi.org/10.1234/NORMALIZED"), *libraryDraft("new", "New paper", ""), *libraryDraft("arxiv-alias", "arXiv paper", "")}
			incoming.Papers[2].Metadata.Identifiers.Arxiv = "https://arxiv.org/abs/2404.06654v2"
			body := string(marshal(map[string]any{"data": incoming, "source_id": source, "idempotency_key": "receipt"}))
			for range 2 {
				r := httptest.NewRequest(http.MethodPost, "/academic/zotero/import?target_dataset_id=papers", strings.NewReader(body))
				r.Header.Set("X-User-Id", "owner")
				w := httptest.NewRecorder()
				ImportZoteroSnapshot(w, r)
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			w := libraryRequest(t, "GET", "/imports?view=operations", "papers", "owner", nil)
			var result struct{ Items []ImportOperation }
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 {
				t.Fatal(w.Code, w.Body.String(), err)
			}
			if len(result.Items) != 1 || result.Items[0].Summary.Total != 3 {
				t.Fatalf("missing or unrelated receipts: %s", w.Body.String())
			}
			wantKind := map[string]string{"browser-local-v3": "library_migration", "library-backup": "library_backup", "local:personal": "zotero"}[source]
			if result.Items[0].EntryType != wantKind {
				t.Fatalf("source mislabeled: got %q, want %q", result.Items[0].EntryType, wantKind)
			}
			var items []orm.PaperImportItem
			db.Where("batch_id=?", result.Items[0].ID).Find(&items)
			ids := map[string]bool{}
			for _, item := range items {
				ids[item.LibraryPaperID] = true
			}
			if !ids["existing"] || !ids["new"] || !ids["arxiv-existing"] || ids["paper"] {
				t.Fatal(ids)
			}
		})
	}
}

func TestSnapshotReceiptReflectsAlreadyStoredPDF(t *testing.T) {
	db, path := zoteroStorageFixture(t)
	if _, err := storeZoteroFile(t.Context(), db, "papers", "paper", "zotero-attachment", path, "article.pdf", "owner", "Owner"); err != nil {
		t.Fatal(err)
	}
	incoming := paperlibrary.EmptyData()
	incoming.Papers = []paperlibrary.Paper{*libraryDraft("paper", "Restored paper", "")}
	if err := recordZoteroImport(t.Context(), db, "papers", "owner", "library-backup", "restore-pdf", nil, marshal(incoming)); err != nil {
		t.Fatal(err)
	}
	w := libraryRequest(t, "GET", "/imports?view=operations", "papers", "owner", nil)
	var result struct{ Items []ImportOperation }
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	if len(result.Items) != 1 || result.Items[0].Summary.Ready != 1 || result.Items[0].Summary.MetadataOnly != 0 {
		t.Fatalf("stored PDF reported as metadata only: %s", w.Body.String())
	}
}

func TestSnapshotReceiptPrefersExistingIDOverConflictingIdentifiers(t *testing.T) {
	for _, identifier := range []string{"doi", "arxiv"} {
		t.Run(identifier, func(t *testing.T) {
			db, _ := zoteroStorageFixture(t)
			existing := paperlibrary.EmptyData()
			existing.Papers = []paperlibrary.Paper{*libraryDraft("a", "Paper A", "10.1234/a"), *libraryDraft("b", "Paper B", "10.1234/b")}
			existing.Papers[0].Metadata.Identifiers.Arxiv = "2404.06654"
			existing.Papers[1].Metadata.Identifiers.Arxiv = "2404.06655"
			if err := paperlibrary.ImportSnapshot(t.Context(), db, "papers", "owner", marshal(existing)); err != nil {
				t.Fatal(err)
			}
			incoming := paperlibrary.EmptyData()
			incoming.Papers = []paperlibrary.Paper{*libraryDraft("a", "Changed title", "")}
			if identifier == "doi" {
				incoming.Papers[0].Metadata.Identifiers.DOI = "10.1234/b"
			} else {
				incoming.Papers[0].Metadata.Identifiers.Arxiv = "2404.06655"
			}
			body := string(marshal(map[string]any{"data": incoming, "source_id": "library-backup", "idempotency_key": "conflicting-id"}))
			r := httptest.NewRequest(http.MethodPost, "/academic/zotero/import?target_dataset_id=papers", strings.NewReader(body))
			r.Header.Set("X-User-Id", "owner")
			w := httptest.NewRecorder()
			ImportZoteroSnapshot(w, r)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var items []orm.PaperImportItem
			if err := db.Find(&items).Error; err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].LibraryPaperID != "a" {
				t.Fatalf("receipt includes papers not imported: %+v", items)
			}
		})
	}
}
