package academic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/doc"
)

func TestArxivImportResumesUnsubmittedDocumentAfterCancellation(t *testing.T) {
	for _, linked := range []bool{false, true} {
		name := "unlinked document"
		if linked {
			name = "previously associated document"
		}
		t.Run(name, func(t *testing.T) {
			svc, downloads := arxivImportTestService(t)
			db := svc.db
			if err := db.AutoMigrate(&orm.UploadedFile{}, &orm.UserUIPreferences{}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
			if err := db.Model(&orm.Dataset{}).Where("id = ?", "dataset").Update("processing_level", "parsed").Error; err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// The real bridge has committed the document and task when submission
			// first queries the task. A disconnected request can stop it here.
			if err := db.Callback().Query().Before("gorm:query").Register("cancel_arxiv_before_submission", func(tx *gorm.DB) {
				if tx.Statement.Table == "tasks" {
					cancel()
				}
			}); err != nil {
				t.Fatal(err)
			}
			svc.importFile = doc.ImportLocalFile
			first, err := svc.importPaper(ctx, "2404.06654v2", "dataset", "user", "User")
			if !errors.Is(err, context.Canceled) || first.DocumentID == "" || first.DocumentTaskID == "" {
				t.Fatalf("expected a saved document and canceled submission: result=%+v err=%v", first, err)
			}
			if err := db.Callback().Query().Remove("cancel_arxiv_before_submission"); err != nil {
				t.Fatal(err)
			}
			if linked {
				// Older recovery code associated this orphan without submitting
				// its task; retries must also repair that library-match path.
				var document orm.Document
				if err := db.Take(&document, "id = ?", first.DocumentID).Error; err != nil {
					t.Fatal(err)
				}
				if _, err := svc.associate(context.Background(), first.Paper, document, doc.LocalFileImportResult{}, nil); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().UTC()
			if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{"user_id": "user", "document_parsing_enabled": false, "created_at": now, "updated_at": now}).Error; err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				retried, err := svc.importPaper(context.Background(), "2404.06654v2", "dataset", "user", "User")
				if err == nil || !strings.Contains(err.Error(), "paused") || retried.ProcessingStatus != "failed" || retried.DocumentID != first.DocumentID || retried.DocumentTaskID != first.DocumentTaskID {
					t.Fatalf("retry concealed an unsubmitted task: result=%+v err=%v", retried, err)
				}
			}
			// A store-only destination completes through the same real bridge,
			// so the final task state proves recovery actually resumed the task.
			if err := db.Model(&orm.Dataset{}).Where("id = ?", "dataset").Update("processing_level", "stored").Error; err != nil {
				t.Fatal(err)
			}
			recovered, err := svc.importPaper(context.Background(), "2404.06654v2", "dataset", "user", "User")
			if err != nil || recovered.Status != "already_present" || recovered.ProcessingStatus != "ready" || recovered.ProcessingError != "" || recovered.DocumentID != first.DocumentID || recovered.DocumentTaskID != first.DocumentTaskID || len(recovered.ContentSHA256) != 64 {
				t.Fatalf("retry failed to finish the saved document: result=%+v err=%v", recovered, err)
			}
			var task orm.Task
			if err := db.Take(&task, "id = ?", first.DocumentTaskID).Error; err != nil {
				t.Fatal(err)
			}
			var ext struct {
				State string `json:"task_state"`
			}
			if err := json.Unmarshal(task.Ext, &ext); err != nil || ext.State != "SUCCEEDED" {
				t.Fatalf("recovered task was not started: ext=%s err=%v", task.Ext, err)
			}
			for _, model := range []any{&orm.Document{}, &orm.Task{}, &orm.AcademicWorkDocument{}} {
				var count int64
				if err := db.Model(model).Count(&count).Error; err != nil || count != 1 {
					t.Fatalf("recovery duplicated %T: count=%d err=%v", model, count, err)
				}
			}
			if downloads.Load() != 1 {
				t.Fatalf("recovery downloaded %d copies", downloads.Load())
			}
		})
	}
}

func TestArxivImportRecoveryDoesNotResubmitAcceptedOrAmbiguousTasks(t *testing.T) {
	for _, state := range []string{"RUNNING", "WAITING", "external task", "missing metadata"} {
		t.Run(state, func(t *testing.T) {
			t.Setenv("LAZYMIND_READONLY_SCHEMA", "")
			svc, downloads := arxivImportTestService(t)
			first, err := svc.importPaper(context.Background(), "2404.06654v2", "dataset", "user", "User")
			if err != nil {
				t.Fatal(err)
			}
			db := svc.db
			if err := db.AutoMigrate(&orm.UserUIPreferences{}, &readonlyorm.LazyLLMDocServiceTaskRow{}); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.Dataset{}).Where("id = ?", "dataset").Update("processing_level", "parsed").Error; err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{"user_id": "user", "document_parsing_enabled": false, "created_at": now, "updated_at": now}).Error; err != nil {
				t.Fatal(err)
			}
			ext, _ := json.Marshal(map[string]string{"task_state": state})
			if state == "external task" {
				ext = json.RawMessage(`{}`)
				if err := db.Model(&orm.Document{}).Where("id = ?", first.DocumentID).Update("lazyllm_doc_id", "accepted-document").Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "accepted-task", DocID: "accepted-document", KbID: "dataset", Status: "WORKING", UpdatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if state == "missing metadata" {
				ext = nil
			}
			if err := db.Model(&orm.Task{}).Where("id = ?", first.DocumentTaskID).Updates(map[string]any{"task_type": string(doc.TaskTypeParseUploaded), "ext": json.RawMessage(ext)}).Error; err != nil {
				t.Fatal(err)
			}
			retried, err := svc.importPaper(context.Background(), "2404.06654v2", "dataset", "user", "User")
			if state == "missing metadata" {
				if err == nil {
					t.Fatal("unknown task metadata must not be treated as an unsubmitted task")
				}
			} else if err != nil || retried.DocumentID != first.DocumentID || retried.DocumentTaskID != first.DocumentTaskID || retried.ProcessingStatus != "parsing" {
				t.Fatalf("recovery resubmitted existing processing: result=%+v err=%v", retried, err)
			}
			var tasks int64
			if err := db.Model(&orm.Task{}).Count(&tasks).Error; err != nil || tasks != 1 || downloads.Load() != 1 {
				t.Fatalf("recovery duplicated accepted work: tasks=%d downloads=%d err=%v", tasks, downloads.Load(), err)
			}
		})
	}
}
