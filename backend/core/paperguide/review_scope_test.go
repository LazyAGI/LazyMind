package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
)

// A fact omitted by Plan must still reach whole review and its local repair.
func TestIndependentReviewScopeRepairsPlanOmission(t *testing.T) {
	for _, extracted := range []bool{false, true} {
		for _, pause := range []string{"", "plan", "whole", "repair"} {
			t.Run(fmt.Sprintf("extracted=%t/pause=%s", extracted, pause), func(t *testing.T) {
				d, id := fixture(t)
				d.Blocks = []pdfir.Block{{ID: "title", Type: "title", Text: "Study", Page: 0}, {ID: "missing", Type: "text", Text: "The collection process was independently validated.", Page: 0}}
				if extracted {
					for i := 0; i < 40; i++ {
						d.Blocks = append(d.Blocks, pdfir.Block{ID: fmt.Sprint("filler", i), Type: "text", Text: strings.Repeat("Unselected supporting context. ", 80), Page: 0})
					}
				}
				snap, err := BuildSourceSnapshot(d, id)
				if err != nil {
					t.Fatal(err)
				}
				missing := snap.Evidence[1].ID
				repo := Repository{DB: guideDB(t)}
				scope := Scope{id.OwnerID, id.DatasetID, id.DocumentID, "en"}
				model := ModelIdentity{ID: "m", ContextTokens: 128000}
				if extracted {
					model.ContextTokens = 64000
				}
				calls := map[string]int{}
				stop := false
				planSources := ""
				svc := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }}
				svc.Call = func(ctx context.Context, task string, in map[string]any, _ map[string]any) (json.RawMessage, error) {
					key := task
					if task == "paper.guide.section" {
						key += ":" + string(in["role"].(Role))
						if in["findings"] != nil {
							key += ":repair"
						}
					}
					if task == "paper.guide.review" {
						ss := in["sections"].([]GuideSection)
						if len(ss) == 1 {
							key += ":" + string(ss[0].Role)
						} else {
							key += ":whole"
							if len(ss[0].Claims) > 0 {
								key += ":after"
							}
						}
					}
					if !stop && ((pause == "plan" && key == "paper.guide.section:background") || (pause == "whole" && strings.HasSuffix(key, ":repair")) || (pause == "repair" && strings.HasSuffix(key, ":whole:after"))) {
						stop = true
						return nil, context.Canceled
					}
					calls[key]++
					ev := in["evidence"].([]GuideEvidence)
					has := false
					for _, e := range ev {
						if e.ID == missing {
							has = true
						}
					}
					switch task {
					case "paper.guide.extract":
						claims := []GuideClaim{}
						for i, e := range ev {
							if e.ID == missing || e.ID == snap.Evidence[0].ID {
								claims = append(claims, GuideClaim{ID: fmt.Sprint("c", i), Statement: e.Text, Attribution: AttributionPaper, EvidenceIDs: []string{e.ID}, Conditions: []string{}, SupportStatus: SupportSupported})
							}
						}
						return json.Marshal(GuideExtract{Claims: claims, OpenQuestions: []string{}})
					case "paper.guide.plan":
						planSources = digest(ev)
						return json.Marshal(baselinePlan(snap))
					case "paper.guide.section":
						s := teachingSection(in["role"].(Role))
						if in["findings"] != nil {
							if !has || digest(ev) != planSources {
								t.Fatal("repair lost independent planning evidence")
							}
							if digest(in["source_sections"]) != digest(snap.Sections) {
								t.Fatal("repair lost original source directory")
							}
							s.Claims = []GuideClaim{{ID: "background-new", Statement: "The collection process was independently validated.", Attribution: AttributionPaper, EvidenceIDs: []string{missing}, Conditions: []string{}, SupportStatus: SupportSupported}}
							s.ClaimIDs = []string{"background-new"}
							s.Blocks[0].Attribution = AttributionPaper
							s.Blocks[0].ClaimIDs = s.ClaimIDs
							s.Blocks[0].Text = s.Claims[0].Statement
						} else if has {
							t.Fatal("initial section unexpectedly widened")
						}
						return json.Marshal(s)
					case "paper.guide.review":
						ss := in["sections"].([]GuideSection)
						if len(ss) == 1 {
							if has {
								t.Fatal("single review unexpectedly widened")
							}
						} else {
							if !has || digest(ev) != planSources {
								t.Fatal("whole review lost Plan-independent source")
							}
							if extracted && len(in["coverage"].(GuideCoverage).Warnings) == 0 {
								t.Fatal("review scope warning missing")
							}
							if digest(in["source_sections"]) != digest(snap.Sections) {
								t.Fatal("review lost original source directory")
							}
							if len(ss[0].Claims) == 0 {
								return json.Marshal(GuideReview{Findings: []GuideFinding{{TargetType: "block", TargetID: "b_background", IssueType: "missing_explanation", Suggestion: "Explain how validation of the collection process supports reliability."}}})
							}
						}
						return json.Marshal(GuideReview{Findings: []GuideFinding{}})
					}
					panic(task)
				}
				rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test", Model: model})
				if err != nil {
					t.Fatal(err)
				}
				run := func(r orm.PaperGuideRevision) error {
					_, e := svc.Run(t.Context(), asyncjob.Job{ID: r.JobID, PayloadJSON: mustJSON(JobPayload{r.ID, r.Epoch})}, testFence{repo.DB})
					return e
				}
				err = run(rev)
				if pause != "" {
					if err == nil {
						t.Fatal("expected interruption")
					}
					priorSteps, e := repo.Steps(t.Context(), rev.ID)
					if e != nil {
						t.Fatal(e)
					}
					priorExtracts := calls["paper.guide.extract"]
					resumed, e := repo.Resume(t.Context(), scope, rev.ID, model)
					if e != nil {
						t.Fatal(e)
					}
					err = run(resumed)
					if calls["paper.guide.extract"] != priorExtracts {
						t.Fatal("replayed extraction on resume")
					}
					afterSteps, e := repo.Steps(t.Context(), rev.ID)
					if e != nil {
						t.Fatal(e)
					}
					byKey := map[string]orm.PaperGuideStep{}
					for _, step := range afterSteps {
						byKey[step.StepKey] = step
					}
					for _, step := range priorSteps {
						if strings.HasPrefix(step.StepKey, "request:") {
							continue
						}
						after := byKey[step.StepKey]
						if step.InputHash != after.InputHash || step.OutputJSON != after.OutputJSON || step.ReviewJSON != after.ReviewJSON {
							t.Fatalf("mutated checkpoint %s", step.StepKey)
						}
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				for key, n := range calls {
					if key != "paper.guide.extract" && n != 1 {
						t.Fatalf("replayed %s %d", key, n)
					}
				}
				got, e := svc.Revision(t.Context(), scope, rev.ID, 0)
				if e != nil {
					t.Fatal(e)
				}
				if len(got.Sections) != 4 || got.Sections[0].Version != 2 || got.Sections[0].Claims[0].EvidenceIDs[0] != missing {
					t.Fatalf("repair not published: %+v", got.Sections)
				}
				row, e := repo.Revision(t.Context(), scope, rev.ID)
				if e != nil {
					t.Fatal(e)
				}
				if pause == "" && row.CallsUsed != 12+calls["paper.guide.extract"] {
					t.Fatalf("unexpected stage/call expansion: calls=%d", row.CallsUsed)
				}
				if extracted && (calls["paper.guide.extract"] < 2 || len(got.Coverage.Warnings) == 0 || got.Status != "limited") {
					t.Fatal("extracted boundary not disclosed")
				}
				frozen, _ := svc.Source.Read(t.Context(), "test")
				if digest(frozen) != digest(snap) {
					t.Fatal("frozen source mutated")
				}
			})
		}
	}
}

func TestSavedPlanMissingExtractRejectsBeforeCalls(t *testing.T) {
	d, id := fixture(t)
	for i := 0; i < 30; i++ {
		d.Blocks = append(d.Blocks, pdfir.Block{ID: fmt.Sprint("large", i), Type: "text", Text: strings.Repeat("context ", 250), Page: 0})
	}
	snap, _ := BuildSourceSnapshot(d, id)
	x := execution{snap: snap, model: ModelIdentity{ContextTokens: 48000}, steps: map[string]orm.PaperGuideStep{"plan": {OutputJSON: string(mustJSON(baselinePlan(snap)))}}}
	x.s.Call = func(context.Context, string, map[string]any, map[string]any) (json.RawMessage, error) {
		t.Fatal("reconstructed extraction with provider")
		return nil, nil
	}
	_, _, err := x.planInput(t.Context())
	if !errors.Is(err, ErrEvidence) {
		t.Fatalf("missing checkpoint: %v", err)
	}
}

func TestIndependentScopeFinalInputOverflowDoesNotNarrowOrCall(t *testing.T) {
	for _, correction := range []bool{false, true} {
		t.Run(fmt.Sprint(correction), func(t *testing.T) {
			d, id := fixture(t)
			d.Blocks = []pdfir.Block{{ID: "title", Type: "title", Text: "Study", Page: 0}, {ID: "independent", Type: "text", Text: strings.Repeat("Source context. ", 700), Page: 0}}
			snap, _ := BuildSourceSnapshot(d, id)
			repo := Repository{DB: guideDB(t)}
			scope := Scope{id.OwnerID, id.DatasetID, id.DocumentID, "en"}
			model := ModelIdentity{ID: "m", ContextTokens: 40000}
			whole, repair := 0, 0
			svc := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }}
			svc.Call = func(_ context.Context, task string, in map[string]any, _ map[string]any) (json.RawMessage, error) {
				switch task {
				case "paper.guide.extract":
					t.Fatal("silently narrowed independent source")
				case "paper.guide.plan":
					return json.Marshal(baselinePlan(snap))
				case "paper.guide.section":
					if in["findings"] != nil {
						repair++
						t.Fatal("overflow correction reached model")
					}
					s := teachingSection(in["role"].(Role))
					if !correction {
						s.Blocks[0].Text = strings.Repeat("Long explanation. ", 180)
					}
					return json.Marshal(s)
				case "paper.guide.review":
					if len(in["sections"].([]GuideSection)) == 4 {
						whole++
						if !correction {
							t.Fatal("overflow whole review reached model")
						}
						findings := []GuideFinding{}
						for i := 0; i < 48; i++ {
							findings = append(findings, GuideFinding{TargetID: "b_background", TargetType: "block", IssueType: "missing_explanation", Suggestion: strings.Repeat("𐐀", 800)})
						}
						return json.Marshal(GuideReview{Findings: findings})
					}
					return json.Marshal(GuideReview{Findings: []GuideFinding{}})
				}
				return nil, nil
			}
			rev, e := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test", Model: model})
			if e != nil {
				t.Fatal(e)
			}
			_, e = svc.Run(t.Context(), asyncjob.Job{ID: rev.JobID, PayloadJSON: mustJSON(JobPayload{rev.ID, rev.Epoch})}, testFence{repo.DB})
			if !errors.Is(e, ErrContext) {
				t.Fatalf("expected honest final overflow: %v", e)
			}
			want := 0
			if correction {
				want = 1
			}
			if whole != want || repair != 0 {
				t.Fatalf("whole=%d repair=%d", whole, repair)
			}
		})
	}
}
