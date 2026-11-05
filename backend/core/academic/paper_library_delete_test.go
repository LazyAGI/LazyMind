package academic

import (
	"encoding/json"
	"gorm.io/gorm"
	"io"
	"lazymind/core/common/orm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLibraryPermanentDeleteAndReimport(t *testing.T) {
	db := paperLibraryTestDB(t)
	saved := importDraft(t, "papers", "before-delete", libraryDraft("a", "A", "10.1234/a"))
	paper := saved.Data.Papers[0]
	if err := db.Create(&orm.AcademicUserPaperState{UserID: "owner", DatasetID: "papers", PaperID: paper.AcademicWorkID, ReadingStatus: "read", IsImportant: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Save(&orm.PaperLibraryLegacyState{DatasetID: "papers", ExtrasJSON: []byte(`{"annotations":[{"id":"mark","paperId":"a","text":"old mark"}]}`)}).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Create(&orm.PaperLibraryNote{DatasetID: "papers", ID: "note", PaperID: paper.ID, Content: "private note"}).Error; err != nil {
		t.Fatal(err)
	}
	w := libraryRequest(t, "POST", "/library/papers:delete", "papers", "owner", map[string]any{"items": []libraryTarget{{ID: paper.ID, Revision: paper.Revision}}})
	if w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if got := readLibrary(t, "papers"); len(got.Data.Papers) != 0 {
		t.Fatalf("paper still visible: %+v", got)
	}
	var personalStateCount int64
	if err := db.Model(&orm.AcademicUserPaperState{}).Where("dataset_id = ? AND paper_id = ?", "papers", paper.AcademicWorkID).Count(&personalStateCount).Error; err != nil || personalStateCount != 0 {
		t.Fatalf("personal paper state survived deletion: count=%d err=%v", personalStateCount, err)
	}
	importDraft(t, "papers", "after-delete", libraryDraft("fresh", "A", "10.1234/a"))
	got := readLibrary(t, "papers")
	raw, _ := json.Marshal(got.Data)
	if strings.Contains(string(raw), "old mark") {
		t.Fatal("deleted annotation returned")
	}
	if len(got.Data.Papers) != 1 || len(got.Data.Notes) != 0 || len(got.Data.Attachments) != 0 {
		t.Fatalf("old data returned: %+v", got)
	}
}

func TestLibraryPermanentDeleteOriginalsAndAtomicConflict(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *gorm.DB
			if driver == "postgres" {
				db = paperLibraryTestDB(t, libraryPostgresDB(t))
			} else {
				db = paperLibraryTestDB(t)
			}
			testPermanentDeleteOriginals(t, db)
		})
	}
}
func testPermanentDeleteOriginals(t *testing.T, db *gorm.DB) {
	previousTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"items":[{"dataset_id":"papers","exists":false,"allowed":true}],"source_map":{},"code":200,"data":[]}`)), Header: make(http.Header), Request: r}, nil
	})
	if err := db.AutoMigrate(&orm.DocumentProcessingState{}, &orm.UploadedFile{}); err != nil {
		t.Fatal(err)
	}
	saved := importDraft(t, "papers", "docs", libraryDraft("a", "A", "10.1234/a"), libraryDraft("b", "B", "10.1234/b"))
	a, b := saved.Data.Papers[0], saved.Data.Papers[1]
	for _, id := range []string{"original", "duplicate", "unrelated"} {
		if err := db.Create(&orm.Document{ID: id, DatasetID: "papers", DisplayName: id + ".pdf", Ext: []byte(`{}`), Tags: []byte(`[]`)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"original", "duplicate"} {
		attachment := orm.PaperLibraryAttachment{DatasetID: "papers", ID: id, PaperID: a.ID, DocumentID: id}
		if id == "duplicate" {
			now := time.Now()
			attachment.DetachedAt = &now
		}
		if err := db.Create(&attachment).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := func(user string, rev int64) *httptest.ResponseRecorder {
		return libraryRequest(t, "POST", "/library/papers:delete", "papers", user, map[string]any{"items": []libraryTarget{{ID: a.ID, Revision: a.Revision}, {ID: b.ID, Revision: rev}}})
	}
	if w := request("stranger", b.Revision); w.Code != 403 && w.Code != 404 {
		t.Fatalf("unauthorized: %d %s", w.Code, w.Body.String())
	}
	if w := request("owner", b.Revision+1); w.Code != 409 {
		t.Fatalf("conflict: %d %s", w.Code, w.Body.String())
	}
	var count int64
	db.Model(&orm.Document{}).Where("deleted_at IS NULL").Count(&count)
	if count != 3 || len(readLibrary(t, "papers").Data.Papers) != 2 {
		t.Fatal("partial deletion after conflict")
	}
	if w := request("owner", b.Revision); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	db.Model(&orm.Document{}).Where("deleted_at IS NULL AND id IN ?", []string{"original", "duplicate"}).Count(&count)
	if count != 0 {
		t.Fatal("original or detached duplicate still active")
	}
	db.Model(&orm.Document{}).Where("deleted_at IS NULL AND id = ?", "unrelated").Count(&count)
	if count != 1 {
		t.Fatal("unrelated document deleted")
	}
	importDraft(t, "papers", "reimport", libraryDraft("fresh", "A", "10.1234/a"))
	if got := readLibrary(t, "papers"); len(got.Data.Attachments) != 0 {
		t.Fatal("old attachments resurrected")
	}
}
