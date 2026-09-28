package doc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestCreateTaskFromUploadedFileUsesTransactionConnection(t *testing.T) {
	for _, missingDataset := range []bool{false, true} {
		name := "bind upload"
		if missingDataset {
			name = "missing dataset rolls back"
		}
		t.Run(name, func(t *testing.T) {
			db := newDocumentTestDB(t)
			if err := db.AutoMigrate(&orm.DocumentProcessingState{}); err != nil {
				t.Fatal(err)
			}
			// A single connection reproduces the proxy's exclusion of root-DB
			// queries while a transaction holds the database gate.
			sqlDB, err := db.DB.DB()
			if err != nil {
				t.Fatal(err)
			}
			sqlDB.SetMaxOpenConns(1)
			now := time.Now().UTC()
			if !missingDataset {
				seedDocumentServiceDatasetWithKBAndAlgo(t, db, "dataset-source", "dataset-source", "general_algo", "user-1", now)
			}
			upload := newTestUploadedFile("upload-1", "user-1", "", UploadedFileStateUploaded, filepath.Join(t.TempDir(), "source.txt"), now)
			if err := db.Create(&upload).Error; err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			req := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
			tasks, err := createTaskFromUploadedFile(req, "dataset-source", "user-1", "User One", CreateTaskItem{UploadFileID: upload.UploadFileID}, string(TaskTypeParseUploaded))
			if ctx.Err() != nil {
				t.Fatalf("upload binding exhausted request deadline: %v", ctx.Err())
			}
			if missingDataset {
				if err == nil {
					t.Fatal("expected missing dataset error")
				}
			} else if err != nil || len(tasks) != 1 || tasks[0].AlgoID != "general_algo" {
				t.Fatalf("create upload task: tasks=%+v err=%v", tasks, err)
			}
			if err := db.Where("upload_file_id = ?", upload.UploadFileID).Take(&upload).Error; err != nil {
				t.Fatal(err)
			}
			wantStatus, wantCount := UploadedFileStateBound, int64(1)
			if missingDataset {
				wantStatus, wantCount = UploadedFileStateUploaded, 0
			}
			if upload.Status != wantStatus {
				t.Fatalf("upload status=%s want=%s", upload.Status, wantStatus)
			}
			if !missingDataset && (upload.TaskID != tasks[0].ID || upload.DocumentID != tasks[0].DocID) {
				t.Fatalf("upload is not bound to the created document and task: %+v", upload)
			}
			for _, model := range []any{&orm.Document{}, &orm.Task{}, &orm.DocumentProcessingState{}} {
				var count int64
				if err := db.Model(model).Count(&count).Error; err != nil || count != wantCount {
					t.Fatalf("%T count=%d want=%d err=%v", model, count, wantCount, err)
				}
			}
		})
	}
}
