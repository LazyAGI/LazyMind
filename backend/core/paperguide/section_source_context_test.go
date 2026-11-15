package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
)

func TestSectionSourceContextUsesExactPriorSummaryBudget(t *testing.T) {
	for _, extra := range []string{"", "x"} {
		name := "exact_fit"
		if extra != "" {
			name = "one_more_byte"
		}
		t.Run(name, func(t *testing.T) {
			snap, _, plan := sectionSourceFixture(t)
			sp := plan.Sections[1]
			data := fullSectionSourceInput(snap, plan)
			data["role"], data["prior_summary"] = sp.Role, "Previous explanation."
			x := newSectionSourceExecution(t, snap, sectionSourceBound(t, "paper.guide.section", data))
			prior := teachingSection(RoleBackground)
			prior.Summary = data["prior_summary"].(string) + extra
			calls := 0
			x.s.Call = func(_ context.Context, task string, in map[string]any, _ map[string]any) (json.RawMessage, error) {
				calls++
				if task != "paper.guide.section" || in["prior_summary"] != prior.Summary {
					t.Fatalf("unexpected task or summary: %s %v", task, in["prior_summary"])
				}
				wantCount := len(snap.Evidence)
				if extra != "" {
					wantCount = 3 // one direct source and both sources of the required claim
				}
				if evidence := in["evidence"].([]GuideEvidence); len(evidence) != wantCount {
					t.Fatalf("selected %d evidence items, want %d", len(evidence), wantCount)
				}
				if _, full := in["source_sections"]; full != (extra == "") {
					t.Fatalf("incorrect source directory presence: %t", full)
				}
				return nil, context.Canceled
			}
			_, _, err := x.writeSection(t.Context(), plan, sp, []GuideSection{prior}, 1, nil, false, nil)
			if !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatalf("section calls=%d, error=%v", calls, err)
			}
		})
	}
}

func TestSectionSourceContextUsesActualReviewTaskAtExactBoundary(t *testing.T) {
	for _, extra := range []string{"", "x"} {
		snap, ids, plan := sectionSourceFixture(t)
		sp := plan.Sections[1]
		draft := teachingSection(sp.Role)
		draft.ClaimIDs = []string{"cross-role"}
		draft.Blocks[0].ClaimIDs = draft.ClaimIDs
		draft.Blocks[0].EvidenceIDs = []string{ids["draft-block"]}
		reviewInput := fullSectionSourceInput(snap, plan)
		reviewInput["sections"] = []GuideSection{draft}
		budget := sectionSourceBound(t, "paper.guide.review", reviewInput)
		// The only extra typed occurrence from the actual draft is its direct
		// reference. At the REVIEW limit it is needed to admit the full packet.
		if saved := sectionSourceBound(t, "paper.guide.section", reviewInput) - budget; saved != 64 {
			t.Fatalf("want 64 bytes of draft-only credit, got %d", saved)
		}
		x := newSectionSourceExecution(t, snap, budget)
		draft.Blocks[0].Text += extra
		calls := 0
		x.s.Call = func(_ context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
			calls++
			evidence := input["evidence"].([]GuideEvidence)
			switch task {
			case "paper.guide.section":
				if len(evidence) != len(snap.Evidence) {
					t.Fatal("initial draft should use the full planning packet")
				}
				return json.Marshal(draft)
			case "paper.guide.review":
				if full := len(evidence) == len(snap.Evidence); full != (extra == "") {
					t.Fatalf("draft extra=%q: review selected %d sources", extra, len(evidence))
				}
				if !slices.ContainsFunc(evidence, func(ev GuideEvidence) bool { return ev.ID == ids["draft-block"] }) {
					t.Fatal("review dropped the actual draft's direct source")
				}
				return json.Marshal(GuideReview{Findings: []GuideFinding{}})
			default:
				t.Fatalf("unexpected task %s", task)
				return nil, nil
			}
		}
		if _, _, err := x.writeSection(t.Context(), plan, sp, nil, 1, nil, false, nil); err != nil || calls != 2 {
			t.Fatalf("draft extra=%q review calls=%d err=%v", extra, calls, err)
		}
	}
}

func TestSectionSourceContextReviewAndRepairRetainDraftDependenciesOnResume(t *testing.T) {
	snap, ids, plan := sectionSourceFixture(t)
	plain, field, authored, direct := ids["one"], ids["assigned-claim"], ids["draft-claim"], ids["draft-block"]
	want := []string{plain, field, ids["cross-first"], ids["cross-second"], authored, direct, ids["physical-parent"]}
	for _, ev := range snap.Evidence {
		if ev.ParentAssetID == ids["physical-parent"] {
			want = append(want, ev.ID)
		}
	}
	draft := teachingSection(RoleBackground)
	draft.Claims = []GuideClaim{sourceBoundTestClaim("background-new", authored)}
	draft.ClaimIDs = []string{"assigned", "cross-role", "background-new"}
	draft.Blocks[0].ClaimIDs = draft.ClaimIDs
	draft.Blocks[0].EvidenceIDs = []string{direct}
	draft.Blocks[0].Text = strings.Repeat("x", 7000)
	table := GuideBlock{ID: "background-table", Kind: "table", Text: "Reported results.", Attribution: AttributionTeaching,
		ClaimIDs: []string{}, EvidenceIDs: []string{}, AssetID: ids["physical-parent"]}
	draft.Blocks = append(draft.Blocks, table)
	repaired := teachingSection(RoleBackground)
	repaired.Claims, repaired.ClaimIDs = draft.Claims, draft.ClaimIDs
	repaired.Blocks[0].ClaimIDs = draft.ClaimIDs
	repaired.Blocks[0].EvidenceIDs = []string{direct}
	repaired.Blocks = append(repaired.Blocks, table)
	findings := []GuideFinding{
		{TargetType: "block", TargetID: draft.Blocks[0].ID, IssueType: "coherence", Suggestion: strings.Repeat("𐐀", 800)},
		{TargetType: "block", TargetID: draft.Blocks[0].ID, IssueType: "coherence", Suggestion: strings.Repeat("𐐁", 800)},
	}
	fullDraftInput := fullSectionSourceInput(snap, plan)
	fullDraftInput["role"] = RoleBackground
	budget := sectionSourceBound(t, "paper.guide.section", fullDraftInput) + 4800
	x := newSectionSourceExecution(t, snap, budget)
	assertClosed := func(in map[string]any) {
		t.Helper()
		got := []string{}
		for _, ev := range in["evidence"].([]GuideEvidence) {
			got = append(got, ev.ID)
		}
		if len(got) != len(want) {
			t.Fatalf("closed packet has %d sources, want %d", len(got), len(want))
		}
		for _, id := range want {
			if !slices.Contains(got, id) {
				t.Fatalf("closed packet dropped source %s", id)
			}
		}
		if in["source_sections"] != nil || in["previous_section"] != nil {
			t.Fatal("unexpected fields in closed request")
		}
	}
	var interrupted bool
	var repairHashes []string
	var backgroundDrafts, backgroundReviews int
	x.s.Call = func(_ context.Context, task string, in map[string]any, _ map[string]any) (json.RawMessage, error) {
		switch task {
		case "paper.guide.plan":
			return json.Marshal(plan)
		case "paper.guide.section":
			role := in["role"].(Role)
			if role != RoleBackground {
				out := teachingSection(role)
				if role == RoleQuestion {
					out.ClaimIDs = []string{"cross-role"}
					out.Blocks[0].ClaimIDs = out.ClaimIDs
				}
				return json.Marshal(out)
			}
			if in["findings"] == nil {
				backgroundDrafts++
				if len(in["evidence"].([]GuideEvidence)) != len(snap.Evidence) {
					t.Fatal("initial draft did not get full planning sources")
				}
				return json.Marshal(draft)
			}
			assertClosed(in)
			if got := in["findings"].([]GuideFinding); !slices.Equal(got, findings) {
				t.Fatalf("repair findings changed: %d", len(got))
			}
			repairHashes = append(repairHashes, digest([]any{TaskVersion, in}))
			if !interrupted {
				interrupted = true
				return nil, context.Canceled
			}
			return json.Marshal(repaired)
		case "paper.guide.review":
			ss := in["sections"].([]GuideSection)
			if len(ss) == 1 && ss[0].Role == RoleBackground && ss[0].Blocks[0].Text == draft.Blocks[0].Text {
				backgroundReviews++
				assertClosed(in)
				return json.Marshal(GuideReview{Findings: findings})
			}
			if len(ss) == 1 && ss[0].Role == RoleBackground && len(in["evidence"].([]GuideEvidence)) != len(snap.Evidence) {
				t.Fatal("short repaired draft should independently fit the full review packet")
			}
			return json.Marshal(GuideReview{Findings: []GuideFinding{}})
		default:
			t.Fatalf("unexpected task %s", task)
			return nil, nil
		}
	}
	run := func() error {
		_, err := x.s.Run(t.Context(), asyncjob.Job{ID: x.rev.JobID, PayloadJSON: mustJSON(JobPayload{x.rev.ID, x.rev.Epoch})}, x.f)
		return err
	}
	if err := run(); err == nil || !interrupted {
		t.Fatalf("expected interruption after the bounded repair request: %v", err)
	}
	before := sourceContextSteps(t, x)
	var err error
	x.rev, err = x.s.Repo.Resume(t.Context(), x.scope, x.rev.ID, x.model)
	if err != nil {
		t.Fatal(err)
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	after := sourceContextSteps(t, x)
	for _, key := range []string{"section:background:v1:draft", "section:background:v1:review", "correction:block:b_background"} {
		if before[key].InputHash == "" || before[key] != after[key] {
			t.Fatalf("resumed execution changed checkpoint %s", key)
		}
	}
	if len(repairHashes) != 2 || repairHashes[0] != repairHashes[1] ||
		after["section:background:v2:draft"].InputHash != repairHashes[0] || backgroundDrafts != 1 || backgroundReviews != 1 {
		t.Fatalf("unexpected resume: hashes=%v drafts=%d reviews=%d", repairHashes, backgroundDrafts, backgroundReviews)
	}
	rev, err := x.s.Repo.Revision(t.Context(), x.scope, x.rev.ID)
	if err != nil || rev.CallsUsed != 13 || rev.RecoveryCalls != 1 || rev.CorrectionCount != 1 || rev.ExtractCalls != 0 || rev.Stage != "completed" {
		t.Fatalf("unexpected ledger or status: calls=%d recovery=%d corrections=%d extracts=%d stage=%s err=%v", rev.CallsUsed, rev.RecoveryCalls, rev.CorrectionCount, rev.ExtractCalls, rev.Stage, err)
	}
}

func TestSectionSourceContextTooLargeDraftNeverReservesReviewTicket(t *testing.T) {
	snap, _, _ := sectionSourceFixture(t)
	p := baselinePlan(snap)
	data := fullSectionSourceInput(snap, p)
	data["role"] = RoleBackground
	x := newSectionSourceExecution(t, snap, sectionSourceBound(t, "paper.guide.section", data))
	calls := 0
	x.s.Call = func(_ context.Context, task string, _ map[string]any, _ map[string]any) (json.RawMessage, error) {
		calls++
		if task != "paper.guide.section" {
			t.Fatalf("overflow review reached %s", task)
		}
		out := teachingSection(RoleBackground)
		for _, id := range []string{"one", "two", "three", "four", "five"} {
			b := out.Blocks[0]
			b.ID = id
			b.Text = strings.Repeat("x", 7500)
			out.Blocks = append(out.Blocks, b)
		}
		return json.Marshal(out)
	}
	_, _, err := x.writeSection(t.Context(), p, p.Sections[0], nil, 1, nil, false, nil)
	steps := sourceContextSteps(t, x)
	rev, revErr := x.s.Repo.Revision(t.Context(), x.scope, x.rev.ID)
	if !errors.Is(err, ErrContext) || calls != 1 || len(steps) != 2 || revErr != nil || rev.CallsUsed != 1 {
		t.Fatalf("overflow model calls=%d, checkpoints=%d, ledger=%d, error=%v/%v", calls, len(steps), rev.CallsUsed, err, revErr)
	}
	for key := range steps {
		if strings.Contains(key, ":review") {
			t.Fatalf("overflow reserved review checkpoint %s", key)
		}
	}
}

func TestSectionSourceContextMissingRetainedDependencyDoesNotReserveTicket(t *testing.T) {
	snap, ids, plan := sectionSourceFixture(t)
	keep := []string{}
	for _, ev := range snap.Evidence {
		if ev.ID != ids["cross-second"] {
			keep = append(keep, ev.ID)
		}
	}
	retained, err := RestrictEvidence(snap, keep)
	if err != nil {
		t.Fatal(err)
	}
	sp := plan.Sections[1]
	data := fullSectionSourceInput(retained, plan)
	data["role"] = sp.Role
	x := newSectionSourceExecution(t, snap, sectionSourceBound(t, "paper.guide.section", data)-1)
	x.planningPacket = retained
	calls := 0
	x.s.Call = func(context.Context, string, map[string]any, map[string]any) (json.RawMessage, error) {
		calls++
		return nil, context.Canceled
	}
	_, _, err = x.writeSection(t.Context(), plan, sp, nil, 1, nil, false, nil)
	rev, revErr := x.s.Repo.Revision(t.Context(), x.scope, x.rev.ID)
	if !errors.Is(err, ErrEvidence) || calls != 0 || len(sourceContextSteps(t, x)) != 0 || revErr != nil || rev.CallsUsed != 0 {
		t.Fatalf("missing retained source got calls=%d, ledger=%d, error=%v/%v", calls, rev.CallsUsed, err, revErr)
	}
}

func sectionSourceFixture(t *testing.T) (GuideSourceSnapshot, map[string]string, GuidePlan) {
	t.Helper()
	d, id := tableFragmentDocument(t)
	d.Blocks = slices.DeleteFunc(d.Blocks, func(b pdfir.Block) bool { return b.Type == "image" || b.Type == "formula" })
	for _, key := range []string{"assigned-claim", "cross-first", "cross-second", "draft-claim", "draft-block", "unselected"} {
		d.Blocks = append(d.Blocks, pdfir.Block{ID: key, Type: "text", Text: "Original evidence for " + key, Page: 1})
	}
	d.Blocks = append(d.Blocks, pdfir.Block{ID: "extra", Type: "text", Text: strings.Repeat("Independent original source. ", 600), Page: 1})
	snap, err := BuildSourceSnapshot(d, id)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, ev := range snap.Evidence {
		if ev.ParentAssetID == "" {
			ids[ev.SourceBlockID] = ev.ID
		}
	}
	p := baselinePlan(snap)
	p.Claims = []GuideClaim{
		sourceBoundTestClaim("assigned", ids["assigned-claim"]),
		sourceBoundTestClaim("cross-role", ids["cross-first"], ids["cross-second"]),
	}
	p.Sections[0].ClaimIDs, p.Sections[1].ClaimIDs = []string{"assigned"}, []string{"cross-role"}
	p.Sections[0].AssetIDs = []string{ids["physical-parent"]}
	p.RequiredAssets = []GuideAssetDecision{{ID: ids["physical-parent"], Included: true, Reason: "The reported results appear in this table."}}
	if err := ValidatePlan(snap, p); err != nil {
		t.Fatal(err)
	}
	return snap, ids, p
}

func sourceBoundTestClaim(id string, evidence ...string) GuideClaim {
	return GuideClaim{ID: id, Statement: "The source reports a controlled measurement.", Attribution: AttributionPaper,
		SupportStatus: SupportSupported, EvidenceIDs: evidence, Conditions: []string{}}
}

func fullSectionSourceInput(snap GuideSourceSnapshot, plan GuidePlan) map[string]any {
	return map[string]any{"language": "en", "evidence": snap.Evidence, "coverage": snap.Coverage, "source_sections": snap.Sections, "plan": plan}
}

func sectionSourceBound(t *testing.T, task string, input map[string]any) int {
	t.Helper()
	data, err := json.MarshalIndent(input, "", "")
	if err != nil {
		t.Fatal(err)
	}
	bound, ok := guideInputBound(task, data)
	if !ok {
		t.Fatal("test input exceeds transport budget")
	}
	return bound
}

func newSectionSourceExecution(t *testing.T, snap GuideSourceSnapshot, budget int) execution {
	t.Helper()
	repo := Repository{DB: guideDB(t)}
	model := ModelIdentity{ID: "m", ContextTokens: 100000, OutputReserveTokens: 100000 - budget}
	scope := Scope{snap.Identity.OwnerID, snap.Identity.DatasetID, snap.Identity.DocumentID, "en"}
	rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: snap.Identity.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "fixture", Model: model})
	if err != nil {
		t.Fatal(err)
	}
	return execution{
		s: Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil },
			ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }},
		scope: scope, rev: rev, model: model, snap: snap, planningPacket: snap, f: testFence{repo.DB}, steps: map[string]orm.PaperGuideStep{},
	}
}

func sourceContextSteps(t *testing.T, x execution) map[string]orm.PaperGuideStep {
	t.Helper()
	steps, err := x.s.Repo.Steps(t.Context(), x.rev.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]orm.PaperGuideStep{}
	for _, step := range steps {
		out[step.StepKey] = step
	}
	return out
}
