package doc

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPaperUploadRecordsOriginalTaskAndReplaysWithoutAnotherDocument(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.PaperImportBatch{}, &orm.PaperImportItem{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	dataset := orm.Dataset{ID: "papers", Ext: json.RawMessage(`{"library_kind":"paper"}`), BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&dataset).Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "paper.pdf")
	if err := os.WriteFile(path, []byte("%PDF-study"), 0600); err != nil {
		t.Fatal(err)
	}
	source := newTestUploadedFile("paper-upload", "user-1", "hash", UploadedFileStateUploaded, path, now)
	source.DatasetID = dataset.ID
	source.Ext = mustJSON(uploadedFileExt{StoredPath: path, OriginalFilename: "paper.pdf", FileSize: 10, ContentType: "application/pdf"})
	if err := db.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	req := CreateTaskRequest{PaperImport: &PaperUploadContext{IdempotencyKey: "upload-request", LibraryPaperID: "library-paper", Title: "Uploaded study"}, Items: []CreateTaskItem{{UploadFileID: source.UploadFileID}}}
	request := httptest.NewRequest("POST", "/", nil)
	request.Header.Set("X-User-Id", "user-1")
	request, batchID, replay, err := preparePaperUpload(request, &dataset, req)
	if err != nil || replay != nil {
		t.Fatalf("prepare: %v %+v", err, replay)
	}
	tasks, err := createTaskFromUploadedFile(request, dataset.ID, "user-1", "User", req.Items[0], string(TaskTypeParseUploaded))
	if err != nil {
		t.Fatal(err)
	}
	_, replayedID, replay, err := preparePaperUpload(request, &dataset, req)
	if err != nil || replay == nil || replay.ID != tasks[0].ID || replay.DocID != tasks[0].DocID || replayedID != batchID {
		t.Fatalf("replay: %v %+v", err, replay)
	}
	var item orm.PaperImportItem
	db.Where("batch_id=?", batchID).Take(&item)
	if item.DocumentTaskID != tasks[0].ID || item.TitleSnapshot != "Uploaded study" || item.AcademicWorkID != "" {
		t.Fatalf("bad receipt: %+v", item)
	}
	var count int64
	db.Model(&orm.Document{}).Where("dataset_id=?", dataset.ID).Count(&count)
	if count != 1 {
		t.Fatalf("duplicate documents: %d", count)
	}
	req.PaperImport.LibraryPaperID = "other-paper"
	if _, _, _, err := preparePaperUpload(request, &dataset, req); err == nil {
		t.Fatal("accepted mismatched replay")
	}
}
