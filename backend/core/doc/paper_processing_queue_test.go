package doc

import (
	"lazymind/core/common/orm"
	"testing"
)

func TestPaperProcessingQueueIsDurableAndIdempotent(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Document{ID: "saved", DatasetID: "papers"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Task{ID: "parse", DocID: "saved", DatasetID: "papers"}).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		results, err := queuePaperProcessing(t.Context(), db.DB, "owner", "papers", []string{"parse"}, "")
		if err != nil || len(results) != 1 || results[0].DocumentID != "saved" {
			t.Fatal(results, err)
		}
	}
	var count int64
	db.Model(&orm.AsyncJob{}).Where("job_type=?", paperProcessingJob).Count(&count)
	if count != 1 {
		t.Fatalf("queued %d jobs", count)
	}
	var job orm.AsyncJob
	db.First(&job)
	if job.Status != "pending" {
		t.Fatalf("job did not remain durably queued: %s", job.Status)
	}
}
func TestPaperProcessingQueueRejectsUnsavedDocuments(t *testing.T) {
	db := newDocumentTestDB(t)
	db.AutoMigrate(&orm.AsyncJob{})
	db.Create(&orm.Task{ID: "missing", DocID: "absent", DatasetID: "papers"})
	if _, err := queuePaperProcessing(t.Context(), db.DB, "owner", "papers", []string{"missing"}, ""); err == nil {
		t.Fatal("accepted unsaved file")
	}
	var count int64
	db.Model(&orm.AsyncJob{}).Count(&count)
	if count != 0 {
		t.Fatal("orphan processing job")
	}
}
