package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"lazymind/core/asyncjob"
	"lazymind/core/pdfir"
	"reflect"
	"testing"
)

func TestPageRegionCatalogBindsFrozenRegionInsteadOfParent(t *testing.T) {
	d, id := fixture(t)
	blocks := []pdfir.Block{}
	for _, b := range d.Blocks {
		if b.Type != "table" {
			blocks = append(blocks, b)
		}
	}
	d.Blocks = blocks
	parent := d.Blocks[1]
	d.Precision = &pdfir.SourcePrecision{Version: pdfir.SourcePrecisionVersion, SourceSHA256: d.SourceSHA256, BaseIRDigest: pdfir.Digest("base"), ArchiveDigest: pdfir.Digest("zip"), Regions: []pdfir.SourceRegion{
		{ID: "region_a", ParentBlockID: parent.ID, Page: 0, Text: "First-page source", Spans: []pdfir.SourceSpan{{Type: "text", Content: "First-page source", BBox: []float64{.1, .1, .2, .2}}}},
		{ID: "region_b", ParentBlockID: parent.ID, Page: 1, Text: "透明😀 hyphen-\nated $x_i$", Spans: []pdfir.SourceSpan{{Type: "text", Content: "透明😀 hyphen-\nated $x_i$", BBox: []float64{.1, .1, .2, .2}}}},
	}}
	s, err := BuildSourceSnapshot(d, id)
	if err != nil {
		t.Fatal(err)
	}
	regions := []GuideEvidence{}
	for _, e := range s.Evidence {
		if e.SourceBlockID == parent.ID {
			regions = append(regions, e)
		}
	}
	if len(regions) != 2 || regions[1].Locator.Page != 1 || regions[1].Locator.Kind != "page" || regions[1].SourceRegionID != "region_b" {
		t.Fatalf("wrong regions %+v", regions)
	}
	if regions[1].Text != d.Precision.Regions[1].Text || !reflect.DeepEqual(d.Blocks, s.Blocks) {
		t.Fatal("frozen source changed")
	}
	loc, err := ResolveTextLocator(s, regions[1].ID, "透明😀")
	if err != nil || loc.Page != 1 || loc.Kind != "page" || len(loc.BBox) != 0 {
		t.Fatalf("guessed parent offsets: %+v %v", loc, err)
	}
	if _, err := ResolveTextLocator(s, regions[1].ID, parent.Text); err == nil {
		t.Fatal("parent text substituted for region")
	}
	if err := ValidateSnapshot(s, id, s.IRDigest); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(s)
	var frozen GuideSourceSnapshot
	json.Unmarshal(raw, &frozen)
	frozen.Evidence[2].Text = "tampered"
	if ValidateSnapshot(frozen, id, s.IRDigest) == nil {
		t.Fatal("tampered region accepted")
	}
}
func TestHistoricalV2CatalogReconstructsWithoutPrecision(t *testing.T) {
	d, id := fixture(t)
	old, err := buildSourceSnapshot(d, id, "paper-guide-source-v2")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSnapshot(old, id, old.IRDigest); err != nil {
		t.Fatal(err)
	}
	current, err := BuildSourceSnapshot(d, id)
	if err != nil {
		t.Fatal(err)
	}
	if current.CatalogVersion != SourceCatalogVersion || old.Evidence[0].ID == current.Evidence[0].ID {
		t.Fatal("catalog contracts mixed")
	}
}

func TestPrecisionDigestDisablesHistoricalV2CoordinatesButKeepsBody(t *testing.T) {
	d, id := fixture(t)
	blocks := []pdfir.Block{}
	for _, b := range d.Blocks {
		if b.Type != "table" {
			blocks = append(blocks, b)
		}
	}
	d.Blocks = blocks
	old, err := buildSourceSnapshot(d, id, "paper-guide-source-v2")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(d)
	d.Precision = &pdfir.SourcePrecision{Version: pdfir.SourcePrecisionVersion, SourceSHA256: d.SourceSHA256, BaseIRDigest: pdfir.Digest(string(raw)), ArchiveDigest: pdfir.Digest("retained"), Regions: []pdfir.SourceRegion{}}
	current, err := BuildSourceSnapshot(d, id)
	if err != nil {
		t.Fatal(err)
	}
	if current.IRDigest == old.IRDigest || current.Identity.SourceSHA256 != old.Identity.SourceSHA256 {
		t.Fatal("wrong currentness contract")
	}
	repo := Repository{DB: guideDB(t)}
	scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "en"}
	model := ModelIdentity{ID: "m"}
	rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: old.IRDigest, SnapshotPath: "old", Model: model})
	if err != nil {
		t.Fatal(err)
	}
	section := teachingSection(RoleBackground)
	section.Version = 1
	section.PublicationStatus = "published"
	section.Blocks[0].EvidenceIDs = []string{old.Evidence[1].ID}
	for _, step := range []StepWrite{{Key: "plan", InputHash: "plan", Output: mustJSON(baselinePlan(old))}, {Key: "section:background:v1", InputHash: "section", Output: mustJSON(section), Published: true}} {
		if err = repo.PutStep(t.Context(), testFence{repo.DB}, rev.ID, rev.Epoch, step); err != nil {
			t.Fatal(err)
		}
	}
	if err = repo.Transition(t.Context(), testFence{repo.DB}, rev.ID, rev.Epoch, "completed", ""); err != nil {
		t.Fatal(err)
	}
	service := Service{Repo: repo, Source: &historicalSource{fixtureSource{current, nil}, old}, Authorize: func(context.Context, Scope, bool) error { return nil }}
	got, err := service.Revision(t.Context(), scope, rev.ID, 0)
	if err != nil || got.SourceCurrent || len(got.Sections) != 1 {
		t.Fatalf("history lost: %+v %v", got, err)
	}
	ev, same, err := service.Evidence(t.Context(), scope, rev.ID, old.Evidence[1].ID)
	if err != nil || same || ev.Text != old.Evidence[1].Text || ev.ID != old.Evidence[1].ID || len(ev.Locator.BBox) != 0 {
		t.Fatalf("old source reused: %+v %v", ev, err)
	}
	selected, err := service.Selection(t.Context(), scope, rev.ID, SelectionRequest{SectionKey: "section:background:v1", BlockID: "b_background", Start: 0, End: 3})
	if err != nil || selected.SourceCurrent || selected.Text != "The" {
		t.Fatalf("historical selection changed: %+v %v", selected, err)
	}
}
func TestV5CandidateCannotSpendAfterPrecisionContractChange(t *testing.T) {
	d, id := fixture(t)
	snap, _ := BuildSourceSnapshot(d, id)
	repo := Repository{DB: guideDB(t)}
	scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "en"}
	model := ModelIdentity{ID: "m", ContextTokens: 128000}
	rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "old", Model: model})
	if err != nil {
		t.Fatal(err)
	}
	repo.DB.Model(&rev).Update("fingerprint", digest([]any{id.SourceSHA256, snap.IRDigest, "paper-guide-source-v2", "paper-guide-v5", model}))
	calls := 0
	service := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }, Call: func(context.Context, string, map[string]any, map[string]any) (json.RawMessage, error) {
		calls++
		return nil, nil
	}}
	_, err = service.Run(t.Context(), asyncjob.Job{ID: rev.JobID, PayloadJSON: mustJSON(JobPayload{rev.ID, rev.Epoch})}, testFence{repo.DB})
	if !errors.Is(err, ErrRegenerate) || calls != 0 {
		t.Fatalf("old contract spent: %d %v", calls, err)
	}
}

func TestExpandedCatalogAdmissionNeverTruncatesEvidenceOrSections(t *testing.T) {
	x := execution{model: ModelIdentity{ContextTokens: 10000000}}
	evidence := make([]GuideEvidence, 2049)
	if x.inputFits(map[string]any{"evidence": evidence}) {
		t.Fatal("2049 page entries silently admitted")
	}
	if !x.inputFits(map[string]any{"evidence": evidence[:2048]}) {
		t.Fatal("2048 evidence contract changed")
	}
	x.snap.Sections = make([]SourceSection, 513)
	if _, _, err := x.planInput(t.Context()); !errors.Is(err, ErrContext) {
		t.Fatalf("513 source sections were not rejected before spending: %v", err)
	}
	if len(x.snap.Sections) != 513 {
		t.Fatal("source sections truncated")
	}
}
