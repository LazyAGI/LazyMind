package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureSource struct {
	s   GuideSourceSnapshot
	err error
}

func (f *fixtureSource) Load(context.Context, Scope, bool) (SourceState, error) {
	return SourceState{Hash: f.s.Identity.SourceSHA256, Snapshot: &f.s, Path: "test-snapshot"}, f.err
}
func (f *fixtureSource) Read(context.Context, string) (GuideSourceSnapshot, error) { return f.s, nil }
func baselinePlan(s GuideSourceSnapshot) GuidePlan {
	a := ArgumentExperiment
	p := GuidePlan{Profiles: GuideProfiles{Primary: &a, Secondary: []ArgumentType{}}, Questions: []string{"What happened?"}, Claims: []GuideClaim{}, TermPolicy: []string{}, RequiredAssets: []GuideAssetDecision{}}
	for _, role := range Roles() {
		p.Sections = append(p.Sections, GuideSectionPlan{Role: role, Title: string(role), Focus: "Explain the measured sample.", Terms: []string{}, ClaimIDs: []string{}, EvidenceIDs: []string{s.Evidence[0].ID}, AssetIDs: []string{}})
	}
	return p
}
func TestRunnerPublishesReviewedSectionsAndRestartsWithoutModelCalls(t *testing.T) {
	d, id := fixture(t)
	d.Warnings = []string{"Figure pixels are unavailable; only extracted text is readable."}
	snap, _ := BuildSourceSnapshot(d, id)
	src := &fixtureSource{s: snap}
	repo := Repository{DB: guideDB(t)}
	scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "zh"}
	model := ModelIdentity{ID: "model", ContextTokens: 128000}
	calls := 0
	service := Service{Repo: repo, Source: src, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }, Call: func(ctx context.Context, task string, input map[string]any, cfg map[string]any) (json.RawMessage, error) {
		calls++
		var out any
		switch task {
		case "paper.guide.plan":
			out = baselinePlan(snap)
		case "paper.guide.section":
			role := input["role"].(Role)
			out = GuideSection{Role: role, Title: string(role), Blocks: []GuideBlock{{ID: "b_" + string(role), Kind: "paragraph", Text: "The measured sample.", Attribution: AttributionTeaching, ClaimIDs: []string{}, EvidenceIDs: []string{}}}, Claims: []GuideClaim{}, ClaimIDs: []string{}, Summary: "Measured sample."}
		case "paper.guide.review":
			out = GuideReview{Findings: []GuideFinding{}}
		default:
			t.Fatalf("unexpected %s", task)
		}
		return json.Marshal(out)
	}}
	rev, e := repo.Create(context.Background(), CreateRevision{Scope: scope, SourceSHA256: snap.Identity.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test-snapshot", Model: model})
	if e != nil {
		t.Fatal(e)
	}
	job := asyncjob.Job{ID: rev.JobID, CreateUserID: id.OwnerID, PayloadJSON: mustJSON(JobPayload{rev.ID, rev.Epoch})}
	if _, e = service.Run(context.Background(), job, testFence{repo.DB}); e != nil {
		t.Fatal(e)
	}
	got, e := repo.Revision(context.Background(), scope, rev.ID)
	if e != nil || got.Stage != "completed" || got.Status != "limited" || got.CallsUsed != 10 || calls != 10 {
		t.Fatalf("result %+v calls %d err %v", got, calls, e)
	}
	sections, e := service.Revision(context.Background(), scope, rev.ID, 0)
	if e != nil || len(sections.Sections) != 4 {
		t.Fatalf("published %+v: %v", sections, e)
	}
	selection, err := service.Selection(context.Background(), scope, rev.ID, SelectionRequest{SectionKey: "section:background:v1", BlockID: "b_background", Start: 0, End: 3})
	if err != nil || !selection.SourceCurrent || selection.Text != "The" {
		t.Fatalf("teaching selection: %+v %v", selection, err)
	}
	if err := service.SaveProgress(context.Background(), scope, ProgressRequest{RevisionID: rev.ID, SectionKey: "section:background:v1", BlockID: "b_background", Offset: 9999, Mode: "article"}); !errors.Is(err, ErrEvidence) {
		t.Fatalf("out of bounds progress: %v", err)
	}
	if _, e = service.Run(context.Background(), job, testFence{repo.DB}); e != nil {
		t.Fatal(e)
	}
	if calls != 10 {
		t.Fatal("completed run called model")
	}
}
func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func TestRunnerDoesNotPublishAfterSourceOrPermissionChanges(t *testing.T) {
	for _, failure := range []error{ErrForbidden, ErrSourceChanged} {
		t.Run(failure.Error(), func(t *testing.T) {
			d, id := fixture(t)
			snap, _ := BuildSourceSnapshot(d, id)
			src := &fixtureSource{s: snap}
			repo := Repository{DB: guideDB(t)}
			scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "en"}
			model := ModelIdentity{ID: "m"}
			rev, e := repo.Create(context.Background(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test-snapshot", Model: model})
			if e != nil {
				t.Fatal(e)
			}
			service := Service{Repo: repo, Source: src, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }, Call: func(context.Context, string, map[string]any, map[string]any) (json.RawMessage, error) {
				src.err = failure
				return json.Marshal(baselinePlan(snap))
			}}
			_, e = service.Run(context.Background(), asyncjob.Job{ID: rev.JobID, PayloadJSON: mustJSON(JobPayload{rev.ID, rev.Epoch})}, testFence{repo.DB})
			if !errors.Is(e, failure) {
				t.Fatal(e)
			}
			var n int64
			repo.DB.Model(&orm.PaperGuideStep{}).Where("revision_id = ? AND published = ?", rev.ID, true).Count(&n)
			if n != 0 {
				t.Fatal("published after revocation")
			}
		})
	}
}

func teachingSection(role Role) GuideSection {
	return GuideSection{Role: role, Title: string(role), Blocks: []GuideBlock{{ID: "b_" + string(role), Kind: "paragraph", Text: "The measured sample.", Attribution: AttributionTeaching, ClaimIDs: []string{}, EvidenceIDs: []string{}}}, Claims: []GuideClaim{}, ClaimIDs: []string{}, Summary: "Measured sample."}
}
func TestRunnerLateWorkerCannotPublishOverRegeneration(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) { runLateWorkerCannotPublish(t, guideDB(t)) })
	t.Run("postgres", func(t *testing.T) { runLateWorkerCannotPublish(t, guidePostgres(t)) })
}
func runLateWorkerCannotPublish(t *testing.T, db *gorm.DB) {
	d, id := fixture(t)
	snap, _ := BuildSourceSnapshot(d, id)
	source := &fixtureSource{s: snap}
	repo := Repository{DB: db}
	scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "en"}
	model := ModelIdentity{ID: "m", ContextTokens: 128000}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	service := Service{Repo: repo, Source: source, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }, Call: func(ctx context.Context, task string, input map[string]any, cfg map[string]any) (json.RawMessage, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		switch task {
		case "paper.guide.plan":
			return json.Marshal(baselinePlan(snap))
		case "paper.guide.section":
			return json.Marshal(teachingSection(input["role"].(Role)))
		default:
			return json.Marshal(GuideReview{Findings: []GuideFinding{}})
		}
	}}
	first, e := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test", Model: model})
	if e != nil {
		t.Fatal(e)
	}
	asyncjob.Register(JobType, service.Run)
	ctx1, cancel1 := context.WithCancel(context.Background())
	worker1 := asyncjob.Start(ctx1, repo.DB, asyncjob.Options{JobTypes: []string{JobType}, WorkerID: "old", Concurrency: 1, PollInterval: time.Millisecond, LockTTL: time.Minute, SerializeResources: true})
	<-started
	if e = repo.Cancel(t.Context(), scope, first.ID); e != nil {
		t.Fatal(e)
	}
	second, e := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test", Model: model, Regenerate: true})
	if e != nil {
		t.Fatal(e)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	worker2 := asyncjob.Start(ctx2, repo.DB, asyncjob.Options{JobTypes: []string{JobType}, WorkerID: "new", Concurrency: 1, PollInterval: time.Millisecond, LockTTL: time.Minute, SerializeResources: true})
	deadline := time.Now().Add(5 * time.Second)
	complete := false
	for time.Now().Before(deadline) {
		row, e := repo.Revision(t.Context(), scope, second.ID)
		if e == nil && row.Stage == "completed" {
			complete = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	cancel1()
	cancel2()
	<-worker1.Done()
	<-worker2.Done()
	if !complete {
		t.Fatal("new worker failed to finish")
	}
	var n int64
	repo.DB.Model(&orm.PaperGuideStep{}).Where("revision_id = ? AND published = ?", first.ID, true).Count(&n)
	if n != 0 {
		t.Fatal("late response published")
	}
	g, e := repo.Guide(t.Context(), scope)
	if e != nil || g.PublishedRevisionID != second.ID {
		t.Fatal("new revision overwritten")
	}
}

func TestLongPlanExtractsCoverAllOriginalEvidenceAndRemainBounded(t *testing.T) {
	for _, window := range []int{40000, 48000} {
		t.Run(fmt.Sprint(window), func(t *testing.T) {
			d, id := fixture(t)
			d.Blocks = []pdfir.Block{{ID: "title", Type: "title", Text: "Study", Page: 0}}
			for i := 0; i < 30; i++ {
				d.Blocks = append(d.Blocks, pdfir.Block{ID: fmt.Sprintf("text-%d", i), Type: "text", Text: strings.Repeat("Material and conditions. ", 80), Page: 0})
			}
			snap, e := BuildSourceSnapshot(d, id)
			if e != nil {
				t.Fatal(e)
			}
			repo := Repository{DB: guideDB(t)}
			scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "en"}
			model := ModelIdentity{ID: "m", ContextTokens: window, OutputReserveTokens: window * 2 / 5}
			seen := map[string]bool{}
			extractCalls := 0
			service := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }, Call: func(ctx context.Context, task string, input map[string]any, cfg map[string]any) (json.RawMessage, error) {
				if task != "paper.guide.extract" {
					t.Fatalf("unexpected task %s", task)
				}
				encoded, err := json.MarshalIndent(input, "", "")
				if err != nil {
					t.Fatal(err)
				}
				evidence := input["evidence"].([]GuideEvidence)
				if len(encoded)+guideInputDefaultsReserve+len(evidence)*guideEvidenceExpansionReserve+guidePromptReserve > model.ContextTokens-model.OutputReserveTokens {
					t.Fatal("extract dispatched without full prompt reserve")
				}
				extractCalls++
				chosen := evidence[0]
				for _, ev := range evidence {
					seen[ev.ID] = true
					if len(ev.Text) < len(chosen.Text) {
						chosen = ev
					}
				}
				return json.Marshal(GuideExtract{Claims: []GuideClaim{{ID: fmt.Sprintf("claim%d", extractCalls), Statement: "The source describes sample conditions.", Attribution: AttributionPaper, EvidenceIDs: []string{chosen.ID}, Conditions: []string{}, SupportStatus: SupportSupported}}, OpenQuestions: []string{}})
			}}
			rev, e := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test", Model: model})
			if e != nil {
				t.Fatal(e)
			}
			x := execution{s: service, scope: scope, rev: rev, model: model, snap: snap, f: testFence{repo.DB}, steps: map[string]orm.PaperGuideStep{}}
			input, _, e := x.planInput(t.Context())
			if window == 40000 {
				if !errors.Is(e, ErrContext) || extractCalls < 2 || extractCalls > 8 || len(seen) != len(snap.Evidence) {
					t.Fatalf("bounded plan rejection: calls=%d seen=%d err=%v", extractCalls, len(seen), e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if extractCalls < 2 || extractCalls > 8 || len(seen) != len(snap.Evidence) || !x.inputFits(input) {
				t.Fatalf("coverage=%d/%d calls=%d", len(seen), len(snap.Evidence), extractCalls)
			}
			model.ContextTokens = 100
			x.model = model
			before := extractCalls
			if _, _, e = x.planInput(t.Context()); !errors.Is(e, ErrContext) || extractCalls != before {
				t.Fatal("oversize source dispatched a partial plan")
			}
		})
	}
}

func TestWholeReviewCorrectionIsImmutableAndResumable(t *testing.T) {
	d, id := fixture(t)
	snap, _ := BuildSourceSnapshot(d, id)
	repo := Repository{DB: guideDB(t)}
	scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "zh"}
	model := ModelIdentity{ID: "m", ContextTokens: 128000}
	failFinal := true
	calls := 0
	service := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }, Call: func(ctx context.Context, task string, input map[string]any, cfg map[string]any) (json.RawMessage, error) {
		calls++
		switch task {
		case "paper.guide.plan":
			return json.Marshal(baselinePlan(snap))
		case "paper.guide.section":
			section := teachingSection(input["role"].(Role))
			if input["findings"] != nil {
				section.Blocks[0].Text = "The corrected sample."
			}
			return json.Marshal(section)
		case "paper.guide.review":
			sections := input["sections"].([]GuideSection)
			if len(sections) == 4 {
				if sections[0].Blocks[0].Text == "The measured sample." {
					return json.Marshal(GuideReview{Findings: []GuideFinding{{TargetID: "b_background", TargetType: "block", IssueType: "coherence", Suggestion: "Clarify the sample."}}})
				}
				if failFinal {
					return nil, errors.New("provider api_key=secret-must-not-persist")
				}
			}
		}
		return json.Marshal(GuideReview{Findings: []GuideFinding{}})
	}}
	rev, e := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test", Model: model})
	if e != nil {
		t.Fatal(e)
	}
	job := asyncjob.Job{ID: rev.JobID, PayloadJSON: mustJSON(JobPayload{rev.ID, rev.Epoch})}
	if _, e = service.Run(t.Context(), job, testFence{repo.DB}); e == nil {
		t.Fatal("final review failure not surfaced")
	}
	before, e := service.Selection(t.Context(), scope, rev.ID, SelectionRequest{"section:background:v1", "b_background", 0, 3})
	if e != nil || before.Text != "The" {
		t.Fatal(e)
	}
	steps, err := repo.Steps(t.Context(), rev.ID)
	if err != nil {
		t.Fatal(err)
	}
	sawTransport := false
	for _, step := range steps {
		if strings.Contains(step.OutputJSON, "secret-must-not-persist") {
			t.Fatal("raw provider error persisted")
		}
		if strings.Contains(step.OutputJSON, `"error_class":"transport"`) {
			sawTransport = true
		}
	}
	if !sawTransport {
		t.Fatal("safe diagnostic classification missing")
	}
	failFinal = false
	resumed, e := repo.Resume(t.Context(), scope, rev.ID, model)
	if e != nil {
		t.Fatal(e)
	}
	job.ID = resumed.JobID
	job.PayloadJSON = mustJSON(JobPayload{rev.ID, resumed.Epoch})
	if _, e = service.Run(t.Context(), job, testFence{repo.DB}); e != nil {
		t.Fatal(e)
	}
	final, e := service.Revision(t.Context(), scope, rev.ID, 0)
	if e != nil || final.Run.Stage != "completed" || final.Sections[0].Version != 2 || final.Sections[0].PublicationStatus != "published" {
		t.Fatalf("not corrected: %+v %v", final, e)
	}
	var old orm.PaperGuideStep
	repo.DB.Where("revision_id = ? AND step_key = ?", rev.ID, "section:background:v1").Take(&old)
	if !strings.Contains(old.OutputJSON, "The measured sample.") {
		t.Fatal("overwrote published v1")
	}
	if calls != 14 {
		t.Fatalf("expected 12 successful calls plus 2 recovery calls, got %d", calls)
	}
}

func TestInterruptedRequestKeepsReservedTimeAndCallsOnResume(t *testing.T) {
	d, id := fixture(t)
	snap, _ := BuildSourceSnapshot(d, id)
	repo := Repository{DB: guideDB(t)}
	scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "en"}
	model := ModelIdentity{ID: "m", ContextTokens: 128000}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }, Call: func(context.Context, string, map[string]any, map[string]any) (json.RawMessage, error) {
		cancel()
		return nil, context.Canceled
	}}
	rev, e := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test", Model: model})
	if e != nil {
		t.Fatal(e)
	}
	_, e = service.Run(ctx, asyncjob.Job{ID: rev.JobID, PayloadJSON: mustJSON(JobPayload{rev.ID, rev.Epoch})}, testFence{repo.DB})
	if e == nil {
		t.Fatal("interrupted request completed")
	}
	repo.DB.Model(&orm.AsyncJob{}).Where("id = ?", rev.JobID).Update("status", "failed")
	resumed, e := repo.Resume(t.Context(), scope, rev.ID, model)
	if e != nil {
		t.Fatal(e)
	}
	if resumed.CallsUsed != 1 || resumed.ActiveMillis != 2*callMillis {
		t.Fatalf("lost uncertain spend: calls=%d ms=%d", resumed.CallsUsed, resumed.ActiveMillis)
	}
}

func TestWholeReviewProjectsExactPublishedVersions(t *testing.T) {
	for _, prepublishedV2 := range []bool{true, false} {
		t.Run(fmt.Sprintf("prepublished_v2_%t", prepublishedV2), func(t *testing.T) {
			d, id := fixture(t)
			snap, _ := BuildSourceSnapshot(d, id)
			repo := Repository{DB: guideDB(t)}
			scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "zh"}
			model := ModelIdentity{ID: "m", ContextTokens: 128000}
			var before int64
			whole := 0
			service := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }}
			service.Call = func(ctx context.Context, task string, input map[string]any, cfg map[string]any) (json.RawMessage, error) {
				switch task {
				case "paper.guide.plan":
					return json.Marshal(baselinePlan(snap))
				case "paper.guide.section":
					section := teachingSection(input["role"].(Role))
					if input["findings"] != nil {
						section.Blocks[0].Text = "Corrected sample."
					}
					return json.Marshal(section)
				case "paper.guide.review":
					sections := input["sections"].([]GuideSection)
					target := ""
					if len(sections) == 1 && prepublishedV2 && sections[0].Role == RoleBackground && sections[0].Blocks[0].Text == "The measured sample." {
						target = "b_background"
					}
					if len(sections) == 4 {
						whole++
						target = "b_background"
						if whole == 2 {
							target = "b_" + string(Roles()[1])
						}
						var current orm.PaperGuideRevision
						repo.DB.Where("guide_id IN (?)", repo.DB.Model(&orm.PaperGuide{}).Select("id").Where("scope_key = ?", scope.Key())).Take(&current)
						before = current.ChangeVersion
					}
					if target != "" {
						return json.Marshal(GuideReview{Findings: []GuideFinding{{TargetID: target, TargetType: "block", IssueType: "coherence", Suggestion: "Clarify sample."}}})
					}
				}
				return json.Marshal(GuideReview{Findings: []GuideFinding{}})
			}
			rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test", Model: model})
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Run(t.Context(), asyncjob.Job{ID: rev.JobID, PayloadJSON: mustJSON(JobPayload{rev.ID, rev.Epoch})}, testFence{repo.DB})
			if !errors.Is(err, ErrQuality) {
				t.Fatalf("expected quality pause: %v", err)
			}
			delta, err := service.Revision(t.Context(), scope, rev.ID, before)
			if err != nil {
				t.Fatal(err)
			}
			wantRole := RoleBackground
			wantVersion := 2
			if !prepublishedV2 {
				wantRole = Roles()[1]
				wantVersion = 1
			}
			found := false
			for _, section := range delta.Sections {
				if section.Role == wantRole {
					found = true
					if section.Version != wantVersion || section.PublicationStatus != "correction_required" || section.ChangeVersion <= before {
						t.Fatalf("incorrect projection: %+v", section)
					}
				}
			}
			if !found {
				t.Fatalf("incremental response omitted flagged version: %+v", delta.Sections)
			}
		})
	}
}

func TestLegacyCandidateCannotSpendOrResumeUnderNewContract(t *testing.T) {
	for _, version := range []string{"paper-guide-v1", "paper-guide-v2", "paper-guide-v3", "paper-guide-v4"} {
		t.Run(version, func(t *testing.T) {
			d, id := fixture(t)
			snap, _ := BuildSourceSnapshot(d, id)
			repo := Repository{DB: guideDB(t)}
			scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "zh"}
			model := ModelIdentity{ID: "m", ContextTokens: 128000}
			rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "legacy", Model: model})
			if err != nil {
				t.Fatal(err)
			}
			oldFingerprint := digest([]any{id.SourceSHA256, snap.IRDigest, version, model})
			if version != "paper-guide-v1" {
				oldFingerprint = digest([]any{id.SourceSHA256, snap.IRDigest, SourceCatalogVersion, version, model})
			}
			repo.DB.Model(&rev).Updates(map[string]any{"fingerprint": oldFingerprint, "stage": "paused", "calls_used": 1})
			calls := 0
			service := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }, Call: func(context.Context, string, map[string]any, map[string]any) (json.RawMessage, error) {
				calls++
				return nil, nil
			}}
			_, err = service.Run(t.Context(), asyncjob.Job{ID: rev.JobID, PayloadJSON: mustJSON(JobPayload{rev.ID, rev.Epoch})}, testFence{repo.DB})
			if err == nil || err.Error() != "paper_guide_regeneration_required" || calls != 0 {
				t.Fatalf("legacy candidate spent calls=%d err=%v", calls, err)
			}
			_, err = repo.Resume(t.Context(), scope, rev.ID, model)
			if err == nil || err.Error() != "paper_guide_regeneration_required" {
				t.Fatalf("mixed-contract resume: %v", err)
			}
			if err = repo.Cancel(t.Context(), scope, rev.ID); err != nil {
				t.Fatalf("legacy candidate must remain cancellable: %v", err)
			}
		})
	}
}
