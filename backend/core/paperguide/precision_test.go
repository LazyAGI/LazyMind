package paperguide

import (
	"context"
	"encoding/json"
	"errors"
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

func TestPrecisionDigestDisablesHistoricalV4CoordinatesButKeepsBody(t *testing.T) {
	d, id := fixture(t)
	blocks := []pdfir.Block{}
	for _, b := range d.Blocks {
		if b.Type != "table" {
			blocks = append(blocks, b)
		}
	}
	d.Blocks = blocks
	d.Precision = &pdfir.SourcePrecision{Version: 2, SourceSHA256: d.SourceSHA256, BaseIRDigest: pdfir.Digest("base"), ArchiveDigest: pdfir.Digest("retained"), Regions: []pdfir.SourceRegion{}}
	old, err := buildSourceSnapshot(d, id, "paper-guide-source-v4")
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
	response, err := service.Evidence(t.Context(), scope, rev.ID, old.Evidence[1].ID)
	ev, same := response.Evidence, response.SourceCurrent
	if err != nil || same || ev.Text != old.Evidence[1].Text || ev.ID != old.Evidence[1].ID || len(ev.Locator.BBox) != 0 {
		t.Fatalf("old source reused: %+v %v", ev, err)
	}
}
func TestContainmentMetadataIsRequiredAndBound(t *testing.T) {
	d, id := fixture(t)
	d.Blocks = d.Blocks[:2]
	for _, mode := range []string{"no precision", "absent set", "empty set", "page only", "unknown", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			doc := d
			if mode != "no precision" {
				doc.Precision = &pdfir.SourcePrecision{Version: pdfir.SourcePrecisionVersion, SourceSHA256: d.SourceSHA256, BaseIRDigest: pdfir.Digest("base"), ArchiveDigest: pdfir.Digest("zip")}
			}
			ids := []string{}
			if mode == "page only" {
				ids = append(ids, d.Blocks[1].ID)
			}
			if mode == "unknown" {
				ids = append(ids, "foreign")
			}
			if mode == "duplicate" {
				ids = append(ids, d.Blocks[1].ID, d.Blocks[1].ID)
			}
			if mode != "no precision" && mode != "absent set" {
				doc.Precision.PageOnlyBlockIDs = &ids
			}
			snap, err := BuildSourceSnapshot(doc, id)
			if mode == "unknown" || mode == "duplicate" {
				if err == nil {
					t.Fatal("invalid containment IDs accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := "page"
			if mode == "empty set" {
				want = "text_span"
			}
			if snap.Evidence[1].Locator.Kind != want {
				t.Fatalf("missing proof authorized geometry: %+v", snap.Evidence[1])
			}
			if err = ValidateSnapshot(snap, id, snap.IRDigest); err != nil {
				t.Fatal(err)
			}
			if mode == "page only" {
				doc.Precision.PageOnlyBlockIDs = &[]string{}
				precise, err := BuildSourceSnapshot(doc, id)
				if err != nil || precise.IRDigest == snap.IRDigest {
					t.Fatal("permissions absent from digest")
				}
			}
		})
	}
}

func TestFrozenV4TableIdentityKeepsOriginalBoxAndIDs(t *testing.T) {
	d, id := tableFragmentDocument(t)
	d.Precision.Version = 2
	d.Precision.Regions = append(d.Precision.Regions, pdfir.SourceRegion{ID: "identity", Kind: "table_identity", ParentBlockID: "physical-parent", Page: 0, Text: "Outside measurement note.", Spans: []pdfir.SourceSpan{{Type: "text", Content: "Outside measurement note.", BBox: []float64{.1, .85, .9, .9}}}})
	old, err := buildSourceSnapshot(d, id, "paper-guide-source-v4")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(old)
	var frozen GuideSourceSnapshot
	if err = json.Unmarshal(raw, &frozen); err != nil {
		t.Fatal(err)
	}
	if err = ValidateSnapshot(frozen, id, old.IRDigest); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old.Evidence, frozen.Evidence) {
		t.Fatal("old evidence reinterpreted")
	}
	for _, e := range frozen.Evidence {
		if e.SourceBlockID == "physical-parent" && e.SourceType == "table" && e.Locator.Kind != "table_cells" {
			t.Fatal("historical box changed")
		}
	}
	d.Precision.Version = pdfir.SourcePrecisionVersion
	d.Precision.PageOnlyBlockIDs = &[]string{"physical-parent"}
	current, err := BuildSourceSnapshot(d, id)
	if err != nil {
		t.Fatal(err)
	}
	if current.IRDigest == old.IRDigest {
		t.Fatal("old source still current")
	}
	for _, e := range current.Evidence {
		if e.SourceBlockID == "physical-parent" && e.SourceType == "table" && e.Locator.Kind != "page" {
			t.Fatal("new source reused old geometry")
		}
	}
}

func TestOriginalDisplayEvidenceIsCurrentAndRevisionScoped(t *testing.T) {
	d, identity := fixture(t)
	d.Blocks = []pdfir.Block{
		{ID: "synthetic-chart", Type: "chart", Page: 1, BBox: []float64{.1, .2, .3, .4}},
		{ID: "inline", Type: "text", Page: 0, Text: "The estimate $y=x+1$ is inline.", BBox: []float64{.2, .2, .8, .25}},
	}
	unverified, err := BuildSourceSnapshot(d, identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(unverified.Assets) != 1 || unverified.Assets[0].OriginalDisplay == nil || len(unverified.Assets[0].OriginalDisplay.BBox) != 0 {
		t.Fatal("an unverified box or inline expression was upgraded to a displayed original")
	}
	contained := []string{}
	d.Precision = &pdfir.SourcePrecision{Version: 3, SourceSHA256: identity.SourceSHA256, BaseIRDigest: pdfir.Digest("synthetic-base"), ArchiveDigest: pdfir.Digest("synthetic-archive"), PageOnlyBlockIDs: &contained}
	current, err := BuildSourceSnapshotWithFigureProofs(d, identity, []pdfir.OriginalFigureProof{
		{BlockID: "synthetic-chart", Type: "chart", Page: 1, Caption: "Figure 1: chart", Parts: [][]float64{{.1, .2, .3, .4}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := Repository{DB: guideDB(t)}
	scope := Scope{OwnerID: identity.OwnerID, DatasetID: identity.DatasetID, DocumentID: identity.DocumentID, Language: "en"}
	rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: identity.SourceSHA256, IRDigest: current.IRDigest, SnapshotPath: "old", Model: ModelIdentity{ID: "synthetic"}})
	if err != nil {
		t.Fatal(err)
	}
	source := &historicalSource{fixtureSource{current, nil}, current}
	service := Service{Repo: repo, Source: source, Authorize: func(_ context.Context, s Scope, _ bool) error {
		if s != scope {
			return ErrForbidden
		}
		return nil
	}}
	id := current.Assets[0].ID
	got, err := service.Evidence(t.Context(), scope, rev.ID, id)
	if err != nil || !got.SourceCurrent || got.OriginalDisplay == nil || !reflect.DeepEqual(got.OriginalDisplay.BBox, []float64{.1, .2, .3, .4}) {
		t.Fatalf("current ACL could not resolve frozen original: %+v %v", got, err)
	}
	// A published guide with the same source and caption can gain an exact crop
	// without changing its immutable source or evidence locator.
	old, err := BuildSourceSnapshotWithFigureProofs(d, identity, []pdfir.OriginalFigureProof{
		{BlockID: "synthetic-chart", Type: "chart", Page: 1, Caption: "Figure 1: chart"},
	})
	if err != nil {
		t.Fatal(err)
	}
	source.legacy = old
	got, err = service.Evidence(t.Context(), scope, rev.ID, id)
	if err != nil || got.OriginalDisplay == nil || !reflect.DeepEqual(got.OriginalDisplay.BBox, current.Assets[0].OriginalDisplay.BBox) ||
		len(old.Assets[0].OriginalDisplay.BBox) != 0 {
		t.Fatalf("caption-only display was not safely upgraded: %+v %v", got, err)
	}
	otherCaption, err := BuildSourceSnapshotWithFigureProofs(d, identity, []pdfir.OriginalFigureProof{
		{BlockID: "synthetic-chart", Type: "chart", Page: 1, Caption: "Figure 2: different"},
	})
	if err != nil {
		t.Fatal(err)
	}
	source.legacy = otherCaption
	got, err = service.Evidence(t.Context(), scope, rev.ID, id)
	if err != nil || got.OriginalDisplay == nil || len(got.OriginalDisplay.BBox) != 0 {
		t.Fatalf("mismatched caption reused a crop: %+v %v", got, err)
	}
	foreign := scope
	foreign.DocumentID = "other-document"
	if _, err = service.Evidence(t.Context(), foreign, rev.ID, id); !errors.Is(err, ErrForbidden) {
		t.Fatalf("foreign revision supplied an original: %v", err)
	}
	source.fixtureSource.s = unverified
	got, err = service.Evidence(t.Context(), scope, rev.ID, id)
	if err != nil || got.SourceCurrent || got.OriginalDisplay != nil || got.Evidence.Locator.Kind != "page" || got.Evidence.Locator.Page != 1 {
		t.Fatalf("stale IR with same PDF reused display: %+v %v", got, err)
	}
}
