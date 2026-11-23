package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

func TestResumeAfterPersistedPlanOrAllSectionsReusesModelResponses(t *testing.T) {
	for _, stage := range []string{"writing", "checking"} {
		t.Run(stage, func(t *testing.T) {
			svc, scope, rev, job := fastFixture(t)
			fence := testFence{svc.Repo.DB}
			var calls atomic.Int32
			svc.Call = func(_ context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
				calls.Add(1)
				if task == "paper.explainer.plan" {
					return json.Marshal(fastFixturePlan())
				}
				return json.Marshal(fastFixtureChapter(input["chapter_id"].(string)))
			}
			// Fail the transition transaction once, after the prior checkpoints committed.
			var injected atomic.Bool
			if err := svc.Repo.DB.Callback().Update().Before("gorm:update").Register("test:interrupt_transition", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Model.(*orm.PaperGuideRevision); !ok {
					return
				}
				if fields, ok := tx.Statement.Dest.(map[string]any); ok && fields["stage"] == stage && injected.CompareAndSwap(false, true) {
					tx.AddError(errors.New("simulated unavailable database at transition"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Run(t.Context(), job, fence); err == nil || !injected.Load() {
				t.Fatalf("interruption not observed: %v", err)
			}
			before, err := svc.Revision(t.Context(), scope, rev.ID, 0)
			if err != nil || before.Plan == nil || before.Run.Stage != "paused" {
				t.Fatalf("incorrect interrupted state: %v %+v", err, before.Run)
			}
			wantCalls := int32(1)
			if stage == "checking" {
				wantCalls = 9
			}
			if calls.Load() != wantCalls {
				t.Fatalf("before resume calls=%d want=%d", calls.Load(), wantCalls)
			}
			for _, section := range before.Sections {
				if (len(section.Blocks) > 0) != (stage == "checking") {
					t.Fatalf("incorrect pre-resume section %s", section.Key)
				}
			}
			model, _, err := svc.ResolveModel(t.Context(), scope, "")
			if err != nil {
				t.Fatal(err)
			}
			next, err := svc.Repo.Resume(t.Context(), scope, rev.ID, model)
			if err != nil {
				t.Fatal(err)
			}
			_, err = svc.Run(t.Context(), asyncjob.Job{ID: next.JobID, PayloadJSON: mustJSON(JobPayload{next.ID, next.Epoch})}, fence)
			if err != nil {
				t.Fatal(err)
			}
			final, err := svc.Repo.Revision(t.Context(), scope, next.ID)
			if err != nil || final.Stage != "completed" || final.CallsUsed != 9 || final.RecoveryCalls != 0 || calls.Load() != 9 {
				t.Fatalf("unexpected replay: calls=%d final=%+v err=%v", calls.Load(), final, err)
			}
		})
	}
}

func TestFailedReplacementPreservesPublishedArticle(t *testing.T) {
	svc, scope, first, job := fastFixture(t)
	ctx := t.Context()
	fence := testFence{svc.Repo.DB}
	svc.Call = func(_ context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
		if task == "paper.explainer.plan" {
			return json.Marshal(fastFixturePlan())
		}
		return json.Marshal(fastFixtureChapter(input["chapter_id"].(string)))
	}
	if _, err := svc.Run(ctx, job, fence); err != nil {
		t.Fatal(err)
	}
	before, err := svc.Revision(ctx, scope, first.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	model, _, err := svc.ResolveModel(ctx, scope, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Repo.Create(ctx, CreateRevision{Scope: scope, Regenerate: true, SourceSHA256: first.SourceSHA256, IRDigest: first.IRDigest, SnapshotPath: first.SnapshotPath, Model: model, IdempotencyKey: "replacement"})
	if err != nil {
		t.Fatal(err)
	}
	svc.Call = func(_ context.Context, _ string, _ map[string]any, _ map[string]any) (json.RawMessage, error) {
		return json.RawMessage(`{"broken":`), nil
	}
	if _, err = svc.Run(ctx, asyncjob.Job{ID: second.JobID, PayloadJSON: mustJSON(JobPayload{second.ID, second.Epoch})}, fence); err == nil {
		t.Fatal("invalid replacement succeeded")
	}
	s, err := svc.Status(ctx, scope)
	if err != nil || s.PublishedRevision == nil || s.PublishedRevision.ID != first.ID || s.CandidateRevision == nil || s.CandidateRevision.ID != second.ID || s.CandidateRevision.Run.CallsUsed != 1 || s.CandidateRevision.Run.Stage != "paused" {
		t.Fatalf("lost published version or incorrectly billed attempt: %+v %v", s, err)
	}
	// Observe what the reader actually loads from SQL, including discarded private rows.
	privateRows := 0
	if err := svc.Repo.DB.Callback().Query().After("gorm:query").Register("test:revision_payload", func(tx *gorm.DB) {
		steps, ok := tx.Statement.Dest.(*[]orm.PaperGuideStep)
		if !ok {
			return
		}
		for _, step := range *steps {
			if strings.HasPrefix(step.StepKey, "fast:") || strings.HasPrefix(step.StepKey, "request:") {
				privateRows++
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	after, err := svc.Revision(ctx, scope, first.ID, 0)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("published article changed after failed replacement: %v", err)
	}
	if privateRows != 0 {
		t.Fatalf("reading published article loaded %d private model/checkpoint rows", privateRows)
	}
}

func TestStatusReportsInsufficientMaterialBeforeGeneration(t *testing.T) {
	svc, scope, rev, _ := fastFixture(t)
	source := svc.Source.(*fixtureSource)
	source.s.Coverage = GuideCoverage{AbstractOnly: true}
	ctx := t.Context()
	_, _, err := svc.Generate(ctx, scope, GenerateRequest{Regenerate: true, ExpectedSourceHash: rev.SourceSHA256})
	if !errors.Is(err, ErrMaterial) {
		t.Fatalf("unexpected generation outcome: %v", err)
	}
	s, err := svc.Status(ctx, scope)
	if err != nil || !s.CanRead || s.CanGenerate || s.UnavailableReason != ErrMaterial.Error() {
		t.Fatalf("reading status advertises an impossible generation: canRead=%v canGenerate=%v reason=%q err=%v", s.CanRead, s.CanGenerate, s.UnavailableReason, err)
	}
}

type publishingSource struct {
	SourceLoader
	publish func() error
}

func (s publishingSource) Load(ctx context.Context, scope Scope, prepare bool) (SourceState, error) {
	if err := s.publish(); err != nil {
		return SourceState{}, err
	}
	return s.SourceLoader.Load(ctx, scope, prepare)
}

func TestRevisionDeltaStaysWithinResponseVersion(t *testing.T) {
	svc, scope, rev, _ := fastFixture(t)
	ctx := t.Context()
	fence := testFence{svc.Repo.DB}
	plan := GuidePlan{Layout: "flashpapers", Sections: []GuideSectionPlan{{Role: "overview", Title: "Summary"}}}
	if err := svc.Repo.PutStep(ctx, fence, rev.ID, rev.Epoch, StepWrite{Key: "plan", InputHash: "plan", Output: mustMarshal(plan)}); err != nil {
		t.Fatal(err)
	}
	before, err := svc.Revision(ctx, scope, rev.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A worker commits while this HTTP read checks the source, after the
	// revision's version was captured and before its sections are fetched.
	reader := svc
	reader.Source = publishingSource{svc.Source, func() error {
		section := publicChapter(fastFixtureChapter("overview"), nil)
		return svc.Repo.PutStep(ctx, fence, rev.ID, rev.Epoch, StepWrite{
			Key: sectionKey(section.Role, 1), InputHash: "chapter", Output: mustMarshal(section), Published: true,
		})
	}}
	overlap, err := reader.Revision(ctx, scope, rev.ID, before.Version)
	if err != nil || overlap.Version != before.Version || len(overlap.Sections) != 0 {
		t.Fatalf("cursor %d, response version %d with %d unexpected sections, err %v", before.Version, overlap.Version, len(overlap.Sections), err)
	}
	next, err := svc.Revision(ctx, scope, rev.ID, overlap.Version)
	if err != nil || len(next.Sections) != 1 || next.Sections[0].ChangeVersion != next.Version || next.Sections[0].PublicationStatus != "published" {
		t.Fatalf("new chapter missing on next read: %+v, %v", next.Sections, err)
	}
	last, err := svc.Revision(ctx, scope, rev.ID, next.Version)
	if err != nil || len(last.Sections) != 0 {
		t.Fatalf("chapter delivered again: %+v, %v", last.Sections, err)
	}
}
