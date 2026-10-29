package academic

import (
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
	if err := db.Model(&orm.PaperLibraryEntry{}).Where("dataset_id=? AND id=?", "papers", paper.ID).Updates(map[string]any{"status": "read", "revision": 2}).Error; err != nil {
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
