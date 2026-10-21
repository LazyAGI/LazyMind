package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

func paperImportRetryFixture(t *testing.T) (*gorm.DB, asyncjob.Job, orm.PaperImportItem) {
	t.Helper()
	db := importErrorDB(t)
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.Document{}, &orm.Task{}, &orm.DocumentProcessingState{}, &orm.UserUIPreferences{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "saved.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\nsaved paper"), 0600); err != nil {
		t.Fatal(err)
	}
	ext, _ := json.Marshal(map[string]any{"stored_path": path, "content_type": "application/pdf"})
	item := orm.PaperImportItem{ID: "retry-item", BatchID: "retry-batch", AcademicWorkID: "retry-work", DocumentID: "saved-document", ContentSHA256: strings.Repeat("a", 64), Status: "failed", Stage: "import", ErrorCode: "import_failed", SelectedCandidateJSON: json.RawMessage(`{"url":"file:///must-not-download.pdf"}`), ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), ErrorDetailsJSON: json.RawMessage(`{"message":"processing service unavailable"}`)}
	for _, row := range []any{
		&orm.Dataset{ID: "dataset", ProcessingLevel: "stored"},
		&orm.Document{ID: item.DocumentID, DatasetID: "dataset", DisplayName: "saved.pdf", Ext: ext, Tags: json.RawMessage(`[]`)},
		&orm.AcademicWork{ID: item.AcademicWorkID, CanonicalTitle: "Saved paper", NormalizedTitle: "saved paper", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)},
		&orm.PaperImportBatch{ID: item.BatchID, TargetDatasetID: "dataset", Status: "failed", TotalItems: 1, SourceDocumentIDsJSON: json.RawMessage(`[]`), PolicySnapshotJSON: json.RawMessage(`{}`)},
		&item,
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, asyncjob.Job{PayloadJSON: json.RawMessage(`{"batch_id":"retry-batch","user_id":"user"}`)}, item
}

func TestPaperImportRetryRecoversAssociationAndTaskWithoutDownloading(t *testing.T) {
	db, job, item := paperImportRetryFixture(t)
	if _, err := handlePaperImportJob(context.Background(), job, importErrorReporter{}); err != nil {
		t.Fatalf("retry should reuse the saved file and recover its task: %v", err)
	}
	if err := db.Take(&item, "id = ?", item.ID).Error; err != nil {
		t.Fatal(err)
	}
	if item.Status != "succeeded" || item.DocumentID != "saved-document" || item.DocumentTaskID == "" || item.ErrorCode != "" || string(item.ErrorDetailsJSON) != "{}" {
		t.Fatalf("saved document was not recovered: %+v", item)
	}
	var documents int64
	if err := db.Model(&orm.Document{}).Count(&documents).Error; err != nil || documents != 1 {
		t.Fatalf("retry created another document: count=%d error=%v", documents, err)
	}
	var link orm.AcademicWorkDocument
	if err := db.Take(&link, "academic_work_id = ? AND document_id = ?", item.AcademicWorkID, item.DocumentID).Error; err != nil {
		t.Fatalf("missing academic association: %v", err)
	}
	if link.ContentSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("lost stored content identity: %+v", link)
	}
}

func TestPaperImportRetryPreservesProcessingFailureEvenWithAssociation(t *testing.T) {
	db, job, item := paperImportRetryFixture(t)
	if err := db.Model(&orm.Dataset{}).Where("id = ?", "dataset").Update("processing_level", "parsed").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{"user_id": "user", "document_parsing_enabled": false, "created_at": time.Now().UTC(), "updated_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.AcademicWorkDocument{AcademicWorkID: item.AcademicWorkID, DatasetID: "dataset", DocumentID: item.DocumentID}).Error; err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := handlePaperImportJob(context.Background(), job, importErrorReporter{}); err == nil {
			t.Fatal("failed processing retry must remain visible, even when its document is associated")
		}
		if err := db.Take(&item, "id = ?", item.ID).Error; err != nil {
			t.Fatal(err)
		}
		if item.Status != "failed" || item.ErrorCode != "import_failed" || item.DocumentID != "saved-document" || !strings.Contains(string(item.ErrorDetailsJSON), "paused") {
			t.Fatalf("retry hid processing failure: %+v", item)
		}
	}
	var documents, tasks int64
	if err := db.Model(&orm.Document{}).Count(&documents).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.Task{}).Count(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if documents != 1 || tasks != 1 {
		t.Fatalf("failed retries created duplicates: documents=%d tasks=%d", documents, tasks)
	}
}

func TestPaperImportRetryAcquiresAgainWhenSavedDocumentIsUnavailable(t *testing.T) {
	for _, reason := range []string{"deleted", "missing_document", "missing_file", "different_dataset"} {
		t.Run(reason, func(t *testing.T) {
			db, job, item := paperImportRetryFixture(t)
			if err := db.Create(&orm.AcademicWorkDocument{AcademicWorkID: item.AcademicWorkID, DatasetID: "dataset", DocumentID: item.DocumentID}).Error; err != nil {
				t.Fatal(err)
			}
			var document orm.Document
			if err := db.Take(&document, "id = ?", item.DocumentID).Error; err != nil {
				t.Fatal(err)
			}
			var err error
			switch reason {
			case "deleted":
				err = db.Delete(&document).Error
			case "missing_document":
				err = db.Unscoped().Delete(&document).Error
			case "different_dataset":
				err = db.Model(&document).Update("dataset_id", "other").Error
			case "missing_file":
				var ext struct {
					StoredPath string `json:"stored_path"`
				}
				if err = json.Unmarshal(document.Ext, &ext); err == nil {
					err = os.Remove(ext.StoredPath)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := handlePaperImportJob(context.Background(), job, importErrorReporter{}); err == nil {
				t.Fatal("retry reused an unavailable file instead of attempting the invalid download URL")
			}
			if err := db.Take(&item, "id = ?", item.ID).Error; err != nil {
				t.Fatal(err)
			}
			if item.ErrorCode != "download_failed" {
				t.Fatalf("retry did not attempt a fresh acquisition: %+v", item)
			}
		})
	}
}

func TestPaperImportRetryReusesPartialImportResult(t *testing.T) {
	for _, failure := range []string{"processing", "association"} {
		t.Run(failure, func(t *testing.T) {
			db, job, item := paperImportRetryFixture(t)
			if err := db.Unscoped().Delete(&orm.Document{}, "id = ?", item.DocumentID).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&item).Updates(map[string]any{"document_id": "", "content_sha256": "", "status": "pending"}).Error; err != nil {
				t.Fatal(err)
			}
			downloads, imports := 0, 0
			service := paperImportService{
				acquire: func(context.Context, FulltextCandidate) (string, int64, error) {
					downloads++
					path := filepath.Join(t.TempDir(), "download.pdf")
					content := []byte("%PDF-1.7\ndownloaded paper")
					return path, int64(len(content)), os.WriteFile(path, content, 0600)
				},
				importFile: func(ctx context.Context, request doc.LocalFileImportRequest) (doc.LocalFileImportResult, error) {
					imports++
					id := fmt.Sprintf("imported-%d", imports)
					content, err := os.ReadFile(request.SourcePath)
					if err != nil {
						return doc.LocalFileImportResult{}, err
					}
					path := filepath.Join(t.TempDir(), id+".pdf")
					if err := os.WriteFile(path, content, 0600); err != nil {
						return doc.LocalFileImportResult{}, err
					}
					ext, _ := json.Marshal(map[string]any{"stored_path": path, "content_type": "application/pdf"})
					state := "SUCCEEDED"
					if failure == "processing" {
						state = "FAILED"
					}
					taskExt, _ := json.Marshal(map[string]string{"task_state": state})
					for _, row := range []any{
						&orm.Document{ID: id, DatasetID: request.DatasetID, DisplayName: request.DisplayName, Ext: ext, Tags: json.RawMessage(`[]`)},
						&orm.Task{ID: "task-" + id, DatasetID: request.DatasetID, DocID: id, TaskType: string(doc.TaskTypeParseUploaded), Ext: taskExt},
					} {
						if err := db.WithContext(ctx).Create(row).Error; err != nil {
							return doc.LocalFileImportResult{}, err
						}
					}
					result := doc.LocalFileImportResult{DocumentID: id, TaskID: "task-" + id, ContentSHA256: strings.Repeat("b", 64)}
					if failure == "processing" {
						return result, errors.New("processing service unavailable")
					}
					return result, nil
				},
			}
			if failure == "association" {
				if err := db.Callback().Create().Before("gorm:create").Register("fail_import_association", func(tx *gorm.DB) {
					if tx.Statement.Table == "academic_work_documents" {
						tx.AddError(errors.New("association write unavailable"))
					}
				}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := service.handleJob(context.Background(), job, importErrorReporter{}); err == nil {
				t.Fatal("partial import must report the failure")
			}
			if err := db.Take(&item, "id = ?", item.ID).Error; err != nil {
				t.Fatal(err)
			}
			if item.Status != "failed" || item.DocumentID != "imported-1" || item.DocumentTaskID != "task-imported-1" {
				t.Fatalf("partial import identity was lost: %+v", item)
			}
			if failure == "association" {
				if err := db.Callback().Create().Remove("fail_import_association"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := service.handleJob(context.Background(), job, importErrorReporter{}); err != nil {
				t.Fatalf("retry did not finish the saved import: %v", err)
			}
			if err := db.Take(&item, "id = ?", item.ID).Error; err != nil {
				t.Fatal(err)
			}
			var documents, links, tasks int64
			for _, query := range []struct {
				model any
				count *int64
			}{{&orm.Document{}, &documents}, {&orm.AcademicWorkDocument{}, &links}, {&orm.Task{}, &tasks}} {
				if err := db.Model(query.model).Count(query.count).Error; err != nil {
					t.Fatal(err)
				}
			}
			if item.Status != "succeeded" || item.DocumentID != "imported-1" || documents != 1 || links != 1 || tasks != 1 || downloads != 1 || imports != 1 {
				t.Fatalf("retry duplicated import: item=%+v documents=%d links=%d tasks=%d downloads=%d imports=%d", item, documents, links, tasks, downloads, imports)
			}
		})
	}
}

func TestPaperImportRetryKeepsAlreadyRunningTask(t *testing.T) {
	db, job, item := paperImportRetryFixture(t)
	if err := db.Model(&orm.Dataset{}).Where("id = ?", "dataset").Update("processing_level", "parsed").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{"user_id": "user", "document_parsing_enabled": false, "created_at": time.Now().UTC(), "updated_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	task := orm.Task{ID: "running-task", DatasetID: "dataset", DocID: item.DocumentID, TaskType: string(doc.TaskTypeParseUploaded), Ext: json.RawMessage(`{"task_state":"RUNNING"}`)}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := handlePaperImportJob(context.Background(), job, importErrorReporter{}); err != nil {
		t.Fatalf("retry must recover association without resubmitting an accepted task: %v", err)
	}
	if err := db.Take(&item, "id = ?", item.ID).Error; err != nil {
		t.Fatal(err)
	}
	if item.Status != "succeeded" || item.DocumentTaskID != task.ID {
		t.Fatalf("running task was not reused: %+v", item)
	}
}

func TestPaperImportRetrySubmitsRecoveredTaskForPreviouslyRegisteredDocument(t *testing.T) {
	db, job, item := paperImportRetryFixture(t)
	if err := db.Model(&orm.Document{}).Where("id = ?", item.DocumentID).Update("lazyllm_doc_id", "previous-registration").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.Dataset{}).Where("id = ?", "dataset").Update("processing_level", "parsed").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{"user_id": "user", "document_parsing_enabled": false, "created_at": time.Now().UTC(), "updated_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := handlePaperImportJob(context.Background(), job, importErrorReporter{}); err == nil {
			t.Fatal("a recovered task has never been submitted; old document registration cannot imply success")
		}
		if err := db.Take(&item, "id = ?", item.ID).Error; err != nil {
			t.Fatal(err)
		}
		if item.Status != "failed" || item.DocumentTaskID == "" || !strings.Contains(string(item.ErrorDetailsJSON), "paused") {
			t.Fatalf("recovered task was not submitted: %+v", item)
		}
	}
}
