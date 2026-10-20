package doc

import (
	"bytes"
	"encoding/json"
	"lazymind/core/common/orm"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOriginalHTTPReceivesBytesWithoutCreatingDocument(t *testing.T) {
	db := newDocumentTestDB(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "note.txt")
	_, _ = part.Write([]byte("exact upload bytes"))
	_ = writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/knowledge/originals", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-User-Id", "owner")
	req.Header.Set("X-Tenant-Id", "tenant")
	rec := httptest.NewRecorder()
	UploadOriginalHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("upload failed %d: %s", rec.Code, rec.Body.String())
	}
	var ref OriginalRef
	if err := json.Unmarshal(rec.Body.Bytes(), &ref); err != nil {
		t.Fatal(err)
	}
	if ref.FileRef == "" || ref.ContentSHA256 == "" || ref.Size != 18 {
		t.Fatalf("bad ref: %+v", ref)
	}
	var row orm.UploadedFile
	if err := db.Where("upload_file_id = ?", ref.FileRef).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.CreateUserID != "owner" || row.TenantID != "tenant" {
		t.Fatal("upload owner lost")
	}
	var count int64
	db.Model(&orm.Document{}).Count(&count)
	if count != 0 {
		t.Fatal("acquisition created document")
	}
}
