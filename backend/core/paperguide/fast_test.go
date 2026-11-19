package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
)

func fastFixturePlan() ExplainerPlan {
	p := ExplainerPlan{
		Title: "How the paper works", Subtitle: "An interactive explanation",
		PaperFlow: &GuideFlow{Field: "Adaptation", Problems: []string{"Memory cost"}, Method: "LoRA", Modules: []string{"Frozen weights", "Low-rank update"}, Datasets: []string{}},
	}
	for _, id := range []string{"overview", "why-it-matters", "big-idea", "how-it-works", "rank-playground", "results", "limitations", "glossary"} {
		p.Chapters = append(p.Chapters, ExplainerChapterPlan{ID: id, Title: id, Purpose: "Explain", Outline: []string{"Idea"}, SourceIDs: []string{"s0"}})
	}
	return p
}
func fastFixtureChapter(id string) ExplainerChapter {
	return ExplainerChapter{ID: id, Title: id, Blocks: []ExplainerBlock{{Kind: "paragraph", Text: "A clear explanation.", EvidenceIDs: []string{"s0"}}}}
}
func fastFixture(t *testing.T) (Service, Scope, orm.PaperGuideRevision, asyncjob.Job) {
	t.Helper()
	return fastFixtureWithDB(t, guideDB(t))
}

func fastFixtureWithDB(t *testing.T, db *gorm.DB) (Service, Scope, orm.PaperGuideRevision, asyncjob.Job) {
	t.Helper()
	d, id := fixture(t)
	snap, err := BuildSourceSnapshot(d, id)
	if err != nil {
		t.Fatal(err)
	}
	repo := Repository{DB: db}
	scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "en"}
	model := ModelIdentity{ID: "fast", ContextTokens: 128000}
	svc := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }}
	rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test-snapshot", Model: model})
	if err != nil {
		t.Fatal(err)
	}
	return svc, scope, rev, asyncjob.Job{ID: rev.JobID, PayloadJSON: mustJSON(JobPayload{rev.ID, rev.Epoch})}
}
func TestFastRunnerParallelOrderedPublicationAndNoReview(t *testing.T) {
	svc, scope, rev, job := fastFixture(t)
	var active, peak, calls atomic.Int32
	firstWave := make(chan struct{})
	nextWave := make(chan struct{})
	var once sync.Once
	svc.Call = func(ctx context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
		attempt := calls.Add(1)
		if task == "paper.explainer.plan" {
			plan := fastFixturePlan()
			plan.ReaderProfile = "Basic vectors; unfamiliar with attention"
			plan.SharedExamples = []ExplainerExample{{ID: "tokens", Scenario: "Three tokens", Inputs: []ExplainerExampleInput{{Name: "d", Value: "2", Unit: "dimensions"}}, Relationships: []string{"score=dot(q,k)/sqrt(d)"}}}
			plan.Chapters[3].KnowledgeGaps = []string{"What is normalization?"}
			plan.Chapters[3].ReasoningQuestions = []string{"Why scale the scores?"}
			plan.Chapters[3].ExampleIDs = []string{"tokens"}
			return json.Marshal(plan)
		}
		if task != "paper.explainer.chapter" {
			return nil, errors.New("unexpected review/extraction call")
		}
		plan, ok := input["plan"].(ExplainerPlan)
		if !ok || plan.ReaderProfile == "" || len(plan.SharedExamples) != 1 || plan.SharedExamples[0].Inputs[0].Value != "2" || len(plan.Chapters[3].ReasoningQuestions) != 1 || plan.Chapters[3].ExampleIDs[0] != "tokens" {
			return nil, errors.New("teaching contract lost before chapter generation")
		}
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		if n == 4 {
			once.Do(func() { close(firstWave) })
		}
		select {
		case <-firstWave:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return nil, errors.New("chapter fanout did not overlap")
		}
		id := input["chapter_id"].(string)
		// A worker starts its second chapter only after publishing its first.
		if attempt == 6 {
			close(nextWave)
		}
		if id == "overview" {
			select {
			case <-nextWave:
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
				return nil, errors.New("no chapter published while overview was in flight")
			}
			partial, err := svc.Revision(ctx, scope, rev.ID, 0)
			if err != nil {
				return nil, err
			}
			if len(partial.Sections) != 8 || partial.Sections[0].Role != "overview" || len(partial.Sections[0].Blocks) != 0 {
				return nil, errors.New("in-flight overview was not first in the directory")
			}
			readable := false
			for _, section := range partial.Sections[1:] {
				readable = readable || len(section.Blocks) > 0
			}
			if !readable {
				return nil, errors.New("completed peer missing from incremental publication")
			}
		}
		return json.Marshal(fastFixtureChapter(id))
	}
	if _, err := svc.Run(t.Context(), job, testFence{svc.Repo.DB}); err != nil {
		t.Fatal(err)
	}
	if peak.Load() != 4 || calls.Load() != 9 {
		t.Fatalf("peak=%d calls=%d", peak.Load(), calls.Load())
	}
	out, err := svc.Revision(t.Context(), scope, rev.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Plan.PaperFlow, fastFixturePlan().PaperFlow) {
		t.Fatalf("published flow differs from plan: %+v", out.Plan.PaperFlow)
	}
	if out.Plan.Layout != "flashpapers" || len(out.Sections) != 8 {
		t.Fatalf("bad view: %+v", out)
	}
	for i, c := range fastFixturePlan().Chapters {
		if string(out.Sections[i].Role) != c.ID {
			t.Fatal("completion order escaped into publication")
		}
		if len(out.Sections[i].Blocks[0].EvidenceIDs) != 1 || !regexp.MustCompile(`^ev_[a-f0-9]{64}$`).MatchString(out.Sections[i].Blocks[0].EvidenceIDs[0]) {
			t.Fatal("source alias not canonicalized")
		}
	}
	got, err := svc.Repo.Revision(t.Context(), scope, rev.ID)
	if err != nil || got.Status != "complete" || got.CallsUsed != 9 {
		t.Fatalf("result %+v %v", got, err)
	}
	if _, err = svc.Run(t.Context(), job, testFence{svc.Repo.DB}); err != nil || calls.Load() != 9 {
		t.Fatal("completed run repeated calls", err)
	}
}
func TestFastRunnerRetainsPeersAndResumesOnlyFailedChapter(t *testing.T) {
	svc, scope, rev, job := fastFixture(t)
	var failed atomic.Bool
	var calls atomic.Int32
	svc.Call = func(ctx context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
		calls.Add(1)
		if task == "paper.explainer.plan" {
			return json.Marshal(fastFixturePlan())
		}
		id := input["chapter_id"].(string)
		if id == "big-idea" && failed.CompareAndSwap(false, true) {
			return nil, errors.New("provider unavailable")
		}
		return json.Marshal(fastFixtureChapter(id))
	}
	if _, err := svc.Run(t.Context(), job, testFence{svc.Repo.DB}); err == nil {
		t.Fatal("expected paused run")
	}
	out, err := svc.Revision(t.Context(), scope, rev.ID, 0)
	published := 0
	for _, section := range out.Sections {
		if len(section.Blocks) > 0 {
			published++
		}
	}
	if err != nil || len(out.Sections) != 8 || published != 7 {
		t.Fatalf("partial=%d %v", len(out.Sections), err)
	}
	model, _, _ := svc.ResolveModel(t.Context(), scope, "")
	resumed, err := svc.Repo.Resume(t.Context(), scope, rev.ID, model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Run(t.Context(), asyncjob.Job{ID: resumed.JobID, PayloadJSON: mustJSON(JobPayload{resumed.ID, resumed.Epoch})}, testFence{svc.Repo.DB}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 10 {
		t.Fatalf("repeated completed work: %d calls", calls.Load())
	}
}
func TestFastRunnerCancellationPreventsPublication(t *testing.T) {
	svc, scope, rev, job := fastFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var once sync.Once
	svc.Call = func(ctx context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
		if task == "paper.explainer.plan" {
			return json.Marshal(fastFixturePlan())
		}
		once.Do(cancel)
		<-ctx.Done()
		return json.Marshal(fastFixtureChapter(input["chapter_id"].(string)))
	}
	if _, err := svc.Run(ctx, job, testFence{svc.Repo.DB}); err == nil {
		t.Fatal("canceled request succeeded")
	}
	out, err := svc.Revision(t.Context(), scope, rev.ID, 0)
	if err != nil {
		t.Fatalf("published canceled work: %+v %v", out, err)
	}
	for _, section := range out.Sections {
		if len(section.Blocks) > 0 {
			t.Fatal("published canceled chapter")
		}
	}
}
func TestFastLightChecksAndSourceSampling(t *testing.T) {
	p := fastFixturePlan()
	if validateExplainerPlan(p) != nil {
		t.Fatal("valid plan")
	}
	p.Chapters[1].ID = "overview"
	if validateExplainerPlan(p) == nil {
		t.Fatal("duplicate accepted")
	}
	c := fastFixtureChapter("overview")
	if validateExplainerChapter(c, "results") == nil {
		t.Fatal("identity ignored")
	}
	c.Blocks[0].Kind = "playground"
	if validateExplainerChapter(c, "overview") == nil {
		t.Fatal("empty playground")
	}
	c.Blocks[0].HTML = "<!doctype html><script>let x=1</script>"
	if validateExplainerChapter(c, "overview") != nil {
		t.Fatal("playground rejected")
	}
	s := publicChapter(c, map[string]string{"s0": "canonical"})
	if s.Blocks[0].Playground.HTML != c.Blocks[0].HTML {
		t.Fatal("lost module")
	}
}

func TestFastSourcePackingPreservesFullParagraphsAndWholePaperSpread(t *testing.T) {
	x := execution{model: ModelIdentity{ContextTokens: 128000}, scope: Scope{Language: "en"}}
	x.snap.Pages = []pdfir.Page{{Index: 0}, {Index: 99}}
	for i := 0; i < 100; i++ {
		x.snap.Evidence = append(x.snap.Evidence, GuideEvidence{ID: stringInt(int64(i)), Text: strings.Repeat("mechanism ", 50), Locator: GuideLocator{Page: i}})
	}
	input, aliases, err := x.explainerInput()
	if err != nil {
		t.Fatal(err)
	}
	if input["page_count"] != 100 {
		t.Fatalf("physical page count = %v", input["page_count"])
	}
	if len(aliases) != 100 || strings.Count(input["source_text"].(string), strings.Repeat("mechanism ", 50)) != 100 {
		t.Fatal("fitting paper was truncated")
	}
	for i := range x.snap.Evidence {
		x.snap.Evidence[i].Text = strings.Repeat("long paragraph mechanism ", 100)
	}
	input, aliases, err = x.explainerInput()
	if err != nil {
		t.Fatal(err)
	}
	if len(input["source_text"].(string)) > 71600 || aliases["s0"] != "0" || aliases["s99"] != "99" || len(aliases) < 15 {
		t.Fatalf("unbounded or narrow sample: %d", len(aliases))
	}
	if !strings.Contains(input["source_text"].(string), strings.Repeat("long paragraph mechanism ", 100)) {
		t.Fatal("whole paragraphs lost")
	}
	again, _, _ := x.explainerInput()
	if input["source_text"] != again["source_text"] {
		t.Fatal("non-deterministic sampling")
	}
}

func TestFastLargeContextKeepsMainResultTable(t *testing.T) {
	x := execution{model: ModelIdentity{ContextTokens: 1050000}, scope: Scope{Language: "en"}}
	for i := 0; i < 100; i++ {
		x.snap.Evidence = append(x.snap.Evidence, GuideEvidence{ID: stringInt(int64(i)), Text: strings.Repeat("source paragraph ", 55), Locator: GuideLocator{Page: i / 4}})
	}
	x.snap.Evidence[33].Text = "Main results | Full fine-tuning | Low-rank adaptation | 73.8 | 74.0"
	input, aliases, err := x.explainerInput()
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 100 || !strings.Contains(input["source_text"].(string), x.snap.Evidence[33].Text) {
		t.Fatal("large-context reader dropped part of a fitting paper, including primary results")
	}
}

func TestFastRunnerReportsQueuedAndGeneratingBeforePublication(t *testing.T) {
	svc, scope, rev, job := fastFixture(t)
	svc.ChapterConcurrency = 1
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	svc.Call = func(ctx context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
		if task == "paper.explainer.plan" {
			return json.Marshal(fastFixturePlan())
		}
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return json.Marshal(fastFixtureChapter(input["chapter_id"].(string)))
	}
	finished := make(chan error, 1)
	go func() { _, err := svc.Run(t.Context(), job, testFence{svc.Repo.DB}); finished <- err }()
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never started")
	}
	out, err := svc.Revision(t.Context(), scope, rev.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if out.Plan == nil || len(out.Sections) != 8 {
		t.Fatal("outline missing before first chapter", out)
	}
	for i, section := range out.Sections {
		want := "queued"
		if i == 0 {
			want = "generating"
		}
		if section.PublicationStatus != want || len(section.Blocks) != 0 {
			t.Fatalf("chapter %d: %+v", i, section)
		}
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	next, err := svc.Revision(t.Context(), scope, rev.ID, out.Version)
	if err != nil || len(next.Sections) != 8 {
		t.Fatal("missing incremental publication", err)
	}
	for i, section := range next.Sections {
		if len(section.Blocks) == 0 || section.Key != out.Sections[i].Key {
			t.Fatal("placeholder not replaced in place")
		}
	}
}

func TestExplainerFigureCatalogAndPublication(t *testing.T) {
	x := execution{model: ModelIdentity{ContextTokens: 128000}, snap: GuideSourceSnapshot{
		Evidence: []GuideEvidence{{ID: "ev-figure", Text: "Figure 1: low-rank adaptation", Locator: GuideLocator{Page: 0}}},
		Assets:   []GuideAsset{{ID: "ev-figure", Kind: "figure", Locator: GuideLocator{Page: 0}, OriginalDisplay: &GuideOriginalDisplay{Kind: "figure", Page: 0, Caption: "Figure 1: low-rank adaptation", BBox: []float64{0.1, 0.2, 0.5, 0.6}}}},
	}}
	input, aliases, err := x.explainerInput()
	if err != nil {
		t.Fatal(err)
	}
	figures := input["figures"].([]ExplainerFigure)
	if len(figures) != 1 || figures[0].Caption != "Figure 1: low-rank adaptation" || figures[0].Page != 1 {
		t.Fatalf("catalog: %+v", figures)
	}
	chapter := ExplainerChapter{ID: "big-idea", Title: "Idea", Blocks: []ExplainerBlock{{Kind: "figure", Text: "The frozen and trainable paths.", FigureID: figures[0].ID}}}
	if err := validateExplainerFigures(ExplainerPlan{}, &chapter, figures); err != nil {
		t.Fatal(err)
	}
	s := publicChapter(chapter, aliases)
	if s.Blocks[0].AssetID != "ev-figure" || s.Blocks[0].Kind != "figure" || s.Blocks[0].Text != chapter.Blocks[0].Text {
		t.Fatalf("lost image: %+v", s)
	}
	chapter.Blocks[0].FigureID = "f999"
	if err := validateExplainerFigures(ExplainerPlan{}, &chapter, figures); err == nil {
		t.Fatal("invented figure accepted")
	}
}

func TestFastDynamicMethodsAndDeepDiveOrder(t *testing.T) {
	p := fastFixturePlan()
	p.Chapters[3].ID = "training-process"
	deep := ExplainerChapterPlan{ID: "further-analysis", Title: "Further analysis"}
	p.Chapters = append(p.Chapters[:6], append([]ExplainerChapterPlan{deep}, p.Chapters[6:]...)...)
	if err := validateExplainerPlan(p); err != nil {
		t.Fatalf("dynamic methods and deep dive rejected: %v", err)
	}
	bad := fastFixturePlan()
	bad.Chapters[2], bad.Chapters[3] = bad.Chapters[3], bad.Chapters[2]
	if validateExplainerPlan(bad) == nil {
		t.Fatal("method before core idea accepted")
	}
}

func TestPublicChapterEmphasisPreservesSourceText(t *testing.T) {
	const text = "Estimate noise at each noise level."
	for _, tc := range []struct{ role, kind, phrase, want string }{
		{"training", "paragraph", "Estimate noise", "Estimate noise"},
		{"training", "paragraph", "noise", ""},
		{"training", "paragraph", "missing", ""},
		{"training", "formula", "Estimate noise", ""},
		{"glossary", "paragraph", "Estimate noise", ""},
	} {
		c := ExplainerChapter{ID: tc.role, Blocks: []ExplainerBlock{{Kind: tc.kind, Text: text, Emphasis: tc.phrase}}}
		b := publicChapter(c, nil).Blocks[0]
		if b.Text != text || b.Emphasis != tc.want {
			t.Errorf("%s/%s %q: got text %q, emphasis %q", tc.role, tc.kind, tc.phrase, b.Text, b.Emphasis)
		}
	}
}

func TestFastPackingFitsFigureHeavyEscapedSource(t *testing.T) {
	x := execution{model: ModelIdentity{ContextTokens: 32768}, scope: Scope{Language: "zh"}}
	x.snap.Sections = []SourceSection{{Title: strings.Repeat("题", 1100)}}
	for i := 0; i < 100; i++ {
		id := stringInt(int64(i))
		caption := "Figure " + id + " " + strings.Repeat("A & B ", 18)
		x.snap.Evidence = append(x.snap.Evidence, GuideEvidence{ID: id, Text: strings.Repeat("< & >", 100)})
		x.snap.Assets = append(x.snap.Assets, GuideAsset{ID: id, Kind: "figure", OriginalDisplay: &GuideOriginalDisplay{Caption: caption}})
	}
	input, aliases, err := x.explainerInput()
	if err != nil {
		t.Fatal(err)
	}
	title := input["paper_title"].(string)
	figures := input["figures"].([]ExplainerFigure)
	if !x.inputFits(input) || len([]rune(title)) > 1000 || len(figures) == 0 || len(figures) >= len(x.snap.Assets) || input["source_text"] == "" {
		t.Fatalf("unusable plan input, fits=%t title runes=%d figures=%d", x.inputFits(input), len([]rune(title)), len(figures))
	}
	for _, f := range figures {
		if f.Caption != "Figure "+aliases[f.ID]+" "+strings.Repeat("A & B ", 17)+"A & B" {
			t.Fatalf("damaged caption or alias: %+v", f)
		}
	}
	input["plan"] = fastFixturePlan()
	input["chapter_id"] = "overview"
	if !x.inputFits(input) {
		t.Fatal("no space left for the chapter plan")
	}
}

func TestFastPackingRetainsResumableRequest(t *testing.T) {
	x := execution{model: ModelIdentity{ContextTokens: 32768}}
	text := strings.Repeat("a", 10000)
	x.snap.Evidence = []GuideEvidence{{ID: "ev0", Text: text}}
	for i := 0; i < 40; i++ {
		x.snap.Assets = append(x.snap.Assets, GuideAsset{ID: stringInt(int64(i)), Kind: "figure",
			OriginalDisplay: &GuideOriginalDisplay{Caption: strings.Repeat("x", 100)}})
	}
	input, _, err := x.explainerInput()
	if err != nil {
		t.Fatal(err)
	}
	// The historical sampler truncates a 2,000-byte block before its ellipsis.
	want := ("[s0 page 1] " + text)[:1995] + " …\n"
	if input["source_text"] != want || len(input["figures"].([]ExplainerFigure)) != 40 {
		t.Fatal("a request that previously fit changed during resume")
	}
}

func TestReturnedChapterCannotPublishAfterCancelOrReplacement(t *testing.T) {
	checkReturnedChapterFence(t, guideDB)
}

func checkReturnedChapterFence(t *testing.T, database func(*testing.T) *gorm.DB) {
	t.Helper()
	for _, action := range []string{"cancel", "replace"} {
		t.Run(action, func(t *testing.T) {
			svc, scope, rev, job := fastFixtureWithDB(t, database(t))
			svc.ChapterConcurrency = 1
			var chapterReturned atomic.Bool
			reached := make(chan struct{})
			release := make(chan struct{})
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			svc.Call = func(_ context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
				if task == "paper.explainer.plan" {
					return json.Marshal(fastFixturePlan())
				}
				chapterReturned.Store(true)
				return json.Marshal(fastFixtureChapter(input["chapter_id"].(string)))
			}
			// The worker has the model response, but has not opened its next write transaction.
			svc.Repo.PrepareCheckpoint = func(ctx context.Context, _ orm.PaperGuide) (context.Context, error) {
				if chapterReturned.CompareAndSwap(true, false) {
					close(reached)
					select {
					case <-release:
					case <-ctx.Done():
						return ctx, ctx.Err()
					}
				}
				return ctx, nil
			}
			done := make(chan error, 1)
			go func() { _, err := svc.Run(ctx, job, testFence{svc.Repo.DB}); done <- err }()
			select {
			case <-reached:
			case err := <-done:
				t.Fatalf("worker exited before chapter checkpoint: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("chapter checkpoint not reached")
			}
			expectedCandidate := rev.ID
			if action == "replace" {
				next, err := svc.Repo.Create(ctx, CreateRevision{Scope: scope, Regenerate: true, SourceSHA256: rev.SourceSHA256, IRDigest: rev.IRDigest, SnapshotPath: rev.SnapshotPath, Model: ModelIdentity{ID: "fast", ContextTokens: 128000}})
				if err != nil {
					t.Fatal(err)
				}
				expectedCandidate = next.ID
			} else if err := svc.Repo.Cancel(ctx, scope, rev.ID); err != nil {
				t.Fatal(err)
			}
			close(release)
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("superseded worker succeeded")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("superseded worker did not exit")
			}
			g, err := svc.Repo.Guide(ctx, scope)
			if err != nil || g.CandidateRevisionID != expectedCandidate || g.PublishedRevisionID != "" {
				t.Fatalf("unexpected guide: %+v %v", g, err)
			}
			old, err := svc.Repo.Revision(ctx, scope, rev.ID)
			if err != nil || old.Stage != "canceled" || old.CallsUsed != 2 {
				t.Fatalf("unexpected canceled run: %+v %v", old, err)
			}
			steps, err := svc.Repo.Steps(ctx, rev.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, step := range steps {
				if step.Published || step.StepKey == "fast:chapter:overview" {
					t.Fatalf("stale result persisted: %s", step.StepKey)
				}
			}
		})
	}
}

func TestResumeDeltaReplacesPreviousEpochGeneratingPlaceholder(t *testing.T) {
	svc, scope, rev, _ := fastFixture(t)
	ctx := t.Context()
	fence := testFence{svc.Repo.DB}
	plan := GuidePlan{Layout: "flashpapers", Sections: []GuideSectionPlan{{Role: "overview", Title: "Summary"}}}
	put := func(key string, data any) {
		t.Helper()
		if err := svc.Repo.PutStep(ctx, fence, rev.ID, rev.Epoch, StepWrite{Key: key, InputHash: digest(data), Output: mustMarshal(data)}); err != nil {
			t.Fatal(err)
		}
	}
	put("plan", plan)
	put("chapter-start:"+stringInt(rev.Epoch)+":overview", "overview")
	if err := svc.Repo.Transition(ctx, fence, rev.ID, rev.Epoch, "paused", ErrModelChanged.Error()); err != nil {
		t.Fatal(err)
	}
	before, err := svc.Revision(ctx, scope, rev.ID, 0)
	if err != nil || len(before.Sections) != 1 || before.Sections[0].PublicationStatus != "generating" {
		t.Fatalf("missing first-attempt placeholder: %+v %v", before, err)
	}
	model, _, err := svc.ResolveModel(ctx, scope, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Repo.Resume(ctx, scope, rev.ID, model); err != nil {
		t.Fatal(err)
	}
	delta, err := svc.Revision(ctx, scope, rev.ID, before.Version)
	if err != nil || delta.Run.Stage != "queued" || len(delta.Sections) != 1 || delta.Sections[0].PublicationStatus != "queued" || delta.Sections[0].ChangeVersion <= before.Version {
		t.Fatalf("client cannot replace previous attempt's generating placeholder: version=%d stage=%s delta=%+v err=%v", delta.Version, delta.Run.Stage, delta.Sections, err)
	}
}

func TestChapterFitsByOmittingUnassignedFigureCaptions(t *testing.T) {
	x := execution{model: ModelIdentity{ContextTokens: 32768}, scope: Scope{Language: "zh"}}
	x.snap.Evidence = []GuideEvidence{{ID: "ev-text", Text: "Paper excerpt with the measured comparison."}}
	for i := 0; i < 40; i++ {
		x.snap.Assets = append(x.snap.Assets, GuideAsset{ID: "figure-" + stringInt(int64(i)), Kind: "figure", OriginalDisplay: &GuideOriginalDisplay{Caption: strings.Repeat("caption ", 24)}})
	}
	input, aliases, err := x.explainerInput()
	if err != nil {
		t.Fatal(err)
	}
	plan := fastFixturePlan()
	for i := range plan.Chapters {
		plan.Chapters[i].Outline = []string{strings.Repeat("设计与比较", 90)}
	}
	chapter := plan.Chapters[3]
	chapter.FigureIDs = []string{"f35"}
	plan.Chapters[3] = chapter
	if len(input["figures"].([]ExplainerFigure)) != 40 || aliases["f35"] != "figure-35" {
		t.Fatal("planning fixture unexpectedly dropped figures")
	}
	got, _ := x.chapterInput(input, plan, chapter, aliases)
	if !x.inputFits(got) {
		t.Fatal("entire summary job would pause even though the assigned figure and full paper text fit")
	}
	fs := got["figures"].([]ExplainerFigure)
	if len(fs) != 1 || fs[0] != input["figures"].([]ExplainerFigure)[35] || got["source_text"] != input["source_text"] || !reflect.DeepEqual(got["plan"], plan) {
		t.Fatal("lost required caption, original alias, source text or shared plan")
	}
	if len(input["figures"].([]ExplainerFigure)) != 40 {
		t.Fatal("modified the shared plan input of another worker")
	}
}

func TestSampledSourceAliasesMatchOnlyVisibleExcerpts(t *testing.T) {
	marker := regexp.MustCompile(`(?m)^\[s([0-9]+) page ([0-9]+)\] `)
	for _, language := range []string{"zh", "en"} {
		t.Run(language, func(t *testing.T) {
			x := execution{model: ModelIdentity{ContextTokens: 32768}, scope: Scope{Language: language}}
			words := "temperature < 25 & "
			if language == "zh" {
				words = "温度低于二十五度 & "
			}
			for i := 0; i < 180; i++ {
				ev := GuideEvidence{ID: fmt.Sprintf("document-A-block-%d", i), Text: fmt.Sprintf("paragraph-%d ", i) + strings.Repeat(words, 80), Locator: GuideLocator{Page: i / 2}}
				x.snap.Evidence = append(x.snap.Evidence, ev)
			}
			input, aliases, err := x.explainerInput()
			if err != nil {
				t.Fatal(err)
			}
			body := input["source_text"].(string)
			if !x.inputFits(input) || !utf8.ValidString(body) {
				t.Fatal("unusable sampled input")
			}
			matches := marker.FindAllStringSubmatchIndex(body, -1)
			if len(matches) == 0 || len(matches) >= len(x.snap.Evidence) || len(matches) != len(aliases) {
				t.Fatalf("visible=%d aliases=%d source=%d", len(matches), len(aliases), len(x.snap.Evidence))
			}
			for j, m := range matches {
				number := body[m[2]:m[3]]
				index, err := strconv.Atoi(number)
				if err != nil || index >= len(x.snap.Evidence) {
					t.Fatalf("bad source marker %q", number)
				}
				ev := x.snap.Evidence[index]
				stop := len(body)
				if j+1 < len(matches) {
					stop = matches[j+1][0]
				}
				excerpt := strings.TrimSuffix(strings.TrimSuffix(body[m[1]:stop], "\n"), " …")
				if aliases["s"+number] != ev.ID || body[m[4]:m[5]] != strconv.Itoa(ev.Locator.Page+1) || excerpt == "" || !strings.HasPrefix(ev.Text, excerpt) {
					t.Fatalf("wrong evidence or page for s%s", number)
				}
			}
		})
	}
}

func TestChapterBudgetKeepsAssignedExcerptsAndFitsTheSharedPlan(t *testing.T) {
	x := execution{model: ModelIdentity{ContextTokens: 65536}}
	for i := 0; i < 100; i++ {
		x.snap.Evidence = append(x.snap.Evidence, GuideEvidence{ID: fmt.Sprintf("block-%d", i), Text: fmt.Sprintf("Paragraph %d ", i) + strings.Repeat("内容 & A ", 36), Locator: GuideLocator{Page: i}})
	}
	input, aliases, err := x.explainerInput()
	if err != nil {
		t.Fatal(err)
	}
	plan := fastFixturePlan()
	for i := range plan.Chapters {
		plan.Chapters[i].Purpose = strings.Repeat("共享规划", 165)
	}
	chapter := plan.Chapters[0]
	chapter.SourceIDs = []string{"s0", "s99"}
	plan.Chapters[0] = chapter
	before := input["source_text"].(string)
	if aliases["s99"] != "block-99" || !strings.Contains(before, x.snap.Evidence[99].Text) {
		t.Fatal("expected complete last source paragraph in plan")
	}
	payload, visible := x.chapterInput(input, plan, chapter, aliases)
	if !x.inputFits(payload) {
		t.Fatal("chapter still exceeds budget despite ample room for its assigned evidence")
	}
	body := payload["source_text"].(string)
	if before == body || input["source_text"] != before || !reflect.DeepEqual(plan, payload["plan"]) {
		t.Fatal("failed to isolate the chapter-specific reduction")
	}
	for _, i := range []int{0, 99} {
		if !strings.Contains(body, fmt.Sprintf("[s%d page %d] %s\n", i, i+1, x.snap.Evidence[i].Text)) {
			t.Fatalf("lost or modified required source s%d", i)
		}
	}
	for alias, id := range aliases {
		if strings.Contains(body, "["+alias+" page ") != (visible[alias] == id) {
			t.Fatalf("invalid visibility for %s", alias)
		}
	}
	if strings.Count(body, " page ") <= 2 {
		t.Fatal("spare budget was not used for additional context")
	}
	// Exercise the durable checkpoint path with a fresh worker and a
	// round-tripped source, as on a process restart.
	resumed := execution{model: x.model, scope: x.scope}
	if err := json.Unmarshal(mustMarshal(x.snap), &resumed.snap); err != nil {
		t.Fatal(err)
	}
	initial, refs, err := resumed.explainerInput()
	if err != nil {
		t.Fatal(err)
	}
	replayed, replayedRefs := resumed.chapterInput(initial, plan, chapter, refs)
	const key = "fast:chapter:overview"
	resumed.steps = map[string]orm.PaperGuideStep{key: {InputHash: digest([]any{TaskVersion, payload}), OutputJSON: string(mustMarshal(fastFixtureChapter(chapter.ID)))}}
	var cached ExplainerChapter
	err = resumed.task(t.Context(), key, "paper.explainer.chapter", replayed, &cached, func() error { return validateExplainerChapter(cached, chapter.ID) })
	if err != nil || !reflect.DeepEqual(visible, replayedRefs) || cached.ID != chapter.ID {
		t.Fatal("checkpoint could not be reused", err)
	}
	// If the plan explicitly needs every excerpt, do not silently sacrifice
	// required material or spend a model attempt on the oversized request.
	chapter.SourceIDs = nil
	for i := range x.snap.Evidence {
		alias := fmt.Sprintf("s%d", i)
		if _, ok := aliases[alias]; ok {
			chapter.SourceIDs = append(chapter.SourceIDs, alias)
		}
	}
	plan.Chapters[0] = chapter
	oversized, _ := x.chapterInput(input, plan, chapter, aliases)
	if oversized["source_text"] != before || !errors.Is(x.task(t.Context(), "chapter", "unused", oversized, nil, nil), ErrContext) {
		t.Fatal("unfit required evidence was lost or billed")
	}
}
