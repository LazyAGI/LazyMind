package doc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/maintenancegate"
	"lazymind/core/common/orm"
	"lazymind/core/readingpolicy"
)

func TestAutomaticReadingEnrollmentUpdatePause(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedDocumentListDataset(t, db, "update-reading-dataset", "owner")
	if err := db.Model(&orm.Dataset{}).Where("id=?", "update-reading-dataset").Update("scene", "academic_papers").Error; err != nil {
		t.Fatal(err)
	}
	policy := readingpolicy.Defaults()
	policy.Preset = "full"
	policy, err := readingpolicy.Save(ctx, db.DB, "owner", policy)
	if err != nil {
		t.Fatal(err)
	}
	seed := func(id string) {
		t.Helper()
		seedDocumentListDoc(t, db, "update-reading-dataset", id, id+".pdf", policy.AutomaticEligibleAfter.Add(time.Hour+time.Second), "owner", nil)
		source := filepath.Join(t.TempDir(), "paper.pdf")
		if err := os.WriteFile(source, []byte("%PDF-update-fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&orm.Document{}).Where("id=?", id).Update("ext", mustJSON(documentExt{StoredPath: source, OriginalFilename: id + ".pdf"})).Error; err != nil {
			t.Fatal(err)
		}
	}
	checkJobs := func(documents ...string) {
		t.Helper()
		var jobs []orm.AsyncJob
		if err := db.Where("job_type=?", ReadingPreparationJob).Find(&jobs).Error; err != nil {
			t.Fatal(err)
		}
		if want := 2 * len(documents); len(jobs) != want {
			t.Fatalf("enrolled jobs: got %d, want %d", len(jobs), want)
		}
		parse, study := map[string]int{}, map[string]int{}
		for _, job := range jobs {
			var payload readingPreparationPayload
			if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
				t.Fatal(err)
			}
			switch {
			case slices.Equal(payload.Scopes, []string{readingpolicy.PDFParse}):
				parse[payload.DocumentID]++
			case slices.Equal(payload.Scopes, []string{readingpolicy.Formula, readingpolicy.Figure, readingpolicy.Table}):
				study[payload.DocumentID]++
			default:
				t.Fatalf("unexpected preparation scopes: %v", payload.Scopes)
			}
		}
		for _, id := range documents {
			if parse[id] != 1 || study[id] != 1 {
				t.Fatalf("document %s has %d PDF parse jobs and %d study jobs", id, parse[id], study[id])
			}
		}
	}
	pause := func(epoch uint64) *maintenancegate.Ticket {
		t.Helper()
		ticket, err := PauseDeferredIndexingForUpdate(epoch)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ResumeDeferredIndexingFromUpdate(epoch) })
		return ticket
	}
	seed("first")
	first := pause(1)
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := enrollReadingPreparation(ctx, db.DB, nil); err != nil {
		t.Fatal(err)
	}
	checkJobs()
	if err := ResumeDeferredIndexingFromUpdate(1); err != nil {
		t.Fatal(err)
	}

	// Stop the actual background owner after INSERT, inside the outer transaction.
	entered, release := make(chan struct{}), make(chan struct{})
	var blockOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	if err := db.Callback().Create().After("gorm:create").Register("unit:reading-enrollment-commit", func(tx *gorm.DB) {
		if tx.Statement.Table == "async_jobs" {
			blockOnce.Do(func() { close(entered); <-release })
		}
	}); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := StartReadingPreparation(runCtx, db.DB)
	defer func() {
		unblock()
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("reading owner did not stop")
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("reading owner did not enroll")
	}
	second := pause(2)
	if !errors.Is(second.Validate(), maintenancegate.ErrActive) {
		t.Fatal("update reported idle before the enrollment transaction committed")
	}
	if err := enrollReadingPreparation(ctx, db.DB, nil); err != nil {
		t.Fatal(err)
	}
	unblock()
	waitCtx, stopWait := context.WithTimeout(ctx, 3*time.Second)
	defer stopWait()
	if err := second.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	checkJobs("first")
	if err := ResumeDeferredIndexingFromUpdate(2); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(second.Validate(), maintenancegate.ErrStale) {
		t.Fatal("resume retained a valid pause receipt")
	}
	seed("second")
	if err := enrollReadingPreparation(ctx, db.DB, nil); err != nil {
		t.Fatal(err)
	}
	checkJobs("first", "second")
}
