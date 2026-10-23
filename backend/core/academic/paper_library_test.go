package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
)

func TestPaperLibraryAtomicSaveAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "libraries.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&orm.PaperLibrary{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	for _, revision := range []int64{0, 1} {
		start := make(chan struct{})
		results := make(chan bool, 8)
		errors := make(chan error, 8)
		var group sync.WaitGroup
		for i := 0; i < 8; i++ {
			group.Add(1)
			go func(i int) {
				defer group.Done()
				<-start
				saved, err := savePaperLibrary(context.Background(), db, "papers", revision, json.RawMessage(paperLibraryFixture(fmt.Sprintf("Writer %d", i))))
				results <- saved
				errors <- err
			}(i)
		}
		close(start)
		group.Wait()
		close(results)
		close(errors)
		winners := 0
		for saved := range results {
			if saved {
				winners++
			}
		}
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
		if winners != 1 {
			t.Fatalf("revision %d had %d successful writers", revision, winners)
		}
	}
	if err = sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, _ := reopened.DB()
	defer connection.Close()
	result, err := readPaperLibrary(context.Background(), reopened, "papers")
	if err != nil || result.Revision != 2 || !strings.Contains(string(result.Data), "Writer ") {
		t.Fatalf("restart lost state: %#v %v", result, err)
	}
}

func TestPaperLibraryBodyLimitAndScanWritePolicy(t *testing.T) {
	paperLibraryTestDB(t)
	w := paperLibraryRequest(t, "PUT", "papers", "owner", `{"revision":0,"data":"`+strings.Repeat("x", maxPaperLibraryBytes)+`"}`)
	if w.Code != 413 {
		t.Fatalf("oversized request: %d", w.Code)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "source-access/by-dataset:batch") {
			var request struct {
				Action string `json:"action"`
			}
			json.NewDecoder(r.Body).Decode(&request)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"items":[{"dataset_id":"papers","exists":true,"allowed":%t}]}`, request.Action == "read"))), Header: make(http.Header), Request: r}, nil
		}
		return previous.RoundTrip(r)
	})
	w = paperLibraryRequest(t, "GET", "papers", "owner", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"can_write":false`) {
		t.Fatalf("scan readonly: %d %s", w.Code, w.Body.String())
	}
	w = paperLibraryRequest(t, "PUT", "papers", "owner", `{"revision":0,"data":`+paperLibraryFixture("No access")+`}`)
	if w.Code != 403 {
		t.Fatalf("scan write denial bypassed: %d %s", w.Code, w.Body.String())
	}
}

func paperLibraryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := importErrorDB(t)
	if err := db.AutoMigrate(&orm.PaperLibrary{}, &orm.Dataset{}, &orm.Document{}, &orm.ACLModel{}, &orm.ACLGroupModel{}, &orm.UserGroupModel{}, &orm.KBModel{}, &orm.VisibilityModel{}); err != nil {
		t.Fatal(err)
	}
	acl.InitStore(&orm.DB{DB: db})
	for _, id := range []string{"papers", "other", "documents"} {
		kind := "paper"
		if id == "documents" {
			kind = "document"
		}
		row := orm.Dataset{ID: id, Ext: json.RawMessage(fmt.Sprintf(`{"library_kind":%q}`, kind)), BaseModel: orm.BaseModel{CreateUserID: "owner"}}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	previous := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"items":[],"source_map":{},"code":200,"data":[]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	return db
}

func paperLibraryRequest(t *testing.T, method, dataset, user, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "/academic/library?target_dataset_id="+dataset, strings.NewReader(body))
	r.Header.Set("X-User-Id", user)
	w := httptest.NewRecorder()
	if method == http.MethodGet {
		GetPaperLibrary(w, r)
	} else {
		PutPaperLibrary(w, r)
	}
	return w
}

func paperLibraryFixture(title string) string {
	return fmt.Sprintf(`{"schemaVersion":3,"folders":[{"id":"folder","name":"Topic"}],"collections":[{"id":"collection","name":"Collection"}],"papers":[{"id":"paper","title":%q,"authors":"Author","year":2026,"venue":"Journal","folderId":"folder","status":"reading","important":true,"tags":["tag"],"source":"doi","parse":"ready","metadata":{"identifiers":{"doi":"10.1234/test"},"confidence":{}},"attachmentIds":[],"noteIds":["note"],"annotationIds":[],"collectionIds":["collection"],"relatedPaperIds":[]}],"attachments":[],"notes":[{"id":"note","paperId":"paper","title":"Note","content":"Durable note","kind":"note","createdAt":"2026-10-21","updatedAt":"2026-10-21"}],"annotations":[],"relations":[],"mergeSnapshots":[],"hiddenArxivWorkIds":["hidden-work"],"hiddenArxivPapers":[]}`, title)
}

func TestPaperLibraryPersistsFullGraphAndRejectsStaleWrites(t *testing.T) {
	db := paperLibraryTestDB(t)
	w := paperLibraryRequest(t, "GET", "papers", "owner", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"revision":0`) || !strings.Contains(w.Body.String(), `"can_write":true`) {
		t.Fatalf("empty: %d %s", w.Code, w.Body.String())
	}
	fixture := paperLibraryFixture("Persistent paper")
	w = paperLibraryRequest(t, "PUT", "papers", "owner", `{"revision":0,"data":`+fixture+`}`)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	// A fresh handler/service reads only database state, independent of the writer.
	w = paperLibraryRequest(t, "GET", "papers", "owner", "")
	var result struct {
		Data     json.RawMessage `json:"data"`
		Revision int64           `json:"revision"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	var got, want any
	json.Unmarshal(result.Data, &got)
	json.Unmarshal([]byte(fixture), &want)
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if w.Code != 200 || result.Revision != 1 || string(gotJSON) != string(wantJSON) {
		t.Fatalf("lost graph: %d %s", w.Code, w.Body.String())
	}
	for _, revision := range []int{0, 2} {
		w = paperLibraryRequest(t, "PUT", "papers", "owner", fmt.Sprintf(`{"revision":%d,"data":%s}`, revision, paperLibraryFixture("overwrite")))
		if w.Code != 409 || !strings.Contains(w.Body.String(), "Persistent paper") {
			t.Fatalf("stale save: %d %s", w.Code, w.Body.String())
		}
	}
	if err := db.Callback().Update().Before("gorm:update").Register("fail_library_write", func(tx *gorm.DB) {
		if tx.Statement.Table == "paper_libraries" {
			tx.AddError(errors.New("disk unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	w = paperLibraryRequest(t, "PUT", "papers", "owner", `{"revision":1,"data":`+paperLibraryFixture("failed")+`}`)
	if w.Code != 500 {
		t.Fatalf("failed DB save accepted: %d %s", w.Code, w.Body.String())
	}
	db.Callback().Update().Remove("fail_library_write")
	w = paperLibraryRequest(t, "GET", "papers", "owner", "")
	if !strings.Contains(w.Body.String(), "Persistent paper") || !strings.Contains(w.Body.String(), `"revision":1`) {
		t.Fatalf("failed write damaged state: %s", w.Body.String())
	}
	w = paperLibraryRequest(t, "GET", "other", "owner", "")
	if !strings.Contains(w.Body.String(), `"revision":0`) {
		t.Fatalf("cross-dataset state: %s", w.Body.String())
	}
}

func TestPaperLibraryAuthorizationAndValidation(t *testing.T) {
	db := paperLibraryTestDB(t)
	acl.GetStore().AddACL(acl.ResourceTypeDB, "papers", acl.GranteeUser, "reader", acl.PermissionDatasetRead, "owner", nil)
	acl.GetStore().AddACL(acl.ResourceTypeDB, "papers", acl.GranteeUser, "uploader", acl.PermissionDatasetUpload, "owner", nil)
	acl.GetStore().AddACL(acl.ResourceTypeDB, "papers", acl.GranteeUser, "writer", acl.PermissionDatasetWrite, "owner", nil)
	valid := `{"revision":0,"data":` + paperLibraryFixture("Paper") + `}`
	for _, tt := range []struct {
		method, dataset, user, body string
		status                      int
	}{
		{"GET", "papers", "", "", 400}, {"GET", "unknown", "owner", "", 403}, {"GET", "papers", "stranger", "", 403},
		{"GET", "documents", "owner", "", 400}, {"PUT", "papers", "reader", valid, 403}, {"PUT", "papers", "uploader", valid, 403},
		{"PUT", "papers", "owner", `{"revision":-1,"data":{}}`, 400}, {"PUT", "papers", "owner", `{"revision":0,"data":null}`, 400},
		{"PUT", "papers", "owner", valid + ` {}`, 400}, {"PUT", "papers", "owner", `{"data":` + paperLibraryFixture("Paper") + `}`, 400},
	} {
		w := paperLibraryRequest(t, tt.method, tt.dataset, tt.user, tt.body)
		if w.Code != tt.status {
			t.Fatalf("%+v: %d %s", tt, w.Code, w.Body.String())
		}
	}
	w := paperLibraryRequest(t, "GET", "papers", "reader", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"can_write":false`) {
		t.Fatalf("reader: %d %s", w.Code, w.Body.String())
	}
	w = paperLibraryRequest(t, "PUT", "papers", "writer", valid)
	if w.Code != 200 {
		t.Fatalf("writer: %d %s", w.Code, w.Body.String())
	}
	if err := db.Model(&orm.PaperLibrary{}).Where("dataset_id = ?", "papers").Update("data_json", `{"schemaVersion":99}`).Error; err != nil {
		t.Fatal(err)
	}
	w = paperLibraryRequest(t, "GET", "papers", "owner", "")
	if w.Code != 500 {
		t.Fatalf("corrupt state silently reset: %d %s", w.Code, w.Body.String())
	}
}

func TestPaperLibraryRejectsForeignDocumentAndKeepsMigrationArchive(t *testing.T) {
	db := paperLibraryTestDB(t)
	if err := db.Create(&orm.Document{ID: "foreign-doc", DatasetID: "other", BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	json.Unmarshal([]byte(paperLibraryFixture("Paper")), &data)
	data["attachments"] = []any{map[string]any{"id": "a", "paperId": "paper", "name": "File", "kind": "pdf", "source": "local", "addedAt": "today", "contentRef": "foreign-doc"}}
	encoded, _ := json.Marshal(data)
	w := paperLibraryRequest(t, "PUT", "papers", "owner", `{"revision":0,"data":`+string(encoded)+`}`)
	if w.Code != 400 {
		t.Fatalf("foreign document accepted: %d %s", w.Code, w.Body.String())
	}
	data["attachments"] = []any{}
	data["migrationSnapshots"] = []any{map[string]any{"id": "legacy-hash", "createdAt": "today", "source": "browser-local-v3", "data": json.RawMessage(paperLibraryFixture("Archived legacy"))}}
	encoded, _ = json.Marshal(data)
	w = paperLibraryRequest(t, "PUT", "papers", "owner", `{"revision":0,"data":`+string(encoded)+`}`)
	if w.Code != 200 {
		t.Fatalf("migration save: %d %s", w.Code, w.Body.String())
	}
	w = paperLibraryRequest(t, "GET", "papers", "owner", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Archived legacy") {
		t.Fatalf("lost archive: %d %s", w.Code, w.Body.String())
	}
}
