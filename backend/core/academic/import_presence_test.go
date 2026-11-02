package academic

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"io"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDeletedPaperReachesDownloadThroughSharedWorker(t *testing.T) {
	db := sourceTestDB(t)
	if e := db.AutoMigrate(&orm.Dataset{}, &orm.ACLModel{}); e != nil {
		t.Fatal(e)
	}
	prev := store.DB()
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(prev, prev, nil) })
	work := orm.AcademicWork{ID: "work", CanonicalTitle: "Repro", NormalizedTitle: "repro", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)}
	d := orm.Document{ID: "deleted-doc", DatasetID: "target", DisplayName: "paper.pdf", Ext: json.RawMessage(`{}`)}
	link := orm.AcademicWorkDocument{AcademicWorkID: "work", DatasetID: "target", DocumentID: d.ID}
	for _, row := range []any{&work, &d, &link} {
		if e := db.Create(row).Error; e != nil {
			t.Fatal(e)
		}
	}
	_, p, presenceErr := findEquivalentPresentWork(context.Background(), work.ID, "target")
	if presenceErr != nil || p.DocumentID != d.ID {
		t.Fatal("control presence failed")
	}
	if _, e := sourcePresentDocument(db, "target", work.ID, ""); e != nil {
		t.Fatal("control active-document query failed", e)
	}
	if err := db.Model(&orm.Document{}).Where("id = ?", d.ID).Update("dataset_id", "other").Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := findEquivalentPresentWork(context.Background(), work.ID, "target"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("association to document in another library counted as present: %v", err)
	}
	if err := db.Model(&orm.Document{}).Where("id = ?", d.ID).Update("dataset_id", "target").Error; err != nil {
		t.Fatal(err)
	}

	t.Logf("control before deletion: presence_error=%v document=%s", presenceErr, p.DocumentID)
	if e := db.Model(&orm.Document{}).Where("id = ?", d.ID).Update("deleted_at", time.Now().UTC()).Error; e != nil {
		t.Fatal(e)
	}
	_, p, presenceErr = findEquivalentPresentWork(context.Background(), work.ID, "target")
	_, activeErr := sourcePresentDocument(db, "target", work.ID, "")
	t.Logf("after deletion: presence_error=%v document=%s active_query=%v", presenceErr, p.DocumentID, activeErr)
	if !errors.Is(presenceErr, gorm.ErrRecordNotFound) || !errors.Is(activeErr, gorm.ErrRecordNotFound) {
		t.Fatal("review not reproduced")
	}
	// The shared worker must reach acquisition. A blocked local URL stops it
	// before network access, so the test remains hermetic.
	previousTransport := http.DefaultTransport
	http.DefaultTransport = sourceTransport(func(r *http.Request) (*http.Response, error) {

		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"items":[{"dataset_id":"target","exists":false,"allowed":true}],"source_map":{}}`))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
	b := orm.PaperImportBatch{ID: "batch", EntryType: "conversation_sources", TargetDatasetID: "target", Status: "running", AsyncJobID: "job", CreatedBy: "alice", SourceDocumentIDsJSON: json.RawMessage(`[]`), PolicySnapshotJSON: json.RawMessage(`{}`)}
	item := orm.PaperImportItem{ID: "item", BatchID: b.ID, AcademicWorkID: work.ID, Status: "pending", Stage: "download", ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), SelectedCandidateJSON: json.RawMessage(`{"url":"http://127.0.0.1/reimport.pdf"}`), ErrorDetailsJSON: json.RawMessage(`{}`)}
	for _, row := range []any{&orm.Dataset{ID: "target", DisplayName: "papers", BaseModel: orm.BaseModel{CreateUserID: "alice"}}, &orm.AsyncJob{ID: "job", JobType: "paper_import", Status: "running", AttemptCount: 1}, &b, &item} {
		if e := db.Create(row).Error; e != nil {
			t.Fatal(e)
		}
	}
	workerErr := processImportItem(context.Background(), asyncjob.Job{ID: "job", AttemptCount: 1}, b, item, doc.DatasetCatalogCaller{UserID: "alice", Permissions: []string{"document.write", "document.read"}}, nil)
	if workerErr == nil || !strings.Contains(workerErr.Error(), "non-public address") {
		t.Fatalf("reimport did not reach download: %v", workerErr)
	}
	var got orm.PaperImportItem
	if e := db.First(&got, "id = ?", item.ID).Error; e != nil {
		t.Fatal(e)
	}
	var active int64
	if e := db.Model(&orm.Document{}).Where("dataset_id = ? AND deleted_at IS NULL", "target").Count(&active).Error; e != nil {
		t.Fatal(e)
	}
	t.Logf("actual worker: status=%s stage=%s error_code=%s document_id=%s active_documents=%d", got.Status, got.Stage, got.ErrorCode, got.DocumentID, active)
	if got.ErrorCode == "already_present" || got.DocumentID != "" || got.Stage != "download" || active != 0 {
		t.Fatalf("deleted document reused: %+v", got)
	}
}

func TestEquivalentPresencePropagatesDatabaseFailure(t *testing.T) {
	db := sourceTestDB(t)
	previous := store.DB()
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(previous, previous, nil) })
	work := orm.AcademicWork{ID: "work", CanonicalTitle: "paper", NormalizedTitle: "paper", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)}
	if err := db.Create(&work).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&orm.AcademicWorkDocument{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := findEquivalentPresentWork(context.Background(), work.ID, "target"); err == nil || errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("database failure treated as absence: %v", err)
	}
}
