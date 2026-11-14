package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"path/filepath"
	"testing"
	"time"
)

func guideDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "guide.db")), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.AutoMigrate(&orm.AsyncJob{}, &orm.PaperGuide{}, &orm.PaperGuideRevision{}, &orm.PaperGuideStep{}, &orm.PaperGuideProgress{}); e != nil {
		t.Fatal(e)
	}
	return db
}
func TestRepositoryReuseEpochBudgetAndImmutableSteps(t *testing.T) {
	ctx := context.Background()
	repo := Repository{DB: guideDB(t)}
	scope := Scope{OwnerID: "u", DatasetID: "ds", DocumentID: "doc", Language: "zh"}
	req := CreateRevision{Scope: scope, SourceSHA256: "hash", Model: ModelIdentity{Ref: "m", ID: "m", Name: "model"}, IdempotencyKey: "first"}
	a, e := repo.Create(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	b, e := repo.Create(ctx, req)
	if e != nil || a.ID != b.ID {
		t.Fatalf("reuse: %v", e)
	}
	fence := testFence{repo.DB}
	if e = repo.Reserve(ctx, fence, a.ID, a.Epoch, "plan", false); e != nil {
		t.Fatal(e)
	}
	if e = repo.PutStep(ctx, fence, a.ID, a.Epoch, StepWrite{Key: "plan", InputHash: "input", Output: json.RawMessage(`{"ok":true}`)}); e != nil {
		t.Fatal(e)
	}
	if e = repo.PutStep(ctx, fence, a.ID, a.Epoch, StepWrite{Key: "plan", InputHash: "other", Output: json.RawMessage(`{"ok":false}`)}); !errors.Is(e, ErrConflict) {
		t.Fatalf("mutable checkpoint: %v", e)
	}
	if e = repo.Cancel(ctx, scope, a.ID); e != nil {
		t.Fatal(e)
	}
	if e = repo.PutStep(ctx, fence, a.ID, a.Epoch, StepWrite{Key: "late", InputHash: "late", Output: json.RawMessage(`{}`)}); !errors.Is(e, ErrConflict) {
		t.Fatalf("canceled response: %v", e)
	}
	resumed, e := repo.Resume(ctx, scope, a.ID, req.Model)
	if e != nil {
		t.Fatal(e)
	}
	if resumed.CallsUsed != 1 || resumed.Epoch == a.Epoch {
		t.Fatalf("lost budget or epoch: %+v", resumed)
	}
	if _, e = repo.Resume(ctx, scope, a.ID, ModelIdentity{ID: "changed"}); !errors.Is(e, ErrModelChanged) {
		t.Fatalf("changed model: %v", e)
	}
	req.Regenerate = true
	req.IdempotencyKey = "second"
	newer, e := repo.Create(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	if newer.ID == a.ID {
		t.Fatal("regeneration overwrote old revision")
	}
	if e = repo.PutStep(ctx, fence, a.ID, resumed.Epoch, StepWrite{Key: "late", InputHash: "late", Output: json.RawMessage(`{}`)}); !errors.Is(e, ErrConflict) {
		t.Fatalf("old revision write: %v", e)
	}
	old, e := repo.Revision(ctx, scope, a.ID)
	if e != nil || old.CallsUsed != 1 {
		t.Fatalf("old revision: %v", e)
	}
}

type testFence struct{ db *gorm.DB }

func (f testFence) WithLease(ctx context.Context, cb func(*gorm.DB) error) error {
	return f.db.WithContext(ctx).Transaction(cb)
}
func (f testFence) SetProgress(context.Context, int64, int64) error { return nil }
func (f testFence) Heartbeat(context.Context) error                 { return nil }
func TestBudgetNeverRefundsUnconfirmedRequests(t *testing.T) {
	repo := Repository{DB: guideDB(t)}
	ctx := context.Background()
	scope := Scope{OwnerID: "u", DatasetID: "d", DocumentID: "p", Language: "en"}
	row, e := repo.Create(ctx, CreateRevision{Scope: scope, SourceSHA256: "hash", Model: ModelIdentity{ID: "model"}})
	if e != nil {
		t.Fatal(e)
	}
	fence := testFence{repo.DB}
	for i := 0; i < 24; i++ {
		kind := "section"
		if i < 8 {
			kind = "extract"
		}
		if e = repo.Reserve(ctx, fence, row.ID, row.Epoch, kind, i >= 22); e != nil {
			t.Fatalf("reservation %d: %v", i, e)
		}
	}
	if e = repo.Reserve(ctx, fence, row.ID, row.Epoch, "review", false); !errors.Is(e, ErrBudget) {
		t.Fatalf("25th request: %v", e)
	}
	if e = repo.DB.Model(&orm.PaperGuideRevision{}).Where("id = ?", row.ID).Update("active_millis", int64((30*time.Minute)/time.Millisecond)).Error; e != nil {
		t.Fatal(e)
	}
	if e = repo.Reserve(ctx, fence, row.ID, row.Epoch, "review", false); !errors.Is(e, ErrBudget) {
		t.Fatal(e)
	}
}

func TestDeletedDocumentCancelsOnlyGuideJobsAndHidesAllRevisions(t *testing.T) {
	db := guideDB(t)
	repo := Repository{DB: db}
	scope := Scope{OwnerID: "u", DatasetID: "d", DocumentID: "p", Language: "en"}
	rev, e := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: "hash", Model: ModelIdentity{ID: "m"}})
	if e != nil {
		t.Fatal(e)
	}
	db.Create(&orm.AsyncJob{ID: "other-job", JobType: "unrelated", Status: "pending", ResourceID: "u"})
	if e = db.Transaction(func(tx *gorm.DB) error { return InvalidateDocumentTx(t.Context(), tx, "d", "p") }); e != nil {
		t.Fatal(e)
	}
	if _, e = repo.Revision(t.Context(), scope, rev.ID); !errors.Is(e, gorm.ErrRecordNotFound) {
		t.Fatal("deleted guide readable")
	}
	var job orm.AsyncJob
	db.Where("id = ?", rev.JobID).Take(&job)
	if job.Status != "canceled" {
		t.Fatal("active guide not canceled")
	}
	job = orm.AsyncJob{}
	db.Where("id = ?", "other-job").Take(&job)
	if job.Status != "pending" {
		t.Fatal("unrelated job canceled")
	}
}

func TestWholeReviewCheckpointBindingsAreImmutable(t *testing.T) {
	repo := Repository{DB: guideDB(t)}
	scope := Scope{OwnerID: "u", DatasetID: "ds", DocumentID: "doc", Language: "zh"}
	rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: "hash", Model: ModelIdentity{ID: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	write := StepWrite{Key: "whole_review:1", InputHash: "input", Output: mustJSON(GuideReview{Findings: []GuideFinding{}}), Review: wholeReviewBindings([]GuideSection{{Role: RoleBackground, Version: 2}})}
	if err = repo.PutStep(t.Context(), testFence{repo.DB}, rev.ID, rev.Epoch, write); err != nil {
		t.Fatal(err)
	}
	steps, err := repo.Steps(t.Context(), rev.ID)
	if err != nil || len(steps) != 1 || steps[0].ReviewJSON != string(write.Review) {
		t.Fatalf("missing atomic review bindings: %+v %v", steps, err)
	}
	write.Review = wholeReviewBindings([]GuideSection{{Role: RoleBackground, Version: 1}})
	if err = repo.PutStep(t.Context(), testFence{repo.DB}, rev.ID, rev.Epoch, write); !errors.Is(err, ErrConflict) {
		t.Fatalf("accepted different review bindings: %v", err)
	}
}
