package academic

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/doc"
)

// Cancellation and association rollback are covered by the shared pipeline
// tests. These regressions preserve real task recovery and rejected receipts.
func TestUnifiedArxivImportRejectedStartResultRetainsRecoverableBinding(t *testing.T) {
	db, batch, item, _ := importPipelineFixture(t)
	seedPipelineCache(t, db, "v2")
	submits := 0
	service, err := doc.NewOriginalService(doc.OriginalServiceDeps{DB: db, SubmitProcessing: func(context.Context, doc.DatasetCatalogCaller, string, string) (doc.StartTaskResult, error) {
		submits++
		if submits == 1 {
			return doc.StartTaskResult{Status: "FAILED", SubmitStatus: "REJECTED", Message: "store document task failed"}, nil
		}
		return doc.StartTaskResult{Status: "STARTED", SubmitStatus: "ACCEPTED"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	job := asyncjob.Job{ID: batch.AsyncJobID, AttemptCount: 1}
	if err := processImportItem(t.Context(), job, batch, item, sourceImportCaller(batch), service); err == nil || !strings.Contains(err.Error(), "store document task failed") {
		t.Fatalf("rejected submission reported success: %v", err)
	}
	if err := db.Where("id=?", item.ID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Status == "succeeded" || item.DocumentID == "" || item.DocumentTaskID == "" || len(item.ContentSHA256) != 64 {
		t.Fatalf("failed binding lost its receipt: %+v", item)
	}
	document, task, hash := item.DocumentID, item.DocumentTaskID, item.ContentSHA256
	if err := processImportItem(t.Context(), job, batch, item, sourceImportCaller(batch), service); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id=?", item.ID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Status != "succeeded" || item.DocumentID != document || item.DocumentTaskID != task || item.ContentSHA256 != hash || submits != 2 {
		t.Fatalf("retry lost committed identity: %+v submits=%d", item, submits)
	}
	for _, model := range []any{&orm.Document{}, &orm.Task{}, &orm.AcademicWorkDocument{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("duplicated %T: count=%d err=%v", model, count, err)
		}
	}
}

func TestUnifiedArxivImportRealBridgeRecoversPausedSubmissionWithoutDuplicateFile(t *testing.T) {
	db, batch, item, _ := importPipelineFixture(t)
	seedPipelineCache(t, db, "v2")
	if err := db.Model(&orm.Dataset{}).Where("id=?", batch.TargetDatasetID).Update("processing_level", "parsed").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{"user_id": batch.CreatedBy, "document_parsing_enabled": false, "created_at": now, "updated_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	service, err := doc.NewOriginalService(doc.OriginalServiceDeps{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	job := asyncjob.Job{ID: batch.AsyncJobID, AttemptCount: 1}
	for attempt := 0; attempt < 2; attempt++ {
		if err := processImportItem(t.Context(), job, batch, item, sourceImportCaller(batch), service); err == nil || !strings.Contains(err.Error(), "paused") {
			t.Fatalf("paused parser reported success: %v", err)
		}
		if err := db.Where("id=?", item.ID).Take(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	document, task, hash := item.DocumentID, item.DocumentTaskID, item.ContentSHA256
	if err := db.Model(&orm.Dataset{}).Where("id=?", batch.TargetDatasetID).Update("processing_level", "stored").Error; err != nil {
		t.Fatal(err)
	}
	// Reopening the bridge simulates the next Core process receiving this retry.
	service, err = doc.NewOriginalService(doc.OriginalServiceDeps{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	if err := processImportItem(t.Context(), job, batch, item, sourceImportCaller(batch), service); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id=?", item.ID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Status != "succeeded" || item.DocumentID != document || item.DocumentTaskID != task || item.ContentSHA256 != hash || len(hash) != 64 {
		t.Fatalf("real bridge lost persisted binding: %+v", item)
	}
	var saved orm.Task
	if err := db.Where("id=?", task).Take(&saved).Error; err != nil {
		t.Fatal(err)
	}
	var state struct {
		State string `json:"task_state"`
	}
	if err := json.Unmarshal(saved.Ext, &state); err != nil || state.State != "SUCCEEDED" {
		t.Fatalf("task was never resumed: %s %v", saved.Ext, err)
	}
	for _, model := range []any{&orm.Document{}, &orm.Task{}, &orm.AcademicWorkDocument{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("duplicated %T: count=%d err=%v", model, count, err)
		}
	}
}

func TestUnifiedArxivImportDoesNotReuseMissingOrDeletedOriginal(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing_file", true: "deleted_document"}[deleted], func(t *testing.T) {
			db, batch, item, service := importPipelineFixture(t)
			template := item
			seedPipelineCache(t, db, "v2")
			job := asyncjob.Job{ID: batch.AsyncJobID, AttemptCount: 1}
			if err := processImportItem(t.Context(), job, batch, item, sourceImportCaller(batch), service); err != nil {
				t.Fatal(err)
			}
			var first orm.PaperImportItem
			if err := db.Where("id=?", item.ID).Take(&first).Error; err != nil {
				t.Fatal(err)
			}
			var document orm.Document
			if err := db.Where("id=?", first.DocumentID).Take(&document).Error; err != nil {
				t.Fatal(err)
			}
			if deleted {
				if err := db.Model(&document).Update("deleted_at", time.Now().UTC()).Error; err != nil {
					t.Fatal(err)
				}
			} else {
				var ext struct {
					Path string `json:"stored_path"`
				}
				if err := json.Unmarshal(document.Ext, &ext); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(ext.Path); err != nil {
					t.Fatal(err)
				}
			}
			if err := processImportItem(t.Context(), job, batch, first, sourceImportCaller(batch), service); !errors.Is(err, doc.ErrImportedDocumentUnavailable) {
				t.Fatalf("bound item concealed its unavailable original: %v", err)
			}
			item.ID = "reimport"
			if err := db.Create(&item).Error; err != nil {
				t.Fatal(err)
			}
			if err := processImportItem(t.Context(), job, batch, item, sourceImportCaller(batch), service); err != nil {
				t.Fatal(err)
			}
			if err := db.Where("id=?", item.ID).Take(&item).Error; err != nil {
				t.Fatal(err)
			}
			if item.Status != "succeeded" || item.DocumentID == first.DocumentID || item.ContentSHA256 != first.ContentSHA256 {
				t.Fatalf("unavailable original reused: status=%s original=%s replacement=%s hash=%s", item.Status, first.DocumentID, item.DocumentID, item.ContentSHA256)
			}
			var historical, replacement orm.Document
			if err := db.Where("id=?", first.DocumentID).Take(&historical).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Where("id=?", item.DocumentID).Take(&replacement).Error; err != nil {
				t.Fatal(err)
			}
			if historical.DisplayName != document.DisplayName || (!deleted && (historical.DeletedAt != nil || historical.DisplayName == replacement.DisplayName)) {
				t.Fatal("recovery rewrote history or reused its active name")
			}
			template.ID = "repeat-after-recovery"
			if err := db.Create(&template).Error; err != nil {
				t.Fatal(err)
			}
			if err := processImportItem(t.Context(), job, batch, template, sourceImportCaller(batch), service); err != nil {
				t.Fatal(err)
			}
			if err := db.Where("id=?", template.ID).Take(&template).Error; err != nil {
				t.Fatal(err)
			}
			if template.Status != "skipped" || template.DocumentID != replacement.ID {
				t.Fatalf("healthy replacement not reused: status=%s document=%s", template.Status, template.DocumentID)
			}
		})
	}
}

// A worker can lose its final item receipt after the parser accepted the task.
// Retrying that item must preserve accepted work even when new parsing is paused.
func TestUnifiedArxivImportRecoveryPreservesAcceptedAndAmbiguousTasks(t *testing.T) {
	for _, state := range []string{"RUNNING", "WAITING", "SUCCEEDED", "external task", "missing remote observation", "submission unknown", "missing metadata"} {
		t.Run(state, func(t *testing.T) {
			db, batch, item, service := importPipelineFixture(t)
			seedPipelineCache(t, db, "v2")
			job := asyncjob.Job{ID: batch.AsyncJobID, AttemptCount: 1}
			if err := processImportItem(t.Context(), job, batch, item, sourceImportCaller(batch), service); err != nil {
				t.Fatal(err)
			}
			if err := db.Where("id=?", item.ID).Take(&item).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.Dataset{}).Where("id=?", batch.TargetDatasetID).Updates(map[string]any{"processing_level": "parsed", "kb_id": "accepted-kb"}).Error; err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{"user_id": batch.CreatedBy, "document_parsing_enabled": false, "created_at": now, "updated_at": now}).Error; err != nil {
				t.Fatal(err)
			}
			ext := marshal(map[string]string{"task_state": state})
			if state == "external task" || state == "missing remote observation" {
				t.Setenv("LAZYMIND_READONLY_SCHEMA", "")
				if err := db.AutoMigrate(&readonlyorm.LazyLLMDocServiceTaskRow{}); err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&orm.Document{}).Where("id=?", item.DocumentID).Update("lazyllm_doc_id", "accepted-document").Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&orm.Task{}).Where("id=?", item.DocumentTaskID).Update("lazyllm_task_id", "accepted-task").Error; err != nil {
					t.Fatal(err)
				}
				if state == "external task" {
					if err := db.Create(&readonlyorm.LazyLLMDocServiceTaskRow{TaskID: "accepted-task", DocID: "accepted-document", KbID: "accepted-kb", Status: "WORKING", UpdatedAt: now}).Error; err != nil {
						t.Fatal(err)
					}
				}
				ext = json.RawMessage(`{}`)
			} else if state == "submission unknown" {
				ext = json.RawMessage(`{"task_state":"FAILED","market_submission":"submitting"}`)
			} else if state == "missing metadata" {
				ext = nil
			}
			if err := db.Model(&orm.Task{}).Where("id=?", item.DocumentTaskID).Update("ext", ext).Error; err != nil {
				t.Fatal(err)
			}
			service, err := doc.NewOriginalService(doc.OriginalServiceDeps{DB: db})
			if err != nil {
				t.Fatal(err)
			}
			err = processImportItem(t.Context(), job, batch, item, sourceImportCaller(batch), service)
			if state == "missing metadata" || state == "missing remote observation" || state == "submission unknown" {
				if err == nil || !strings.Contains(err.Error(), "verification") {
					t.Fatalf("ambiguous task was resubmitted instead of retaining its outcome: %v", err)
				}
			} else if err != nil {
				t.Fatalf("accepted work was resubmitted: %v", err)
			}
			var task orm.Task
			if err := db.Where("id=?", item.DocumentTaskID).Take(&task).Error; err != nil {
				t.Fatal(err)
			}
			if state != "submission unknown" && strings.Contains(string(task.Ext), "FAILED") {
				t.Fatalf("recovery rewrote accepted task as failed: %s", task.Ext)
			}
			for _, model := range []any{&orm.Document{}, &orm.Task{}, &orm.AcademicWorkDocument{}} {
				var count int64
				if err := db.Model(model).Count(&count).Error; err != nil || count != 1 {
					t.Fatalf("recovery duplicated %T: %d %v", model, count, err)
				}
			}
		})
	}
}
