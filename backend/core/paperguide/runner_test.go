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
	"reflect"
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
	oldOutcome := make(chan error, 1)
	asyncjob.Register(JobType, func(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
		result, err := service.Run(ctx, job, reporter)
		if job.ID == first.JobID {
			oldOutcome <- err
		}
		return result, err
	})
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
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
	defer cancel2()
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
	// Observe the old handler's outcome while its worker context is active.
	select {
	case err := <-oldOutcome:
		if err == nil {
			t.Error("stale worker accepted late PLAN")
		}
	case <-time.After(5 * time.Second):
		t.Error("stale worker did not return from its late PLAN")
	}
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
	old, e := repo.Revision(t.Context(), scope, first.ID)
	if e != nil || old.CallsUsed != 1 || old.ActiveMillis != 300000 {
		t.Fatalf("stale worker changed reservation: calls=%d active=%d err=%v", old.CallsUsed, old.ActiveMillis, e)
	}
	steps, e := repo.Steps(t.Context(), first.ID)
	if e != nil || len(steps) != 1 || !strings.HasPrefix(steps[0].StepKey, "request:plan:") || steps[0].OutputJSON != "" {
		t.Fatalf("stale worker settled or wrote a checkpoint: %+v %v", steps, e)
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
			if extractCalls < 2 || extractCalls > 8 || len(seen) != len(snap.Evidence) || !x.inputFits("paper.guide.plan", input) {
				t.Fatalf("coverage=%d/%d calls=%d", len(seen), len(snap.Evidence), extractCalls)
			}
			// A saved Plan fixes extraction provenance. Reconstruction must have
			// byte-identical input/hash and must never call the model again.
			x.steps["plan"] = orm.PaperGuideStep{}
			beforeResume := extractCalls
			rebuilt, rebuiltPacket, err := x.planInput(t.Context())
			if err != nil || digest(rebuilt) != digest(input) || extractCalls != beforeResume {
				t.Fatalf("reconstruction changed input or replayed extract: %v", err)
			}
			if digest(rebuilt["evidence"]) != digest(rebuiltPacket.Evidence) {
				t.Fatal("packet/input differ")
			}
			original := x.steps["extract:1"]
			for _, mutation := range []string{"missing", "hash", "json", "unknown_evidence"} {
				bad := original
				switch mutation {
				case "missing":
					delete(x.steps, "extract:1")
				case "hash":
					bad.InputHash = "wrong"
					x.steps["extract:1"] = bad
				case "json":
					bad.OutputJSON = "{corrupt"
					x.steps["extract:1"] = bad
				case "unknown_evidence":
					bad.OutputJSON = string(mustJSON(GuideExtract{Claims: []GuideClaim{{ID: "c", Statement: "Unknown", Attribution: AttributionPaper, EvidenceIDs: []string{"unknown"}, SupportStatus: SupportSupported}}}))
					x.steps["extract:1"] = bad
				}
				if _, _, err := x.planInput(t.Context()); err == nil || extractCalls != beforeResume {
					t.Fatalf("accepted/replayed corrupt checkpoint %s: %v", mutation, err)
				}
				x.steps["extract:1"] = original
			}
			delete(x.steps, "plan")
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
	h := newDeadlineGuide(t, false)
	var inputHash string
	calls := 0
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithCancel(t.Context())
		h.Call = func(_ context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
			calls++
			if task != "paper.guide.plan" {
				t.Fatalf("unexpected recovery task: %s", task)
			}
			currentHash := digest([]any{TaskVersion, input})
			if attempt == 0 {
				inputHash = currentHash
			} else if currentHash != inputHash {
				t.Fatal("recovery changed the original PLAN request")
			}
			cancel()
			return mustJSON(baselinePlan(h.snap)), nil
		}
		_, err := h.run(ctx)
		cancel()
		if err == nil || calls != attempt+1 {
			t.Fatalf("interrupted execution %d completed: calls=%d err=%v", attempt, calls, err)
		}
		if err = h.Repo.DB.Model(&orm.AsyncJob{}).Where("id = ?", h.rev.JobID).Update("status", "failed").Error; err != nil {
			t.Fatal(err)
		}
		h.rev, err = h.Repo.Resume(t.Context(), h.scope, h.rev.ID, h.model)
		if err != nil {
			t.Fatal(err)
		}
		if h.rev.CallsUsed != attempt+1 || h.rev.RecoveryCalls != attempt || h.rev.PlannedCalls != 1 ||
			h.rev.ActiveMillis != int64(attempt+1)*300000 {
			t.Fatalf("lost uncertain spend: %+v", h.rev)
		}
		for _, step := range h.checkpoints() {
			if !strings.HasPrefix(step.StepKey, "request:plan:") || step.OutputJSON != "" || step.InputHash != inputHash {
				t.Fatalf("recovery changed the unconfirmed ticket: %+v", step)
			}
		}
	}
	_, err := h.run(t.Context())
	if row := h.row(); !errors.Is(err, ErrBudget) || calls != 3 || row.CallsUsed != 3 || row.RecoveryCalls != 2 || len(h.checkpoints()) != 3 {
		t.Fatalf("third recovery was dispatched: calls=%d total=%d recovery=%d err=%v", calls, row.CallsUsed, row.RecoveryCalls, err)
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
			var wholeFinding GuideFinding
			const originalNotation = "> The original notation.〔e1〕"
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
					quote := section.Blocks[0]
					quote.ID += "_quote"
					quote.Text = originalNotation
					section.Blocks = append(section.Blocks, quote)
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
						wholeFinding = GuideFinding{TargetID: target, TargetType: "block", IssueType: "missing_condition", Suggestion: "State that the measurement applies only to dry samples."}
						return json.Marshal(GuideReview{Findings: []GuideFinding{wholeFinding}})
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
			var stored orm.PaperGuideStep
			var review GuideReview
			reviewKey := "whole_review:2"
			if prepublishedV2 {
				reviewKey = "whole_review:1"
			}
			if err = repo.DB.Where("revision_id = ? AND step_key = ?", rev.ID, reviewKey).Take(&stored).Error; err != nil ||
				json.Unmarshal([]byte(stored.OutputJSON), &review) != nil || !reflect.DeepEqual(review.Findings, []GuideFinding{wholeFinding}) ||
				delta.ReviewSummary == nil || !reflect.DeepEqual(delta.ReviewSummary.Findings, review.Findings) {
				t.Fatalf("substantive finding changed in storage or projection: stored=%+v projected=%+v err=%v", review, delta.ReviewSummary, err)
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
					if section.Version != wantVersion || section.PublicationStatus != "correction_required" || section.ChangeVersion <= before ||
						len(section.Blocks) != 2 || section.Blocks[1].Text != originalNotation {
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
	for _, version := range []string{"paper-guide-v1", "paper-guide-v2", "paper-guide-v3", "paper-guide-v4", "paper-guide-v5", "paper-guide-v6", "paper-guide-v7", "paper-guide-v8", "paper-guide-v9", "paper-guide-v10", "paper-guide-v11", "paper-guide-v12", "paper-guide-v13", "paper-guide-v14", "paper-guide-v15", "paper-guide-v16", "paper-guide-v17", "paper-guide-v18", "paper-guide-v19", "paper-guide-v20"} {
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
			planInput := map[string]any{
				"language": scope.Language, "evidence": snap.Evidence, "coverage": snap.Coverage,
				"source_sections": snap.Sections,
			}
			checkpoint := StepWrite{Key: "plan", InputHash: digest([]any{version, planInput}), Output: mustJSON(baselinePlan(snap))}
			if err = repo.PutStep(t.Context(), testFence{repo.DB}, rev.ID, rev.Epoch, checkpoint); err != nil {
				t.Fatal(err)
			}
			if err = repo.DB.Model(&rev).Updates(map[string]any{"fingerprint": oldFingerprint, "stage": "paused", "calls_used": 1}).Error; err != nil {
				t.Fatal(err)
			}
			calls, resolutions := 0, 0
			service := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) {
				resolutions++
				return model, nil, nil
			}, Call: func(context.Context, string, map[string]any, map[string]any) (json.RawMessage, error) {
				calls++
				return nil, nil
			}}
			_, err = service.Run(t.Context(), asyncjob.Job{ID: rev.JobID, PayloadJSON: mustJSON(JobPayload{rev.ID, rev.Epoch})}, testFence{repo.DB})
			if err == nil || err.Error() != "paper_guide_regeneration_required" || calls != 0 || resolutions != 0 {
				t.Fatalf("legacy candidate reached model resolution or call: resolutions=%d calls=%d err=%v", resolutions, calls, err)
			}
			_, err = repo.Resume(t.Context(), scope, rev.ID, model)
			if err == nil || err.Error() != "paper_guide_regeneration_required" {
				t.Fatalf("mixed-contract resume: %v", err)
			}
			steps, err := repo.Steps(t.Context(), rev.ID)
			if err != nil || len(steps) != 1 || steps[0].StepKey != checkpoint.Key ||
				steps[0].InputHash != checkpoint.InputHash || steps[0].OutputJSON != string(checkpoint.Output) {
				t.Fatalf("legacy checkpoint changed: %+v %v", steps, err)
			}
			if err = repo.Cancel(t.Context(), scope, rev.ID); err != nil {
				t.Fatalf("legacy candidate must remain cancellable: %v", err)
			}
		})
	}
}
