package academic

import (
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"io"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func paperLibraryTestDB(t *testing.T, databases ...*gorm.DB) *gorm.DB {
	t.Helper()
	db := importErrorDB(t, databases...)
	if e := db.AutoMigrate(&orm.PaperLibraryEntry{}, &orm.PaperLibraryFolder{}, &orm.PaperLibraryNote{}, &orm.PaperLibraryAttachment{}, &orm.AcademicUserPaperState{}, &orm.Dataset{}, &orm.ACLModel{}, &orm.ACLGroupModel{}, &orm.UserGroupModel{}, &orm.KBModel{}, &orm.VisibilityModel{}); e != nil {
		t.Fatal(e)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	acl.InitStore(&orm.DB{DB: db})
	for _, id := range []string{"papers", "other", "documents"} {
		scene := "academic_papers"
		if id == "documents" {
			scene = "general"
		}
		if e := db.Create(&orm.Dataset{ID: id, Scene: scene, ProcessingLevel: "stored", Ext: marshal(map[string]any{}), BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; e != nil {
			t.Fatal(e)
		}
	}
	old := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"items":[],"source_map":{},"code":200,"data":[]}`)), Header: make(http.Header), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	return db
}
func libraryRequest(t *testing.T, method, path, dataset, user string, body any) *httptest.ResponseRecorder {
	t.Helper()
	router := mux.NewRouter()
	router.HandleFunc("/library", GetPaperLibrary).Methods("GET")
	router.HandleFunc("/library/papers/{library_paper_id}", PatchLibraryPaper).Methods("PATCH")
	router.HandleFunc("/library/papers:move", MoveLibraryPapers)
	router.HandleFunc("/library/papers:remove", RemoveLibraryPapers)
	router.HandleFunc("/library/papers/{library_paper_id}/attachments/{attachment_id}:detach", DetachLibraryAttachment)
	router.HandleFunc("/library/papers/{library_paper_id}/notes", AddLibraryNote)
	router.HandleFunc("/library/folders", CreateLibraryFolder).Methods("POST")
	router.HandleFunc("/library/folders/{folder_id}", RenameLibraryFolder).Methods("PATCH")
	router.HandleFunc("/library/folders/{folder_id}:delete", DeleteLibraryFolder)
	router.HandleFunc("/papers/{paper_id}/importance", SetImportance)
	router.HandleFunc("/imports", CreateImport).Methods("POST")
	router.HandleFunc("/imports", ListImports).Methods("GET")
	router.HandleFunc("/imports/{batch_id}", GetImport).Methods("GET")
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	r := httptest.NewRequest(method, path+sep+"target_dataset_id="+dataset, strings.NewReader(string(marshal(body))))
	r.Header.Set("X-User-Id", user)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}
func libraryDraft(id, title, doi string) *paperlibrary.Paper {
	return &paperlibrary.Paper{ID: id, Metadata: paperlibrary.Metadata{Title: title, Authors: []string{"Jane Doe", "John Smith", "Alex Lee", "Chris Wang"}, Venue: "Journal", Year: 2025, Identifiers: paperlibrary.Identifiers{DOI: doi}}, Tags: []string{}, Source: "local"}
}
func importDraft(t *testing.T, dataset, key string, papers ...*paperlibrary.Paper) libraryChanges {
	t.Helper()
	items := []selectedImportItem{}
	for _, p := range papers {
		items = append(items, selectedImportItem{Paper: p})
	}
	w := libraryRequest(t, "POST", "/imports", dataset, "owner", createImportRequest{EntryType: "bibliography", TargetDatasetID: dataset, IdempotencyKey: key, Format: "bib", Items: items})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out libraryChanges
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func readLibrary(t *testing.T, dataset string) paperlibrary.View {
	t.Helper()
	w := libraryRequest(t, "GET", "/library", dataset, "owner", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out paperlibrary.View
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestLibraryMetadataIdentityAndIndependentRevisions(t *testing.T) {
	db := paperLibraryTestDB(t)
	saved := importDraft(t, "papers", "two-papers", libraryDraft("a", "A", "10.1234/a"), libraryDraft("b", "B", "10.1234/b"))
	a, b := saved.Data.Papers[0], saved.Data.Papers[1]
	corrected := a.Metadata
	corrected.Identifiers.DOI = "10.1234/corrected"
	w := libraryRequest(t, "PATCH", "/library/papers/a", "papers", "owner", map[string]any{"revision": a.Revision, "metadata": corrected})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = libraryRequest(t, "PATCH", "/library/papers/b", "papers", "owner", map[string]any{"revision": b.Revision, "status": "read"})
	if w.Code != 200 {
		t.Fatal("unrelated paper conflicted", w.Code, w.Body.String())
	}
	view := readLibrary(t, "papers")
	for _, p := range view.Data.Papers {
		if p.ID == "a" {
			var work orm.AcademicWork
			if e := db.Where("id=?", p.AcademicWorkID).Take(&work).Error; e != nil {
				t.Fatal(e)
			}
			if work.DOINormalized != "10.1234/corrected" || p.Metadata.Identifiers.DOI != work.DOINormalized {
				t.Fatal("bibliography and identity disagree")
			}
		}
	}
	w = libraryRequest(t, "PATCH", "/library/papers/a", "papers", "owner", map[string]any{"revision": a.Revision, "status": "read"})
	if w.Code != 409 {
		t.Fatal("stale edit replaced newer data", w.Code)
	}
	if view.PaperCount != 2 || len(view.Data.Papers[0].Metadata.Authors) != 4 {
		t.Fatal("paper or author data lost")
	}
}
func TestLibraryImportReplayAndBatchMoveAreAtomic(t *testing.T) {
	db := paperLibraryTestDB(t)
	saved := importDraft(t, "papers", "original", libraryDraft("a", "A", "10.1234/a"), libraryDraft("b", "B", "10.1234/b"))
	importDraft(t, "papers", "original", libraryDraft("a", "A", "10.1234/a"), libraryDraft("b", "B", "10.1234/b"))
	var count int64
	db.Model(&orm.PaperImportBatch{}).Count(&count)
	if count != 1 {
		t.Fatal("import replay duplicated receipt")
	}
	w := libraryRequest(t, "POST", "/library/folders", "papers", "owner", map[string]any{"id": "folder", "name": "Topic"})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	items := []libraryTarget{{ID: "a", Revision: saved.Data.Papers[0].Revision}, {ID: "b", Revision: 99}}
	w = libraryRequest(t, "POST", "/library/papers:move", "papers", "owner", map[string]any{"items": items, "folder_id": "folder"})
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, p := range readLibrary(t, "papers").Data.Papers {
		if p.FolderID != nil {
			t.Fatal("batch move partially committed")
		}
	}
}
func TestLibraryNotesAndDirectoryDeletionRetainMaterials(t *testing.T) {
	db := paperLibraryTestDB(t)
	for _, f := range []map[string]any{{"id": "parent", "name": "Parent"}, {"id": "child", "name": "Child", "parent_id": "parent"}} {
		w := libraryRequest(t, "POST", "/library/folders", "papers", "owner", f)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	draft := libraryDraft("a", "A", "10.1234/a")
	folder := "child"
	draft.FolderID = &folder
	importDraft(t, "papers", "with-folder", draft)
	for i := 0; i < 2; i++ {
		w := libraryRequest(t, "POST", "/library/papers/a/notes", "papers", "owner", map[string]any{"id": "note", "content": "Do not lose this note"})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := libraryRequest(t, "POST", "/library/folders/parent:delete", "papers", "owner", map[string]any{"revision": 1})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	view := readLibrary(t, "papers")
	if len(view.Data.Folders) != 0 || view.Data.Papers[0].FolderID != nil || len(view.Data.Notes) != 1 {
		t.Fatal("directory removal lost or duplicated reading data", view)
	}
	db.Model(&orm.PaperLibraryNote{}).Count(&view.PaperCount)
	if view.PaperCount != 1 {
		t.Fatal("notes were rewritten")
	}
}
func TestLibrarySinglePaperMutationHasBoundedReads(t *testing.T) {
	db := paperLibraryTestDB(t)
	importDraft(t, "papers", "seed", libraryDraft("a", "A", "10.1234/a"))
	queries := 0
	db.Callback().Query().After("gorm:query").Register("library-read-count", func(*gorm.DB) { queries++ })
	t.Cleanup(func() { db.Callback().Query().Remove("library-read-count") })
	patch := func(revision int64) {
		w := libraryRequest(t, "PATCH", "/library/papers/a", "papers", "owner", map[string]any{"revision": revision, "status": "reading"})
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	patch(1)
	queries = 0
	patch(2)
	small := queries
	works := []orm.AcademicWork{}
	entries := []orm.PaperLibraryEntry{}
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("bulk-%d", i)
		works = append(works, orm.AcademicWork{ID: id, CanonicalTitle: id, AuthorsJSON: marshal([]string{}), ExternalIDsJSON: marshal(map[string]string{}), ProvenanceJSON: marshal(map[string]any{})})
		entries = append(entries, orm.PaperLibraryEntry{DatasetID: "papers", ID: id, AcademicWorkID: id, Status: "unread", TagsJSON: marshal([]string{}), MetadataJSON: marshal(map[string]any{}), Revision: 1})
	}
	if e := db.CreateInBatches(&works, 100).Error; e != nil {
		t.Fatal(e)
	}
	if e := db.CreateInBatches(&entries, 100).Error; e != nil {
		t.Fatal(e)
	}
	queries = 0
	patch(3)
	if queries > small+2 {
		t.Fatalf("single edit reads grew with library size: %d -> %d", small, queries)
	}
}
func TestLibraryAccessAndForeignFoldersAreRejected(t *testing.T) {
	paperLibraryTestDB(t)
	importDraft(t, "papers", "seed", libraryDraft("a", "A", "10.1234/a"))
	w := libraryRequest(t, "PATCH", "/library/papers/a", "papers", "stranger", map[string]any{"revision": 1, "status": "read"})
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = libraryRequest(t, "POST", "/library/folders", "other", "owner", map[string]any{"id": "foreign", "name": "Foreign"})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = libraryRequest(t, "POST", "/library/papers:move", "papers", "owner", map[string]any{"items": []libraryTarget{{ID: "a", Revision: 1}}, "folder_id": "foreign"})
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	if readLibrary(t, "papers").Data.Papers[0].FolderID != nil {
		t.Fatal("foreign folder accepted")
	}
}
