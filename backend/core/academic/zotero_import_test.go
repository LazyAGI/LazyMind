package academic

import (
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
