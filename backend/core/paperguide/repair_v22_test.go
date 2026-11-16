package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
)

func findingFor(s GuideSection) GuideFinding {
	return GuideFinding{TargetType: "block", TargetID: s.Blocks[0].ID, IssueType: "coherence", Suggestion: "Explain the measurement."}
}

func TestV22RepairSendsAllOrderedFindingsForOneReservation(t *testing.T) {
	h := newLocalRepairRun(t)
	h.requireFinalBackground = false
	want := []GuideFinding{localC, localB, localA, localBFollowup}
	h.review = func(sections []GuideSection) []GuideFinding {
		if len(sections) == 1 && sections[0].Role == RoleBackground && sections[0].Summary == "Initial draft." {
			return want
		}
		return nil
	}
	var received []GuideFinding
	call := h.service.Call
	h.service.Call = func(ctx context.Context, task string, input map[string]any, cfg map[string]any) (json.RawMessage, error) {
		if findings, ok := input["findings"].([]GuideFinding); ok {
			received = append(received, findings...)
			if h.row().CorrectionCount != 1 || h.checkpoints()["repair-reservation:"+localV2].ID == "" || h.checkpoints()[localV2].Published {
				t.Fatal("repair dispatched without a committed reservation or before review")
			}
		}
		return call(ctx, task, input, cfg)
	}
	if err := h.run(t.Context(), testFence{h.service.Repo.DB}); err != nil {
		t.Fatal(err)
	}
	steps := h.checkpoints()
	if !reflect.DeepEqual(received, want) || h.row().CallsUsed != 12 || h.row().CorrectionCount != 1 || !steps[localV2].Published || steps["section:background:v1"].Published {
		t.Fatalf("repair/review did not retain the full batch: findings=%+v calls=%v", received, h.seen)
	}
	for _, f := range want {
		if steps["correction:"+f.TargetType+":"+f.TargetID].ID == "" {
			t.Fatalf("target ownership missing for %s", f.TargetID)
		}
	}
}

func TestV22UnknownRequestRejectsChangedInputAcrossEpoch(t *testing.T) {
	h := newLocalRepairRun(t)
	h.requireFinalBackground = false
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := testFence{h.service.Repo.DB}
	h.onV3 = cancel
	if err := h.run(ctx, f); err == nil {
		t.Fatal("expected the real v3 attempt to stop before being settled")
	}
	h.onV3 = nil
	h.resume()
	before, calls := h.checkpoints(), h.row().CallsUsed
	found := false
	for key, step := range before {
		if strings.HasPrefix(key, "request:"+localV3+":draft:") && step.OutputJSON == "" {
			found = true
		}
	}
	if !found {
		t.Fatal("no real unknown request to recover")
	}
	x := execution{s: h.service, scope: h.scope, rev: h.rev, f: f, model: h.model, steps: before}
	changed := maps.Clone(h.v3Input)
	changed["prior_summary"] = "New context cannot create a second first attempt."
	var section GuideSection
	err := x.task(t.Context(), localV3+":draft", "paper.guide.section", changed, &section, func() error { return nil })
	if !errors.Is(err, ErrConflict) || h.row().CallsUsed != calls || len(h.seen) != calls || !reflect.DeepEqual(before, h.checkpoints()) {
		t.Fatalf("input drift reached a marker or dispatch: err=%v calls=%v", err, h.seen)
	}
}

func TestV22WholeRepairResumesExactBindings(t *testing.T) {
	const questionV2 = "section:question:v2"
	for _, tc := range []struct {
		stop             string
		corrupt          string
		calls, wantCalls int
	}{
		{localV2 + ":draft", "", 11, 13},
		{"whole_review:2", "", 13, 13},
		{localV2, "", 13, 13},
		{localV2, questionV2 + ":draft", 13, 13},
		{localV2, "whole_review:2", 13, 13},
		{localV2, "plan", 13, 13},
		{localV2, "missing-whole-2", 13, 13},
	} {
		t.Run(tc.stop+"/"+tc.corrupt, func(t *testing.T) {
			h := newLocalRepairRun(t)
			h.requireFinalBackground = false
			h.review = func(sections []GuideSection) []GuideFinding {
				if len(sections) == 4 && sections[0].Summary == "Initial draft." {
					return []GuideFinding{findingFor(sections[1]), localB, localBFollowup}
				}
				return nil
			}
			provider := h.service.Call
			h.service.Call = func(ctx context.Context, task string, input map[string]any, cfg map[string]any) (json.RawMessage, error) {
				if findings, ok := input["findings"].([]GuideFinding); ok {
					want := []GuideFinding{localB, localBFollowup}
					if input["role"] == RoleQuestion {
						want = []GuideFinding{{TargetType: "block", TargetID: "b_question", IssueType: "coherence", Suggestion: "Explain the measurement."}}
					}
					if !reflect.DeepEqual(findings, want) {
						t.Fatalf("whole repair lost or reordered a role's findings: got %+v want %+v", findings, want)
					}
				}
				return provider(ctx, task, input, cfg)
			}
			repo := h.service.Repo
			f := testFence{repo.DB}
			if err := h.run(t.Context(), localCheckpointFence{f, h, tc.stop}); err == nil || len(h.seen) != tc.calls {
				t.Fatalf("did not stop at %s: %v, calls=%v", tc.stop, err, h.seen)
			}
			before := h.checkpoints()
			if tc.stop == localV2 && (!before[localV2].Published || before[questionV2].Published) {
				t.Fatal("expected only the first whole repair to be published")
			}
			h.resume()
			switch tc.corrupt {
			case questionV2 + ":draft", "plan", "missing-whole-2":
				key := tc.corrupt
				if key == "missing-whole-2" {
					key = "whole_review:2"
				}
				if err := repo.DB.Delete(&orm.PaperGuideStep{}, "id = ?", before[key].ID).Error; err != nil {
					t.Fatal(err)
				}
				delete(before, key)
			case "whole_review:2":
				step := before[tc.corrupt]
				var bindings []reviewedSection
				if err := json.Unmarshal([]byte(step.ReviewJSON), &bindings); err != nil {
					t.Fatal(err)
				}
				bindings[1] = reviewedSection{Key: "section:question:v1", Version: 1}
				step.ReviewJSON = string(mustJSON(bindings))
				if err := repo.DB.Model(&step).Update("review_json", step.ReviewJSON).Error; err != nil {
					t.Fatal(err)
				}
				before[tc.corrupt] = step
			}
			err := h.run(t.Context(), f)
			after := h.checkpoints()
			if len(h.seen) != tc.wantCalls || h.row().CallsUsed != tc.wantCalls {
				t.Fatalf("whole work repeated or omitted: %v calls=%v", err, h.seen)
			}
			if tc.corrupt != "" {
				if !(errors.Is(err, ErrEvidence) || errors.Is(err, ErrConflict)) || !reflect.DeepEqual(before, after) {
					t.Fatalf("unbound whole repair was promoted or rewritten: %v", err)
				}
				return
			}
			if err != nil || h.row().Stage != "completed" || h.row().CorrectionCount != 2 || !after[localV2].Published || !after[questionV2].Published {
				t.Fatalf("whole review did not complete original bounded repairs: %v row=%+v", err, h.row())
			}
			for key, step := range before {
				if !reflect.DeepEqual(step, after[key]) {
					t.Fatalf("resume rewrote immutable checkpoint %s", key)
				}
			}
			if tc.stop == localV2 && len(after) != len(before)+1 {
				t.Fatalf("partial promotion rewrote extra checkpoints: before=%d after=%d", len(before), len(after))
			}
		})
	}
}

func TestV22RecoveryBudgetOnCompleteRepairPaths(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		extracted, mixed, thirdRecovery    bool
		planned, extracts, recovery, total int
	}{
		{"local-v2-v3-direct", false, false, false, 14, 0, 2, 16},
		{"local-then-whole-with-eight-extracts", true, true, false, 14, 8, 2, 24},
		{"third-recovery-stops-before-network", false, false, true, 5, 0, 2, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var h *localRepairRun
			if tc.extracted {
				doc, id := fixture(t)
				doc.Blocks = nil
				for i := 0; i < tc.extracts; i++ {
					doc.Blocks = append(doc.Blocks,
						pdfir.Block{ID: fmt.Sprintf("retained-%d", i), Type: "text", Page: 0, Text: "The source records the condition."},
						pdfir.Block{ID: fmt.Sprintf("filler-%d", i), Type: "text", Page: 0, Text: strings.Repeat("Supplementary material. ", 490)})
				}
				snap, err := BuildSourceSnapshot(doc, id)
				if err != nil {
					t.Fatal(err)
				}
				h = newLocalRepairRunWithSource(t, snap, 40000)
			} else {
				h = newLocalRepairRun(t)
			}
			if tc.mixed {
				h.requireFinalBackground = false
				h.review = func(sections []GuideSection) []GuideFinding {
					switch {
					case len(sections) == 1 && sections[0].Role == RoleBackground && sections[0].Summary == "Initial draft.":
						return []GuideFinding{localA}
					case len(sections) == 4 && sections[1].Summary == "Initial draft.":
						return []GuideFinding{findingFor(sections[1])}
					}
					return nil
				}
			}
			provider := h.service.Call
			bad := map[string]bool{"plan": true, "repair": true, "review": tc.thirdRecovery}
			retained := map[string]bool{}
			h.service.Call = func(ctx context.Context, task string, input map[string]any, cfg map[string]any) (json.RawMessage, error) {
				checkpoint := ""
				switch task {
				case "paper.guide.extract":
					h.seen = append(h.seen, "extract")
					var claims []GuideClaim
					for _, e := range input["evidence"].([]GuideEvidence) {
						if strings.HasPrefix(e.SourceBlockID, "retained-") {
							claims = append(claims, GuideClaim{ID: e.SourceBlockID, Statement: e.Text, Attribution: AttributionPaper, EvidenceIDs: []string{e.ID}, Conditions: []string{}, SupportStatus: SupportSupported})
							retained[e.ID] = true
						}
					}
					return mustJSON(GuideExtract{Claims: claims, OpenQuestions: []string{}}), nil
				case "paper.guide.plan":
					if tc.extracted {
						for _, e := range input["evidence"].([]GuideEvidence) {
							if !retained[e.ID] {
								t.Fatal("the reduced packet lost extraction provenance")
							}
						}
					}
					checkpoint = "plan"
				case "paper.guide.section":
					if input["role"] == RoleBackground && reflect.DeepEqual(input["findings"], []GuideFinding{localA}) {
						checkpoint = "repair"
					}
				case "paper.guide.review":
					ss := input["sections"].([]GuideSection)
					if len(ss) == 1 && ss[0].Role == RoleBackground && ss[0].Summary == "First repair." {
						checkpoint = "review"
					}
				}
				if bad[checkpoint] {
					bad[checkpoint] = false
					h.seen = append(h.seen, "malformed:"+checkpoint)
					return json.RawMessage("not-json"), nil
				}
				return provider(ctx, task, input, cfg)
			}
			err := h.run(t.Context(), testFence{h.service.Repo.DB})
			row, steps := h.row(), h.checkpoints()
			if row.CallsUsed != tc.total || len(h.seen) != tc.total || row.PlannedCalls != tc.planned || row.ExtractCalls != tc.extracts || row.RecoveryCalls != tc.recovery {
				t.Fatalf("wrong call budget: counts=%+v calls=%v err=%v", row, h.seen, err)
			}
			if tc.thirdRecovery {
				if !errors.Is(err, ErrBudget) || row.Stage != "paused" || row.CorrectionCount != 1 || steps[localV2].Published || steps[localV2+":review"].ID != "" {
					t.Fatalf("third recovery spent or published: %v row=%+v", err, row)
				}
				return
			}
			if err != nil || row.Stage != "completed" || row.CorrectionCount != 2 {
				t.Fatalf("expected successful bounded repair: %v row=%+v", err, row)
			}
			if tc.mixed {
				var bindings []reviewedSection
				want := []reviewedSection{{localV2, 2}, {"section:question:v2", 2}, {"section:contribution:v1", 1}, {"section:justification:v1", 1}}
				if json.Unmarshal([]byte(steps["whole_review:2"].ReviewJSON), &bindings) != nil || !reflect.DeepEqual(bindings, want) || !steps[want[1].Key].Published {
					t.Fatalf("mixed whole review did not bind exact replacements: %+v", bindings)
				}
			} else {
				h.assertCompletedLocalChain(16, 2, "completed")
			}
		})
	}
}

func TestV22RepairReplaysOnlyAnIntactLedgerAndReview(t *testing.T) {
	for _, defect := range []string{"findings reordered", "finding removed", "finding added", "finding rewritten", "base changed", "target missing", "target legacy format", "count disagrees", "mode changed"} {
		t.Run(defect, func(t *testing.T) {
			h := newLocalRepairRun(t)
			h.requireFinalBackground = false
			h.review = func(sections []GuideSection) []GuideFinding {
				if len(sections) == 1 && sections[0].Role == RoleBackground && sections[0].Summary == "Initial draft." {
					return []GuideFinding{localB, localBFollowup}
				}
				return nil
			}
			repo := h.service.Repo
			f := testFence{repo.DB}
			reservationKey := "repair-reservation:" + localV2
			if err := h.run(t.Context(), localCheckpointFence{f, h, reservationKey}); err == nil || len(h.seen) != 3 {
				t.Fatalf("expected interruption between real reservation and dispatch: %v calls=%v", err, h.seen)
			}
			h.resume()
			steps := h.checkpoints()
			replace := func(key, field string, value any) {
				t.Helper()
				step := steps[key]
				if err := repo.DB.Model(&step).Update(field, value).Error; err != nil {
					t.Fatal(err)
				}
			}
			switch defect {
			case "findings reordered", "finding removed", "finding added", "finding rewritten":
				findings := []GuideFinding{localB, localBFollowup}
				switch defect {
				case "findings reordered":
					findings[0], findings[1] = findings[1], findings[0]
				case "finding removed":
					findings = findings[:1]
				case "finding added":
					findings = append(findings, localC)
				case "finding rewritten":
					findings[1].Suggestion = "Explain where a similar condition is found."
				}
				replace("section:background:v1:review", "output_json", string(mustJSON(GuideReview{Findings: findings})))
			case "base changed":
				key := "section:background:v1:draft"
				replace(key, "output_json", strings.Replace(steps[key].OutputJSON, "The measured sample.", "The original sample.", 1))
			case "target missing":
				if err := repo.DB.Delete(&orm.PaperGuideStep{}, "id = ?", steps["correction:block:background-condition"].ID).Error; err != nil {
					t.Fatal(err)
				}
			case "target legacy format":
				replace("correction:block:background-condition", "output_json", string(mustJSON(localB)))
			case "count disagrees":
				if err := repo.DB.Model(&h.rev).Update("correction_count", 2).Error; err != nil {
					t.Fatal(err)
				}
			case "mode changed":
				var reservation repairReservation
				if err := json.Unmarshal([]byte(steps[reservationKey].OutputJSON), &reservation); err != nil {
					t.Fatal(err)
				}
				reservation.Mode = "whole"
				replace(reservationKey, "output_json", string(mustJSON(reservation)))
				replace(reservationKey, "input_hash", digest(reservation))
				replace("correction:block:background-condition", "input_hash", digest(reservation))
			}
			before, count := h.checkpoints(), h.row().CorrectionCount
			err := h.run(t.Context(), f)
			if !(errors.Is(err, ErrQuality) || errors.Is(err, ErrConflict) || errors.Is(err, ErrEvidence)) ||
				h.row().CorrectionCount != count || h.row().CallsUsed != 3 || len(h.seen) != 3 || !reflect.DeepEqual(before, h.checkpoints()) {
				t.Fatalf("corrupt resume rewrote the ledger or invoked the model: %v calls=%v", err, h.seen)
			}
		})
	}
}

func TestV22MaximumFindingBatchIsNeverTruncatedForContext(t *testing.T) {
	for _, window := range []int{40000, 262144} {
		t.Run(fmt.Sprint(window), func(t *testing.T) {
			doc, id := fixture(t)
			snap, err := BuildSourceSnapshot(doc, id)
			if err != nil {
				t.Fatal(err)
			}
			h := newLocalRepairRunWithSource(t, snap, window)
			h.requireFinalBackground = false
			findings := make([]GuideFinding, 48)
			for i := range findings {
				findings[i] = GuideFinding{TargetType: localA.TargetType, TargetID: localA.TargetID, IssueType: localA.IssueType, Suggestion: fmt.Sprintf("%02d", i) + strings.Repeat("界", 798)}
			}
			h.review = func(sections []GuideSection) []GuideFinding {
				if len(sections) == 1 && sections[0].Role == RoleBackground && sections[0].Summary == "Initial draft." {
					return findings
				}
				return nil
			}
			base := h.service.Call
			writes := 0
			h.service.Call = func(ctx context.Context, task string, input map[string]any, cfg map[string]any) (json.RawMessage, error) {
				if f, ok := input["findings"].([]GuideFinding); ok {
					writes++
					if !reflect.DeepEqual(f, findings) {
						t.Fatal("maximum ordered batch changed before dispatch")
					}
				}
				return base(ctx, task, input, cfg)
			}
			err = h.run(t.Context(), testFence{h.service.Repo.DB})
			if h.row().CorrectionCount != 1 {
				t.Fatalf("maximum batch lost its reservation: %v", err)
			}
			if window == 40000 {
				if !errors.Is(err, ErrContext) || writes != 0 || h.row().CallsUsed != 3 {
					t.Fatalf("oversized batch trimmed or dispatched: %v calls=%v", err, h.seen)
				}
			} else if err != nil || writes != 1 || !h.checkpoints()[localV2].Published {
				t.Fatalf("maximum batch that fits should be reviewed and published: %v calls=%v", err, h.seen)
			}
		})
	}
}

func TestV22RepairFencesLateResponses(t *testing.T) {
	for _, tc := range []struct {
		change string
		want   error
	}{
		{"source", ErrSourceChanged},
		{"model", ErrModelChanged},
		{"epoch", ErrConflict},
	} {
		t.Run(tc.change, func(t *testing.T) {
			h := newLocalRepairRun(t)
			repo := h.service.Repo
			origEpoch := h.rev.Epoch
			h.onV3 = func() {
				switch tc.change {
				case "source":
					h.service.Source.(*fixtureSource).s.Identity.SourceSHA256 = "changed-source"
				case "model":
					h.model.ParametersHash = "changed-model"
				case "epoch":
					if err := repo.Cancel(t.Context(), h.scope, h.rev.ID); err != nil {
						t.Fatal(err)
					}
					h.resume()
				}
			}
			err := h.run(t.Context(), testFence{repo.DB})
			row, steps := h.row(), h.checkpoints()
			if !errors.Is(err, tc.want) || row.CorrectionCount != 2 || row.CallsUsed != 6 || len(h.seen) != 6 ||
				steps["repair-reservation:"+localV3].ID == "" || steps[localV3+":draft"].ID != "" || steps[localV3].Published {
				t.Fatalf("late repair escaped fence: %v row=%+v calls=%v", err, row, h.seen)
			}
			if tc.change == "epoch" && (row.Epoch == origEpoch || row.Stage != "queued") {
				t.Fatalf("old execution touched the new epoch: %+v", row)
			}
		})
	}
}

func TestV22AttemptBudgetHardGuardsBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, task               string
		calls, planned, extracts int
	}{
		{"fifteenth planned", "paper.guide.plan", 14, 14, 0},
		{"ninth extract", "paper.guide.extract", 8, 0, 8},
		// Counters deliberately isolate the total guard from either per-kind guard.
		{"twenty-fifth overall", "paper.guide.plan", 24, 13, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newLocalRepairRun(t)
			repo := h.service.Repo
			if err := repo.DB.Model(&h.rev).Updates(map[string]any{"calls_used": tc.calls, "planned_calls": tc.planned, "extract_calls": tc.extracts}).Error; err != nil {
				t.Fatal(err)
			}
			snap := h.service.Source.(*fixtureSource).s
			x := execution{s: h.service, scope: h.scope, rev: h.rev, model: h.model, snap: snap, f: testFence{repo.DB}, steps: h.checkpoints()}
			var out any
			err := x.task(t.Context(), "budget-probe", tc.task, x.input(snap), &out, func() error { return nil })
			row := h.row()
			if !errors.Is(err, ErrBudget) || len(h.seen) != 0 || len(h.checkpoints()) != 0 || row.CallsUsed != tc.calls ||
				row.PlannedCalls != tc.planned || row.ExtractCalls != tc.extracts {
				t.Fatalf("over-limit invocation acquired a marker or dispatched: %v row=%+v calls=%v", err, row, h.seen)
			}
		})
	}
}
