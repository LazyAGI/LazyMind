package paperguide

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func guidePostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN not configured")
	}
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	admin, e := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	name := "paper_guide_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if e = admin.Exec("CREATE DATABASE " + name).Error; e != nil {
		t.Fatal(e)
	}
	u.Path = "/" + name
	db, e := gorm.Open(postgres.Open(u.String()), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		_ = admin.Exec("DROP DATABASE " + name + " WITH (FORCE)").Error
		adminSQL, _ := admin.DB()
		_ = adminSQL.Close()
	})
	if e = db.AutoMigrate(&orm.AsyncJob{}, &orm.PaperGuide{}, &orm.PaperGuideRevision{}, &orm.PaperGuideStep{}, &orm.PaperGuideProgress{}); e != nil {
		t.Fatal(e)
	}
	return db
}
func TestPostgresConcurrentGenerateHasOneCandidate(t *testing.T) {
	repo := Repository{DB: guidePostgres(t)}
	scope := Scope{OwnerID: "u", DatasetID: "d", DocumentID: "p", Language: "zh"}
	ids := make(chan string, 12)
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			row, e := repo.Create(context.Background(), CreateRevision{Scope: scope, SourceSHA256: "hash", Model: ModelIdentity{ID: "m"}, IdempotencyKey: fmt.Sprintf("request-%d", i)})
			if e != nil {
				errs <- e
			} else {
				ids <- row.ID
			}
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	id := ""
	for next := range ids {
		if id != "" && next != id {
			t.Fatal("multiple active candidates")
		}
		id = next
	}
	var count int64
	repo.DB.Model(&orm.PaperGuideRevision{}).Count(&count)
	if count != 1 {
		t.Fatalf("revisions=%d", count)
	}
}

func TestPostgresConcurrentRegenerationWithSameKeyEnqueuesOnce(t *testing.T) {
	ctx := t.Context()
	repo := Repository{DB: guidePostgres(t)}
	req := CreateRevision{Scope: Scope{"u", "d", "p", "en"}, SourceSHA256: "hash", Model: ModelIdentity{ID: "m"}, IdempotencyKey: "original"}
	original, err := repo.Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	req.Regenerate = true
	req.IdempotencyKey = "same-regeneration"
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 12)
	start := make(chan struct{})
	for i := 0; i < cap(results); i++ {
		go func() { <-start; rev, err := repo.Create(ctx, req); results <- result{rev.ID, err} }()
	}
	close(start)
	var replacement string
	for i := 0; i < cap(results); i++ {
		res := <-results
		if res.err != nil {
			t.Errorf("create: %v", res.err)
			continue
		}
		if replacement == "" {
			replacement = res.id
		}
		if res.id != replacement || res.id == original.ID {
			t.Errorf("unexpected revision %q", res.id)
		}
	}
	var jobs, revisions int64
	if err := repo.DB.Model(&orm.AsyncJob{}).Count(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.DB.Model(&orm.PaperGuideRevision{}).Count(&revisions).Error; err != nil {
		t.Fatal(err)
	}
	g, err := repo.Guide(ctx, req.Scope)
	if err != nil || jobs != 2 || revisions != 2 || g.CandidateRevisionID != replacement || g.Epoch != original.Epoch+1 {
		t.Fatalf("jobs=%d revisions=%d guide=%+v err=%v", jobs, revisions, g, err)
	}
}

func TestPostgresReturnedChapterCannotPublishAfterCancelOrReplacement(t *testing.T) {
	checkReturnedChapterFence(t, guidePostgres)
}

func TestPostgresLeaseTakeoverBeforeChapterWrite(t *testing.T) {
	svc, scope, rev, job := fastFixtureWithDB(t, guidePostgres(t))
	svc.ChapterConcurrency = 1
	ctx, cancel := context.WithCancel(t.Context())
	oldReturned, newCalling := make(chan struct{}), make(chan struct{})
	releaseOld, releaseNew := make(chan struct{}), make(chan struct{})
	type attemptResult struct {
		attempt int
		err     error
	}
	results := make(chan attemptResult, 2)
	const jobType = "test.paper-guide-lease"
	if err := svc.Repo.DB.Model(&orm.AsyncJob{}).Where("id = ?", job.ID).Update("job_type", jobType).Error; err != nil {
		t.Fatal(err)
	}
	asyncjob.Register(jobType, func(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
		worker := svc
		oldReady := false
		worker.Call = func(ctx context.Context, task string, input, _ map[string]any) (json.RawMessage, error) {
			if task == "paper.explainer.plan" {
				return json.Marshal(fastFixturePlan())
			}
			id := input["chapter_id"].(string)
			if id == "overview" {
				if job.AttemptCount == 1 {
					oldReady = true
				} else {
					close(newCalling)
					select {
					case <-releaseNew:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
			}
			c := fastFixtureChapter(id)
			c.Blocks[0].Text = fmt.Sprintf("attempt %d", job.AttemptCount)
			return json.Marshal(c)
		}
		worker.Repo.PrepareCheckpoint = func(ctx context.Context, _ orm.PaperGuide) (context.Context, error) {
			if oldReady {
				oldReady = false
				close(oldReturned)
				select {
				case <-releaseOld:
				case <-ctx.Done():
					return ctx, ctx.Err()
				}
			}
			return ctx, nil
		}
		result, err := worker.Run(ctx, job, reporter)
		results <- attemptResult{job.AttemptCount, err}
		return result, err
	})
	opts := asyncjob.Options{WorkerID: "same-worker-id", JobTypes: []string{jobType}, RequireLiveLease: true, SerializeResources: true, Concurrency: 1, LockTTL: time.Minute, PollInterval: 10 * time.Millisecond}
	// Reusing the worker identifier models a process restart. The attempt number
	// must fence the old write even with the same owner, revision, and epoch.
	runners := []*asyncjob.Runner{asyncjob.Start(ctx, svc.Repo.DB, opts)}
	defer func() {
		cancel()
		for _, runner := range runners {
			<-runner.Done()
		}
	}()
	wait := func(ch chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			t.Fatal("worker did not reach barrier")
		}
	}
	wait(oldReturned)
	if err := svc.Repo.DB.Model(&orm.AsyncJob{}).Where("id = ?", job.ID).Update("lock_until", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	runners = append(runners, asyncjob.Start(ctx, svc.Repo.DB, opts))
	wait(newCalling)
	close(releaseOld)
	select {
	case stale := <-results:
		if stale.attempt != 1 || stale.err == nil {
			t.Fatalf("stale worker result %+v", stale)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("old worker did not exit")
	}
	article, err := svc.Revision(ctx, scope, rev.ID, 0)
	if err != nil || article.Run.Stage != "writing" || len(article.Sections) != 8 || len(article.Sections[0].Blocks) != 0 {
		t.Fatal("old worker changed active chapter", err)
	}
	close(releaseNew)
	select {
	case fresh := <-results:
		if fresh.attempt != 2 || fresh.err != nil {
			t.Fatalf("replacement result %+v", fresh)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("replacement did not finish")
	}
	article, err = svc.Revision(ctx, scope, rev.ID, 0)
	if err != nil || article.Run.Stage != "completed" || article.Run.CallsUsed != 10 || len(article.Sections) != 8 {
		t.Fatal("replacement did not publish or repeated work", err)
	}
	for _, section := range article.Sections {
		if len(section.Blocks) != 1 || section.Blocks[0].Text != "attempt 2" {
			t.Fatalf("wrong attempt in section %s", section.Role)
		}
	}
}

func TestConcurrentCancelAdvancesReaderVersionOnce(t *testing.T) {
	ctx := t.Context()
	repo := Repository{DB: guidePostgres(t)}
	scope := Scope{OwnerID: "owner", DatasetID: "set", DocumentID: "paper", Language: "zh"}
	revision, err := repo.Create(ctx, CreateRevision{Scope: scope, SourceSHA256: "source"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := repo.Guide(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	replies := make(chan error, 8)
	start := make(chan struct{})
	for range cap(replies) {
		go func() {
			<-start
			replies <- repo.Cancel(ctx, scope, revision.ID)
		}()
	}
	close(start)
	for range cap(replies) {
		if err := <-replies; err != nil {
			t.Error(err)
		}
	}
	after, err := repo.Guide(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Revision(ctx, scope, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Stage != "canceled" || run.Epoch != revision.Epoch+1 || run.ChangeVersion != revision.ChangeVersion+1 ||
		after.ChangeVersion != before.ChangeVersion+1 || after.Epoch != run.Epoch {
		t.Fatalf("stage %s; epoch %d -> %d; revision version %d -> %d; guide version %d -> %d",
			run.Stage, revision.Epoch, run.Epoch, revision.ChangeVersion, run.ChangeVersion, before.ChangeVersion, after.ChangeVersion)
	}
}
