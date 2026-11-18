package paperguide

import (
	"context"
	"encoding/json"
	"errors"
 "regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

func fastFixturePlan() ExplainerPlan {
	p := ExplainerPlan{Title: "How the paper works", Subtitle: "An interactive explanation"}
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
	d, id := fixture(t)
	snap, err := BuildSourceSnapshot(d, id)
	if err != nil {
		t.Fatal(err)
	}
	repo := Repository{DB: guideDB(t)}
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
	var once sync.Once
	svc.Call = func(ctx context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
		calls.Add(1)
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
		if id == "overview" {
			time.Sleep(40 * time.Millisecond)
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
	for i := 0; i < 100; i++ {
		x.snap.Evidence = append(x.snap.Evidence, GuideEvidence{ID: stringInt(int64(i)), Text: strings.Repeat("mechanism ", 50), Locator: GuideLocator{Page: i}})
	}
	input, aliases, err := x.explainerInput()
	if err != nil {
		t.Fatal(err)
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
