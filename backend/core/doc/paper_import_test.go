package doc

import (
	"crypto/sha256"
	"encoding/hex"
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
	installOriginalSourceTransport(t)
	if err := db.AutoMigrate(&orm.PaperImportBatch{}, &orm.PaperImportItem{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	dataset := orm.Dataset{ID: "papers", Scene: "academic_papers", Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&dataset).Error; err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	path := filepath.Join(root, "paper.pdf")
	if err := os.WriteFile(path, []byte("%PDF-study\n%%EOF"), 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("%PDF-study\n%%EOF"))
	source := newTestUploadedFile("paper-upload", "user-1", hex.EncodeToString(sum[:]), UploadedFileStateUploaded, path, now)
	source.DatasetID = dataset.ID
	source.Ext = mustJSON(uploadedFileExt{StoredPath: path, OriginalFilename: "paper.pdf", FileSize: int64(len("%PDF-study\n%%EOF")), ContentType: "application/pdf"})
	if err := db.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	req := CreateTaskRequest{PaperImport: &PaperUploadContext{IdempotencyKey: "upload-request", LibraryPaperID: "library-paper", Title: "Uploaded study", SourceVersion: "v2", Paper: mustJSON(map[string]any{"id": "library-paper", "metadata": map[string]any{"title": "Uploaded study", "authors": []string{}}, "tags": []string{}})}, Items: []CreateTaskItem{{UploadFileID: source.UploadFileID}}}
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
	var candidate struct {
		Version string `json:"version"`
	}
	if e := json.Unmarshal(item.SelectedCandidateJSON, &candidate); e != nil || candidate.Version != "v2" {
		t.Fatal("upload recovery lost file version", candidate, e)
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
