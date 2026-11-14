package paperguide

import (
	"context"
	"encoding/json"
	"fmt"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Explicit offline admission measurement: authorized DEV corpus and an
// independently exported model-limit allowlist, without DB/provider requests.
func TestPhysicalTableCorpusAdmission(t *testing.T) {
	root := os.Getenv("LAZYMIND_PRECISION_CORPUS_ROOT")
	metadata := os.Getenv("LAZYMIND_GUIDE_BUDGET_METADATA")
	if root == "" || metadata == "" {
		t.Skip("offline source and budget metadata required")
	}
	raw, e := os.ReadFile(filepath.Join(root, ".superpowers/sdd/实施与验收/corpus-ready.json"))
	if e != nil {
		t.Fatal(e)
	}
	var corpus map[string]struct {
		Document string `json:"document_id"`
		IR       string `json:"ir_path"`
		Hash     string `json:"source_sha256"`
	}
	if e = json.Unmarshal(raw, &corpus); e != nil {
		t.Fatal(e)
	}
	raw, e = os.ReadFile(metadata)
	if e != nil {
		t.Fatal(e)
	}
	var limits struct {
		Models []struct {
			Name    string  `json:"name"`
			Context string  `json:"max_input_tokens"`
			Output  *string `json:"max_output_tokens"`
		} `json:"models"`
	}
	if e = json.Unmarshal(raw, &limits); e != nil {
		t.Fatal(e)
	}
	archives := map[string]string{}
	e = filepath.WalkDir(filepath.Join(root, "data/core/uploads"), func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() && d.Name() == "mineru-result.zip" {
			for k, x := range corpus {
				if strings.Contains(path, string(filepath.Separator)+x.Document+string(filepath.Separator)) {
					archives[k] = path
				}
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	for key, item := range corpus {
		t.Run(key, func(t *testing.T) {
			data, e := os.ReadFile(item.IR)
			if e != nil {
				t.Fatal(e)
			}
			var base pdfir.Document
			if e = json.Unmarshal(data, &base); e != nil {
				t.Fatal(e)
			}
			zip, e := os.ReadFile(archives[key])
			if e != nil {
				t.Fatal(e)
			}
			d, e := pdfir.ProjectSourcePrecision(base, zip)
			if e != nil {
				t.Fatal(e)
			}
			snap, e := BuildSourceSnapshot(d, SourceIdentity{SourceSHA256: item.Hash, OwnerID: "offline", DatasetID: "offline", DocumentID: item.Document})
			if e != nil {
				t.Fatal(e)
			}
			// Frozen v4 catalog semantics over the same complete payload provide a
			// locator/count control; this is not a replay of historical region IDs.
			previous := d
			precision := *d.Precision
			precision.Version, precision.PageOnlyBlockIDs = 2, nil
			previous.Precision = &precision
			old, e := buildSourceSnapshot(previous, snap.Identity, "paper-guide-source-v4")
			if e != nil {
				t.Fatal(e)
			}
			if len(old.Evidence) != len(snap.Evidence) || len(old.Assets) != len(snap.Assets) || len(old.Sections) != len(snap.Sections) {
				t.Fatal("catalog material count changed")
			}
			newPageOnly, unchangedPageOnly := 0, 0
			for i, entry := range snap.Evidence {
				prior := old.Evidence[i]
				if entry.Text != prior.Text || entry.SourceBlockID != prior.SourceBlockID || entry.SourceType != prior.SourceType || entry.Locator.Page != prior.Locator.Page || len(entry.FragmentEvidenceIDs) != len(prior.FragmentEvidenceIDs) {
					t.Fatal("catalog material changed")
				}
				if !reflect.DeepEqual(entry.Locator, prior.Locator) {
					if !reflect.DeepEqual(entry.Locator, GuideLocator{Kind: "page", Page: prior.Locator.Page}) {
						t.Fatal("unexpected locator mutation")
					}
					newPageOnly++
				} else if entry.Locator.Kind == "page" {
					unchangedPageOnly++
				}
			}
			t.Logf("new_page_only=%d inherited_page_only=%d evidence_text_and_counts_unchanged=true", newPageOnly, unchangedPageOnly)
			for _, m := range limits.Models {
				context, e := strconv.Atoi(m.Context)
				if e != nil {
					t.Fatal(e)
				}
				output := 0
				if m.Output != nil {
					output, e = strconv.Atoi(*m.Output)
					if e != nil {
						t.Fatal(e)
					}
				}
				x := execution{model: ModelIdentity{ContextTokens: context, OutputReserveTokens: output}, snap: snap, scope: Scope{Language: "zh"}}
				input := x.input(snap)
				input["source_sections"] = snap.Sections
				raw, _ := json.MarshalIndent(input, "", "")
				bound := len(raw) + guideInputDefaultsReserve + len(snap.Evidence)*guideEvidenceExpansionReserve + guidePromptReserve
				fit := len(snap.Sections) <= 512 && x.inputFits(input)
				t.Logf("model=%s full_input_upper_bound=%d evidence=%d sections=%d direct_admission=%t", m.Name, bound, len(snap.Evidence), len(snap.Sections), fit)
				if fit {
					measureIndependentReviewEnvelope(t, &x, snap, false)
				} else {
					calls, err := offlineMockAdmission(t, snap, x.model)
					t.Logf("model=%s bounded_extract_mock_calls=%d final_plan_admission=%t", m.Name, calls, err == nil)
					if err != nil {
						t.Errorf("bounded mock admission: %v", err)
					}
				}
			}
		})
	}
}

// Only deterministic synthetic outputs are returned. This measures packet and
// budget feasibility, not model answer quality or real generated claim coverage.
func offlineMockAdmission(t *testing.T, snap GuideSourceSnapshot, model ModelIdentity) (int, error) {
	t.Helper()
	repo := Repository{DB: guideDB(t)}
	scope := Scope{OwnerID: snap.Identity.OwnerID, DatasetID: snap.Identity.DatasetID, DocumentID: snap.Identity.DocumentID, Language: "zh"}
	calls := 0
	seen := map[string]bool{}
	service := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }, Call: func(_ context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
		if task != "paper.guide.extract" {
			t.Fatal("unexpected nonextract mock")
		}
		calls++
		es := input["evidence"].([]GuideEvidence)
		chosen := es[0]
		for _, e := range es {
			seen[e.ID] = true
			if len(e.Text) < len(chosen.Text) {
				chosen = e
			}
		}
		return mustJSON(GuideExtract{Claims: []GuideClaim{{ID: fmt.Sprintf("admission-%d", calls), Statement: "Synthetic admission check.", Attribution: AttributionPaper, EvidenceIDs: []string{chosen.ID}, Conditions: []string{}, SupportStatus: SupportSupported}}, OpenQuestions: []string{}}), nil
	}}
	rev, e := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: snap.Identity.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "offline", Model: model})
	if e != nil {
		return 0, e
	}
	x := execution{s: service, scope: scope, rev: rev, model: model, snap: snap, f: testFence{repo.DB}, steps: map[string]orm.PaperGuideStep{}}
	input, packet, e := x.planInput(t.Context())
	if e == nil {
		measureIndependentReviewEnvelope(t, &x, packet, true)
		x.steps["plan"] = orm.PaperGuideStep{}
		rebuilt, _, err := x.planInput(t.Context())
		if err != nil || digest(rebuilt) != digest(input) {
			t.Fatalf("offline immutable reconstruction differs: %v", err)
		}
	}
	if e == nil && len(seen) != len(snap.Evidence) {
		return calls, fmt.Errorf("lost extract evidence %d/%d", len(seen), len(snap.Evidence))
	}
	return calls, e
}

// A declared realistic synthetic envelope, not a guarantee for every legal
// model output: 16 claims, 32 prose blocks of 300 CJK characters, four roles.
// Correction additionally stresses schema maxima of 48 suggestions and summary.
func measureIndependentReviewEnvelope(t *testing.T, x *execution, packet GuideSourceSnapshot, extracted bool) {
	t.Helper()
	x.planningPacket, x.extractedScope = packet, extracted
	p := baselinePlan(packet)
	for i := 0; i < 16; i++ {
		c := GuideClaim{ID: fmt.Sprintf("envelope-c%d", i), Statement: strings.Repeat("陈述", 75), Attribution: AttributionPaper, EvidenceIDs: []string{packet.Evidence[0].ID}, Conditions: []string{strings.Repeat("条件", 25)}, SupportStatus: SupportSupported}
		p.Claims = append(p.Claims, c)
		p.Sections[i%4].ClaimIDs = append(p.Sections[i%4].ClaimIDs, c.ID)
	}
	sections := []GuideSection{}
	for i, role := range Roles() {
		s := teachingSection(role)
		s.Blocks = nil
		s.ClaimIDs = append([]string{}, p.Sections[i].ClaimIDs...)
		s.Summary = strings.Repeat("总结", 150)
		for j := 0; j < 8; j++ {
			s.Blocks = append(s.Blocks, GuideBlock{ID: fmt.Sprintf("%s-b%d", role, j), Kind: "paragraph", Text: strings.Repeat("解释", 150), Attribution: AttributionTeaching, ClaimIDs: []string{}, EvidenceIDs: []string{}})
		}
		s.Blocks[0].ClaimIDs = append([]string{}, s.ClaimIDs...)
		s.Blocks[0].Attribution = AttributionPaper
		if err := ValidateSection(packet, p, role, s); err != nil {
			t.Fatal(err)
		}
		sections = append(sections, s)
	}
	if err := ValidatePlan(packet, p); err != nil {
		t.Fatal(err)
	}
	review := x.independentInput()
	review["plan"] = p
	review["sections"] = sections
	correction := x.independentInput()
	correction["plan"] = p
	correction["role"] = RoleBackground
	correction["prior_summary"] = strings.Repeat("𐐀", 4096)
	findings := []GuideFinding{}
	for i := 0; i < 48; i++ {
		findings = append(findings, GuideFinding{TargetType: "block", TargetID: "background-b0", IssueType: "missing_explanation", Suggestion: strings.Repeat("𐐀", 800)})
	}
	correction["findings"] = findings
	if err := ValidateReview(p, sections, GuideReview{Findings: findings}); err != nil {
		t.Fatal(err)
	}
	bound := func(in map[string]any) int {
		b, _ := json.MarshalIndent(in, "", "")
		return len(b) + guideInputDefaultsReserve + len(packet.Evidence)*guideEvidenceExpansionReserve + guidePromptReserve
	}
	t.Logf("independent_review evidence=%d extracted=%t realistic_review_bound=%d correction_stress_bound=%d review_fits=%t correction_fits=%t", len(packet.Evidence), extracted, bound(review), bound(correction), x.inputFits(review), x.inputFits(correction))
	if !x.inputFits(review) || !x.inputFits(correction) {
		t.Fatal("declared independent envelope does not fit")
	}
}
