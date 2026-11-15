package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
	"strings"
	"testing"
)

func tableFragmentDocument(t *testing.T) (pdfir.Document, SourceIdentity) {
	t.Helper()
	d, id := fixture(t)
	blocks := []pdfir.Block{}
	for _, b := range d.Blocks {
		if b.Type != "table" {
			blocks = append(blocks, b)
		}
	}
	d.Blocks = blocks
	rows := []pdfir.TableRow{{Cells: []pdfir.TableCell{{Text: "home 11", RowSpan: 1, ColSpan: 1}}}, {Cells: []pdfir.TableCell{{Text: "later 22", RowSpan: 1, ColSpan: 1}}}}
	d.Blocks = append(d.Blocks, pdfir.Block{ID: "continuation", Type: "table", Page: 1, Table: &pdfir.Table{Rows: []pdfir.TableRow{}}}, pdfir.Block{ID: "physical-parent", Type: "table", Page: 0, BBox: []float64{.1, .1, .9, .8}, Table: &pdfir.Table{Rows: rows}})
	d.Precision = &pdfir.SourcePrecision{Version: pdfir.SourcePrecisionVersion, SourceSHA256: d.SourceSHA256, BaseIRDigest: pdfir.Digest("base"), ArchiveDigest: pdfir.Digest("zip")}
	for i, text := range []string{"home 11", "later 22"} {
		body := "<table><tr><td>" + text + "</td></tr></table>"
		rs, _, e := pdfir.ParseSourceTable(body)
		if e != nil {
			t.Fatal(e)
		}
		payload := &pdfir.SourceTableFragment{HTML: body, Rows: rs, BBox: []float64{.1, .1, .9, .8}, PageSize: []float64{100, 100}}
		if i == 1 {
			payload.ContinuationBlockID = "continuation"
		}
		d.Precision.Regions = append(d.Precision.Regions, pdfir.SourceRegion{ID: fmt.Sprintf("fragment%d", i), Kind: "table_body_fragment", ParentBlockID: "physical-parent", Page: i, Text: text, Table: payload})
	}
	return d, id
}
func TestTableFragmentCatalogAndPacket(t *testing.T) {
	d, id := tableFragmentDocument(t)
	s, e := BuildSourceSnapshot(d, id)
	if e != nil {
		t.Fatal(e)
	}
	var parent, child GuideEvidence
	for _, x := range s.Evidence {
		if x.SourceBlockID == "physical-parent" {
			if x.SourceType == "table" {
				parent = x
			} else {
				child = x
			}
		}
	}
	if parent.ID == "" || parent.Text != "" || parent.Locator.Page != 0 || child.ID == "" || child.Text != "later 22" || child.Locator.Page != 1 || child.Locator.Kind != "page" {
		t.Fatalf("incorrect catalog parent=%+v child=%+v", parent, child)
	}
	packet, e := RestrictEvidence(s, []string{parent.ID})
	if e != nil {
		t.Fatal(e)
	}
	if len(packet.Evidence) != 3 {
		t.Fatal("asset packet omitted physical content")
	}
	loc, e := ResolveTextLocator(s, child.ID, "22")
	if e != nil || loc.Page != 1 || len(loc.BBox) != 0 {
		t.Fatalf("fragment text locator %+v %v", loc, e)
	}
	_, assets := evidenceIndex(s)
	if assets[child.ID] != "" {
		t.Fatal("fragment eligible as asset")
	}
	if e := ValidateSnapshot(s, id, s.IRDigest); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(parent.Text, "22") {
		t.Fatal("later fact in parent caption")
	}
}

func TestHistoricalPrecisionV1CatalogV3Reconstruction(t *testing.T) {
	d, id := fixture(t)
	d.Precision = &pdfir.SourcePrecision{Version: 1, SourceSHA256: d.SourceSHA256, BaseIRDigest: pdfir.Digest("base"), ArchiveDigest: pdfir.Digest("archive"), Regions: []pdfir.SourceRegion{}}
	old, e := buildSourceSnapshot(d, id, "paper-guide-source-v3")
	if e != nil {
		t.Fatal(e)
	}
	if e = ValidateSnapshot(old, id, old.IRDigest); e != nil {
		t.Fatal(e)
	}
	if _, e = BuildSourceSnapshot(d, id); e == nil {
		t.Fatal("old precision activated as current")
	}
	for _, e := range old.Evidence {
		if len(e.FragmentEvidenceIDs) > 0 || e.ParentAssetID != "" {
			t.Fatal("historical ID reinterpreted")
		}
	}
	d.Precision = nil
	current, e := BuildSourceSnapshot(d, id)
	if e != nil {
		t.Fatal(e)
	}
	if current.IRDigest == old.IRDigest || current.Evidence[0].ID == old.Evidence[0].ID {
		t.Fatal("old source reused")
	}
}

func TestSnapshotRejectsForgedTableFragmentAssociation(t *testing.T) {
	for _, name := range []string{"wrong merged value", "missing start page", "fake continuation", "unproven prefix", "missing table regions"} {
		t.Run(name, func(t *testing.T) {
			d, id := tableFragmentDocument(t)
			switch name {
			case "wrong merged value":
				d.Blocks[len(d.Blocks)-1].Table.Rows[0].Cells[0].Text = "unaccounted canonical value"
			case "missing start page":
				d.Precision.Regions[0].Page = 1
			case "fake continuation":
				d.Precision.Regions[0].Table.ContinuationBlockID = "unknown"
			case "unproven prefix":
				d.Precision.Regions[0].Table.OmittedPrefixRows = 1
			case "missing table regions":
				d.Precision.Regions = nil
			}
			if _, e := BuildSourceSnapshot(d, id); e == nil {
				t.Fatal("forged table provenance accepted")
			}
		})
	}
}

func TestExtractPlanningRetainsCompletePhysicalTableEvenWithoutExtractClaim(t *testing.T) {
	d, id := tableFragmentDocument(t)
	for i := 0; i < 25; i++ {
		d.Blocks = append(d.Blocks, pdfir.Block{ID: fmt.Sprintf("appendix-%d", i), Type: "text", Page: 0, Text: strings.Repeat("Original appendix conditions. ", 70)})
	}
	// FIG and display equation are late; EXTRACT deliberately cites only text.
	d.Blocks = append(d.Blocks,
		pdfir.Block{ID: "late-fig", Type: "chart", Page: 1, BBox: []float64{.3, .2, .6, .5}},
		pdfir.Block{ID: "late-eq", Type: "formula", Page: 1, BBox: []float64{.3, .6, .6, .8}, Latex: "y = x^2"})
	contained := []string{}
	d.Precision.PageOnlyBlockIDs = &contained
	snap, e := BuildSourceSnapshotWithFigureProofs(d, id, []pdfir.OriginalFigureProof{
		{BlockID: "late-fig", Type: "chart", Page: 1, Parts: [][]float64{{.3, .2, .6, .5}}},
	})
	if e != nil {
		t.Fatal(e)
	}
	repo := Repository{DB: guideDB(t)}
	scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "en"}
	model := ModelIdentity{ID: "mock", ContextTokens: 64000, OutputReserveTokens: 20000}
	seen := map[string]bool{}
	calls := 0
	service := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }, Call: func(_ context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
		if task != "paper.guide.extract" {
			t.Fatal(task)
		}
		calls++
		es := input["evidence"].([]GuideEvidence)
		chosen := es[0]
		for _, ev := range es {
			seen[ev.ID] = true
			if ev.SourceType == "text" && ev.ParentAssetID == "" {
				chosen = ev
				break
			}
		}
		for _, ev := range es {
			seen[ev.ID] = true
		}
		return mustJSON(GuideExtract{Claims: []GuideClaim{{ID: fmt.Sprintf("extract-%d", calls), Statement: "Original conditions.", Attribution: AttributionPaper, EvidenceIDs: []string{chosen.ID}, Conditions: []string{}, SupportStatus: SupportSupported}}, OpenQuestions: []string{}}), nil
	}}
	rev, e := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test", Model: model})
	if e != nil {
		t.Fatal(e)
	}
	x := execution{s: service, scope: scope, rev: rev, model: model, snap: snap, f: testFence{repo.DB}, steps: map[string]orm.PaperGuideStep{}}
	planPacket, packet, e := x.planInput(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if calls < 2 || len(seen) != len(snap.Evidence) {
		t.Fatalf("extract conservation calls=%d seen=%d/%d", calls, len(seen), len(snap.Evidence))
	}
	count := 0
	for _, ev := range packet.Evidence {
		if ev.SourceBlockID == "physical-parent" {
			count++
		}
	}
	if count != 3 {
		t.Fatalf("planning dropped physical table: %d", count)
	}
	var originals []string
	for _, ev := range packet.Evidence {
		if ev.SourceBlockID == "late-fig" || ev.SourceBlockID == "late-eq" {
			originals = append(originals, ev.SourceBlockID)
		}
	}
	if strings.Join(originals, ",") != "late-fig,late-eq" || planPacket["original_asset_candidates"] == nil ||
		calls != 2 || !x.inputFits(planPacket) {
		t.Fatalf("planning lost late original material or changed extract budget: %v, %d calls", originals, calls)
	}
	before := calls
	x.steps["plan"] = orm.PaperGuideStep{}
	replay, rebuilt, e := x.planInput(t.Context())
	if e != nil || digest(replay) != digest(planPacket) || calls != before || digest(rebuilt.Evidence) != digest(packet.Evidence) {
		t.Fatalf("resume lost physical table or replayed extraction: %v", e)
	}
	x.planningPacket, x.extractedScope = rebuilt, true
	input := x.independentInput()
	if digest(input["evidence"]) != digest(packet.Evidence) || digest(input["source_sections"]) != digest(snap.Sections) {
		t.Fatal("independent review/repair packet lost table closure or original directory")
	}
}

func TestMockGuidePlansParentOnceAndCitesPhysicalPage(t *testing.T) {
	d, id := tableFragmentDocument(t)
	snap, e := BuildSourceSnapshot(d, id)
	if e != nil {
		t.Fatal(e)
	}
	var parent, child GuideEvidence
	for _, ev := range snap.Evidence {
		if ev.SourceBlockID == "physical-parent" {
			if ev.SourceType == "table" {
				parent = ev
			} else if ev.Locator.Page == 1 {
				child = ev
			}
		}
	}
	plan := baselinePlan(snap)
	plan.Claims = []GuideClaim{{ID: "later-value", Statement: "The later value is 22.", Attribution: AttributionPaper, EvidenceIDs: []string{child.ID}, Conditions: []string{}, SupportStatus: SupportSupported}}
	plan.RequiredAssets = []GuideAssetDecision{{ID: parent.ID, Included: true, Reason: "Shows the values."}}
	for i := range plan.Sections {
		plan.Sections[i].ClaimIDs = []string{"later-value"}
		plan.Sections[i].EvidenceIDs = []string{child.ID}
	}
	plan.Sections[0].AssetIDs = []string{parent.ID}
	repo := Repository{DB: guideDB(t)}
	scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "en"}
	model := ModelIdentity{ID: "mock", ContextTokens: 128000}
	calls := 0
	service := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }, Call: func(_ context.Context, task string, input map[string]any, _ map[string]any) (json.RawMessage, error) {
		calls++
		switch task {
		case "paper.guide.plan":
			return mustJSON(plan), nil
		case "paper.guide.section":
			role := input["role"].(Role)
			evidence := input["evidence"].([]GuideEvidence)
			n := 0
			for _, ev := range evidence {
				if ev.SourceBlockID == "physical-parent" {
					n++
				}
			}
			if n != 3 {
				t.Fatal("section lost complete physical table")
			}
			section := GuideSection{Role: role, Title: string(role), Claims: []GuideClaim{}, ClaimIDs: []string{"later-value"}, Summary: "The later value is 22.", Blocks: []GuideBlock{{ID: "value-" + string(role), Kind: "paragraph", Text: "The later value is 22.", Attribution: AttributionPaper, ClaimIDs: []string{"later-value"}, EvidenceIDs: []string{child.ID}}}}
			if role == RoleBackground {
				section.Blocks = append(section.Blocks, GuideBlock{ID: "table-view", Kind: "table", Text: "Values reported in the table.", Attribution: AttributionPaper, ClaimIDs: []string{"later-value"}, EvidenceIDs: []string{child.ID}, AssetID: parent.ID})
			}
			return mustJSON(section), nil
		case "paper.guide.review":
			return mustJSON(GuideReview{Findings: []GuideFinding{}}), nil
		}
		t.Fatal(task)
		return nil, nil
	}}
	rev, e := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test", Model: model})
	if e != nil {
		t.Fatal(e)
	}
	_, e = service.Run(t.Context(), asyncjob.Job{ID: rev.JobID, PayloadJSON: mustJSON(JobPayload{rev.ID, rev.Epoch})}, testFence{repo.DB})
	if e != nil {
		t.Fatal(e)
	}
	response, e := service.Evidence(t.Context(), scope, rev.ID, child.ID)
	got, current := response.Evidence, response.SourceCurrent
	if e != nil || !current || got.Locator.Page != 1 || got.Locator.Kind != "page" || len(got.Locator.BBox) != 0 {
		t.Fatalf("wrong physical evidence %+v %v", got, e)
	}
	if calls != 10 {
		t.Fatalf("unexpected bounded pipeline calls=%d", calls)
	}
}

func TestOversizedCompleteTableGroupRejectsBeforeAnyModelCall(t *testing.T) {
	d, id := tableFragmentDocument(t)
	parent := &d.Blocks[len(d.Blocks)-1]
	for i := range d.Precision.Regions {
		r := &d.Precision.Regions[i]
		text := fmt.Sprintf("page%d ", i) + strings.Repeat("x", 20000)
		body := "<table><tr><td>" + text + "</td></tr></table>"
		rows, rendered, e := pdfir.ParseSourceTable(body)
		if e != nil {
			t.Fatal(e)
		}
		r.Text = rendered
		r.Table.HTML = body
		r.Table.Rows = rows
		parent.Table.Rows[i] = rows[0]
	}
	snap, e := BuildSourceSnapshot(d, id)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	x := execution{snap: snap, model: ModelIdentity{ContextTokens: 64000, OutputReserveTokens: 20000}, scope: Scope{Language: "en"}, s: Service{Call: func(context.Context, string, map[string]any, map[string]any) (json.RawMessage, error) {
		calls++
		return nil, nil
	}}}
	_, _, e = x.planInput(t.Context())
	if !errors.Is(e, ErrContext) || calls != 0 {
		t.Fatalf("partial table dispatched calls=%d err=%v", calls, e)
	}
}

func TestIndependentVerifiedEmptyTableKeepsMissingMaterialAsset(t *testing.T) {
	d, id := tableFragmentDocument(t)
	d.Blocks = append(d.Blocks, pdfir.Block{ID: "empty-independent", Type: "table", Page: 0, Table: &pdfir.Table{Rows: []pdfir.TableRow{}}})
	d.Precision.Regions = append(d.Precision.Regions, pdfir.SourceRegion{ID: "empty-region", Kind: "table_body_fragment", ParentBlockID: "empty-independent", Page: 0, Table: &pdfir.SourceTableFragment{HTML: "<table></table>", Rows: []pdfir.TableRow{}, BBox: []float64{.1, .1, .9, .8}, PageSize: []float64{100, 100}}})
	s, e := BuildSourceSnapshot(d, id)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, a := range s.Assets {
		if a.BlockID == "empty-independent" {
			found = true
			if len(a.Warnings) == 0 {
				t.Fatal("missing content warning lost")
			}
		}
	}
	if !found {
		t.Fatal("empty table silently removed")
	}
}
