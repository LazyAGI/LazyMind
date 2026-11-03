package doc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"lazymind/core/common/orm"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOriginalBindingIsIndependentAndRetainsDocumentOnSubmissionFailure(t *testing.T) {
	db := newDocumentTestDB(t)
	installOriginalSourceTransport(t)
	if err := db.AutoMigrate(&orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	now := time.Now().UTC()
	for _, id := range []string{"one", "two"} {
		seedDocumentServiceDatasetWithKBAndAlgo(t, db, id, id, "general_algo", "user-1", now)
	}
	svc, err := NewOriginalService(OriginalServiceDeps{DB: db.DB, SubmitProcessing: func(context.Context, DatasetCatalogCaller, string, string) (StartTaskResult, error) {
		return StartTaskResult{}, errors.New("offline")
	}})
	if err != nil {
		t.Fatal(err)
	}
	caller := DatasetCatalogCaller{UserID: "user-1"}
	ref, err := svc.AcquireOriginal(context.Background(), caller, AcquireOriginalRequest{Filename: "note.txt", ContentType: "text/plain", Reader: strings.NewReader("original immutable bytes")})
	if err != nil {
		t.Fatal(err)
	}
	var first OriginalImportResult
	for _, id := range []string{"one", "two"} {
		result, err := svc.ImportOriginal(context.Background(), caller, ImportOriginalRequest{FileRef: ref.FileRef, DatasetID: id})
		if err != nil {
			t.Fatal(err)
		}
		if result.Document.ID == "" || result.SubmissionStatus != "failed" {
			t.Fatalf("lost document: %+v", result)
		}
		if id == "one" {
			first = result
		} else if first.Document.ID == result.Document.ID {
			t.Fatal("binding reused document")
		}
	}
	var source orm.UploadedFile
	if err := db.Where("upload_file_id = ?", ref.FileRef).Take(&source).Error; err != nil {
		t.Fatal(err)
	}
	if source.DocumentID != "" || source.Status != UploadedFileStateUploaded {
		t.Fatal("original binding overwritten")
	}
	var ext uploadedFileExt
	if err := json.Unmarshal(source.Ext, &ext); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ext.StoredPath); err != nil {
		t.Fatal(err)
	}
	var target documentExt
	if err := json.Unmarshal(first.Document.Ext, &target); err != nil {
		t.Fatal(err)
	}
	if bytes, err := os.ReadFile(target.StoredPath); err != nil || string(bytes) != "original immutable bytes" {
		t.Fatalf("not durable: %q %v", bytes, err)
	}
}
func TestOriginalRejectsForeignExpiredAndMalformedSources(t *testing.T) {
	db := newDocumentTestDB(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	svc, _ := NewOriginalService(OriginalServiceDeps{DB: db.DB})
	caller := DatasetCatalogCaller{UserID: "owner", TenantID: "tenant"}
	ref, err := svc.AcquireOriginal(context.Background(), caller, AcquireOriginalRequest{Filename: "x.txt", Reader: strings.NewReader("hello")})
	if err != nil {
		t.Fatal(err)
	}
	for _, other := range []DatasetCatalogCaller{{UserID: "other", TenantID: "tenant"}, {UserID: "owner", TenantID: "other"}} {
		if _, err := svc.PrepareOriginalBinding(context.Background(), other, ImportOriginalRequest{FileRef: ref.FileRef, DatasetID: "target"}); codeOfDatasetServiceError(err) != DatasetServiceForbidden {
			t.Fatalf("foreign ref error: %v", err)
		}
	}
	svc.now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	if _, err := svc.PrepareOriginalBinding(context.Background(), caller, ImportOriginalRequest{FileRef: ref.FileRef, DatasetID: "target"}); codeOfDatasetServiceError(err) != "SOURCE_EXPIRED" {
		t.Fatalf("expired ref error: %v", err)
	}
	for _, data := range []struct{ name, body string }{{"bad.pdf", "<html>login</html>"}, {"bad.docx", "not a zip"}, {"bad.txt", string([]byte{0xff, 0xfe})}} {
		if _, err := svc.AcquireOriginal(context.Background(), caller, AcquireOriginalRequest{Filename: data.name, Reader: strings.NewReader(data.body)}); err == nil {
			t.Fatalf("malformed accepted: %s", data.name)
		}
	}
}

func installOriginalSourceTransport(t *testing.T) {
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/scan/internal/source-access/by-dataset:batch" {
			return testJSONResponse(404, `{}`), nil
		}
		var req struct {
			DatasetIDs []string `json:"dataset_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		var items []map[string]any
		for _, id := range req.DatasetIDs {
			items = append(items, map[string]any{"dataset_id": id, "exists": false, "allowed": true})
		}
		b, _ := json.Marshal(map[string]any{"items": items})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
}
