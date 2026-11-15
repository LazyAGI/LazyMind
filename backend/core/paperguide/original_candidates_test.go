package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"lazymind/core/asyncjob"
	"lazymind/core/pdfir"
)

func TestOriginalCandidatesDiversifyBodyAndRejectSupplementOnlyMaterial(t *testing.T) {
	d, id := fixture(t)
	d.Pages = []pdfir.Page{{Index: 0}, {Index: 1}, {Index: 2}, {Index: 3}, {Index: 4}, {Index: 5}}
	d.Blocks = []pdfir.Block{{ID: "heading", Type: "text", Page: 0, Text: "Introduction"}}
	figure := func(name string, page int, left float64) pdfir.Block {
		return pdfir.Block{ID: name, Type: "chart", Page: page, BBox: []float64{left, .2, left + .04, .5}}
	}
	for i := 0; i < 20; i++ {
		d.Blocks = append(d.Blocks, figure(fmt.Sprintf("crowded-%d", i), 0, float64(i)/24))
	}
	d.Blocks = append(d.Blocks,
		pdfir.Block{ID: "body", Type: "text", Page: 2, Text: "Results"},
		figure("late", 2, .1), figure("next", 3, .1),
		pdfir.Block{ID: "page-fallback", Type: "chart", Page: 3},
		pdfir.Block{ID: "refs", Type: "text", Page: 4, Text: "References"},
		figure("references-figure", 4, .1),
		pdfir.Block{ID: "annex", Type: "text", Page: 5, Text: "Appendix"},
		figure("appendix-figure", 5, .1),
		pdfir.Block{ID: "appendix-only-equation", Type: "formula", Page: 5, Latex: "z=x+y", BBox: []float64{.1, .1, .2, .2}},
	)
	contained := []string{}
	d.Precision = &pdfir.SourcePrecision{Version: 3, SourceSHA256: id.SourceSHA256, BaseIRDigest: pdfir.Digest("synthetic-base"), ArchiveDigest: pdfir.Digest("synthetic-archive"), PageOnlyBlockIDs: &contained}
	var proofs []pdfir.OriginalFigureProof
	for _, b := range d.Blocks {
		if b.Type == "chart" && len(b.BBox) == 4 {
			proofs = append(proofs, pdfir.OriginalFigureProof{BlockID: b.ID, Type: b.Type, Page: b.Page, Parts: [][]float64{b.BBox}})
		}
	}
	s, err := BuildSourceSnapshotWithFigureProofs(d, id, proofs)
	if err != nil {
		t.Fatal(err)
	}
	candidates := selectOriginalAssets(s, nil)
	blocks := map[string]string{}
	for _, asset := range s.Assets {
		blocks[asset.ID] = asset.BlockID
	}
	var selected []string
	for _, c := range candidates.Figure {
		selected = append(selected, blocks[c.ID]+":"+c.Mode)
	}
	if strings.Join(selected, ",") != "crowded-0:region,late:region,next:region,page-fallback:page" || len(candidates.Formula) != 0 {
		t.Fatalf("unexpected original set: %v; %v", selected, candidates.Formula)
	}
	s.originalCandidates = &candidates
	p := baselinePlan(s)
	page := candidates.Figure[3].ID
	p.RequiredAssets = []GuideAssetDecision{{page, true, "Show the original page."}}
	p.Sections[0].AssetIDs = []string{page}
	if err := ValidatePlan(s, p); err == nil || !strings.Contains(err.Error(), "region missing") {
		t.Fatalf("page-only asset replaced a region minimum: %v", err)
	}
	region := candidates.Figure[1].ID
	p.Sections[0].AssetIDs = append(p.Sections[0].AssetIDs, region)
	p.RequiredAssets = append(p.RequiredAssets, GuideAssetDecision{region, true, "Explain this figure."})
	if err := ValidatePlan(s, p); err != nil {
		t.Fatal(err)
	}
	candidates.Figure[1].Mode = "page" // model input cannot falsify the frozen display contract.
	if err := ValidatePlan(s, p); err == nil || !strings.Contains(err.Error(), "provenance") {
		t.Fatalf("bad mode accepted: %v", err)
	}
}

func TestSectionRequiresOneExplicitlySourcedBlockPerPlannedOriginal(t *testing.T) {
	d, id := fixture(t)
	d.Blocks = []pdfir.Block{
		{ID: "figure", Type: "chart", Page: 0},
		{ID: "formula", Type: "formula", Page: 0, Latex: "x^2"},
	}
	s, err := BuildSourceSnapshot(d, id)
	if err != nil {
		t.Fatal(err)
	}
	figure, formula := s.Assets[0].ID, s.Assets[1].ID
	p := baselinePlan(s)
	p.Sections[0].AssetIDs = []string{figure, formula}
	for _, test := range []struct {
		name   string
		change func(*GuideSection)
		want   string
	}{
		{"complete", func(*GuideSection) {}, ""},
		{"only cited in prose", func(section *GuideSection) { section.Blocks[1].Kind = "paragraph"; section.Blocks[1].AssetID = "" }, "missing planned original"},
		{"one repeat", func(section *GuideSection) {
			repeated := section.Blocks[0]
			repeated.ID = "duplicate"
			section.Blocks = append(section.Blocks, repeated)
		}, "duplicate original"},
		{"wrong source", func(section *GuideSection) { section.Blocks[0].EvidenceIDs = []string{formula} }, "missing self evidence"},
		{"wrong kind", func(section *GuideSection) { section.Blocks[0].Kind = "formula" }, "wrong or unplanned asset"},
	} {
		t.Run(test.name, func(t *testing.T) {
			section := teachingSection(RoleBackground)
			section.Blocks[0].Kind, section.Blocks[0].AssetID, section.Blocks[0].EvidenceIDs = "figure", figure, []string{figure}
			equation := section.Blocks[0]
			equation.ID, equation.Kind, equation.AssetID, equation.EvidenceIDs = "b-formula", "formula", formula, []string{formula}
			section.Blocks = append(section.Blocks, equation)
			test.change(&section)
			err := ValidateSection(s, p, RoleBackground, section)
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("expected %q: %v", test.want, err)
			}
		})
	}
}

func TestRunnerRejectsOriginalDropAcrossDraftAndRepairs(t *testing.T) {
	for _, tc := range []struct {
		mode    string
		calls   int
		publish bool
	}{{"initial", 3, false}, {"local", 5, false}, {"whole", 12, true}, {"retain", 12, true}} {
		t.Run(tc.mode, func(t *testing.T) {
			d, id := fixture(t)
			d.Blocks = []pdfir.Block{
				{ID: "body", Type: "text", Page: 0, Text: "This paper compares a measured sample."},
				{ID: "fig", Type: "chart", Page: 0},
				{ID: "eq", Type: "formula", Page: 0, Latex: "x=y"},
			}
			snap, err := BuildSourceSnapshot(d, id)
			if err != nil {
				t.Fatal(err)
			}
			figure, formula := snap.Assets[0].ID, snap.Assets[1].ID
			p := baselinePlan(snap)
			p.Sections[0].AssetIDs = []string{figure, formula}
			p.RequiredAssets = []GuideAssetDecision{{figure, true, "Show the original comparison."}, {formula, true, "Show the original equation."}}
			repo := Repository{DB: guideDB(t)}
			scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "en"}
			model := ModelIdentity{ID: "synthetic", ContextTokens: 128000}
			calls := 0
			service := Service{
				Repo:         repo,
				Source:       &fixtureSource{s: snap},
				Authorize:    func(context.Context, Scope, bool) error { return nil },
				ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil },
				Call: func(_ context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
					calls++
					switch task {
					case "paper.guide.plan":
						return mustJSON(p), nil
					case "paper.guide.section":
						section := teachingSection(input["role"].(Role))
						if section.Role != RoleBackground {
							return mustJSON(section), nil
						}
						correction := input["findings"] != nil
						if correction {
							section.Summary = "Corrected measurement."
						}
						for i, asset := range []struct{ id, kind string }{{figure, "figure"}, {formula, "formula"}} {
							block := section.Blocks[0]
							if i == 1 && (tc.mode == "initial" || correction && tc.mode != "retain") {
								// A revision still cites the equation in prose.
								section.Blocks[0].EvidenceIDs = append(section.Blocks[0].EvidenceIDs, asset.id)
								continue
							}
							block.ID, block.Kind, block.AssetID, block.EvidenceIDs = "original-"+asset.kind, asset.kind, asset.id, []string{asset.id}
							section.Blocks = append(section.Blocks, block)
						}
						return mustJSON(section), nil
					case "paper.guide.review":
						sections := input["sections"].([]GuideSection)
						if (tc.mode == "local" && len(sections) == 1 || (tc.mode == "whole" || tc.mode == "retain") && len(sections) == 4) &&
							sections[0].Role == RoleBackground && sections[0].Summary != "Corrected measurement." {
							return mustJSON(GuideReview{Findings: []GuideFinding{{TargetType: "block", TargetID: "b_background", IssueType: "coherence", Suggestion: "Clarify the measurement."}}}), nil
						}
						return mustJSON(GuideReview{Findings: []GuideFinding{}}), nil
					default:
						return nil, fmt.Errorf("unexpected task: %s", task)
					}
				},
			}
			rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test", Model: model})
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Run(t.Context(), asyncjob.Job{ID: rev.JobID, PayloadJSON: mustJSON(JobPayload{rev.ID, rev.Epoch})}, testFence{repo.DB})
			if tc.mode == "retain" && err != nil || tc.mode != "retain" && !errors.Is(err, ErrEvidence) || calls != tc.calls {
				t.Fatalf("unexpected runner result: %v, calls=%d want %d", err, calls, tc.calls)
			}
			loaded, err := service.Revision(t.Context(), scope, rev.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.publish {
				if len(loaded.Sections) != 0 {
					t.Fatal("invalid draft was published")
				}
				return
			}
			section := loaded.Sections[0]
			wantVersion := 1
			if tc.mode == "retain" {
				wantVersion = 2
			}
			if section.Version != wantVersion || !strings.Contains(string(mustJSON(section.Blocks)), formula) ||
				section.Blocks[2].AssetID != formula || section.Blocks[1].AssetID != figure {
				t.Fatalf("valid original publication was lost: %+v", section)
			}
		})
	}
}

func TestOriginalCandidatesPrioritizeRetainedAssociationOverUnusedPage(t *testing.T) {
	s := GuideSourceSnapshot{}
	for i := 0; i < 5; i++ {
		page := min(i, 3)
		id := fmt.Sprintf("figure-%d", i)
		s.Evidence = append(s.Evidence, GuideEvidence{ID: id, Locator: GuideLocator{Page: page}})
		s.Assets = append(s.Assets, GuideAsset{ID: id, Kind: "figure", OriginalDisplay: &GuideOriginalDisplay{Page: page, Kind: "figure", BBox: []float64{.1, .1, .2, .2}}})
		if i < 3 {
			s.Sections = append(s.Sections, SourceSection{Kind: "page_group", EvidenceIDs: []string{id}})
		}
	}
	s.Evidence = append(s.Evidence, GuideEvidence{ID: "reported", SourceBlockID: "paragraph", Text: "The results use a second comparison.", Locator: GuideLocator{Page: 3}})
	s.Assets[4].RelatedBlocks = []pdfir.TableRelation{{BlockID: "paragraph", Relation: "mention"}}
	s.Sections = append(s.Sections, SourceSection{Kind: "section", EvidenceIDs: []string{"figure-3", "figure-4", "reported"}})
	got := selectOriginalAssets(s, map[string]bool{"figure-3": true, "reported": true})
	if len(got.Figure) != 4 || got.Figure[0].ID != "figure-3" || got.Figure[1].ID != "figure-4" {
		t.Fatalf("a retained figure association lost its slot to unclassified pages: %+v", got.Figure)
	}
}

func TestOriginalCandidatesReserveFormulaSlotForAnotherBodyPage(t *testing.T) {
	s := GuideSourceSnapshot{}
	retained := map[string]bool{"explanation": true}
	early := SourceSection{Kind: "section"}
	const late = "later-formula"
	for i := range maxOriginalFormulas {
		id := fmt.Sprintf("early-formula-%d", i)
		early.EvidenceIDs = append(early.EvidenceIDs, id)
		retained[id] = true
		s.Evidence = append(s.Evidence, GuideEvidence{ID: id, Locator: GuideLocator{Page: 0}})
		s.Assets = append(s.Assets, GuideAsset{ID: id, Kind: "formula", OriginalDisplay: &GuideOriginalDisplay{Kind: "formula", Page: 0, BBox: []float64{.1, .1, .2, .2}}})
	}
	s.Sections = []SourceSection{early, {Kind: "section", EvidenceIDs: []string{late, "explanation"}}}
	s.Evidence = append(s.Evidence, GuideEvidence{ID: late, Locator: GuideLocator{Page: 9}}, GuideEvidence{ID: "explanation", Text: "This explains the later result.", Locator: GuideLocator{Page: 9}})
	s.Assets = append(s.Assets, GuideAsset{ID: late, Kind: "formula", OriginalDisplay: &GuideOriginalDisplay{Kind: "formula", Page: 9, BBox: []float64{.1, .1, .2, .2}}})
	got := selectOriginalAssets(s, retained)
	if len(got.Formula) != maxOriginalFormulas || got.Formula[1].ID != late {
		t.Fatalf("early citations crowded out a distinct body page: %+v", got.Formula)
	}
}
