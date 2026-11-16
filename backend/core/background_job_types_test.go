package main

import (
	"context"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"testing"
	"time"
)

func TestNoRAGWorkerClaimsDocumentTranslation(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.AsyncJob{})
	claimed := make(chan asyncjob.Job, 1)
	asyncjob.Register("document_pdf_translation", func(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
		claimed <- job
		return asyncjob.Result{}, nil
	})
	t.Cleanup(doc.RegisterPDFTranslationJobs)
	job, err := asyncjob.Enqueue(t.Context(), db.DB, asyncjob.EnqueueRequest{JobType: "document_pdf_translation", ResourceType: "pdf_render_job", ResourceID: "fixture", Payload: map[string]string{}, CreateUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	market, err := asyncjob.Enqueue(t.Context(), db.DB, asyncjob.EnqueueRequest{JobType: doc.MarketInstallJobType, ResourceType: "market", ResourceID: "fixture", Payload: map[string]string{}, CreateUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	runner := asyncjob.Start(ctx, db.DB, asyncjob.Options{ExcludeJobTypes: defaultAsyncJobExcludedTypes(false), Concurrency: 1, PollInterval: 10 * time.Millisecond})
	defer func() { cancel(); <-runner.Done() }()
	select {
	case got := <-claimed:
		if got.ID != job.ID || got.AttemptCount != 1 {
			t.Fatalf("wrong claimed translation: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no-RAG worker left translation permanently pending")
	}
	var remaining orm.AsyncJob
	if err := db.DB.Where("id=?", market.ID).Take(&remaining).Error; err != nil || remaining.Status != "pending" {
		t.Fatalf("RAG market job was not excluded: %+v %v", remaining, err)
	}
}
