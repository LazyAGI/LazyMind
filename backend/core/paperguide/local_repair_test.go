package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

const (
	localV2 = "section:background:v2"
	localV3 = "section:background:v3"
)

var (
	localA         = GuideFinding{TargetType: "block", TargetID: "b_background", IssueType: "unsupported", Suggestion: "Verify the measured result."}
	localB         = GuideFinding{TargetType: "block", TargetID: "background-condition", IssueType: "missing_condition", Suggestion: "Restore the sampling condition."}
	localBFollowup = GuideFinding{TargetType: "block", TargetID: "background-condition", IssueType: "coherence", Suggestion: "Explain when that condition applies."}
	localC         = GuideFinding{TargetType: "block", TargetID: "background-unit", IssueType: "coherence", Suggestion: "Explain the units."}
)

type localRepairRun struct {
	t       *testing.T
	service Service
	scope   Scope
	model   ModelIdentity
	rev     orm.PaperGuideRevision
	seen    []string
	review  func([]GuideSection) []GuideFinding
	onV3    func()
	v3Input map[string]any
	shared  bool
	wholeAt int64
}

func newLocalRepairRun(t *testing.T) *localRepairRun {
	t.Helper()
	doc, id := fixture(t)
	snap, err := BuildSourceSnapshot(doc, id)
	if err != nil {
		t.Fatal(err)
	}
	h := &localRepairRun{
		t: t, scope: Scope{id.OwnerID, id.DatasetID, id.DocumentID, "en"},
		model: ModelIdentity{ID: "m", ContextTokens: 128000},
	}
	h.service = Service{
		Repo: Repository{DB: guideDB(t)}, Source: &fixtureSource{s: snap},
		Authorize:    func(context.Context, Scope, bool) error { return nil },
		ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return h.model, nil, nil },
	}
	h.rev, err = h.service.Repo.Create(t.Context(), CreateRevision{Scope: h.scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test", Model: h.model})
	if err != nil {
		t.Fatal(err)
	}
	h.review = func(sections []GuideSection) []GuideFinding {
		if len(sections) != 1 || sections[0].Role != RoleBackground {
			return nil
		}
		switch sections[0].Summary {
		case "Initial draft.":
			return []GuideFinding{localA}
		case "First repair.":
			return []GuideFinding{localB, localBFollowup}
		default:
			return nil
		}
	}
	h.service.Call = func(ctx context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
		switch task {
		case "paper.guide.plan":
			h.seen = append(h.seen, "plan")
			p := baselinePlan(snap)
			if h.shared {
				p.Claims = []GuideClaim{{ID: "shared", Statement: "The sample has a result.", Attribution: AttributionPaper, EvidenceIDs: []string{snap.Evidence[0].ID}, Conditions: []string{}, SupportStatus: "supported"}}
				for i := range p.Sections {
					p.Sections[i].ClaimIDs = []string{"shared"}
				}
			}
			return json.Marshal(p)
		case "paper.guide.section":
			s := teachingSection(input["role"].(Role))
			if h.shared {
				s.ClaimIDs = []string{"shared"}
				s.Blocks[0].ClaimIDs = []string{"shared"}
			}
			s.Summary = "Initial draft."
			if findings, ok := input["findings"].([]GuideFinding); ok {
				if s.Role == RoleBackground && reflect.DeepEqual(findings, []GuideFinding{localB, localBFollowup}) {
					s.Summary = "Second repair."
					s.Blocks[0].Text = "Final measurement."
				} else {
					s.Summary = "First repair."
					s.Blocks[0].Text = "Intermediate measurement."
				}
			}
			s.Blocks[0].EvidenceIDs = []string{snap.Evidence[0].ID}
			if s.Role == RoleBackground {
				for _, f := range []GuideFinding{localB, localC} {
					b := s.Blocks[0]
					b.ID = f.TargetID
					s.Blocks = append(s.Blocks, b)
				}
			}
			h.seen = append(h.seen, "write:"+string(s.Role)+":"+s.Summary)
			if s.Summary == "Second repair." {
				h.v3Input = maps.Clone(input)
				if h.onV3 != nil {
					h.onV3()
				}
			}
			return json.Marshal(s)
		case "paper.guide.review":
			sections := input["sections"].([]GuideSection)
			if len(sections) == 4 {
				for i, role := range Roles() {
					if sections[i].Role != role {
						t.Fatalf("whole review received out-of-order roles: %+v", sections)
					}
				}
				background := sections[0]
				if background.Blocks[0].ID != "b_background" || background.Blocks[0].Text != "Final measurement." {
					t.Fatalf("whole review received stale background text: %+v", background.Blocks)
				}
				h.wholeAt = h.row().ChangeVersion
				h.seen = append(h.seen, "whole-review")
			} else {
				h.seen = append(h.seen, "review:"+string(sections[0].Role)+":"+sections[0].Summary)
			}
			findings := h.review(sections)
			return json.Marshal(GuideReview{Findings: append([]GuideFinding{}, findings...)})
		default:
			t.Fatalf("unexpected task %q", task)
			return nil, nil
		}
	}
	return h
}

func (h *localRepairRun) run(ctx context.Context, f asyncjob.FencedReporter) error {
	_, err := h.service.Run(ctx, asyncjob.Job{ID: h.rev.JobID, PayloadJSON: mustJSON(JobPayload{h.rev.ID, h.rev.Epoch})}, f)
	return err
}

func (h *localRepairRun) row() orm.PaperGuideRevision {
	h.t.Helper()
	row, err := h.service.Repo.Revision(h.t.Context(), h.scope, h.rev.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	return row
}

func (h *localRepairRun) checkpoints() map[string]orm.PaperGuideStep {
	h.t.Helper()
	steps, err := h.service.Repo.Steps(h.t.Context(), h.rev.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	out := make(map[string]orm.PaperGuideStep, len(steps))
	for _, step := range steps {
		out[step.StepKey] = step
	}
	return out
}

func (h *localRepairRun) resume() {
	h.t.Helper()
	repo := h.service.Repo
	if err := repo.DB.Model(&orm.AsyncJob{}).Where("id = ?", h.rev.JobID).Update("status", "failed").Error; err != nil {
		h.t.Fatal(err)
	}
	rev, err := repo.Resume(h.t.Context(), h.scope, h.rev.ID, h.model)
	if err != nil {
		h.t.Fatal(err)
	}
	h.rev = rev
}

func TestLocalRepairPublishesV3AndWholeReviewBindsIt(t *testing.T) {
	for _, finding := range []bool{false, true} {
		name := "complete"
		if finding {
			name = "whole_finding"
		}
		t.Run(name, func(t *testing.T) {
			h := newLocalRepairRun(t)
			localReview := h.review
			h.review = func(sections []GuideSection) []GuideFinding {
				if len(sections) == 4 && finding {
					return []GuideFinding{localC}
				}
				return localReview(sections)
			}
			err := h.run(t.Context(), testFence{h.service.Repo.DB})
			wantStage, wantStatus := "completed", "published"
			if finding {
				wantStage, wantStatus = "paused", "correction_required"
				if !errors.Is(err, ErrQuality) {
					t.Fatalf("want quality pause on whole finding, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			h.assertCompletedLocalChain(14, 0, wantStage)
			var bindings []reviewedSection
			if err := json.Unmarshal([]byte(h.checkpoints()["whole_review:1"].ReviewJSON), &bindings); err != nil {
				t.Fatal(err)
			}
			if len(bindings) != 4 || bindings[0] != (reviewedSection{Key: localV3, Version: 3}) {
				t.Fatalf("wrong whole-review binding: %+v", bindings)
			}
			after := int64(0)
			if finding {
				after = h.wholeAt
			}
			got, err := h.service.Revision(t.Context(), h.scope, h.rev.ID, after)
			count := 4
			if finding {
				count = 1
			}
			if err != nil || len(got.Sections) != count || !got.SourceCurrent {
				t.Fatalf("revision projection: %+v %v", got, err)
			}
			if s := got.Sections[0]; s.Key != localV3 || s.Version != 3 || s.PublicationStatus != wantStatus || s.ChangeVersion <= after || s.Blocks[0].Text != "Final measurement." {
				t.Fatalf("stale revision projection after %d: %+v", after, s)
			}
			selected, err := h.service.Selection(t.Context(), h.scope, h.rev.ID, SelectionRequest{localV3, "b_background", 0, 5})
			if err != nil || selected.Text != "Final" || !selected.SourceCurrent || len(selected.Evidence) != 1 || selected.Evidence[0].ID != h.service.Source.(*fixtureSource).s.Evidence[0].ID {
				t.Fatalf("v3 selection lost text/source: %+v %v", selected, err)
			}
			if _, err := h.service.Selection(t.Context(), h.scope, h.rev.ID, SelectionRequest{localV2, "b_background", 0, 5}); !errors.Is(err, ErrEvidence) {
				t.Fatalf("unpublished v2 selectable: %v", err)
			}
		})
	}
}

func (h *localRepairRun) assertCompletedLocalChain(calls, recovery int, stage string) {
	h.t.Helper()
	row, steps := h.row(), h.checkpoints()
	if row.CallsUsed != calls || row.PlannedCalls != 14 || row.RecoveryCalls != recovery || row.CorrectionCount != 2 || row.Stage != stage || len(h.seen) != calls {
		h.t.Fatalf("unexpected run counters/stage: %+v provider calls=%v", row, h.seen)
	}
	if !steps[localV3].Published || !strings.Contains(steps[localV3].OutputJSON, "Final measurement.") ||
		!strings.Contains(steps[localV2+":draft"].OutputJSON, "Intermediate measurement.") ||
		steps[localV2+":draft"].Published || steps[localV2].Published || steps["section:background:v1"].Published {
		h.t.Fatal("a draft replaced or obscured the independently reviewed v3")
	}
}

func TestLocalRepairRefusesRepeatedMixedExhaustedAndFourthTargets(t *testing.T) {
	for _, tc := range []struct {
		name               string
		first, second, v3  []GuideFinding
		shared             bool
		calls, corrections int
		rejected           string
	}{
		{"repeated", []GuideFinding{localA}, []GuideFinding{localA}, nil, false, 5, 1, "block:background-condition"},
		{"mixed transaction rolls back new marker", []GuideFinding{localA}, []GuideFinding{localB, localA, localBFollowup}, nil, false, 5, 1, "block:background-condition"},
		{"both already reserved", []GuideFinding{localA, localC}, []GuideFinding{localB}, nil, false, 5, 2, "block:background-condition"},
		{"v3 still has a finding", []GuideFinding{localA}, []GuideFinding{localB, localBFollowup}, []GuideFinding{localC}, false, 7, 2, "block:background-unit"},
		{"shared claim across roles", nil, nil, nil, true, 7, 1, "block:background-condition"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newLocalRepairRun(t)
			h.shared = tc.shared
			h.review = func(sections []GuideSection) []GuideFinding {
				s := sections[0]
				if tc.shared && s.Summary == "Initial draft." {
					return []GuideFinding{{TargetType: "claim", TargetID: "shared", IssueType: "unsupported", Suggestion: "Check the shared claim."}}
				}
				switch s.Summary {
				case "Initial draft.":
					return tc.first
				case "First repair.":
					return tc.second
				default:
					return tc.v3
				}
			}
			err := h.run(t.Context(), testFence{h.service.Repo.DB})
			row, steps := h.row(), h.checkpoints()
			if !errors.Is(err, ErrQuality) || row.Stage != "paused" || row.CallsUsed != tc.calls || len(h.seen) != tc.calls || row.CorrectionCount != tc.corrections {
				t.Fatalf("unauthorized continuation: err=%v row=%+v provider calls=%v", err, row, h.seen)
			}
			if _, ok := steps["correction:"+tc.rejected]; ok {
				t.Fatalf("rejected target left a reservation: %s", tc.rejected)
			}
			if steps[localV3].Published || tc.v3 == nil && steps[localV3+":draft"].ID != "" {
				t.Fatal("published or wrote an unauthorized v3")
			}
			if !tc.shared {
				key, findings := localV2+":review", tc.second
				if tc.v3 != nil {
					key, findings = localV3+":review", tc.v3
				}
				var review GuideReview
				if err := json.Unmarshal([]byte(steps[key].OutputJSON), &review); err != nil || !reflect.DeepEqual(review.Findings, findings) {
					t.Fatalf("substantive findings were lost: %+v %v", review, err)
				}
			}
		})
	}
}

func TestLocalRepairBudgetIsSharedAcrossRoles(t *testing.T) {
	h := newLocalRepairRun(t)
	h.review = func(sections []GuideSection) []GuideFinding {
		s := sections[0]
		if s.Role == RoleBackground || s.Summary != "Initial draft." {
			return nil
		}
		return []GuideFinding{{TargetType: "block", TargetID: s.Blocks[0].ID, IssueType: "coherence", Suggestion: "Explain this measurement."}}
	}
	err := h.run(t.Context(), testFence{h.service.Repo.DB})
	row, steps := h.row(), h.checkpoints()
	if !errors.Is(err, ErrQuality) || row.Stage != "paused" || row.CallsUsed != 13 || row.CorrectionCount != 2 || len(h.seen) != 13 {
		t.Fatalf("global correction budget bypassed: err=%v row=%+v calls=%v", err, row, h.seen)
	}
	if !steps["section:question:v2"].Published || !steps["section:contribution:v2"].Published ||
		steps["correction:block:b_justification"].ID != "" || steps["section:justification:v2:draft"].ID != "" {
		t.Fatal("third role bypassed existing reservations")
	}
}

// Lose the lease just after a real checkpoint commits, before any subsequent
// transaction can write. No test-only checkpoints are inserted.
type localCheckpointFence struct {
	testFence
	h    *localRepairRun
	stop string
}

func (f localCheckpointFence) WithLease(ctx context.Context, cb func(*gorm.DB) error) error {
	if f.h.checkpoints()[f.stop].ID != "" {
		return asyncjob.ErrLeaseLost
	}
	return f.testFence.WithLease(ctx, cb)
}

func TestLocalRepairResumesFromEverySecondTargetCheckpoint(t *testing.T) {
	for _, tc := range []struct {
		stop          string
		providerCalls int
	}{
		{"correction:block:background-condition", 5},
		{localV3 + ":draft", 6},
		{localV3 + ":review", 7},
		{localV3, 7},
	} {
		t.Run(tc.stop, func(t *testing.T) {
			h := newLocalRepairRun(t)
			f := testFence{h.service.Repo.DB}
			err := h.run(t.Context(), localCheckpointFence{f, h, tc.stop})
			before := h.checkpoints()
			if err == nil || before[tc.stop].ID == "" || before["whole_review:1"].ID != "" || len(h.seen) != tc.providerCalls {
				t.Fatalf("did not stop at %s: err=%v calls=%v", tc.stop, err, h.seen)
			}
			h.resume()
			if err := h.run(t.Context(), f); err != nil {
				t.Fatal(err)
			}
			h.assertCompletedLocalChain(14, 0, "completed")
			after := h.checkpoints()
			for key, step := range before {
				if !reflect.DeepEqual(step, after[key]) {
					t.Fatalf("resume rewrote checkpoint %s", key)
				}
			}
			if got := len(after); got != 14+14+4+2 {
				t.Fatalf("duplicate or missing checkpoint after resume: %d", got)
			}
			if tc.stop == localV3+":draft" {
				// Even an exact reservation cannot reuse a draft on different full model input.
				x := execution{s: h.service, scope: h.scope, rev: h.rev, f: f, model: h.model, steps: after}
				changed := maps.Clone(h.v3Input)
				changed["prior_summary"] = "Different leading context."
				var section GuideSection
				if err := x.task(t.Context(), localV3+":draft", "paper.guide.section", changed, &section, func() error { return nil }); !errors.Is(err, ErrConflict) || len(h.seen) != 14 {
					t.Fatalf("changed input reused a checked draft or made a new request: %v", err)
				}
			}
		})
	}
}

func TestLocalRepairKeepsUnknownV3AttemptChargedOnResume(t *testing.T) {
	h := newLocalRepairRun(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := testFence{h.service.Repo.DB}
	h.onV3 = cancel
	if err := h.run(ctx, f); err == nil {
		t.Fatal("interrupted v3 call finished")
	}
	row := h.row()
	if row.CallsUsed != 6 || row.RecoveryCalls != 0 || row.CorrectionCount != 2 || row.ActiveMillis < 120000 || h.checkpoints()[localV3+":draft"].ID != "" {
		t.Fatalf("lost unknown reservation: %+v", row)
	}
	h.onV3 = nil
	h.resume()
	if err := h.run(t.Context(), f); err != nil {
		t.Fatal(err)
	}
	h.assertCompletedLocalChain(15, 1, "completed")
	unknown := 0
	for key, step := range h.checkpoints() {
		if strings.HasPrefix(key, "request:"+localV3+":draft:") && step.OutputJSON == "" {
			unknown++
		}
	}
	if after := h.row(); unknown != 1 || after.ActiveMillis < 120000 {
		t.Fatalf("unknown request refunded: unknown=%d active=%d", unknown, after.ActiveMillis)
	}
}

func TestLocalRepairCancellationFencesLateV3(t *testing.T) {
	h := newLocalRepairRun(t)
	f := testFence{h.service.Repo.DB}
	h.onV3 = func() {
		if err := h.service.Repo.Cancel(t.Context(), h.scope, h.rev.ID); err != nil {
			t.Fatal(err)
		}
	}
	err := h.run(t.Context(), f)
	row, steps := h.row(), h.checkpoints()
	if !errors.Is(err, ErrConflict) || row.Stage != "canceled" || row.CallsUsed != 6 || len(h.seen) != 6 || row.CorrectionCount != 2 ||
		steps[localV3+":draft"].ID != "" || steps[localV3].ID != "" {
		t.Fatalf("late v3 bypassed cancellation: err=%v row=%+v calls=%v", err, row, h.seen)
	}
}

func TestLocalRepairReservationBindsAllFindingsOnReplay(t *testing.T) {
	h := newLocalRepairRun(t)
	x := execution{s: h.service, rev: h.rev, f: testFence{h.service.Repo.DB}}
	findings := []GuideFinding{localB, localBFollowup}
	if err := x.reserveCorrections(t.Context(), localV3, findings, false); err != nil {
		t.Fatal(err)
	}
	before := h.checkpoints()
	changedFirst, changedSecond := localB, localBFollowup
	changedFirst.Suggestion = "Put the sampling condition in the heading."
	changedSecond.Suggestion = "Describe a different interpretation."
	for _, tc := range []struct {
		name, key string
		findings  []GuideFinding
		want      error
	}{
		{"exact", localV3, findings, nil},
		{"changed first", localV3, []GuideFinding{changedFirst, localBFollowup}, ErrQuality},
		{"changed second", localV3, []GuideFinding{localB, changedSecond}, ErrQuality},
		{"omitted second", localV3, []GuideFinding{localB}, ErrQuality},
		{"reordered", localV3, []GuideFinding{localBFollowup, localB}, ErrQuality},
		{"different key", localV2, findings, ErrQuality},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := x.reserveCorrections(t.Context(), tc.key, tc.findings, false); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if !reflect.DeepEqual(before, h.checkpoints()) || h.row().CorrectionCount != 1 {
				t.Fatal("reservation replay changed the target record or count")
			}
		})
	}
}

func TestWholeRepairReservationReplaysExistingMarker(t *testing.T) {
	h := newLocalRepairRun(t)
	repo := h.service.Repo
	f := testFence{repo.DB}
	marker := StepWrite{
		Key: "correction:block:background-condition", InputHash: digest([]any{localV2, localB}), Output: mustJSON(localB),
	}
	if err := repo.PutStep(t.Context(), f, h.rev.ID, h.rev.Epoch, marker); err != nil {
		t.Fatal(err)
	}
	if err := repo.DB.Model(&h.rev).Update("correction_count", 1).Error; err != nil {
		t.Fatal(err)
	}
	before := h.checkpoints()
	x := execution{s: h.service, rev: h.rev, f: f}
	if err := x.reserveCorrections(t.Context(), localV2, []GuideFinding{localB, localBFollowup}, true); err != nil {
		t.Fatalf("saved whole-review repair marker could not be replayed: %v", err)
	}
	if !reflect.DeepEqual(before, h.checkpoints()) || h.row().CorrectionCount != 1 {
		t.Fatal("whole-review replay changed the target record or count")
	}
}
