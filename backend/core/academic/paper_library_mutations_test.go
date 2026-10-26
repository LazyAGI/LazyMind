package academic

import (
	"encoding/json"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
)

func uploadReceipt(t *testing.T, db *gorm.DB, dataset, id, key, paperID, hash string) {
	t.Helper()
	for _, row := range []any{&orm.Document{ID: id, DatasetID: dataset, DisplayName: id + ".pdf", DocumentType: "PDF"}, &orm.PaperImportBatch{ID: key, EntryType: "pdf_upload", TargetDatasetID: dataset, CreatedBy: "owner", IdempotencyKey: key, Status: "awaiting_association", SourceContextJSON: marshal(map[string]any{}), SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]any{})}, &orm.PaperImportItem{ID: key, BatchID: key, DocumentID: id, LibraryPaperID: paperID, Stage: "awaiting_association", Status: "succeeded", ContentSHA256: hash, ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]any{}), SelectedCandidateJSON: marshal(map[string]any{}), ErrorDetailsJSON: marshal(map[string]any{})}} {
		if e := db.Create(row).Error; e != nil {
			t.Fatal(e)
		}
	}
}
func confirmUpload(t *testing.T, paper paperlibrary.Paper, id, key string, confirmed bool) *libraryChanges {
	t.Helper()
	w := libraryRequest(t, "POST", "/imports", "papers", "owner", createImportRequest{EntryType: "pdf_upload", TargetDatasetID: "papers", IdempotencyKey: key, Items: []selectedImportItem{{Paper: &paper, DocumentID: id, Revision: paper.Revision, Confirmed: confirmed, Candidate: FulltextCandidate{Version: paper.ArxivVersion}}}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out libraryChanges
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	return &out
}
func TestLibraryCorrectedIdentityOwnsNewPdfAndDetachIsDurable(t *testing.T) {
	db := paperLibraryTestDB(t)
	paper := importDraft(t, "papers", "metadata", libraryDraft("paper", "Confirmed title", "10.1234/a")).Data.Papers[0]
	m := paper.Metadata
	m.Identifiers.DOI = "10.1234/b"
	w := libraryRequest(t, "PATCH", "/library/papers/paper", "papers", "owner", map[string]any{"revision": paper.Revision, "metadata": m})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	paper = readLibrary(t, "papers").Data.Papers[0]
	uploadReceipt(t, db, "papers", "pdf", "upload", "paper", "hash")
	saved := confirmUpload(t, paper, "pdf", "upload", true)
	paper = saved.Data.Papers[0]
	confirmUpload(t, paper, "pdf", "upload", true)
	var link orm.AcademicWorkDocument
	if e := db.Where("document_id='pdf'").Take(&link).Error; e != nil {
		t.Fatal(e)
	}
	var work orm.AcademicWork
	db.Where("id=?", link.AcademicWorkID).Take(&work)
	if work.DOINormalized != "10.1234/b" || link.AcademicWorkID != paper.AcademicWorkID {
		t.Fatal("new PDF retained stale identity")
	}
	view := readLibrary(t, "papers")
	if view.PaperCount != 1 || view.PDFAttachmentCount != 1 {
		t.Fatal("replay duplicated membership")
	}
	attachment := view.Data.Attachments[0]
	w = libraryRequest(t, "POST", "/library/papers/paper/attachments/"+attachment.ID+":detach", "papers", "owner", map[string]any{"revision": paper.Revision})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, e := registerRecognizedLibraryDocument(t.Context(), db, orm.Document{ID: "pdf", DatasetID: "papers", DisplayName: "pdf.pdf"}, work, false, "", "", "upload"); e != nil {
		t.Fatal(e)
	}
	view = readLibrary(t, "papers")
	if view.PDFAttachmentCount != 0 || len(view.Data.Papers[0].AttachmentIDs) != 0 {
		t.Fatal("recognition restored detached attachment")
	}
	var count int64
	db.Model(&orm.Document{}).Where("id='pdf'").Count(&count)
	if count != 1 {
		t.Fatal("detachment deleted original")
	}
	db.Model(&orm.AcademicWorkDocument{}).Where("document_id='pdf'").Count(&count)
	if count != 1 {
		t.Fatal("detachment deleted historical identity fact")
	}
}
func TestLibraryPdfConfirmationIsAtomicAndRejectsConflictingIdentity(t *testing.T) {
	db := paperLibraryTestDB(t)
	paper := importDraft(t, "papers", "metadata", libraryDraft("paper", "A", "10.1234/a")).Data.Papers[0]
	uploadReceipt(t, db, "papers", "pdf", "upload", "paper", "hash")
	other, _, e := findOrCreateWork(t.Context(), db, WorkInput{Title: "B", DOI: "10.1234/b"})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Create(&orm.AcademicDocumentState{DocumentID: "pdf", DatasetID: "papers", Kind: "paper", Status: "succeeded", PaperID: other.ID, MetadataJSON: marshal(map[string]any{}), Revision: 1}).Error; e != nil {
		t.Fatal(e)
	}
	for _, confirmed := range []bool{false, true} {
		w := libraryRequest(t, "POST", "/imports", "papers", "owner", createImportRequest{EntryType: "pdf_upload", TargetDatasetID: "papers", IdempotencyKey: "upload", Items: []selectedImportItem{{Paper: &paper, DocumentID: "pdf", Revision: paper.Revision, Confirmed: confirmed, Candidate: FulltextCandidate{Version: paper.ArxivVersion}}}})
		if w.Code != 422 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var item orm.PaperImportItem
	db.Where("id='upload'").Take(&item)
	if item.Stage != "awaiting_association" || readLibrary(t, "papers").PDFAttachmentCount != 0 {
		t.Fatal("failed association partially committed")
	}
}
func TestLibrarySameHashAndVersionsPreservePrimaryAndMetadata(t *testing.T) {
	db := paperLibraryTestDB(t)
	paper := importDraft(t, "papers", "metadata", libraryDraft("paper", "Confirmed title", "10.1234/a")).Data.Papers[0]
	for i, id := range []string{"v3", "v1", "duplicate"} {
		hash := id
		if id == "duplicate" {
			hash = "v1"
		}
		uploadReceipt(t, db, "papers", id, "upload-"+id, "paper", hash)
		incoming := paper
		incoming.ArxivVersion = id
		if id == "duplicate" {
			incoming.ArxivVersion = "v1"
		}
		paper = confirmUpload(t, incoming, id, "upload-"+id, true).Data.Papers[0]
		if i == 0 {
			continue
		}
	}
	view := readLibrary(t, "papers")
	if view.PDFAttachmentCount != 2 || paper.Metadata.Title != "Confirmed title" {
		t.Fatal("duplicate or version changed bibliography")
	}
	primary := ""
	for _, a := range view.Data.Attachments {
		if a.IsPrimary {
			primary = a.ContentRef
		}
	}
	if primary != "v3" {
		t.Fatal("old version replaced primary")
	}
}
func TestLibraryImportanceIsScopedToUserAndDataset(t *testing.T) {
	db := paperLibraryTestDB(t)
	a := importDraft(t, "papers", "a", libraryDraft("a", "A", "10.1234/a")).Data.Papers[0]
	importDraft(t, "other", "b", libraryDraft("b", "A", "10.1234/a"))
	w := libraryRequest(t, "PUT", "/papers/"+a.AcademicWorkID+"/importance", "papers", "owner", map[string]any{"dataset_id": "papers", "is_important": true})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if !readLibrary(t, "papers").Data.Papers[0].Important || readLibrary(t, "other").Data.Papers[0].Important {
		t.Fatal("importance crossed library boundary")
	}
	var count int64
	db.Model(&orm.AcademicUserPaperState{}).Where("user_id='owner' AND dataset_id='papers'").Count(&count)
	if count != 1 {
		t.Fatal("missing scoped mark")
	}
}

func TestLibraryLocalCorrectionsDoNotEnrichSharedBibliography(t *testing.T) {
	db := paperLibraryTestDB(t)
	work, _, e := findOrCreateWork(t.Context(), db, WorkInput{DOI: "10.1234/shared", Title: "Public title", Abstract: "Public abstract"})
	if e != nil {
		t.Fatal(e)
	}
	draft := libraryDraft("paper", "Local title", "10.1234/shared")
	draft.Metadata.Abstract = ""
	paper := importDraft(t, "papers", "local", draft).Data.Papers[0]
	if paper.Metadata.Abstract != "" {
		t.Fatal("explicit empty local abstract fell back to public text")
	}
	var unchanged orm.AcademicWork
	if e = db.Where("id=?", work.ID).Take(&unchanged).Error; e != nil {
		t.Fatal(e)
	}
	if unchanged.CanonicalTitle != "Public title" || string(unchanged.AuthorsJSON) != string(work.AuthorsJSON) {
		t.Fatal("local correction changed shared bibliography", unchanged)
	}
	m := paper.Metadata
	m.Identifiers.Arxiv = "invalid-id"
	w := libraryRequest(t, "PATCH", "/library/papers/paper", "papers", "owner", map[string]any{"revision": paper.Revision, "metadata": m})
	if w.Code != 422 {
		t.Fatal("invalid identifier accepted", w.Code, w.Body.String())
	}
}

func libraryPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	raw := os.Getenv("MIGRATION_TEST_POSTGRES_DSN")
	if raw == "" {
		t.Skip("set MIGRATION_TEST_POSTGRES_DSN for PostgreSQL integrity checks")
	}
	admin, e := gorm.Open(postgres.Open(raw), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	adminPool, _ := admin.DB()
	t.Cleanup(func() { adminPool.Close() })
	schema := "paper_library_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if e = admin.Exec("CREATE SCHEMA " + schema).Error; e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { admin.Exec("DROP SCHEMA " + schema + " CASCADE") })
	target, e := url.Parse(raw)
	if e != nil {
		t.Fatal(e)
	}
	query := target.Query()
	query.Set("search_path", schema)
	target.RawQuery = query.Encode()
	db, e := gorm.Open(postgres.Open(target.String()), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	pool, _ := db.DB()
	t.Cleanup(func() { pool.Close() })
	return db
}

func TestLibraryPostgresConcurrentIdentityResolution(t *testing.T) {
	db := libraryPostgresDB(t)
	if e := db.AutoMigrate(&orm.AcademicWork{}); e != nil {
		t.Fatal(e)
	}
	errors := make(chan error, 8)
	ids := make(chan string, 8)
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			var id string
			e := db.WithContext(t.Context()).Transaction(func(tx *gorm.DB) error {
				work, e := resolveLibraryWork(t.Context(), tx, "papers", "entry", paperlibrary.Metadata{Title: "Concurrent paper", Authors: []string{"Author"}, Identifiers: paperlibrary.Identifiers{DOI: "10.1234/concurrent"}})
				id = work.ID
				return e
			})
			errors <- e
			ids <- id
		}()
	}
	workers.Wait()
	close(errors)
	close(ids)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	unique := map[string]bool{}
	for id := range ids {
		unique[id] = true
	}
	if len(unique) != 1 {
		t.Fatal("concurrent imports created distinct canonical identities", unique)
	}
	var count int64
	if e := db.Model(&orm.AcademicWork{}).Count(&count).Error; e != nil || count != 1 {
		t.Fatal("duplicate shared work", count, e)
	}
}

func TestLibrarySameHashCannotBeAssociatedWithAnotherPaper(t *testing.T) {
	db := paperLibraryTestDB(t)
	a := importDraft(t, "papers", "first", libraryDraft("a", "A", "10.1234/a")).Data.Papers[0]
	b := importDraft(t, "papers", "second", libraryDraft("b", "B", "10.1234/b")).Data.Papers[0]
	uploadReceipt(t, db, "papers", "pdf-a", "upload-a", "a", "same-hash")
	confirmUpload(t, a, "pdf-a", "upload-a", true)
	uploadReceipt(t, db, "papers", "pdf-b", "upload-b", "b", "same-hash")
	w := libraryRequest(t, "POST", "/imports", "papers", "owner", createImportRequest{EntryType: "pdf_upload", TargetDatasetID: "papers", IdempotencyKey: "upload-b", Items: []selectedImportItem{{Paper: &b, DocumentID: "pdf-b", Revision: b.Revision, Confirmed: true}}})
	if w.Code != 422 {
		t.Fatal("identical bytes attached to another paper", w.Code, w.Body.String())
	}
	if readLibrary(t, "papers").PDFAttachmentCount != 1 {
		t.Fatal("conflicting attachment partially committed")
	}
}
